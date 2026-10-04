package pluginpack

// defaultlocations_test.go pins that a component is reported where CLAUDE CODE finds it, not
// only where the manifest names it. Claude Code's plugin reference gives every component a
// default location "used when the manifest doesn't point elsewhere", and a manifest is optional
// (https://code.claude.com/docs/en/plugins-reference, "Standard layout" and "How each key
// combines with its default location"). So a manifest-only reading misses code that runs, and
// every disclosure built on Components() inherits the miss.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

// THE HOLE: hooks/hooks.json (a command hook and a hooks module), .mcp.json, a monitors file, a
// bin/ executable and a workflow script, with a manifest that declares none of them. Each one runs
// code once Claude Code loads the plugin, and each must be reported as code-running.
func TestComponentsAtDefaultLocationsAreReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteDefaultLocationPlugin(t, dir, "acme-tools")
	p, ok := Load(dir)
	if !ok {
		t.Fatal("the fixture is not plugin-shaped")
	}

	got := map[string]Component{}
	for _, c := range p.Components() {
		got[c.Name] = c
	}
	for name, where := range map[string]string{
		"hooks": "hooks/hooks.json", "mcpServers": ".mcp.json",
		"monitors": "monitors/monitors.json", "bin": "bin", "workflows": "workflows",
	} {
		c, ok := got[name]
		if !ok {
			t.Errorf("%s at its default location (%s) was not reported. Claude Code loads it "+
				"with no manifest entry, so it runs code yolo never discloses. Got: %+v",
				name, where, p.Components())
			continue
		}
		if !c.RunsCode {
			t.Errorf("%s at %s reports RunsCode=false — it starts a process or runs a script",
				name, where)
		}
		if len(c.Sources) == 0 || c.Sources[len(c.Sources)-1] != where {
			t.Errorf("%s Sources = %v, want it to name %s", name, c.Sources, where)
		}
	}
	if !p.RunsCode() {
		t.Error("a plugin carrying hooks, MCP servers, monitors and executables must report RunsCode")
	}
}

// EVERY DEFAULT LOCATION CLAUDE CODE LOADS is reported, under the name yolo uses for it and with
// that location as its source, code and prose alike: a flat delivery refuses exactly what this
// reports, so a location missing here is a component that disappears from a flat skills dir with
// no word. workflows/, themes/ and a settings.json `agent` were the ones missing.
func TestEveryDefaultLocationIsReported(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteEveryDefaultLocationPlugin(t, dir, "acme-tools")
	p, ok := Load(dir)
	if !ok {
		t.Fatal("the fixture is not plugin-shaped")
	}
	got := map[string]Component{}
	for _, c := range p.Components() {
		got[c.Name] = c
	}
	// Which of them run code: a process, a command or a script started on the user's behalf.
	runs := map[string]bool{"hooks": true, "mcpServers": true, "lspServers": true,
		"monitors": true, "bin": true, "workflows": true, "subagentStatusLine": true}
	for name, where := range pluginpacktest.EveryDefaultLocation {
		c, ok := got[name]
		if !ok {
			t.Errorf("%s at its default location (%s) was not reported, though Claude Code loads "+
				"it with no manifest entry. Got: %+v", name, where, p.Components())
			continue
		}
		if c.RunsCode != runs[name] {
			t.Errorf("%s reports RunsCode=%v, want %v", name, c.RunsCode, runs[name])
		}
		if !slices.Equal(c.Sources, []string{where}) {
			t.Errorf("%s Sources = %v, want only %s", name, c.Sources, where)
		}
	}
}

// ONE COMPONENT NAMED BOTH WAYS IS ONE COMPONENT. hooks, mcpServers and lspServers merge their
// default file with the manifest's declaration; monitors (like commands) REPLACES its default.
// Either way the plugin has one hooks component, and a count of two would make the launch line
// say "hooks (2)" of one plugin.
func TestComponentNamedBothWaysIsReportedOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme-tools")
	pluginpacktest.WriteDefaultLocationPlugin(t, dir, "acme-tools")
	man := `{"name":"acme-tools","hooks":{"PreToolUse":[]},"mcpServers":"./.mcp.json",` +
		`"experimental":{"monitors":"./monitors/monitors.json"}}`
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"),
		[]byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := Load(dir)

	seen := map[string]int{}
	for _, c := range p.Components() {
		seen[c.Name]++
		if c.Name == "hooks" {
			want := []string{".claude-plugin/plugin.json", "hooks/hooks.json"}
			if len(c.Sources) != 2 || c.Sources[0] != want[0] || c.Sources[1] != want[1] {
				t.Errorf("hooks Sources = %v, want %v: the manifest first, then the default", c.Sources, want)
			}
		}
	}
	for _, name := range []string{"hooks", "mcpServers", "monitors", "bin", "workflows"} {
		if seen[name] != 1 {
			t.Errorf("%s reported %d times, want once", name, seen[name])
		}
	}
}

