package perside

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestValidRel(t *testing.T) {
	for _, r := range []string{".venv", "sub/dir", "a/b/c", "packages/web/node_modules"} {
		if !ValidRel(r) {
			t.Errorf("%q should be valid", r)
		}
	}
	for _, r := range []string{"", ".", "/abs", "../escape", "a/../b", "a/..", "{{tera}}", "a/{% if %}"} {
		if ValidRel(r) {
			t.Errorf("%q should be invalid", r)
		}
	}
}

func TestMiseConfigVenvPathLastHitWins(t *testing.T) {
	venv := func(v any) map[string]any {
		return map[string]any{"env": map[string]any{"_": map[string]any{"python": map[string]any{"venv": v}}}}
	}
	resolve := func(fname string) (map[string]any, bool) {
		switch fname {
		case "mise.toml":
			return venv("base-venv"), true
		case "mise.jail.toml":
			return venv("jail-venv"), true
		}
		return nil, false
	}
	if got, ok := MiseConfigVenvPath(resolve); !ok || got != "jail-venv" {
		t.Errorf("last-hit-wins = %q, %v (want jail-venv)", got, ok)
	}
	// A table without `path` is the default .venv, with no create requirement.
	if got, ok := MiseConfigVenvPath(func(string) (map[string]any, bool) {
		return venv(map[string]any{}), true
	}); !ok || got != ".venv" {
		t.Errorf("table default = %q, %v", got, ok)
	}
	// The {{config_root}}/ template is stripped; nothing else is.
	if got, ok := MiseConfigVenvPath(func(fname string) (map[string]any, bool) {
		if fname == "mise.toml" {
			return venv("{{ config_root }}/myvenv"), true
		}
		return nil, false
	}); !ok || got != "myvenv" {
		t.Errorf("config_root strip = %q, %v (want myvenv)", got, ok)
	}
	if _, ok := MiseConfigVenvPath(func(string) (map[string]any, bool) { return nil, false }); ok {
		t.Error("no config must read as absent")
	}
}

// The set every backend reads: the two fixed defaults, plus a venv path the workspace's own
// mise config declares, read from the real file.
func TestDefaultRelsReadTheWorkspaceMiseConfig(t *testing.T) {
	ws := t.TempDir()
	if got, want := DefaultRels(ws), []string{".venv", "node_modules"}; !slices.Equal(got, want) {
		t.Errorf("DefaultRels(empty workspace) = %v, want %v", got, want)
	}
	if err := os.WriteFile(filepath.Join(ws, "mise.toml"),
		[]byte("[env]\n_.python.venv = \"py/env\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := DefaultRels(ws), []string{".venv", "node_modules", "py/env"}; !slices.Equal(got, want) {
		t.Errorf("DefaultRels(mise venv declared) = %v, want %v", got, want)
	}
}

// The default set is a floor: config entries join it, a duplicate is not repeated, and a
// non-string entry is dropped rather than coerced.
func TestShadowCandidatesUnionDefaultsWithTheConfig(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set("per_side_paths", []any{"packages/web/node_modules", "node_modules", 7, "build"})
	got := ShadowCandidates(cfg, t.TempDir())
	want := []string{".venv", "build", "node_modules", "packages/web/node_modules"}
	if !slices.Equal(got, want) {
		t.Errorf("ShadowCandidates = %v, want %v", got, want)
	}
	if got := UserRels(cfg); !slices.Equal(got, []string{"packages/web/node_modules", "node_modules", "build"}) {
		t.Errorf("UserRels = %v: it must keep the user's order and drop the non-string entry", got)
	}
	if got := UserRels(nil); got != nil {
		t.Errorf("UserRels(nil) = %v, want nil", got)
	}
}
