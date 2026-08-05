package darwinpkg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestBuildProfileArgv(t *testing.T) {
	// The UNROOTED form (outLink "") keeps --no-link.
	want := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
		"build", "--impure", "--no-link", "--print-out-paths", "--print-build-logs",
		".#packages.aarch64-darwin.yoloDarwinPackages",
	}
	if got := BuildProfileArgv("", ""); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v\nwant %v", got, want)
	}
	// Custom system flows through.
	if got := BuildProfileArgv("x86_64-darwin", ""); got[len(got)-1] != ".#packages.x86_64-darwin.yoloDarwinPackages" {
		t.Errorf("custom system attr = %q", got[len(got)-1])
	}
}

// A non-empty outLink turns the build into its own GC root: `--out-link <path>` REPLACES
// `--no-link`, so nix registers an indirect root as part of the build (N1). Both halves
// are asserted — the flag present AND --no-link gone — because keeping --no-link alongside
// --out-link is exactly the mistake that would leave the profile unrooted while looking
// fixed.
func TestBuildProfileArgvRootsWithOutLink(t *testing.T) {
	argv := BuildProfileArgv("aarch64-darwin", "/state/build/package-roots/packages")
	joined := strings.Join(argv, " ")
	if !strings.Contains(joined, "--out-link /state/build/package-roots/packages") {
		t.Errorf("outLink must become --out-link <path>: %q", joined)
	}
	if strings.Contains(joined, "--no-link") {
		t.Errorf("--no-link must NOT survive alongside --out-link (nix would not root "+
			"the build): %q", joined)
	}
	// The out-path print stays: the caller still needs the store path itself.
	if !strings.Contains(joined, "--print-out-paths") {
		t.Errorf("--print-out-paths must survive: %q", joined)
	}
}

func TestUnavailableEvalArgv(t *testing.T) {
	want := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
		"eval", "--impure", "--json", ".#darwinUnavailablePackages.aarch64-darwin",
	}
	if got := UnavailableEvalArgv(""); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v\nwant %v", got, want)
	}
}

// The GC root's path: under GlobalStorage (never a bare $HOME leaf), a SIBLING of the
// per-image roots dir, and keyed on the passed home rather than the process $HOME.
func TestProfileRootLink(t *testing.T) {
	got := ProfileRootLink("/homes/alice")
	want := "/homes/alice/.local/share/yolo-jail/build/package-roots/packages"
	if got != want {
		t.Errorf("ProfileRootLink = %q, want %q", got, want)
	}
	// It must NOT live in build/roots — prune.PruneOrphanImageRoots enumerates every
	// symlink there and reaps the ones no recently-loaded IMAGE needs, which would
	// unroot this profile on a routine `yolo prune --apply`.
	if strings.Contains(got, "/build/roots/") {
		t.Errorf("the package root must not share build/roots with the image roots "+
			"(prune would sweep it): %q", got)
	}
	// The dir this link sits in must be exactly what paths.PackageRootsDir names, or the
	// accessor and the link would drift into two different locations.
	t.Setenv("HOME", "/homes/alice")
	if dir := filepath.Dir(got); dir != paths.PackageRootsDir() {
		t.Errorf("ProfileRootLink's dir = %q, want paths.PackageRootsDir() = %q",
			dir, paths.PackageRootsDir())
	}
	// Keyed on the ARGUMENT, not $HOME: a caller that already resolved a home (a guest
	// notch provisioning a home it was handed) must not have the env silently win.
	t.Setenv("HOME", "/homes/bob")
	if again := ProfileRootLink("/homes/alice"); again != want {
		t.Errorf("ProfileRootLink must ignore $HOME, got %q", again)
	}
}

// The root leaf is FIXED, not content-keyed, and that is what makes a CHANGED profile
// replace its root instead of accumulating one per package set. Asserted as a property of
// the path function (two different homes differ; the same home never does) because the
// replacement itself is nix's `--out-link` behavior, not ours.
func TestProfileRootLinkIsStablePerHome(t *testing.T) {
	a := ProfileRootLink("/homes/alice")
	if b := ProfileRootLink("/homes/alice"); a != b {
		t.Errorf("the same home must yield ONE root path (a per-materialization key would "+
			"leak a root per package set): %q vs %q", a, b)
	}
	if c := ProfileRootLink("/homes/carol"); c == a {
		t.Error("two homes must not share one root link")
	}
}

func TestBuildEnv(t *testing.T) {
	base := []string{"PATH=/bin", "YOLO_EXTRA_PACKAGES=stale", "HOME=/root"}
	// Empty packages -> var removed.
	got, err := BuildEnv(base, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range got {
		if kv == "YOLO_EXTRA_PACKAGES=stale" || len(kv) >= 20 && kv[:20] == "YOLO_EXTRA_PACKAGES=" {
			t.Errorf("empty packages should drop YOLO_EXTRA_PACKAGES, got %q", kv)
		}
	}
	// Non-empty -> compact JSON set.
	got, err = BuildEnv(base, []any{"ripgrep", "fd"})
	if err != nil {
		t.Fatal(err)
	}
	found := ""
	for _, kv := range got {
		if len(kv) >= 20 && kv[:20] == "YOLO_EXTRA_PACKAGES=" {
			found = kv[20:]
		}
	}
	if found != `["ripgrep", "fd"]` {
		t.Errorf("YOLO_EXTRA_PACKAGES = %q, want compact JSON", found)
	}
}

func TestProfilePaths(t *testing.T) {
	// Empty out -> empty.
	if pp, env := ProfilePaths("  ", nil); pp != nil || len(env) != 0 {
		t.Errorf("empty out => %v, %v", pp, env)
	}
	// bin always contributed; pkgconfig only when present.
	pp, env := ProfilePaths("/nix/store/abc-prof\n", func(string) bool { return false })
	if !reflect.DeepEqual(pp, []string{"/nix/store/abc-prof/bin"}) {
		t.Errorf("path prefix = %v", pp)
	}
	if len(env) != 0 {
		t.Errorf("no pkgconfig => empty env, got %v", env)
	}
	_, env = ProfilePaths("/nix/store/abc-prof", func(p string) bool { return true })
	if env["PKG_CONFIG_PATH"] != "/nix/store/abc-prof/lib/pkgconfig" {
		t.Errorf("PKG_CONFIG_PATH = %q", env["PKG_CONFIG_PATH"])
	}
}

func TestLockedNixpkgsRev(t *testing.T) {
	dir := t.TempDir()
	lock := filepath.Join(dir, "flake.lock")
	must(t, os.WriteFile(lock, []byte(`{"nodes":{"nixpkgs":{"locked":{"rev":"abc123def"}}}}`), 0o644))
	rev, err := LockedNixpkgsRev(lock)
	if err != nil || rev != "abc123def" {
		t.Errorf("rev = %q, %v", rev, err)
	}
	if _, err := LockedNixpkgsRev(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing lock should error")
	}
}

func TestParseSkippedNames(t *testing.T) {
	if got := ParseSkippedNames(`["foo","bar"]`); !reflect.DeepEqual(got, []string{"foo", "bar"}) {
		t.Errorf("= %v", got)
	}
	if got := ParseSkippedNames(`{}`); got != nil {
		t.Errorf("non-array => nil, got %v", got)
	}
	if got := ParseSkippedNames(`garbage`); got != nil {
		t.Errorf("bad json => nil, got %v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
