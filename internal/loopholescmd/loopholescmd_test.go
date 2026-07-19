package loopholescmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// TestListParity byte-diffs `List` against live `yolo loopholes list` over the
// REAL bundled loopholes (no user/config loopholes). Skips without Python.
func TestListParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	// Hermetic: empty user/workspace config so only bundled loopholes appear,
	// and a temp HOME so UserLoopholesDir is empty. BundledLoopholesDir already
	// points at the repo's bundled_loopholes on both sides.
	home := t.TempDir()
	t.Setenv("HOME", home)

	var buf bytes.Buffer
	deps := Deps{
		Out: &buf, Err: &buf, Cwd: home, InJail: false,
		LoadUserConfig:      func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil },
	}
	List(deps)
	goOut := buf.String()

	// Python: run the list command with the same empty HOME + a cwd with no
	// yolo-jail.jsonc, so discovery sees only bundled loopholes. loopholes_cmd
	// uses BOTH `from src import loopholes` and `from .config import …`, so add
	// the repo root AND repo/src to sys.path with ABSOLUTE paths (the chdir to
	// the workspace would otherwise break relative entries).
	root := repoRoot(t)
	script := `
import sys, os
root = sys.argv[2]
sys.path.insert(0, os.path.join(root, 'src'))
sys.path.insert(0, root)
os.chdir(sys.argv[1])
from cli.loopholes_cmd import loopholes_list
loopholes_list()
`
	cmd := py("-c", script, home, root)
	cmd.Env = append(os.Environ(), "HOME="+home)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python loopholes list failed: %v", err)
	}
	if goOut != string(out) {
		t.Errorf("list mismatch:\n--- go ---\n%s\n--- py ---\n%s", goOut, out)
	}
}

func TestSetEnabledMissingUserLoophole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
	rc := SetEnabled(deps, "nonexistent", true)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(errBuf.String(), "No user-installed loophole at") {
		t.Errorf("err = %q", errBuf.String())
	}
}

func TestSetEnabledRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Create a user-installed loophole manifest.
	userDir := loopholes.UserLoopholesDir()
	lhDir := filepath.Join(userDir, "myhole")
	must(t, os.MkdirAll(lhDir, 0o755))
	must(t, os.WriteFile(filepath.Join(lhDir, "manifest.jsonc"),
		[]byte(`{"name": "myhole", "description": "test", "transport": "none", "enabled": true}`), 0o644))

	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
	if rc := SetEnabled(deps, "myhole", false); rc != 0 {
		t.Fatalf("disable rc = %d, err=%q", rc, errBuf.String())
	}
	if out.String() != "disabled myhole\n" {
		t.Errorf("disable output = %q", out.String())
	}
	// Manifest now has enabled:false.
	data, _ := os.ReadFile(filepath.Join(lhDir, "manifest.jsonc"))
	if !strings.Contains(string(data), "false") {
		t.Errorf("manifest not updated: %s", data)
	}
	out.Reset()
	if rc := SetEnabled(deps, "myhole", true); rc != 0 {
		t.Fatalf("enable rc = %d", rc)
	}
	if out.String() != "enabled myhole\n" {
		t.Errorf("enable output = %q", out.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
