package jsonx

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestJSONDumpsParity feeds a shared corpus through both Python's json.dumps
// (via tools/parity/jsonx_oracle.py) and this package's DumpsSnapshot/
// DumpsCompact, and byte-compares. This is the linchpin gate against
// config-snapshot drift (the most user-visible regression the port can cause —
// a single byte fires a spurious approval prompt on every Python<->Go switch).
//
// Skips (does not fail) when the Python oracle can't run, so `go test ./...`
// still passes in a pure-Go context; CI always has the repo's Python.
func TestJSONDumpsParity(t *testing.T) {
	repoRoot := findRepoRoot(t)
	oracle := filepath.Join(repoRoot, "tools", "parity", "jsonx_oracle.py")
	corpus := filepath.Join(repoRoot, "tools", "parity", "corpus", "jsonx_cases.json")

	py := pythonRunner(t, repoRoot)
	if py == nil {
		t.Skip("no python oracle available (uv/python3 not found)")
	}

	corpusBytes, err := os.ReadFile(corpus)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	// Decode the corpus with our own order-preserving decoder so the values we
	// re-encode carry the same key order Python sees from the source text.
	decoded, err := Decode(corpusBytes)
	if err != nil {
		t.Fatalf("decode corpus: %v", err)
	}
	cases, ok := decoded.([]any)
	if !ok {
		t.Fatalf("corpus is not a JSON array, got %T", decoded)
	}

	// Run the Python oracle over the raw corpus.
	cmd := py(oracle)
	cmd.Stdin = openFile(t, corpus)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("python oracle failed to run (%v) — skipping cross-language parity", err)
	}
	oracleDecoded, err := Decode(out)
	if err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	expected, ok := oracleDecoded.([]any)
	if !ok {
		t.Fatalf("oracle output is not an array, got %T", oracleDecoded)
	}
	if len(expected) != len(cases) {
		t.Fatalf("oracle returned %d results for %d cases", len(expected), len(cases))
	}

	for i, c := range cases {
		exp, ok := expected[i].(*OrderedMap)
		if !ok {
			t.Fatalf("case %d: oracle entry is %T, want object", i, expected[i])
		}
		wantSnap, _ := exp.Get("snapshot")
		wantCompact, _ := exp.Get("compact")

		gotSnap, err := DumpsSnapshot(c)
		if err != nil {
			t.Errorf("case %d: DumpsSnapshot error: %v", i, err)
			continue
		}
		if gotSnap != wantSnap.(string) {
			t.Errorf("case %d snapshot mismatch:\n go:  %q\n py:  %q", i, gotSnap, wantSnap)
		}

		gotCompact, err := DumpsCompact(c)
		if err != nil {
			t.Errorf("case %d: DumpsCompact error: %v", i, err)
			continue
		}
		if gotCompact != wantCompact.(string) {
			t.Errorf("case %d compact mismatch:\n go:  %q\n py:  %q", i, gotCompact, wantCompact)
		}
	}
}

func openFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// pythonRunner returns a factory that builds a *exec.Cmd running the given
// script under the repo's Python, or nil if none is available. Prefers `uv run`
// (the repo's env) then a bare python3.
func pythonRunner(t *testing.T, repoRoot string) func(script string) *exec.Cmd {
	t.Helper()
	if _, err := exec.LookPath("uv"); err == nil {
		return func(script string) *exec.Cmd {
			c := exec.Command("uv", "run", "python", script)
			c.Dir = repoRoot
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(script string) *exec.Cmd {
			c := exec.Command("python3", script)
			c.Dir = repoRoot
			return c
		}
	}
	return nil
}

// findRepoRoot walks up from the test's dir until it finds go.mod.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from test dir")
		}
		dir = parent
	}
}
