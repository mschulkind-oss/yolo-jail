package config

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestSnapshotCrossWrite is the go-port plan Stage 13 STRICT gate (flipped from
// the Stage 2 skip-with-reason plan): a config-snapshot written by one
// implementation must be read as UNCHANGED by the other, so switching between
// the Python and Go CLIs never fires a spurious config-approval prompt.
//
// Both directions are exercised:
//
//	Python writes snapshot -> Go's CheckConfigChanges reads it -> proceed=true,
//	  snapshot bytes untouched.
//	Go writes snapshot     -> Python's unchanged-check reads it -> unchanged=true.
//
// Unlike the other parity tests this is NOT skip-on-missing-python: Stage 13
// requires it green to finish. It still guards the "no python at all" case
// (a pure-Go dev box with neither uv nor python3) by skipping, matching the
// repo's other cross-language tests — but any available Python must agree.
func TestSnapshotCrossWrite(t *testing.T) {
	repoRoot := findRepoRoot(t)
	oracle := filepath.Join(repoRoot, "tools", "parity", "config_oracle.py")
	py := pythonRunner(t, repoRoot)
	if py == nil {
		t.Skip("no python available (uv/python3) — cannot run the cross-write gate")
	}

	// A representative config spanning nesting, unicode, ints/floats/bools/null,
	// and key orders that sort_keys must canonicalize identically on both sides.
	configJSON := `{
	  "runtime": "podman",
	  "packages": ["strace", "gtk4.dev", "café-pkg"],
	  "network": {"mode": "host", "ports": ["8000:8000"]},
	  "resources": {"memory": "8g", "cpus": 2.5, "pids_limit": 4096},
	  "gpu": {"enabled": true, "vendor": "amd", "vaapi": true},
	  "mcp_servers": {"z": {"command": "c"}, "a": null},
	  "nested": {"deep": [[1, 2], {"k": "v", "b": false, "n": null}]},
	  "unicode": "日本語 ☃ café",
	  "env_sources": [{"B": "2", "A": "1"}]
	}`
	config, err := jsonx.Decode([]byte(configJSON))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	configMap := config.(*jsonx.OrderedMap)
	// The compact JSON the oracle needs as its "config" arg (re-decoded Python-
	// side; key order is preserved by object_pairs_hook / our compact form).
	compactConfig, err := jsonx.DumpsCompact(configMap)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}

	t.Run("python_writes_go_reads", func(t *testing.T) {
		dir := t.TempDir()
		ws := filepath.Join(dir, "ws")
		snapPath := ConfigSnapshotPath(ws)

		// Python writes the snapshot.
		runOracle(t, py, oracle, []map[string]any{{
			"op":     "write_snapshot",
			"config": mustDecodeCompact(t, compactConfig),
			"path":   snapPath,
		}})

		before, err := os.ReadFile(snapPath)
		if err != nil {
			t.Fatalf("read python-written snapshot: %v", err)
		}

		// Go reads it: unchanged -> proceed true, no rewrite, no prompt.
		prompter := &failPrompter{t: t}
		ok, err := CheckConfigChanges(ws, configMap, true /*isTTY*/, prompter)
		if err != nil {
			t.Fatalf("CheckConfigChanges: %v", err)
		}
		if !ok {
			t.Fatalf("Go read of Python snapshot returned proceed=false (would prompt)")
		}
		if prompter.called {
			t.Fatalf("Go prompted on an unchanged Python-written snapshot")
		}
		after, _ := os.ReadFile(snapPath)
		if string(after) != string(before) {
			t.Fatalf("Go rewrote the snapshot on an unchanged read:\n before: %q\n after:  %q", before, after)
		}
	})

	t.Run("go_writes_python_reads", func(t *testing.T) {
		dir := t.TempDir()
		ws := filepath.Join(dir, "ws")
		snapPath := ConfigSnapshotPath(ws)

		// Go writes the snapshot via the first-run path.
		ok, err := CheckConfigChanges(ws, configMap, false /*isTTY*/, nil)
		if err != nil {
			t.Fatalf("CheckConfigChanges (first run): %v", err)
		}
		if !ok {
			t.Fatalf("Go first-run returned proceed=false")
		}
		if _, err := os.Stat(snapPath); err != nil {
			t.Fatalf("Go did not write the snapshot: %v", err)
		}

		// Python reads it: unchanged must be true.
		res := runOracle(t, py, oracle, []map[string]any{{
			"op":     "check_unchanged",
			"config": mustDecodeCompact(t, compactConfig),
			"path":   snapPath,
		}})
		first := res[0].(*jsonx.OrderedMap)
		unchanged, _ := first.Get("unchanged")
		if b, _ := unchanged.(bool); !b {
			t.Fatalf("Python read of Go-written snapshot: unchanged=false (would prompt)")
		}
	})
}

// failPrompter fails the test if Prompt is ever called.
type failPrompter struct {
	t      *testing.T
	called bool
}

func (p *failPrompter) Prompt(diffLines []string) bool {
	p.called = true
	p.t.Errorf("unexpected prompt with diff:\n%v", diffLines)
	return false
}

// runOracle invokes the oracle with the given cases and returns the decoded
// result array.
func runOracle(t *testing.T, py func(string) *exec.Cmd, oracle string, cases []map[string]any) []any {
	t.Helper()
	in, err := json.Marshal(cases)
	if err != nil {
		t.Fatalf("marshal cases: %v", err)
	}
	cmd := py(oracle)
	cmd.Stdin = bytes.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("oracle run failed: %v", err)
	}
	decoded, err := jsonx.Decode(out)
	if err != nil {
		t.Fatalf("decode oracle output: %v", err)
	}
	arr, ok := decoded.([]any)
	if !ok {
		t.Fatalf("oracle output not an array: %T", decoded)
	}
	return arr
}

func mustDecodeCompact(t *testing.T, compact string) any {
	t.Helper()
	// The oracle receives cases as standard JSON; a nested config object is fine
	// to pass as the decoded Go value re-marshaled by encoding/json. Decode with
	// jsonx to preserve order, then hand back a Go-native structure json.Marshal
	// understands (OrderedMap marshals via its own path below).
	v, err := jsonx.Decode([]byte(compact))
	if err != nil {
		t.Fatalf("decode compact config: %v", err)
	}
	return orderedToNative(v)
}

// orderedToNative converts jsonx values to encoding/json-marshalable Go values.
// Key order is NOT preserved here (Go map), but the oracle re-serializes with
// sort_keys, so order does not affect the snapshot bytes it computes.
func orderedToNative(v any) any {
	switch t := v.(type) {
	case *jsonx.OrderedMap:
		m := map[string]any{}
		for _, k := range t.Keys() {
			val, _ := t.Get(k)
			m[k] = orderedToNative(val)
		}
		return m
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = orderedToNative(e)
		}
		return out
	default:
		if n, ok := jsonx.AsInt(v); ok {
			return n
		}
		return v
	}
}
