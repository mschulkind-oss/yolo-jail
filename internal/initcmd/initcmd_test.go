package initcmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitConfigFileParity byte-compares the WRITTEN yolo-jail.jsonc against
// live Python init() for both the no-mounts and with-mounts variants. The file
// bytes MUST be identical (config content is byte-critical, unlike the briefing
// which is info-parity). Skips without Python.
func TestInitConfigFileParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	cases := []struct {
		name   string
		mounts []string
	}{
		{"no_mounts", nil},
		{"with_mounts", []string{"~/a", "~/b:/ctx/b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Go side: write into a temp cwd.
			goDir := t.TempDir()
			Init(goDir, tc.mounts, &bytes.Buffer{}, false)
			goContent, err := os.ReadFile(filepath.Join(goDir, "yolo-jail.jsonc"))
			if err != nil {
				t.Fatalf("go init wrote no file: %v", err)
			}
			// Python side.
			pyContent := pyInit(t, py, tc.mounts)
			if string(goContent) != pyContent {
				t.Errorf("config file mismatch (%s):\n%s", tc.name, firstLineDiff(pyContent, string(goContent)))
			}
		})
	}
}

// TestInitUserConfigParity byte-compares the written user config.
func TestInitUserConfigParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	// Go writes into its own HOME; Python writes into a SEPARATE fresh HOME so
	// neither sees the other's file (both would otherwise hit "already exists").
	goHome := t.TempDir()
	t.Setenv("HOME", goHome)
	InitUserConfig(&bytes.Buffer{})
	goPath := filepath.Join(goHome, ".config", "yolo-jail", "config.jsonc")
	goContent, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatalf("go init-user-config wrote no file: %v", err)
	}
	pyContent := pyInitUser(t, py, t.TempDir())
	if string(goContent) != pyContent {
		t.Errorf("user config mismatch:\n%s", firstLineDiff(pyContent, string(goContent)))
	}
}

func TestInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "yolo-jail.jsonc")
	must(t, os.WriteFile(existing, []byte("{ existing }"), 0o644))
	var buf bytes.Buffer
	Init(dir, nil, &buf, false)
	if !strings.Contains(buf.String(), "yolo-jail.jsonc already exists.") {
		t.Errorf("expected already-exists message, got %q", buf.String())
	}
	// The existing file is NOT overwritten.
	if data, _ := os.ReadFile(existing); string(data) != "{ existing }" {
		t.Error("existing config was clobbered")
	}
}

func TestInitGitignore(t *testing.T) {
	dir := t.TempDir()
	Init(dir, nil, &bytes.Buffer{}, false)
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), ".yolo/") {
		t.Errorf(".gitignore missing .yolo/: %q", gi)
	}
	// Re-running doesn't duplicate the entry (config now exists → early return,
	// but even the append guard checks containment).
	before := string(gi)
	Init(dir, nil, &bytes.Buffer{}, false)
	after, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(after) != before {
		t.Errorf(".gitignore changed on re-run:\n%q\n->\n%q", before, after)
	}
}

func pyInit(t *testing.T, py func(...string) *exec.Cmd, mounts []string) string {
	t.Helper()
	d := t.TempDir()
	// Build the mount args.
	// Absolute src path inserted BEFORE the chdir (a relative 'src' entry would
	// break once we chdir into the temp workspace).
	root := repoRoot(t)
	script := `
import sys, os, io, contextlib
sys.path.insert(0, os.path.join(sys.argv[1], "src"))
sys.path.insert(0, sys.argv[1])
os.chdir(sys.argv[2])
from cli import init_cmd
init_cmd._print_init_briefing = lambda p: None
mounts = sys.argv[3:]
with contextlib.redirect_stdout(io.StringIO()):
    init_cmd.init(mount=mounts)
sys.stdout.write(open(os.path.join(sys.argv[2], "yolo-jail.jsonc")).read())
`
	args := append([]string{"-c", script, root, d}, mounts...)
	out, err := py(args...).Output()
	if err != nil {
		t.Skipf("python init failed: %v", err)
	}
	return string(out)
}

func pyInitUser(t *testing.T, py func(...string) *exec.Cmd, home string) string {
	t.Helper()
	// Silence init_user_config's own stdout ("Created …") so only the file
	// content reaches our pipe.
	script := `
import sys, os, io, contextlib; sys.path.insert(0, 'src')
from cli import init_cmd
with contextlib.redirect_stdout(io.StringIO()):
    init_cmd.init_user_config()
from cli.paths import USER_CONFIG_PATH
sys.stdout.write(open(USER_CONFIG_PATH).read())
`
	cmd := py("-c", script)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python init-user-config failed: %v", err)
	}
	return string(out)
}

func firstLineDiff(py, goStr string) string {
	pl := strings.Split(py, "\n")
	gl := strings.Split(goStr, "\n")
	n := len(pl)
	if len(gl) < n {
		n = len(gl)
	}
	for i := 0; i < n; i++ {
		if pl[i] != gl[i] {
			return "line " + itoa(i+1) + ":\n py: " + q(pl[i]) + "\n go: " + q(gl[i])
		}
	}
	if len(pl) != len(gl) {
		return "line count py=" + itoa(len(pl)) + " go=" + itoa(len(gl))
	}
	return "(trailing bytes differ)"
}

func q(s string) string { return `"` + s + `"` }
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
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
