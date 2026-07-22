package darwinpkg

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildProfileArgv(t *testing.T) {
	want := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
		"build", "--impure", "--no-link", "--print-out-paths", "--print-build-logs",
		".#packages.aarch64-darwin.yoloDarwinPackages",
	}
	if got := BuildProfileArgv(""); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
	}
	// Custom system flows through.
	if got := BuildProfileArgv("x86_64-darwin"); got[len(got)-1] != ".#packages.x86_64-darwin.yoloDarwinPackages" {
		t.Errorf("custom system attr = %q", got[len(got)-1])
	}
}

func TestUnavailableEvalArgv(t *testing.T) {
	want := []string{
		"nix", "--extra-experimental-features", "nix-command flakes",
		"--accept-flake-config",
		"eval", "--impure", "--json", ".#darwinUnavailablePackages.aarch64-darwin",
	}
	if got := UnavailableEvalArgv(""); !reflect.DeepEqual(got, want) {
		t.Errorf("argv = %v", got)
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
