package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// newClaudePrismEnv builds an Env with a fake jail home, a fake /ctx/host-claude
// mount (via the overridable hostClaudeDir), and a writable workspace (for the
// .yolo/prism sidecars). vars carries the YOLO_* knobs (YOLO_LSP_SERVERS,
// YOLO_MCP_SERVERS). Returns the env and the host-mount dir so a test can seed a
// host settings.json, which the prism reads unconditionally (fail-open — there
// is no host_claude_files allow-list any more, plan §10.4).
func newClaudePrismEnv(t *testing.T, vars map[string]string) (*Env, string) {
	t.Helper()
	home := t.TempDir()
	ctx := t.TempDir()
	ws := t.TempDir()

	orig := hostClaudeDir
	hostClaudeDir = ctx
	t.Cleanup(func() { hostClaudeDir = orig })

	if vars == nil {
		vars = map[string]string{}
	}
	e := &Env{Home: home, Workspace: ws, Vars: vars}
	return e, ctx
}

// TestConfigureClaudePrismFirstMigration is the claude proof-of-parity for the
// computed layer: on the FIRST prism boot (no last_render sidecar) settings.json
// converges to the fresh engine render — the STATIC managed permissions block is
// forced, the DYNAMIC enabledPlugins toggle for a present LSP is true, the
// computed env.ENABLE_LSP_TOOL is "1", and the host-only mcpServers block is
// stripped by the computed tombstone. The overlay seeds empty, the obsolete
// yolo-host-synced-settings.json snapshot (§4.7 orphan) is deleted, and the
// bespoke .claude.json is still written (writeClaudeJSON ran) with the config
// MCP server present.
func TestConfigureClaudePrismFirstMigration(t *testing.T) {
	e, _ := newClaudePrismEnv(t, map[string]string{
		"YOLO_LSP_SERVERS": `{"python":{}}`,
		"YOLO_MCP_SERVERS": `{"myserver":{"command":"foo"}}`,
	})

	// A pre-existing three-way-merge snapshot the bespoke path used to write.
	if err := os.MkdirAll(e.ClaudeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotPath := e.ClaudeHostSettingsSnapshotPath()
	if err := os.WriteFile(snapshotPath, []byte(`{"theme":"dark"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureClaudePrism(e); err != nil {
		t.Fatalf("ConfigureClaudePrism: %v", err)
	}

	settingsPath := filepath.Join(e.ClaudeDir(), "settings.json")
	got := decodeJSONFile(t, settingsPath)

	// STATIC managed permissions block.
	perms, ok := got["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("permissions missing/!object: %v", got["permissions"])
	}
	if allow, ok := perms["allow"].([]any); !ok || len(allow) != 0 {
		t.Errorf("permissions.allow = %v, want []", perms["allow"])
	}
	if perms["defaultMode"] != "acceptEdits" {
		t.Errorf("permissions.defaultMode = %v, want acceptEdits", perms["defaultMode"])
	}
	if got["skipDangerousModePermissionPrompt"] != true {
		t.Errorf("skipDangerousModePermissionPrompt = %v, want true", got["skipDangerousModePermissionPrompt"])
	}

	// DYNAMIC enabledPlugins: python is present -> its plugin is enabled.
	plugins, ok := got["enabledPlugins"].(map[string]any)
	if !ok {
		t.Fatalf("enabledPlugins missing/!object: %v", got["enabledPlugins"])
	}
	if plugins["pyright-lsp@claude-plugins-official"] != true {
		t.Errorf("enabledPlugins[pyright] = %v, want true", plugins["pyright-lsp@claude-plugins-official"])
	}
	// A LSP that is NOT configured must not be enabled (tombstoned).
	if _, present := plugins["gopls-lsp@claude-plugins-official"]; present {
		t.Errorf("enabledPlugins[gopls] present (%v); should be tombstoned", plugins["gopls-lsp@claude-plugins-official"])
	}

	// DYNAMIC env.ENABLE_LSP_TOOL: at least one LSP is configured.
	env, ok := got["env"].(map[string]any)
	if !ok {
		t.Fatalf("env missing/!object: %v", got["env"])
	}
	if env["ENABLE_LSP_TOOL"] != "1" {
		t.Errorf("env.ENABLE_LSP_TOOL = %v, want \"1\"", env["ENABLE_LSP_TOOL"])
	}

	// mcpServers never belongs in settings.json (it lives in .claude.json). The
	// computed tombstone must strip it from the composed output.
	if _, present := got["mcpServers"]; present {
		t.Errorf("settings.json has mcpServers (%v); computed tombstone must strip it", got["mcpServers"])
	}

	// Overlay seeds empty on first migration.
	overlay := decodeJSONFile(t, prismOverlayPath(e, "claude", "settings"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}

	// The obsolete snapshot orphan is deleted (§4.7).
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Errorf("yolo-host-synced-settings.json should be deleted on migration, stat err = %v", err)
	}

	// The bespoke .claude.json still gets written, with the config MCP server.
	claudeJSON := decodeJSONFile(t, e.ClaudeJSONPath())
	mcp, ok := claudeJSON["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf(".claude.json mcpServers missing/!object: %v", claudeJSON["mcpServers"])
	}
	if _, present := mcp["myserver"]; !present {
		t.Errorf(".claude.json mcpServers missing myserver: %v", mcp)
	}
}

// TestConfigureClaudePrismStripsHostMCPServers proves the computed mcpServers
// tombstone deletes a HOST-provided mcpServers block: even when the host mount's
// settings.json declares mcpServers, the rendered settings.json carries none —
// mcpServers is a .claude.json concern.
func TestConfigureClaudePrismStripsHostMCPServers(t *testing.T) {
	e, ctx := newClaudePrismEnv(t, map[string]string{})
	hostJSON := `{"theme":"dark","mcpServers":{"evil":{"command":"rm"}}}`
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"), []byte(hostJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureClaudePrism(e); err != nil {
		t.Fatalf("ConfigureClaudePrism: %v", err)
	}

	got := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))
	// The host theme still merges in (host layer is read + composed).
	if got["theme"] != "dark" {
		t.Errorf("theme = %v, want dark (host merge)", got["theme"])
	}
	// ...but the host mcpServers is stripped by the computed tombstone.
	if _, present := got["mcpServers"]; present {
		t.Errorf("host mcpServers survived (%v); computed tombstone must strip it", got["mcpServers"])
	}
}

// TestConfigureClaudePrismNoLSP proves the DYNAMIC toggles collapse when no LSP
// server is configured: no yolo plugin key is set true and env.ENABLE_LSP_TOOL
// is absent. NOTE the documented byte-shape gap: the computed tombstone on the
// sole env key leaves an EMPTY env:{} object rather than pruning the block (the
// bespoke path pruned it). claude reads an empty env block identically, so this
// is harmless; the invariant tested here is that the KEY is gone.
func TestConfigureClaudePrismNoLSP(t *testing.T) {
	e, _ := newClaudePrismEnv(t, map[string]string{})

	if err := ConfigureClaudePrism(e); err != nil {
		t.Fatalf("ConfigureClaudePrism: %v", err)
	}

	got := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))

	if plugins, ok := got["enabledPlugins"].(map[string]any); ok {
		for _, pm := range claudeLSPPluginOrder {
			if plugins[pm.plugin] == true {
				t.Errorf("enabledPlugins[%s] = true, want unset (no LSP configured)", pm.plugin)
			}
		}
	}

	// env.ENABLE_LSP_TOOL must be absent regardless of whether env:{} lingers.
	if env, ok := got["env"].(map[string]any); ok {
		if _, present := env["ENABLE_LSP_TOOL"]; present {
			t.Errorf("env.ENABLE_LSP_TOOL present (%v); want absent with no LSP", env["ENABLE_LSP_TOOL"])
		}
	}
}

// TestConfigureClaudePrismUserSettingSurvives proves the §5 overlay loop for
// claude: a top-level key the agent adds to settings.json in-jail on boot 1 is
// captured and SURVIVES boot 2's regeneration, while the managed block and the
// dynamic computed toggles regenerate around it.
func TestConfigureClaudePrismUserSettingSurvives(t *testing.T) {
	e, _ := newClaudePrismEnv(t, map[string]string{
		"YOLO_LSP_SERVERS": `{"python":{}}`,
	})

	// Boot 1: first migration seeds the baseline.
	if err := ConfigureClaudePrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	settingsPath := filepath.Join(e.ClaudeDir(), "settings.json")

	// The agent adds a personal top-level key in-jail.
	edited := decodeJSONFile(t, settingsPath)
	edited["myTopKey"] = "mine"
	editedBytes, _ := json.Marshal(edited)
	if err := os.WriteFile(settingsPath, editedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2: steady state. The engine captures the edit into the overlay and
	// re-emits it while the managed + computed layers regenerate.
	if err := ConfigureClaudePrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeJSONFile(t, settingsPath)
	if got["myTopKey"] != "mine" {
		t.Errorf("myTopKey = %v, want mine (in-jail edit must survive regen)", got["myTopKey"])
	}
	// Managed block still enforced.
	perms, ok := got["permissions"].(map[string]any)
	if !ok || perms["defaultMode"] != "acceptEdits" {
		t.Errorf("permissions.defaultMode = %v, want acceptEdits (managed regenerates)", got["permissions"])
	}
	// Dynamic plugin toggle still regenerates.
	plugins, ok := got["enabledPlugins"].(map[string]any)
	if !ok || plugins["pyright-lsp@claude-plugins-official"] != true {
		t.Errorf("enabledPlugins[pyright] = %v, want true (computed regenerates)", got["enabledPlugins"])
	}
}
