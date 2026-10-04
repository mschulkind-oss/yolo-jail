package pluginpack

// copilothooks_test.go pins that a plugin's hooks are reported where GITHUB COPILOT CLI loads
// them, not only where Claude Code does. Copilot reads Claude-format plugins too, and its plugin
// loader (read in the shipped Copilot CLI 1.0.91; the evidence is beside the components table)
// takes a plugin's hooks from the manifest's `hooks`, else from a `hooks.json` at the plugin ROOT,
// else from `hooks/hooks.json`, and, for a plugin whose manifest names an Agent Plugins `$schema`,
// from `com.github.copilot/hooks/hooks.json` alone. Claude Code reads neither Copilot-only file, so
// a reading that followed Claude Code's layout alone disclosed nothing for hooks Copilot runs.

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile writes body at rel inside dir, creating its parents.
func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// copilotHooks is a hooks file in Copilot's own shape, one command hook.
const copilotHooks = `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":"echo started"}]}}`

// hooksComponent returns the plugin's hooks component, or nil when none is reported.
func hooksComponent(p *Plugin) *Component {
	for _, c := range p.Components() {
		if c.Name == "hooks" {
			return &c
		}
	}
	return nil
}

// THE HOLE: hooks at either of Copilot's own default locations, with a manifest that declares
// none, must be reported as code-running, naming the file.
func TestHooksAtCopilotsDefaultLocationsAreReported(t *testing.T) {
	for _, tc := range []struct {
		rel, manifestDir, manifest string
	}{
		// A Claude-format plugin: Copilot reads a root hooks.json before hooks/hooks.json.
		{"hooks.json", PreferredManifestDir, `{"name":"p"}`},
		// An Agent Plugins spec plugin: Copilot reads its hooks under com.github.copilot/ only.
		{"com.github.copilot/hooks/hooks.json", ".plugin",
			`{"$schema":"https://agent-plugins.org/schemas/1.1.0/plugin.schema.json","name":"p"}`},
	} {
		t.Run(tc.rel, func(t *testing.T) {
			dir := writePluginAt(t, t.TempDir(), "p", tc.manifestDir, tc.manifest)
			writeFile(t, dir, tc.rel, copilotHooks)
			p, ok := Load(dir)
			if !ok {
				t.Fatal("the fixture is not plugin-shaped")
			}
			hooks := hooksComponent(p)
			if hooks == nil {
				t.Fatalf("hooks at %s were not reported. Copilot loads them with no manifest "+
					"entry, so they run code yolo never discloses. Got: %+v", tc.rel, p.Components())
			}
			if !hooks.RunsCode {
				t.Errorf("hooks at %s report RunsCode=false", tc.rel)
			}
			if len(hooks.Sources) != 1 || hooks.Sources[0] != tc.rel {
				t.Errorf("hooks Sources = %v, want [%s]", hooks.Sources, tc.rel)
			}
			if !p.RunsCode() {
				t.Errorf("a plugin carrying hooks at %s must report RunsCode", tc.rel)
			}
		})
	}
}

// A root hooks.json beside hooks/hooks.json is ONE hooks component with both sources, Claude
// Code's location first. Copilot takes the first of the two that loads and Claude Code reads only
// hooks/hooks.json, so between them both files can run.
func TestBothDefaultHooksFilesAreOneComponent(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p"}`)
	writeFile(t, dir, "hooks.json", copilotHooks)
	writeFile(t, dir, "hooks/hooks.json", `{"hooks":{"SessionStart":[{"hooks":[`+
		`{"type":"command","command":"echo started"}]}]}}`)
	p, _ := Load(dir)
	seen := 0
	for _, c := range p.Components() {
		if c.Name == "hooks" {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("hooks reported %d times, want once: %+v", seen, p.Components())
	}
	want := []string{"hooks/hooks.json", "hooks.json"}
	if got := hooksComponent(p).Sources; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("hooks Sources = %v, want %v", got, want)
	}
}

// THE CONTROL: Copilot reads a root hooks.json only when the manifest's `hooks` does not load,
// and Claude Code never reads it. So when every manifest declares `hooks`, the root file runs
// nowhere and is not named; the manifest still reports the component.
func TestRootHooksFileGivesWayToADeclaredHooksField(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p","hooks":"./config/hooks.json"}`)
	writeFile(t, dir, "config/hooks.json", copilotHooks)
	writeFile(t, dir, "hooks.json", copilotHooks)
	p, _ := Load(dir)
	hooks := hooksComponent(p)
	if hooks == nil {
		t.Fatalf("the manifest's hooks were not reported: %+v", p.Components())
	}
	if len(hooks.Sources) != 1 || hooks.Sources[0] != ".claude-plugin/plugin.json" {
		t.Errorf("hooks Sources = %v, want only the manifest: a root hooks.json gives way to a "+
			"declared hooks field", hooks.Sources)
	}
}

// Copilot's two locations are plugin machinery, so a flat delivery of a plugin whose root is
// itself a skill keeps them out of the copy, as it does hooks/.
func TestComponentPathsExcludeCopilotsHooksLocations(t *testing.T) {
	dir := writePlugin(t, t.TempDir(), "p", `{"name":"p","skills":["./"]}`)
	writeFile(t, dir, "hooks.json", copilotHooks)
	writeFile(t, dir, "com.github.copilot/hooks/hooks.json", copilotHooks)
	p, _ := Load(dir)
	excluded := map[string]bool{}
	for _, path := range p.ComponentPaths() {
		excluded[path] = true
	}
	for _, rel := range []string{"hooks.json", "com.github.copilot"} {
		if !excluded[filepath.Join(p.Dir, rel)] {
			t.Errorf("%s is not among the plugin's machinery paths, so a flat copy of the plugin "+
				"root would carry it: %v", rel, p.ComponentPaths())
		}
	}
}
