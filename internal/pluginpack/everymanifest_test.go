package pluginpack

// everymanifest_test.go pins that a component is reported when ANY manifest a tool reads
// declares it, not only the first one yolo happens to find, and that a plugin's settings are
// read for the one setting that runs a command.
//
// The tools disagree about which manifest is THE manifest. Claude Code reads
// `.claude-plugin/plugin.json` first and stops there, stripping a leading UTF-8 byte order mark
// (measured on 2.1.288: `claude plugin validate` and `claude plugin details` over a
// ~/.claude/skills plugin). Copilot searches `.plugin`, the root, `.github/plugin` and then
// `.claude-plugin`, taking the first that parses. manifestDirs is Copilot's order, so a tree
// whose root also carries a plugin.json had its `.claude-plugin` manifest, the one Claude Code
// runs, read by nobody in yolo.

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const inlineHooks = `"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]}`

func componentByName(p *Plugin, name string) (Component, bool) {
	for _, c := range p.Components() {
		if c.Name == name {
			return c, true
		}
	}
	return Component{}, false
}

// THE HOLE: a second plugin.json that declares nothing, at a location yolo searches before
// `.claude-plugin/`, hid the inline hooks and MCP server of the manifest Claude Code loads.
func TestComponentsOfEveryManifestAreReported(t *testing.T) {
	for _, decoyDir := range []string{".", ".plugin", ".github/plugin"} {
		t.Run(decoyDir, func(t *testing.T) {
			dir := writePlugin(t, t.TempDir(), "p",
				`{"name":"p",`+inlineHooks+`,"mcpServers":{"db":{"command":"node"}}}`)
			decoy := filepath.Join(dir, filepath.FromSlash(decoyDir), "plugin.json")
			if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(decoy, []byte(`{"name":"p"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			p, ok := Load(dir)
			if !ok {
				t.Fatal("a tree with two manifests is not plugin-shaped")
			}
			for _, name := range []string{"hooks", "mcpServers"} {
				c, found := componentByName(p, name)
				if !found {
					t.Errorf("%s declared inline in .claude-plugin/plugin.json was not reported "+
						"once %s/plugin.json also exists. Claude Code loads that manifest and runs "+
						"it. Got: %+v", name, decoyDir, p.Components())
					continue
				}
				if !slices.Contains(c.Sources, ".claude-plugin/plugin.json") {
					t.Errorf("%s Sources = %v, want it to name .claude-plugin/plugin.json", name, c.Sources)
				}
			}
		})
	}
}

// A component declared by two manifests is still one component.
func TestComponentDeclaredByTwoManifestsIsReportedOnce(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p",`+inlineHooks+`}`)
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"),
		[]byte(`{"name":"p",`+inlineHooks+`}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, _ := Load(dir)
	n := 0
	for _, c := range p.Components() {
		if c.Name == "hooks" {
			n++
			if len(c.Sources) != 2 {
				t.Errorf("hooks Sources = %v, want both manifests", c.Sources)
			}
		}
	}
	if n != 1 {
		t.Errorf("hooks reported %d times, want once: %+v", n, p.Components())
	}
}

// A manifest yolo cannot parse does not hide one it can: Copilot skips an unparseable manifest
// and reads the next, and Claude Code never reads the root one.
func TestAnUnparseableManifestDoesNotHideTheNext(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p",`+inlineHooks+`}`)
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := Load(dir)
	if !ok {
		t.Fatal("a broken root plugin.json made the tree not a plugin, though Claude Code loads " +
			"its .claude-plugin manifest. Delivered as ordinary content, the tree reaches a flat " +
			"skills dir whole, where Claude Code adopts it and runs its hooks")
	}
	if got := p.ManifestRel(); got != ".claude-plugin/plugin.json" {
		t.Errorf("ManifestRel() = %q, want the manifest that parsed", got)
	}
	if !p.RunsCode() {
		t.Errorf("the .claude-plugin manifest's hooks were not reported: %+v", p.Components())
	}
}

// Claude Code strips a leading UTF-8 byte order mark before parsing a manifest, so a manifest
// that starts with one is a plugin.
func TestAManifestWithAByteOrderMarkIsAPlugin(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", "\ufeff"+`{"name":"p",`+inlineHooks+`}`)
	p, ok := Load(dir)
	if !ok {
		t.Fatal("a manifest with a byte order mark is not a plugin to yolo, yet Claude Code " +
			"2.1.288 loads it and runs its hooks")
	}
	if p.Name() != "p" || !p.RunsCode() {
		t.Errorf("Name() = %q, RunsCode() = %v; want p and true", p.Name(), p.RunsCode())
	}
}

// subagentStatusLine is a shell command Claude Code runs to draw each subagent's row in the
// agent panel, and a plugin may set it in a root settings.json or in its manifest's `settings`
// (Claude Code's plugin reference, "Default settings"). `agent`, the other setting a plugin may
// set, runs nothing.
func TestSubagentStatusLineIsCode(t *testing.T) {
	const line = `{"subagentStatusLine":{"type":"command","command":"./status.sh"}}`
	cases := []struct {
		name, manifest, settings string
		want                     bool
		source                   string
	}{
		{"settings.json", `{"name":"p"}`, line, true, "settings.json"},
		{"manifest settings", `{"name":"p","settings":` + line + `}`, "", true, ".claude-plugin/plugin.json"},
		{"agent only", `{"name":"p","settings":{"agent":"reviewer"}}`, `{"agent":"reviewer"}`, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePlugin(t, t.TempDir(), "p", tc.manifest)
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"),
					[]byte(tc.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p, _ := Load(dir)
			c, found := componentByName(p, "subagentStatusLine")
			if found != tc.want {
				t.Fatalf("subagentStatusLine reported = %v, want %v: %+v", found, tc.want, p.Components())
			}
			if !tc.want {
				return
			}
			if !c.RunsCode || !slices.Contains(c.Sources, tc.source) {
				t.Errorf("got %+v, want RunsCode from %s", c, tc.source)
			}
		})
	}
}

// `agent` is the other setting Claude Code keeps from a plugin's settings (2.1.289 keeps exactly
// ["agent","subagentStatusLine"]): it names an agent "to use for the main thread", applying that
// agent's system prompt, tool restrictions and model. It runs nothing, but it changes every
// session the plugin is enabled in, so a flat delivery that cannot carry it must say so, and it
// can say so only of a component Components reports.
func TestMainSessionAgentSettingIsReported(t *testing.T) {
	cases := []struct {
		name, manifest, settings, source string
	}{
		{"settings.json", `{"name":"p"}`, `{"agent":"reviewer"}`, "settings.json"},
		{"manifest settings", `{"name":"p","settings":{"agent":"reviewer"}}`, "", ".claude-plugin/plugin.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := writePlugin(t, t.TempDir(), "p", tc.manifest)
			if tc.settings != "" {
				if err := os.WriteFile(filepath.Join(dir, "settings.json"),
					[]byte(tc.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			p, _ := Load(dir)
			c, found := componentByName(p, "agent")
			if !found {
				t.Fatalf("a plugin setting the main session's agent reported no agent component: %+v",
					p.Components())
			}
			if c.RunsCode || !slices.Equal(c.Sources, []string{tc.source}) {
				t.Errorf("got %+v, want prose from %s alone", c, tc.source)
			}
		})
	}
}
