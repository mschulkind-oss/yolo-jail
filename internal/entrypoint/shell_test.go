package entrypoint

import (
	"strings"
	"testing"
)

// D6: the MCP npm install must be gated on the ENABLED presets. It used to be
// unconditional — probed on an empty-agent jail, it installed 112 npm packages for
// zero agents and zero configured presets.
func TestBootstrapMCPInstallIsPresetGated(t *testing.T) {
	// No presets: the package list is empty, so the install block is inert.
	e := &Env{Home: t.TempDir(), Vars: map[string]string{}}
	script := BootstrapScript(e)
	if strings.Contains(script, `YOLO_MCP_NPM="chrome-devtools-mcp`) {
		t.Errorf("no presets configured but chrome-devtools-mcp is still installed:\n%s", script)
	}
	if !strings.Contains(script, `YOLO_MCP_NPM=""`) {
		t.Errorf("expected an empty package list with no presets:\n%s", script)
	}

	// One preset: only its package.
	e = &Env{Home: t.TempDir(), Vars: map[string]string{
		"YOLO_MCP_PRESETS": `["sequential-thinking"]`,
	}}
	script = BootstrapScript(e)
	if !strings.Contains(script, "@modelcontextprotocol/server-sequential-thinking") {
		t.Errorf("enabled preset's package missing:\n%s", script)
	}
	// Assert on the INSTALL LIST, not the whole script: the per-package `case`
	// dispatch legitimately names every known preset binary.
	if !strings.Contains(script, `YOLO_MCP_NPM="@modelcontextprotocol/server-sequential-thinking"`) {
		t.Errorf("install list should hold only the enabled preset's package:\n%s", script)
	}

	// Both presets: both packages, in config order.
	e = &Env{Home: t.TempDir(), Vars: map[string]string{
		"YOLO_MCP_PRESETS": `["chrome-devtools","sequential-thinking"]`,
	}}
	script = BootstrapScript(e)
	if !strings.Contains(script, `YOLO_MCP_NPM="chrome-devtools-mcp @modelcontextprotocol/server-sequential-thinking"`) {
		t.Errorf("both presets should install both packages in order:\n%s", script)
	}
}
