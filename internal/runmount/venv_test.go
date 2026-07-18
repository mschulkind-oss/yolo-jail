package runmount

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidPerSideRel(t *testing.T) {
	valid := []string{".venv", "sub/dir", "a/b/c"}
	for _, r := range valid {
		if !ValidPerSideRel(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	invalid := []string{"", ".", "/abs", "../escape", "a/../b", "{{tera}}", "a/{% if %}"}
	for _, r := range invalid {
		if ValidPerSideRel(r) {
			t.Errorf("%q should be invalid", r)
		}
	}
}

func TestMiseConfigVenvPath(t *testing.T) {
	// String form; last-hit-wins.
	resolve := func(fname string) (map[string]any, bool) {
		switch fname {
		case "mise.toml":
			return map[string]any{"env": map[string]any{"_": map[string]any{"python": map[string]any{"venv": "base-venv"}}}}, true
		case "mise.jail.toml":
			return map[string]any{"env": map[string]any{"_": map[string]any{"python": map[string]any{"venv": "jail-venv"}}}}, true
		}
		return nil, false
	}
	if got, ok := MiseConfigVenvPath(resolve); !ok || got != "jail-venv" {
		t.Errorf("last-hit-wins = %q, %v (want jail-venv)", got, ok)
	}
	// Table form: path default .venv, no create needed.
	tbl := func(string) (map[string]any, bool) {
		return map[string]any{"env": map[string]any{"_": map[string]any{"python": map[string]any{"venv": map[string]any{}}}}}, true
	}
	if got, ok := MiseConfigVenvPath(tbl); !ok || got != ".venv" {
		t.Errorf("table default = %q, %v", got, ok)
	}
	// config_root template stripped.
	tera := func(fname string) (map[string]any, bool) {
		if fname == "mise.toml" {
			return map[string]any{"env": map[string]any{"_": map[string]any{"python": map[string]any{"venv": "{{ config_root }}/myvenv"}}}}, true
		}
		return nil, false
	}
	if got, ok := MiseConfigVenvPath(tera); !ok || got != "myvenv" {
		t.Errorf("config_root strip = %q, %v (want myvenv)", got, ok)
	}
	// Absent.
	if _, ok := MiseConfigVenvPath(func(string) (map[string]any, bool) { return nil, false }); ok {
		t.Error("no config => absent")
	}
}

// TestMiseVenvParity byte-diffs against live _mise_config_venv_path over temp
// workspaces. Skips without Python.
func TestMiseVenvParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"string_base", map[string]string{"mise.toml": "[env._.python]\nvenv = \"base-venv\"\n"}},
		{"last_wins", map[string]string{
			"mise.toml":      "[env._.python]\nvenv = \"base-venv\"\n",
			"mise.jail.toml": "[env._.python]\nvenv = \"jail-venv\"\n",
		}},
		{"table_default", map[string]string{"mise.toml": "[env._.python.venv]\ncreate = true\n"}},
		{"table_path", map[string]string{"mise.toml": "[env._.python.venv]\npath = \"custom\"\n"}},
		{"config_root", map[string]string{"mise.toml": "[env._.python]\nvenv = \"{{config_root}}/v\"\n"}},
		{"none", map[string]string{"mise.toml": "[tools]\nnode = \"20\"\n"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for fn, content := range tc.files {
				must(t, os.WriteFile(filepath.Join(dir, fn), []byte(content), 0o644))
			}
			goVal, goOk := MiseConfigVenvPathFromDir(dir)
			script := `
import sys; sys.path.insert(0, 'src')
import json
from pathlib import Path
from cli.run_cmd import _mise_config_venv_path
v = _mise_config_venv_path(Path(sys.argv[1]))
print(json.dumps({"val": v}))
`
			out, err := py("-c", script, dir).Output()
			if err != nil {
				t.Skipf("python failed: %v", err)
			}
			var got struct {
				Val *string `json:"val"`
			}
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			pyOk := got.Val != nil
			pyVal := ""
			if got.Val != nil {
				pyVal = *got.Val
			}
			if goOk != pyOk || goVal != pyVal {
				t.Errorf("go=(%q,%v) py=(%q,%v)", goVal, goOk, pyVal, pyOk)
			}
		})
	}
}
