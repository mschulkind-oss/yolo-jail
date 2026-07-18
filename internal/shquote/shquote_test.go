package shquote

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// goldenQuote pins shlex.quote's observed output over adversarial argv.
var goldenQuote = map[string]string{
	"":                  "''",
	"plain":             "plain",
	"with space":        "'with space'",
	"a'b":               `'a'"'"'b'`,
	"$var":              `'$var'`,
	"a=b,c.d/e-f:g@h%i": "a=b,c.d/e-f:g@h%i", // all in the safe set
	"semi;colon":        "'semi;colon'",
	"back\\slash":       `'back\slash'`,
	"tab\there":         "'tab\there'",
	"newline\nhere":     "'newline\nhere'",
	"quote\"dq":         `'quote"dq'`,
	"glob*":             "'glob*'",
	"café":              "'café'", // non-ASCII: \w is ASCII-only in shlex
	"~home":             "'~home'",
	"(paren)":           "'(paren)'",
}

func TestQuoteGolden(t *testing.T) {
	for in, want := range goldenQuote {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJoinGolden(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b c"}, "a 'b c'"},
		{[]string{"echo", "$HOME", "a'b"}, `echo '$HOME' 'a'"'"'b'`},
		{[]string{"podman", "run", "--rm", "-e", "X=y z"}, "podman run --rm -e 'X=y z'"},
	}
	for _, tc := range cases {
		if got := Join(tc.in); got != tc.want {
			t.Errorf("Join(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestQuoteParity byte-compares against live Python shlex.quote for a broad
// adversarial corpus. Skips when the Python oracle is unavailable.
func TestQuoteParity(t *testing.T) {
	corpus := []string{
		"", "plain", "with space", "a'b", "a\"b", "a'b\"c", "$var", "`cmd`",
		"semi;colon", "pipe|x", "amp&y", "glob*", "bracket[1]", "brace{a}",
		"back\\slash", "tab\there", "newline\nhere", "cr\rhere", "null\x00byte",
		"café", "münchen", "日本語", "emoji😀", "~home", "(paren)", "<redirect>",
		"a=b,c.d/e-f:g@h%i", "--flag=value", "path/to/file.ext",
		"'single'", "\"double\"", "mixed '\" quotes", "trailing ", " leading",
	}
	oracle := runTextOracle(t, map[string]any{"quote": corpus})
	if oracle == nil {
		t.Skip("python oracle unavailable")
	}
	want := oracle["quote"].(map[string]any)
	for _, s := range corpus {
		if got := Quote(s); got != want[s].(string) {
			t.Errorf("Quote(%q) = %q, python = %q", s, got, want[s])
		}
	}
}

// runTextOracle runs tools/parity/text_oracle.py with the given spec and
// returns the decoded result, or nil if Python isn't available.
func runTextOracle(t *testing.T, spec map[string]any) map[string]any {
	t.Helper()
	root := repoRoot(t)
	specBytes, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command("uv", "run", "python", filepath.Join(root, "tools", "parity", "text_oracle.py"))
	} else if _, err := exec.LookPath("python3"); err == nil {
		cmd = exec.Command("python3", filepath.Join(root, "tools", "parity", "text_oracle.py"))
	} else {
		return nil
	}
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(specBytes)
	out, err := cmd.Output()
	if err != nil {
		t.Logf("oracle failed: %v", err)
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	return result
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
