package reporoot

import (
	"os"
	"path/filepath"
	"testing"
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
	if !ok || got != mustAbs(t, dir) {
		t.Fatalf("Resolve() = (%q,%v), want (%q,true)", got, ok, mustAbs(t, dir))
	}
}

// Resolve step 1 rejects an env pointing at a dir with neither flake.nix nor
// go.mod (an empty/foreign mount must not be trusted as the repo).
func TestResolveEnvEmptyDirRejected(t *testing.T) {
	empty := t.TempDir()
	// cwd-walk must also not find a checkout, so run from an isolated dir.
	getenv := func(k string) string {
		if k == "YOLO_REPO_ROOT" {
			return empty
		}
		return ""
	}
	// A non-flake, non-gomod dir → step 1 skips it. Steps 2-4 may still resolve
	// in a dev checkout; assert only that the empty env dir wasn't blindly used.
	if got, ok := Resolve(getenv); ok && got == mustAbs(t, empty) {
		t.Errorf("empty YOLO_REPO_ROOT dir was wrongly accepted: %q", got)
	}
}
