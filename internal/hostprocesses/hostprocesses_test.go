package hostprocesses

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// configCorpus: jsonc config docs exercising the str-filter + `or DEFAULT`
// semantics.
var configCorpus = []string{
	`{"host_processes":{"visible":["sway","waykeeper"],"fields":["pid","comm"]}}`,
	`{"host_processes":{"visible":["a",1,"b"],"fields":[1,2]}}`, // non-str filtered
	`{"host_processes":{"visible":["x"]}}`,                      // fields absent -> DEFAULT
	`{"host_processes":{"visible":["x"],"fields":[]}}`,          // empty fields -> DEFAULT
	`{"host_processes":{}}`,                                     // empty section
	`{}`,                                                        // no section
	`{"host_processes":{"visible":["sway"]} /* comment */}`,     // jsonc comment
	`{"host_processes":{"visible":["a",],}}`,                    // trailing commas
}

// TestLoadConfigParity byte-diffs LoadConfig against the live Python
// _load_config over the corpus. Skips without Python.
func TestLoadConfigParity(t *testing.T) {
	oracle := runHPOracle(t, configCorpus)
	if oracle == nil {
		t.Skip("python oracle unavailable")
	}
	dir := t.TempDir()
	for i, content := range configCorpus {
		p := filepath.Join(dir, "cfg.jsonc")
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got := LoadConfig(p)
		want := oracle[i]
		if !reflect.DeepEqual(got.Visible, normSlice(want.Visible)) {
			t.Errorf("case %d visible: go=%v py=%v", i, got.Visible, want.Visible)
		}
		if !reflect.DeepEqual(got.Fields, normSlice(want.Fields)) {
			t.Errorf("case %d fields: go=%v py=%v", i, got.Fields, want.Fields)
		}
	}
}

// TestLoadConfigMissingFile: a missing file -> empty visible + DEFAULT fields.
func TestLoadConfigMissingFile(t *testing.T) {
	cfg := LoadConfig(filepath.Join(t.TempDir(), "nope.jsonc"))
	if len(cfg.Visible) != 0 {
		t.Errorf("missing-file visible = %v, want empty", cfg.Visible)
	}
	if !reflect.DeepEqual(cfg.Fields, DefaultFields) {
		t.Errorf("missing-file fields = %v, want DEFAULT", cfg.Fields)
	}
}

func normSlice(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type hpResult struct {
	Visible []string `json:"visible"`
	Fields  []string `json:"fields"`
}

func runHPOracle(t *testing.T, configs []string) []hpResult {
	t.Helper()
	root := repoRoot(t)
	spec, _ := json.Marshal(configs)
	var cmd *exec.Cmd
	if _, err := exec.LookPath("uv"); err == nil {
		cmd = exec.Command("uv", "run", "python", filepath.Join(root, "tools", "parity", "host_processes_oracle.py"))
	} else if _, err := exec.LookPath("python3"); err == nil {
		cmd = exec.Command("python3", filepath.Join(root, "tools", "parity", "host_processes_oracle.py"))
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
	var results []hpResult
	if err := json.Unmarshal(out, &results); err != nil {
		t.Fatalf("decode oracle: %v", err)
	}
	return results
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
