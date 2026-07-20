package check

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveRepoRootHonorsUserConfigRepoPath aligns check's repo-root resolver
// with run's: a repo_path in the user config must resolve (step 4), so check and
// run stop disagreeing for repo_path-only installs (no checkout in cwd, no
// YOLO_REPO_ROOT). Before the fix, check only did env + cwd-walk and returned
// ok=false here.
func TestResolveRepoRootHonorsUserConfigRepoPath(t *testing.T) {
	// A fake repo dir with a flake.nix (step-4 acceptance requires flake.nix).
	repoDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(repoDir, "flake.nix"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A HOME whose user config points repo_path at repoDir.
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

	// Run from a dir that is NOT a checkout so the cwd-walk (step 2) misses.
	t.Chdir(t.TempDir())
	t.Setenv("YOLO_REPO_ROOT", "")
	os.Unsetenv("YOLO_REPO_ROOT")

	got, ok := resolveRepoRoot(os.Getenv)
	if !ok {
		t.Fatal("resolveRepoRoot returned ok=false; want it to honor user-config repo_path")
	}
	// Resolve symlinks (macOS /var → /private/var etc.) for a robust compare.
	wantAbs, _ := filepath.EvalSymlinks(repoDir)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if gotAbs != wantAbs {
		t.Errorf("resolveRepoRoot = %q, want %q", got, repoDir)
	}
}

// TestResolveRepoRootRejectsRepoPathWithoutFlake guards that a repo_path
// pointing at a dir with no flake.nix is NOT accepted (matches run's step 4).
func TestResolveRepoRootRejectsRepoPathWithoutFlake(t *testing.T) {
	noFlake := t.TempDir() // deliberately empty
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{ "repo_path": "`+noFlake+`" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("YOLO_REPO_ROOT", "")
	os.Unsetenv("YOLO_REPO_ROOT")

	if _, ok := resolveRepoRoot(os.Getenv); ok {
		t.Error("resolveRepoRoot accepted a repo_path with no flake.nix")
	}
}
