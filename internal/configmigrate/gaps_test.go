package configmigrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// gapMix is one piecemeal stage of a migration: the config files it holds (relative to the user
// config directory, or under "ws/" the workspace; "{{user}}" and "{{ws}}" stand for the two
// directories), and the --user-layer, if any, under the user config directory.
type gapMix struct {
	name  string
	files map[string]string
	layer string
	// want, when set, is what this stage's consumer reads instead of the case's.
	want string
}

// gapRead is what a launch makes of one stage: the effective config, canonical (each legacy null
// in the spelling the readers give it, config.LowerLegacyNulls, so a null and its TOML removal
// spelling compare as the one fact they are), and what the consumer the gap reaches reads from
// it. It fails the test when the stage does not validate: every stage must launch.
func gapRead(t *testing.T, ws, layer string, semantic func(*jsonx.OrderedMap) string) (string, string) {
	t.Helper()
	var merged *jsonx.OrderedMap
	var err error
	withUserLayer(layer, func() { merged, err = config.LoadConfig(ws, false, func(string) {}) })
	if err != nil {
		t.Fatalf("the config does not load: %v", err)
	}
	if errs, _ := config.ValidateConfig(merged, ws, nil); len(errs) > 0 {
		t.Fatalf("the config does not launch: %v\n%s", errs, jsonx.MustDumpsCompact(merged))
	}
	canonical := jsonx.DeepCopyMap(merged)
	config.LowerLegacyNulls(canonical)
	text, err := config.SnapshotJSON(canonical)
	if err != nil {
		t.Fatal(err)
	}
	return text, semantic(merged)
}

// assertMigrationKeepsRead writes mix, reads it, converts every legacy file with `yolo config
// migrate` (the --user-layer with Request.Layer), and reads it again: the effective config and
// the consumer's read are the same before and after, and the consumer's read is want. The
// migration converts every file: nothing is left "not converted".
func assertMigrationKeepsRead(t *testing.T, mix gapMix, want string, semantic func(*jsonx.OrderedMap) string) {
	t.Helper()
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	expand := func(s string) string {
		return strings.NewReplacer("{{user}}", dir, "{{ws}}", ws).Replace(s)
	}
	for rel, body := range mix.files {
		path := filepath.Join(dir, rel)
		if after, ok := strings.CutPrefix(rel, "ws/"); ok {
			path = filepath.Join(ws, after)
		}
		write(t, path, expand(body))
	}
	layer := ""
	if mix.layer != "" {
		layer = filepath.Join(dir, mix.layer)
	}
	before, beforeSemantic := gapRead(t, ws, layer, semantic)
	if beforeSemantic != want {
		t.Fatalf("the fixture reads %s, want %s", beforeSemantic, want)
	}
	requests := []Request{{UserConfigDir: dir, Workspace: ws}}
	if layer != "" {
		requests = append([]Request{{UserConfigDir: dir, Workspace: ws, Layer: layer}}, requests...)
	}
	for _, req := range requests {
		g, err := Discover(req)
		if err != nil {
			t.Fatalf("Discover: %v", err)
		}
		if len(g.Old()) == 0 {
			continue
		}
		p, err := Plan(g)
		if err != nil {
			t.Fatalf("migrate refused a config that launches: %v", err)
		}
		if len(p.NotConverted) > 0 {
			t.Fatalf("not converted: %+v", p.NotConverted)
		}
		if _, err := Apply(p, ApplyOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if layer != "" {
		layer = strings.TrimSuffix(layer, filepath.Ext(layer)) + ".toml"
	}
	for _, f := range []string{filepath.Join(dir, "config.jsonc"), filepath.Join(ws, "yolo-jail.jsonc")} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s is still a legacy file after the migration", f)
		}
	}
	after, afterSemantic := gapRead(t, ws, layer, semantic)
	if after != before {
		t.Errorf("the converted config reads differently:\n  before %s\n  after  %s", before, after)
	}
	if afterSemantic != beforeSemantic {
		t.Errorf("the converted config's consumer reads %s, before it read %s", afterSemantic, beforeSemantic)
	}
}

