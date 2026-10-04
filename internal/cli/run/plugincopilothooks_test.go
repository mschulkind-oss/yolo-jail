package run

// plugincopilothooks_test.go pins the launch's jail-code line for hooks at GitHub Copilot CLI's
// own default locations: a `hooks.json` at the plugin root, which Copilot reads before
// hooks/hooks.json, and `com.github.copilot/hooks/hooks.json`, where it reads an Agent Plugins
// spec plugin's hooks (pluginpack's components table carries the evidence). Claude Code reads
// neither, so they used to reach no launch line at all.

import (
	"strings"
	"testing"
)

// Hooks at either Copilot location are counted on the pack's line like any other hooks.
func TestTheLaunchCountsHooksAtCopilotsDefaultLocations(t *testing.T) {
	for _, rel := range []string{"hooks.json", "com.github.copilot/hooks/hooks.json"} {
		t.Run(rel, func(t *testing.T) {
			said, _ := jailCodeLineFor(t, map[string]string{
				rel: `{"version":1,"hooks":{"sessionStart":[{"type":"command","bash":"echo hi"}]}}`,
			})
			if !strings.Contains(said, "first-mod: 1 wrapped plugin runs code in the jail — hooks (1)") {
				t.Errorf("hooks at %s, which Copilot runs, reached no jail-code line:\n%s", rel, said)
			}
		})
	}
}

// A hooks module is Claude Code's, and Claude Code reads no root hooks.json: a `modules` entry
// there loads nothing, so the line counts the hooks without saying Claude Code runs a module.
func TestTheLaunchNamesNoHooksModuleInARootHooksFile(t *testing.T) {
	said, _ := jailCodeLineFor(t, map[string]string{
		"hooks.json":        `{"version":1,"hooks":{},"modules":["./register.js"]}`,
		"hooks/register.js": hooksModuleJS,
	})
	if !strings.Contains(said, "first-mod: 1 wrapped plugin runs code in the jail — hooks (1)") {
		t.Fatalf("the root hooks file was not counted:\n%s", said)
	}
	if strings.Contains(said, "hooks module") {
		t.Errorf("the line names a hooks module Claude Code never loads:\n%s", said)
	}
}

// Claude Code reads one manifest, .claude-plugin/plugin.json (measured on 2.1.288; see
// pluginpack's manifestDirs). A hooks file named only by one of the manifests Copilot reads
// instead, at the root, under .plugin/ or under .github/plugin/, loads no hooks module, so the
// line counts the hooks without saying Claude Code runs a module.
func TestTheLaunchNamesNoHooksModuleThroughAManifestClaudeCodeDoesNotRead(t *testing.T) {
	for _, manifest := range []string{"plugin.json", ".plugin/plugin.json", ".github/plugin/plugin.json"} {
		t.Run(manifest, func(t *testing.T) {
			said, _ := jailCodeLineFor(t, map[string]string{
				manifest:      `{"name":"first-mod","hooks":"./h.json"}`,
				"h.json":      `{"hooks":{},"modules":["./register.js"]}`,
				"register.js": hooksModuleJS,
			})
			if !strings.Contains(said, "first-mod: 1 wrapped plugin runs code in the jail — hooks (1)") {
				t.Fatalf("the hooks %s declares were not counted:\n%s", manifest, said)
			}
			if strings.Contains(said, "hooks module") {
				t.Errorf("the line names a hooks module that only %s, which Claude Code never "+
					"reads, points to:\n%s", manifest, said)
			}
		})
	}
}
