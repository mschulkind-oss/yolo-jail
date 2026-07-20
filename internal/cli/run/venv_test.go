package run

import (
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
