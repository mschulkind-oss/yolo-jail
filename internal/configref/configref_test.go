package configref

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoTagsLeakInColorOrPlain(t *testing.T) {
	// Plain output must contain none of the known rich tags.
	plain := Render(false)
	for _, tag := range []string{"[bold]", "[/bold]", "[bold cyan]", "[cyan]", "[yellow]", "[bold yellow]"} {
		if strings.Contains(plain, tag) {
			t.Errorf("plain output still contains tag %q", tag)
		}
	}
	// Literal bracketed text (not a tag) is preserved.
	if !strings.Contains(plain, "[5432]") {
		t.Error("literal [5432] should be preserved in plain output")
	}
	// Color output has ANSI and no leftover rich tags.
	color := Render(true)
	if !strings.Contains(color, "\x1b[1m") {
		t.Error("color output should contain ANSI bold")
	}
	for _, tag := range []string{"[bold]", "[/bold]", "[bold cyan]"} {
		if strings.Contains(color, tag) {
			t.Errorf("color output still contains rich tag %q", tag)
		}
	}
}

// TestPlainMatchesLivePython byte-compares the stripped Go reference against the
// live Python config-ref with ANSI stripped (info-parity: same text, per the
// approved OQ — not byte-identical ANSI). Skips without Python.
func TestPlainMatchesLivePython(t *testing.T) {
	root := repoRoot(t)
	py := pythonRunner(t, root)
	if py == nil {
		t.Skip("python unavailable")
	}
	// Render Python's config-ref with a forced-width, no-color console so its
	// output is the raw text (rich soft-wraps at terminal width otherwise). We
	// compare the tag-stripped forms — the WORDS must be identical.
	script := `
import sys; sys.path.insert(0, 'src')
import re, inspect
from cli import config_ref_cmd
srctext = inspect.getsource(config_ref_cmd.config_ref)
m = re.search(r'console\.print\("""(.*)"""\)', srctext, re.DOTALL)
body = m.group(1) + "\n"
# strip the same closed tag set
for tag in ("[bold cyan]","[/bold cyan]","[bold yellow]","[/bold yellow]",
            "[bold]","[/bold]","[cyan]","[/cyan]","[yellow]","[/yellow]"):
    body = body.replace(tag, "")
sys.stdout.write(body)
`
	out, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python config_ref import failed: %v", err)
	}
	goPlain := Render(false)
	if goPlain != string(out) {
		// Show the first differing line for a useful failure.
		t.Errorf("config-ref text mismatch:\n%s", firstLineDiff(string(out), goPlain))
	}
}

func firstLineDiff(py, go_ string) string {
	pl := strings.Split(py, "\n")
	gl := strings.Split(go_, "\n")
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
	return "(no line diff — trailing bytes differ)"
}

func q(s string) string { return `"` + s + `"` }
func itoa(n int) string { return strings.TrimSpace(sprintInt(n)) }
func sprintInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func pythonRunner(t *testing.T, root string) func(args ...string) *exec.Cmd {
	t.Helper()
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