// RC-D52: a legacy null that drops what a lower layer wrote has a TOML removal spelling for every
// key whose null reaches a launch: `lsp_servers.<name>` and `install.mise.<tool>` are "off",
// `security.blocked_tools` is an empty list, `active_provider.<agent>` is "native". In every
// piecemeal stage (the null in a legacy include under a TOML workspace config, in a legacy
// workspace config over a TOML user config, and with both legacy) the config launches, reads what
// the null says, and `yolo config migrate` converts it to a TOML config that reads the same. Fails
// if a removal spelling is dropped from ReadableRemovalPolicies (the migration then leaves the
// null "not converted"), or if the read stops respelling the nulls no merge honored before
// (lowerLegacyScalarNulls; the null then refuses the launch).
func TestMigrationKeepsEveryLegacyNullRead(t *testing.T) {
	lsp := func(cfg *jsonx.OrderedMap) string {
		table, _ := cfg.Get("lsp_servers")
		m, _ := table.(*jsonx.OrderedMap)
		var names []string
		if m != nil {
			for _, k := range m.Keys() {
				if v, _ := m.Get(k); v != "off" && v != nil {
					names = append(names, k)
				}
			}
		}
		return strings.Join(names, ",")
	}
	mise := func(cfg *jsonx.OrderedMap) string { return jsonx.MustDumpsCompact(config.MergeMiseTools(cfg)) }
	blocked := func(cfg *jsonx.OrderedMap) string {
		sec, _ := cfg.Get("security")
		m, _ := sec.(*jsonx.OrderedMap)
		return jsonx.MustDumpsCompact(config.NormalizeBlockedTools(m))
	}
	selection := func(cfg *jsonx.OrderedMap) string {
		s := config.ConfigActiveSelection(cfg)
		text, _ := json.Marshal(map[string]any{"default": s.Default, "named": s.Named})
		return string(text)
	}
	const server = `{"command": "x-ls", "fileExtensions": {".x": "x"}}`
	const serverTOML = "command = 'x-ls'\nfileExtensions = { '.x' = 'x' }\n"
	for _, tc := range []struct {
		key      string
		want     string
		semantic func(*jsonx.OrderedMap) string
		mixes    []gapMix
	}{
		{"lsp_servers", "bar", lsp, []gapMix{
			{"user legacy, workspace TOML", map[string]string{
				"config.jsonc":      `{"lsp_servers": {"foo": ` + server + `}, "include_if_found": ["more.jsonc"]}`,
				"more.jsonc":        `{"lsp_servers": {"foo": null}}`,
				"ws/yolo-jail.toml": "[lsp_servers.bar]\n" + serverTOML,
			}, "", ""},
			{"user TOML, workspace legacy", map[string]string{
				"config.toml":        "[lsp_servers.foo]\n" + serverTOML,
				"ws/yolo-jail.jsonc": `{"lsp_servers": {"foo": null, "bar": ` + server + `}}`,
			}, "", ""},
			{"both legacy", map[string]string{
				"config.jsonc":       `{"lsp_servers": {"foo": ` + server + `}}`,
				"ws/yolo-jail.jsonc": `{"lsp_servers": {"foo": null, "bar": ` + server + `}}`,
			}, "", ""},
		}},
		{"mise_tools", `{"go": "1.22"}`, mise, []gapMix{
			{"user legacy, workspace TOML", map[string]string{
				"config.jsonc":      `{"mise_tools": {"node": "22"}, "include_if_found": ["more.jsonc"]}`,
				"more.jsonc":        `{"mise_tools": {"node": null}}`,
				"ws/yolo-jail.toml": "[install.mise]\ngo = '1.22'\n",
			}, "", ""},
			{"user TOML, workspace legacy", map[string]string{
				"config.toml":        "[install.mise]\nnode = '22'\n",
				"ws/yolo-jail.jsonc": `{"mise_tools": {"node": null, "go": "1.22"}}`,
			}, "", ""},
			{"both legacy", map[string]string{
				"config.jsonc":       `{"mise_tools": {"node": "22"}}`,
				"ws/yolo-jail.jsonc": `{"mise_tools": {"node": null, "go": "1.22"}}`,
			}, "", ""},
		}},
		{"security.blocked_tools", `[{"name": "wget"}]`, blocked, []gapMix{
			{"user legacy, workspace TOML", map[string]string{
				"config.jsonc":      `{"security": {"blocked_tools": ["curl"]}, "include_if_found": ["more.jsonc"]}`,
				"more.jsonc":        `{"security": {"blocked_tools": null}}`,
				"ws/yolo-jail.toml": "[security]\nblocked_tools = ['wget']\n",
			}, "", ""},
			{"user TOML, workspace legacy", map[string]string{
				"config.toml":        "[security]\nblocked_tools = ['curl']\n",
				"ws/yolo-jail.jsonc": `{"security": {"blocked_tools": null}}`,
			}, "", "[]"},
			{"both legacy", map[string]string{
				"config.jsonc":       `{"security": {"blocked_tools": ["curl"]}}`,
				"ws/yolo-jail.jsonc": `{"security": {"blocked_tools": null}}`,
			}, "", "[]"},
		}},
		// A user-scope key: the stages are the user config's own files and a --user-layer.
		{"active_provider", `{"default":["anthropic"],"named":{"claude":["native"],"pi":["anthropic"]}}`, selection, []gapMix{
			{"top TOML, include legacy", map[string]string{
				"config.toml": "include_if_found = ['more.jsonc']\n[active_provider]\ndefault = 'anthropic'\nclaude = ['anthropic']\npi = 'anthropic'\n",
				"more.jsonc":  `{"active_provider": {"claude": null}}`,
			}, "", ""},
			{"a legacy --user-layer over a TOML user config", map[string]string{
				"config.toml": "[active_provider]\ndefault = 'anthropic'\nclaude = ['anthropic']\npi = 'anthropic'\n",
				"layer.jsonc": `{"active_provider": {"claude": null}}`,
			}, "layer.jsonc", ""},
			{"both legacy", map[string]string{
				"config.jsonc": `{"active_provider": {"default": "anthropic", "claude": ["anthropic"], "pi": "anthropic"}, "include_if_found": ["more.jsonc"]}`,
				"more.jsonc":   `{"active_provider": {"claude": null}}`,
			}, "", ""},
		}},
	} {
		for _, mix := range tc.mixes {
			t.Run(tc.key+"/"+mix.name, func(t *testing.T) {
				want := tc.want
				if mix.want != "" {
					want = mix.want
				}
				assertMigrationKeepsRead(t, mix, want, tc.semantic)
			})
		}
	}
}

