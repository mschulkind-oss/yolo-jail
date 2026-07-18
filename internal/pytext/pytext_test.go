package pytext

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// goldenRepr pins Python repr()'s observed output.
var goldenRepr = map[string]string{
	"abc":         "'abc'",
	"a'b":         `"a'b"`,         // has ' but no " -> double-quoted
	"a\"b":        `'a"b'`,         // has " but no ' -> single-quoted
	"a'b\"c":      `'a\'b"c'`,      // both -> single, ' escaped
	"tab\there":   `'tab\there'`,   // \t short escape
	"new\nline":   `'new\nline'`,   // \n short escape
	"café":        "'café'",        // printable non-ASCII stays literal
	"back\\slash": `'back\\slash'`, // backslash doubled
	"":            "''",            // empty
	"quote\"only": `'quote"only'`,  // " without ' -> single-quoted
}

func TestReprGolden(t *testing.T) {
	for in, want := range goldenRepr {
		if got := Repr(in); got != want {
			t.Errorf("Repr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReprParity(t *testing.T) {
	corpus := []string{
		"", "abc", "a'b", "a\"b", "a'b\"c", "tab\there", "new\nline", "cr\rhere",
		"café", "münchen", "日本語", "emoji😀", "back\\slash", "\x01\x1f\x7f",
		"path/to/file", "relative path (got x)", "spaces here",
		"$var", "mixed'\"both", "\x00null", "ünïcödé", "🎉party🎉",
	}
	oracle := runReprOracle(t, corpus)
	if oracle == nil {
		t.Skip("python oracle unavailable")
	}
	for _, s := range corpus {
		if got := Repr(s); got != oracle[s] {
			t.Errorf("Repr(%q) = %q, python = %q", s, got, oracle[s])
		}
	}
}

func runReprOracle(t *testing.T, corpus []string) map[string]string {
	t.Helper()
	root := repoRoot(t)
	specBytes, _ := json.Marshal(map[string]any{"repr": corpus})
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
	var result struct {
		Repr map[string]string `json:"repr"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("decode oracle: %v", err)
	}
	return result.Repr
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
