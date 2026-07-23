package check

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveRepoRootIgnoresUserConfigRepoPath is the retirement guard: the
// user-config repo_path key was dropped (2026-07-23), so check's resolver — like
// run's, since both delegate to the single internal/reporoot.Resolve — must NOT
// resolve a stray repo_path. cwd is isolated so the cwd-walk (step 2) and the
// exe-relative bundle (step 3) both miss; only the retired step 4 could return
// repoDir, so a pass proves it is gone and check/run still agree.
func TestResolveRepoRootIgnoresUserConfigRepoPath(t *testing.T) {
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
	t.Chdir(t.TempDir())
	t.Setenv("YOLO_REPO_ROOT", "")
	os.Unsetenv("YOLO_REPO_ROOT")

	got, ok := resolveRepoRoot(os.Getenv)
	wantAbs, _ := filepath.EvalSymlinks(repoDir)
	gotAbs, _ := filepath.EvalSymlinks(got)
	if ok && gotAbs == wantAbs {
		t.Fatalf("resolveRepoRoot honored the retired repo_path key: %q", got)
	}
}