// RC-D53: a legacy network.ports entry bound to an address or a protocol lowers to machine.ports'
// `address` and `protocol`, and renders the same -p text, in every piecemeal stage; `yolo config
// migrate` converts it (it was "not converted" before) to a TOML config that reads the same.
// Fails if the shared lowering (config.LowerLegacyPublishPorts) loses either field, or if migrate
// stops using it.
func TestMigrationKeepsEveryLegacyPortRead(t *testing.T) {
	specs := func(cfg *jsonx.OrderedMap) string {
		machine, _ := cfg.Get("machine")
		m, _ := machine.(*jsonx.OrderedMap)
		var out []string
		if m != nil {
			ports, _ := m.Get("ports")
			list, _ := ports.([]any)
			for _, entry := range list {
				table, _ := entry.(*jsonx.OrderedMap)
				spec, ok := config.PublishPortSpec(table)
				if !ok {
					return "unrenderable"
				}
				out = append(out, spec)
			}
		}
		return strings.Join(out, ",")
	}
	const legacyPorts = `{"network": {"ports": ["127.0.0.1:9000:90", "5353:53/udp", "0.0.0.0:8443:443/tcp"]}}`
	for _, mix := range []gapMix{
		{"user legacy, workspace TOML", map[string]string{
			"config.jsonc":      legacyPorts,
			"ws/yolo-jail.toml": "[[machine.ports]]\nhost = 8000\njail = 80\n",
		}, "", "127.0.0.1:9000:90,5353:53/udp,0.0.0.0:8443:443/tcp,8000:80"},
		{"user TOML, workspace legacy", map[string]string{
			"config.toml":        "[[machine.ports]]\nhost = 8000\njail = 80\n",
			"ws/yolo-jail.jsonc": legacyPorts,
		}, "", "8000:80,127.0.0.1:9000:90,5353:53/udp,0.0.0.0:8443:443/tcp"},
		{"both legacy", map[string]string{
			"config.jsonc":       `{"network": {"ports": ["8000:80"]}}`,
			"ws/yolo-jail.jsonc": legacyPorts,
		}, "", "8000:80,127.0.0.1:9000:90,5353:53/udp,0.0.0.0:8443:443/tcp"},
	} {
		t.Run(mix.name, func(t *testing.T) { assertMigrationKeepsRead(t, mix, mix.want, specs) })
	}
}
