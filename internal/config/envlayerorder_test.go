package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// envSpelling writes one layer's ordered env list in one of the three spellings a file may use.
type envSpelling string

const (
	spellLegacy  envSpelling = "legacy env_sources"
	spellSources envSpelling = "env.sources"
	spellFiles   envSpelling = "env.files"
)

// envLayerBody is a config file holding entries (paths, or "NAME=value" for an inline table) in
// spelling, and the file name it goes under (name plus the spelling's extension).
func envLayerBody(t *testing.T, spelling envSpelling, entries []string) (ext, body string) {
	t.Helper()
	var legacy, toml []string
	for _, e := range entries {
		if name, value, inline := strings.Cut(e, "="); inline {
			legacy = append(legacy, fmt.Sprintf("{%q: %q}", name, value))
			toml = append(toml, fmt.Sprintf("{ set = { %s = %q } }", name, value))
			continue
		}
		legacy = append(legacy, fmt.Sprintf("%q", e))
		toml = append(toml, fmt.Sprintf("%q", e))
	}
	switch spelling {
	case spellLegacy:
		return ".jsonc", `{"env_sources": [` + strings.Join(legacy, ", ") + "]}\n"
	case spellSources:
		return ".toml", "[env]\nsources = [" + strings.Join(toml, ", ") + "]\n"
	default:
		return ".toml", "[env]\nfiles = [" + strings.Join(toml, ", ") + "]\n"
	}
}

// envOrder is the effective env walk of cfg: each path's base name, each inline table as
// NAME=value.
func envOrder(cfg *jsonx.OrderedMap) []string {
	var out []string
	for _, e := range EnvEntries(cfg) {
		switch e := e.(type) {
		case string:
			out = append(out, filepath.Base(e))
		case *jsonx.OrderedMap:
			for _, k := range e.Keys() {
				v, _ := e.Get(k)
				out = append(out, fmt.Sprintf("%s=%v", k, v))
			}
		}
	}
	return out
}

// RC-D50: the env walk is per layer, in layer order (the user config, the --user-layer, the
// workspace config), whichever spelling each layer used, so a piecemeal migration cannot reorder
// anything. Every combination of spellings across the three layers reads in one order; with an
// inline table in each layer, every combination env.files cannot carry reads in that order too.
func TestEnvWalkIsPerLayerWhateverEachLayerSpells(t *testing.T) {
	t.Run("a legacy null resets the layers beneath", testLegacyEnvSourcesNullResets)
	all := []envSpelling{spellLegacy, spellSources, spellFiles}
	inline := []envSpelling{spellLegacy, spellSources}
	for _, shape := range []struct {
		name      string
		spellings [3][]envSpelling
		entries   [3][]string
		want      []string
	}{
		{"paths", [3][]envSpelling{all, all, all},
			[3][]string{{"u1.env", "u2.env"}, {"l.env"}, {"w.env"}},
			[]string{"u1.env", "u2.env", "l.env", "w.env"}},
		{"inline tables", [3][]envSpelling{inline, inline, inline},
			[3][]string{{"U=1", "u.env"}, {"l.env", "L=1"}, {"W=1", "w.env"}},
			[]string{"U=1", "u.env", "l.env", "L=1", "W=1", "w.env"}},
		// Only the layer holds an inline table, so the layers either side of it may be spelled
		// env.files, which the merge must join to env.sources in layer order.
		{"an inline table between path layers", [3][]envSpelling{all, inline, all},
			[3][]string{{"u.env"}, {"L=1", "l.env"}, {"w.env"}},
			[]string{"u.env", "L=1", "l.env", "w.env"}},
	} {
		for _, user := range shape.spellings[0] {
			for _, layer := range shape.spellings[1] {
				for _, ws := range shape.spellings[2] {
					t.Run(fmt.Sprintf("%s/user %s, layer %s, workspace %s", shape.name, user, layer, ws), func(t *testing.T) {
						home := layerHome(t, "")
						dir := filepath.Join(home, ".config", "yolo-jail")
						abs := func(entries []string) []string {
							out := make([]string, len(entries))
							for i, e := range entries {
								if strings.Contains(e, "=") {
									out[i] = e
								} else {
									out[i] = filepath.Join(dir, e)
								}
							}
							return out
						}
						ext, body := envLayerBody(t, user, abs(shape.entries[0]))
						userName := paths.UserConfigTOMLName
						if ext == ".jsonc" {
							userName = paths.UserConfigLegacyJSONCName
						}
						write(t, filepath.Join(dir, userName), body)
						ext, body = envLayerBody(t, layer, abs(shape.entries[1]))
						layerPath := filepath.Join(dir, "layer"+ext)
						write(t, layerPath, body)
						t.Setenv(UserLayerEnv, layerPath)
						workspace := t.TempDir()
						ext, body = envLayerBody(t, ws, abs(shape.entries[2]))
						write(t, filepath.Join(workspace, "yolo-jail"+ext), body)

						cfg, err := LoadConfig(workspace, true, func(string) {})
						if err != nil {
							t.Fatal(err)
						}
						if got := envOrder(cfg); !slices.Equal(got, shape.want) {
							t.Errorf("env walk = %v, want %v", got, shape.want)
						}
					})
				}
			}
		}
	}
}

// A legacy env_sources null replaced the list beneath it in the legacy merge, and still does
// whatever the layers beneath spell: the --user-layer's null drops the user config's list, and
// the workspace's list after it reads.
func testLegacyEnvSourcesNullResets(t *testing.T) {
	for _, user := range []envSpelling{spellLegacy, spellSources, spellFiles} {
		t.Run(string(user), func(t *testing.T) {
			home := layerHome(t, "")
			dir := filepath.Join(home, ".config", "yolo-jail")
			ext, body := envLayerBody(t, user, []string{filepath.Join(dir, "u.env")})
			userName := paths.UserConfigTOMLName
			if ext == ".jsonc" {
				userName = paths.UserConfigLegacyJSONCName
			}
			write(t, filepath.Join(dir, userName), body)
			layerPath := filepath.Join(dir, "layer.jsonc")
			write(t, layerPath, `{"env_sources": null}`+"\n")
			t.Setenv(UserLayerEnv, layerPath)
			workspace := t.TempDir()
			write(t, filepath.Join(workspace, "yolo-jail.toml"), "[env]\nfiles = ['"+filepath.Join(dir, "w.env")+"']\n")
			cfg, err := LoadConfig(workspace, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := envOrder(cfg), []string{"w.env"}; !slices.Equal(got, want) {
				t.Errorf("env walk = %v, want %v", got, want)
			}
		})
	}
}