// The manifest's monitors key is read in BOTH spellings Claude Code loads: `experimental.monitors`,
// the current one, and the top-level `monitors` it still accepts with a validate warning.
// pluginpack.Manifest read neither.
func TestDeclaredMonitorsAreCode(t *testing.T) {
	for _, man := range []string{
		`{"name":"p","experimental":{"monitors":[{"name":"m","command":"x","description":"d"}]}}`,
		`{"name":"p","monitors":"./config/monitors.json"}`,
	} {
		p, _ := Load(writePlugin(t, t.TempDir(), "p", man))
		var found bool
		for _, c := range p.Components() {
			if c.Name == "monitors" && c.RunsCode {
				found = true
			}
		}
		if !found {
			t.Errorf("manifest %s declares monitors and none was reported as code: %+v",
				man, p.Components())
		}
	}
}

// A default LSP config file is the same class as a default MCP one: Claude Code loads
// `.lsp.json` at the plugin root first, then the manifest's lspServers.
func TestDefaultLSPConfigIsCode(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p"}`)
	if err := os.WriteFile(filepath.Join(dir, ".lsp.json"),
		[]byte(`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := Load(dir)
	if !p.RunsCode() {
		t.Errorf("a root .lsp.json starts a language server, yet RunsCode is false: %+v", p.Components())
	}
}

// THE CONTROL: what Claude Code does NOT load stays unreported, or the marker stops meaning
// anything. A hooks/ directory holding some other file, an empty bin/, and a monitors/ directory
// without monitors.json are all inert.
func TestInertLookalikesAreNotReported(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p"}`)
	for _, rel := range []string{"hooks/pre-tool-use.json", "monitors/README.md"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, _ := Load(dir)
	if comps := p.Components(); len(comps) != 0 {
		t.Errorf("inert lookalikes were reported as components: %+v", comps)
	}
}

// The default DIRECTORIES of the prose components are reported too, as content: a commands/
// directory loads as slash commands with no manifest entry.
func TestDefaultProseDirsAreReportedAsContent(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p"}`)
	for _, rel := range []string{"commands/status.md", "agents/reviewer.md", "output-styles/terse.md",
		"themes/dusk.json"} {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := Load(dir)
	got := map[string]bool{}
	for _, c := range p.Components() {
		got[c.Name] = c.RunsCode
	}
	for _, name := range []string{"commands", "agents", "outputStyles", "themes"} {
		runs, ok := got[name]
		if !ok {
			t.Errorf("%s at its default directory was not reported: %+v", name, p.Components())
		}
		if runs {
			t.Errorf("%s is prose, yet reports RunsCode", name)
		}
	}
	if p.RunsCode() {
		t.Error("a plugin of commands, agents, output styles and themes runs no code")
	}
}

// The manifest keys Claude Code loads beside its default directories: `themes` (and
// `experimental.themes`, which it reads first), `workflows`, and `experimental.syntaxHighlighting`,
// whose highlight.js grammars are JavaScript functions Claude Code calls in its own process. Like
// `commands`, a themes or workflows field REPLACES its default directory, so once the only
// manifest sets the field the default directory is no longer a source.
func TestDeclaredThemesWorkflowsAndGrammarsAreReported(t *testing.T) {
	cases := []struct {
		manifest, name string
		runs           bool
	}{
		{`{"name":"p","themes":"./looks"}`, "themes", false},
		{`{"name":"p","experimental":{"themes":["./looks"]}}`, "themes", false},
		{`{"name":"p","workflows":"./flows/review.js"}`, "workflows", true},
		{`{"name":"p","experimental":{"syntaxHighlighting":{"hljsLanguages":[` +
			`{"id":"acme","remote":"npm:acme-hljs@1.0.0"}]}}}`, "syntaxHighlighting", true},
	}
	for _, tc := range cases {
		dir := writePlugin(t, t.TempDir(), "p", tc.manifest)
		for _, rel := range []string{"themes/dusk.json", "workflows/default.js"} {
			path := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		p, _ := Load(dir)
		c, found := componentByName(p, tc.name)
		if !found {
			t.Errorf("manifest %s declares %s and it was not reported: %+v", tc.manifest, tc.name,
				p.Components())
			continue
		}
		if c.RunsCode != tc.runs {
			t.Errorf("manifest %s: %s RunsCode=%v, want %v", tc.manifest, tc.name, c.RunsCode, tc.runs)
		}
		if !slices.Equal(c.Sources, []string{".claude-plugin/plugin.json"}) {
			t.Errorf("manifest %s: %s Sources = %v, want the manifest alone (the field replaces "+
				"the default directory)", tc.manifest, tc.name, c.Sources)
		}
	}
}
