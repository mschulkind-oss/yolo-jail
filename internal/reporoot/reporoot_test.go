package reporoot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatalf("abs(%q): %v", p, err)
	}
	return a
}

// BundledSourceDirFrom must find complete executable-relative bundles at each
// supported shipping layout and return false when no complete bundle is present.
func TestBundledSourceDirFrom(t *testing.T) {
	prefix := canonicalTempDir(t)
	binDir := filepath.Join(prefix, "bin")
	share := filepath.Join(prefix, "share", "yolo-jail")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMatchedBundle(t, share, "", "")
	if got, ok := BundledSourceDirFrom(binDir); !ok || got != mustAbs(t, share) {
		t.Errorf("system bundle: got (%q,%v), want (%q,true)", got, ok, mustAbs(t, share))
	}

	archive := canonicalTempDir(t)
	archiveShare := filepath.Join(archive, "share", "yolo-jail")
	writeMatchedBundle(t, archiveShare, "", "")
	if got, ok := BundledSourceDirFrom(archive); !ok || got != mustAbs(t, archiveShare) {
		t.Errorf("archive bundle: got (%q,%v), want (%q,true)", got, ok, mustAbs(t, archiveShare))
	}

	beside := canonicalTempDir(t)
	writeMatchedBundle(t, beside, "", "")
	if got, ok := BundledSourceDirFrom(beside); !ok || got != mustAbs(t, beside) {
		t.Errorf("beside bundle: got (%q,%v), want (%q,true)", got, ok, mustAbs(t, beside))
	}
	if _, ok := BundledSourceDirFrom(canonicalTempDir(t)); ok {
		t.Error("empty dir wrongly reported a bundle")
	}
}

func TestResolveFindsMatchingInstalledBundle(t *testing.T) {
	base := canonicalTempDir(t)
	exeDir := filepath.Join(base, "bare-bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(base, "flake-bundle")
	writeMatchedBundle(t, bundle, snapshotA, "installed")
	got := resolveWith(filepath.Join(exeDir, "yolo"), bundle, func(string) string { return "" }, snapshotA, "installed")
	if got.Error != nil || got.Root != mustAbs(t, bundle) {
		t.Fatalf("resolve() = %+v, want matching installed bundle", got)
	}
	if got.Source != FromInstalledBundle {
		t.Errorf("Source = %q, want %q", got.Source, FromInstalledBundle)
	}
}

// The state-dir bundle can NEVER equal the state dir itself — it is a dedicated
// leaf under it. This is the structural invariant that makes the `rm -rf $DEST`
// staging safe: even if a caller aimed staging at FlakeBundleDir and it were
// wiped, GlobalStorage (auth, caches, per-workspace overlays) is its parent, not
// the target. Pins the collision that deleted a real state dir from recurring.
func TestFlakeBundleDirIsNotStateDir(t *testing.T) {
	t.Setenv("HOME", canonicalTempDir(t))
	if paths.FlakeBundleDir() == paths.GlobalStorage() {
		t.Fatal("FlakeBundleDir equals GlobalStorage — staging rm -rf would delete the state dir")
	}
	if filepath.Dir(paths.FlakeBundleDir()) != paths.GlobalStorage() {
		t.Fatalf("FlakeBundleDir %q is not a direct child of GlobalStorage %q",
			paths.FlakeBundleDir(), paths.GlobalStorage())
	}
}

// The retired source selector is rejected rather than silently pairing the
// running launcher with another checkout or bundle.
func TestResolveRejectsObsoleteEnv(t *testing.T) {
	dir := canonicalTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolveWith(filepath.Join(canonicalTempDir(t), "yolo"), "", func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return dir
		}
		return ""
	}, "", "")
	if got.Error == nil || got.Root != "" {
		t.Fatalf("Resolve() selected obsolete env source: %+v", got)
	}
	if !strings.Contains(got.Error.Error(), "YOLO_REPO_ROOT") || !strings.Contains(got.Error.Error(), "just build-dev-bundle") {
		t.Errorf("error lacks actionable obsolete-selector guidance: %v", got.Error)
	}
}

