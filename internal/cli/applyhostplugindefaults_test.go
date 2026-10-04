package cli

// applyhostplugindefaults_test.go pins `yolo host apply`'s delivery lines for a wrapped plugin
// whose code sits at Claude Code's DEFAULT locations with no manifest entry. A namespaced
// delivery copies the tree whole, so hooks/hooks.json, .mcp.json, monitors/monitors.json, bin/
// and workflows/ all land in the real home, and Claude Code loads each one whatever the manifest
// says (https://code.claude.com/docs/en/plugins-reference, "Standard layout").
//
// Every home is a t.TempDir() with XDG_CONFIG_HOME inside it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack/pluginpacktest"
)

func TestApplyHostNamesDefaultLocationPluginCode(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(t.TempDir(), "wrapper")
	writeFile(t, filepath.Join(packDir, "pack.json"),
		`{"name":"wrapper","description":"d","skills_tier":"namespaced","contributes":[`+
			`{"kind":"skills","from":"skills","into":".claude/skills"}]}`)
	pluginpacktest.WriteDefaultLocationPlugin(t, filepath.Join(packDir, "skills", "acme-tools"), "acme-tools")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+packDir+`","name":"wrapper"}]}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	// The per-component line is the --verbose view's, like every per-entry skills line.
	verboseReport(t)

	for _, write := range []bool{false, true} {
		rc, report := applyWith(t, write, strings.NewReader("y\n"))
		if rc != 0 {
			t.Fatalf("host apply (write=%v) rc=%d\n%s", write, rc, report)
		}
		for _, comp := range []string{"hooks", "mcpServers", "monitors", "bin", "workflows"} {
			name := "acme-tools:" + comp
			if !strings.Contains(report, name) {
				t.Errorf("host apply (write=%v) delivered the plugin with no %s line, though "+
					"its %s sits at Claude Code's default location and runs code once the "+
					"tool loads the plugin:\n%s", write, name, comp, report)
			}
		}
	}
	// And the code really did land, so the lines above describe the home.
	for _, rel := range []string{"hooks/hooks.json", ".mcp.json", "monitors/monitors.json", "bin/acme-tool",
		"workflows/review.js"} {
		if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "acme-tools",
			filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s did not arrive with the plugin tree: %v", rel, err)
		}
	}
}
