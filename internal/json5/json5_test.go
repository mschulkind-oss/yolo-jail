package json5

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestDecodeGolden pins hand-picked cases (the hard-requirement features).
func TestDecodeGolden(t *testing.T) {
	cases := []struct {
		in   string
		want string // jsonx.DumpsCompact of the decoded value
	}{
		{`{}`, `{}`},
		{`{"a":1}`, `{"a": 1}`},
		{"// c\n{\"a\":1}", `{"a": 1}`},
		{`/* b */ {"a":1}`, `{"a": 1}`},
		{`{"a":1,}`, `{"a": 1}`},      // trailing comma
		{`[1,2,3,]`, `[1, 2, 3]`},     // trailing comma
		{`{'s':'q'}`, `{"s": "q"}`},   // single quotes
		{`{unq: 1}`, `{"unq": 1}`},    // unquoted key
		{`{"h": 0xff}`, `{"h": 255}`}, // hex
		{`{"p": +5}`, `{"p": 5}`},     // leading plus
		{`{"d": .5}`, `{"d": 0.5}`},   // leading dot
		{`{"t": 5.}`, `{"t": 5.0}`},   // trailing dot
	}
	for _, tc := range cases {
		v, err := Decode([]byte(tc.in))
		if err != nil {
			t.Errorf("Decode(%q) error: %v", tc.in, err)
			continue
		}
		got, err := jsonx.DumpsCompact(v)
		if err != nil {
			t.Errorf("DumpsCompact(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Decode(%q) -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDecodeRejectsMalformed(t *testing.T) {
	for _, in := range []string{`{"a": 1`, `[1 2 3]`, `{a b}`, `nul`, ``, `{} trailing`} {
		if _, err := Decode([]byte(in)); err == nil {
			t.Errorf("Decode(%q) should have errored", in)
		}
	}
}

// TestJSON5Parity: for every corpus doc AND every repo .jsonc, parse with Go
// json5.Decode + re-encode via jsonx.DumpsSnapshot, and byte-compare against
// pyjson5's canonical form (agreeing on accept/reject). Skips without Python.
func TestJSON5Parity(t *testing.T) {
	root := repoRoot(t)
	corpusPath := filepath.Join(root, "tools", "parity", "corpus", "json5_cases.json")
	corpusBytes, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var inputs []string
	if err := json.Unmarshal(corpusBytes, &inputs); err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	// Add every repo .jsonc as a corpus case (read at test time).
	for _, p := range repoJSONCFiles(t, root) {
		b, err := os.ReadFile(p)
		if err == nil {
			inputs = append(inputs, string(b))
		}
	}

	oracle := runJSON5Oracle(t, root, inputs)
	if oracle == nil {
		t.Skip("python oracle unavailable")
	}
	if len(oracle) != len(inputs) {
		t.Fatalf("oracle returned %d results for %d inputs", len(oracle), len(inputs))
	}

	for i, in := range inputs {
		exp := oracle[i]
		v, err := Decode([]byte(in))
		if !exp.OK {
			if err == nil {
				t.Errorf("case %d: Go accepted %q that pyjson5 rejected", i, truncate(in))
			}
			continue
		}
		if err != nil {
			t.Errorf("case %d: Go rejected %q that pyjson5 accepted: %v", i, truncate(in), err)
			continue
		}
		got, err := jsonx.DumpsSnapshot(v)
		if err != nil {
			t.Errorf("case %d: DumpsSnapshot: %v", i, err)
			continue
		}
		if got != exp.Canonical {
			t.Errorf("case %d (%q) canonical mismatch:\n go: %q\n py: %q", i, truncate(in), got, exp.Canonical)
		}
	}
}

type oracleResult struct {
	OK        bool   `json:"ok"`
	Canonical string `json:"canonical"`
}

func runJSON5Oracle(t *testing.T, root string, inputs []string) []oracleResult {
	t.Helper()
	spec, _ := json.Marshal(inputs)
	var cmd *exec.Cmd
	if _, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command("uv", "run", "python", filepath.Join(root, "tools", "parity", "json5_oracle.py"))
	} else if _, err := exec.LookPath("python3"); err == nil {
		cmd = exec.Command("python3", filepath.Join(root, "tools", "parity", "json5_oracle.py"))
	} else {
		return nil
	}
	cmd.Dir = root
	cmd.Stdin = bytes.NewReader(spec)
	out, err := cmd.Output()
	if err != nil {
		t.Logf("oracle failed: %v", err)
		return nil
	}
	var results []oracleResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	return results
}

func repoJSONCFiles(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	// Known locations (avoid walking the whole tree / .yolo module cache).
	candidates := []string{
		filepath.Join(root, "yolo-jail.jsonc"),
	}
	bundled := filepath.Join(root, "src", "bundled_loopholes")
	if entries, err := os.ReadDir(bundled); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				m := filepath.Join(bundled, e.Name(), "manifest.jsonc")
				if _, err := os.Stat(m); err == nil {
					candidates = append(candidates, m)
				}
			}
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func truncate(s string) string {
	if len(s) > 50 {
		return s[:50] + "..."
	}
	return s
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
