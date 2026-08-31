package reporoot

import (
	"os"
	"path/filepath"
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

// BundledSourceDirFrom must find the flake bundle at each of the three shipping
// layouts, and return ok=false when no bundle is present.
func TestBundledSourceDirFrom(t *testing.T) {
	// Homebrew layout: <prefix>/bin/yolo, <prefix>/share/yolo-jail/flake.nix.
	prefix := t.TempDir()
	binDir := filepath.Join(prefix, "bin")
	share := filepath.Join(prefix, "share", "yolo-jail")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := BundledSourceDirFrom(binDir); !ok || got != mustAbs(t, share) {
		t.Errorf("homebrew: got (%q,%v), want (%q,true)", got, ok, mustAbs(t, share))
	}

	// Release-archive layout: <root>/yolo, <root>/share/yolo-jail/flake.nix.
	arch := t.TempDir()
	archShare := filepath.Join(arch, "share", "yolo-jail")
	if err := os.MkdirAll(archShare, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archShare, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := BundledSourceDirFrom(arch); !ok || got != mustAbs(t, archShare) {
		t.Errorf("archive: got (%q,%v), want (%q,true)", got, ok, mustAbs(t, archShare))
	}

	// Bundle unpacked directly beside the binary.
	beside := t.TempDir()
	if err := os.WriteFile(filepath.Join(beside, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := BundledSourceDirFrom(beside); !ok || got != mustAbs(t, beside) {
		t.Errorf("beside: got (%q,%v), want (%q,true)", got, ok, mustAbs(t, beside))
	}

	// No bundle anywhere → not found.
	if _, ok := BundledSourceDirFrom(t.TempDir()); ok {
		t.Error("empty dir wrongly reported a bundle")
	}
}

// Resolve step 3: a from-source `just install` stages the flake bundle under
// paths.FlakeBundleDir (GlobalStorage/flake-bundle), and Resolve finds it with no
// YOLO_REPO_ROOT set — the self-contained-install guarantee. HOME is redirected
// to a temp dir so FlakeBundleDir resolves there.
func TestResolveFindsStateDirBundle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bundle := paths.FlakeBundleDir()
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := Resolve(func(string) string { return "" })
	if !ok || got.Root != mustAbs(t, bundle) {
		t.Fatalf("Resolve() = (%q,%v), want (%q,true) — the staged bundle", got.Root, ok, mustAbs(t, bundle))
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
	t.Setenv("HOME", t.TempDir())
	if paths.FlakeBundleDir() == paths.GlobalStorage() {
		t.Fatal("FlakeBundleDir equals GlobalStorage — staging rm -rf would delete the state dir")
	}
	if filepath.Dir(paths.FlakeBundleDir()) != paths.GlobalStorage() {
		t.Fatalf("FlakeBundleDir %q is not a direct child of GlobalStorage %q",
			paths.FlakeBundleDir(), paths.GlobalStorage())
	}
}

// Resolve step 1: a YOLO_REPO_ROOT that actually contains source wins.
func TestResolveEnvWins(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	getenv := func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return dir
		}
		return ""
	}
	got, ok := Resolve(getenv)
	if !ok || got.Root != mustAbs(t, dir) {
		t.Fatalf("Resolve() = (%q,%v), want (%q,true)", got.Root, ok, mustAbs(t, dir))
	}
	if got.Source != FromEnv {
		t.Errorf("Source = %q, want %q", got.Source, FromEnv)
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
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
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
	empty := t.TempDir()
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
	checkout := t.TempDir()
	for _, name := range []string{"flake.nix", "go.mod"} { // the old step-2 marker pair
		if err := os.WriteFile(filepath.Join(checkout, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
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
	checkout := t.TempDir()
	for _, name := range []string{"flake.nix", "go.mod"} {
		if err := os.WriteFile(filepath.Join(checkout, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deep := filepath.Join(checkout, "internal", "cli")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", t.TempDir())
	t.Chdir(deep)

	if got, ok := Resolve(func(string) string { return "" }); ok && got.Root == mustAbs(t, checkout) {
		t.Fatalf("Resolve() walked up to the checkout %q from a subdir — the cwd-walk is back", got.Root)
	}
}