// Resolve no longer honors a user-config repo_path. The key was retired
// (2026-07-23): the exe-relative bundle (step 2) covers every install channel and
// YOLO_REPO_ROOT (step 1) covers a live checkout, so a config pointer is
// redundant. A stray repo_path must be ignored, NOT resolved — otherwise the
// retirement is a no-op.
//
// HOME is a temp dir so the staged bundle (step 3) misses, and the exe-relative
// bundle (step 2) misses because the test binary has no share/yolo-jail beside
// it. Nothing left can reach repoDir — so a pass proves the key is gone.
func TestResolveIgnoresUserConfigRepoPath(t *testing.T) {
	repoDir := canonicalTempDir(t)
	if err := os.WriteFile(filepath.Join(repoDir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{ "repo_path": "` + repoDir + `" }`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := Resolve(func(string) string { return "" })
	if ok && got.Root == mustAbs(t, repoDir) {
		t.Fatalf("Resolve honored the retired repo_path key: %q", got.Root)
	}
}

// Resolve step 1 rejects an env pointing at a dir with neither flake.nix nor
// go.mod (an empty/foreign mount must not be trusted as the repo).
func TestResolveEnvEmptyDirRejected(t *testing.T) {
	empty := canonicalTempDir(t)
	getenv := func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return empty
		}
		return ""
	}
	// A non-flake, non-gomod dir → step 1 skips it. Steps 2-3 may still resolve a
	// bundle; assert only that the empty env dir wasn't blindly used.
	if got, ok := Resolve(getenv); ok && got.Root == mustAbs(t, empty) {
		t.Errorf("empty YOLO_REPO_ROOT dir was wrongly accepted: %q", got.Root)
	}
}

// TestResolveIgnoresCheckoutInCwd is the cwd-removal guard (2026-08-31). Resolve
// used to walk up from the working directory for a dir holding both flake.nix
// and go.mod, and that walk outranked every bundle — so `yolo` inside a yolo-jail
// checkout silently built its image from a DIFFERENT source than the same `yolo`
// one directory up. It was also the only way the launcher and the image could be
// built from different commits (version.SourceSkew). A checkout sitting in cwd
// must now be invisible; YOLO_REPO_ROOT is the only way to name one.
//
// HOME is a temp dir so the staged bundle (step 3) misses too, leaving nothing
// that could legitimately return the checkout.
func TestResolveIgnoresCheckoutInCwd(t *testing.T) {
	checkout := canonicalTempDir(t)
	for _, name := range []string{"flake.nix", "go.mod"} { // the old step-2 marker pair
		if err := os.WriteFile(filepath.Join(checkout, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", canonicalTempDir(t))
	t.Chdir(checkout)

	if got, ok := Resolve(func(string) string { return "" }); ok && got.Root == mustAbs(t, checkout) {
		t.Fatalf("Resolve() returned the cwd checkout %q (source %q) — the cwd-walk is back",
			got.Root, got.Source)
	}
}

// A subdirectory of a checkout must be just as invisible: the retired walk
// climbed to the root from any depth, so pinning only the root would leave half
// the behaviour unpinned.
func TestResolveIgnoresCheckoutAboveCwd(t *testing.T) {
	checkout := canonicalTempDir(t)
	for _, name := range []string{"flake.nix", "go.mod"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deep := filepath.Join(checkout, "internal", "cli")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", canonicalTempDir(t))
	t.Chdir(deep)

	if got, ok := Resolve(func(string) string { return "" }); ok && got.Root == mustAbs(t, checkout) {
		t.Fatalf("Resolve() walked up to the checkout %q from a subdir — the cwd-walk is back", got.Root)
	}
}

// A launcher that cannot find its own executable names a step a user without a checkout can take,
// not only `just install`.
func TestUnlocatableExecutableNamesAReinstallForEveryUser(t *testing.T) {
	got := resolveWith("", "", func(string) string { return "" }, "", "")
	if got.Error == nil {
		t.Fatal("an empty executable path resolved")
	}
	for _, want := range []string{"Homebrew or a release archive", "just install"} {
		if !strings.Contains(got.Error.Error(), want) {
			t.Errorf("error lacks %q: %v", want, got.Error)
		}
	}
}
