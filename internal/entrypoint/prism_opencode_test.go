package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// opencodeComputedEnv builds an Env with a fake home + workspace and the given
// MCP-server env so ConfigureOpencodePrism's computed layer has content to
// render.
func opencodeComputedEnv(t *testing.T, mcpServersJSON string) *Env {
	t.Helper()
	vars := map[string]string{}
	if mcpServersJSON != "" {
		vars["YOLO_MCP_SERVERS"] = mcpServersJSON
	}
	return &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars}
}

// opencodeConfigPath is the surface path ConfigureOpencodePrism renders.
func opencodeConfigPath(e *Env) string {
	return filepath.Join(e.OpencodeDir(), "opencode.json")
}

// TestConfigureOpencodePrismFirstMigration mirrors the gemini reference port:
// opencode's opencode.json carries a DYNAMIC mcp table (each shared server
// translated into opencode's native {type:local, command:[...], enabled:true}
// schema). On first migration the engine renders the computed mcp table, the
// managed permission="allow", and the default $schema; the sidecars seed with
// an empty overlay; and the obsolete yolo-managed-mcp-servers.json orphan is
// deleted.
func TestConfigureOpencodePrismFirstMigration(t *testing.T) {
	e := opencodeComputedEnv(t, `{"myserver":{"command":"/bin/myserver","args":["--flag"]}}`)

	// Pre-existing bespoke state: an opencode.json with a stale yolo server and
	// the obsolete managed sidecar the bespoke path wrote.
	dir := e.OpencodeDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(configPath,
		[]byte(`{"mcp":{"staleServer":{"type":"local","command":["/gone"],"enabled":true}},"staleTopKey":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "yolo-managed-mcp-servers.json")
	if err := os.WriteFile(sidecar, []byte(`["staleServer"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureOpencodePrism(e); err != nil {
		t.Fatalf("ConfigureOpencodePrism: %v", err)
	}

	got := decodeJSONFile(t, configPath)

	// The computed mcp table lands (myserver from live config), translated to
	// opencode's native schema.
	mcp, ok := got["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp missing or not an object: %v", got["mcp"])
	}
	server, ok := mcp["myserver"].(map[string]any)
	if !ok {
		t.Fatalf("mcp.myserver missing or not an object (computed layer): %v", mcp)
	}
	if server["type"] != "local" {
		t.Errorf("mcp.myserver.type = %v, want local", server["type"])
	}
	if server["enabled"] != true {
		t.Errorf("mcp.myserver.enabled = %v, want true", server["enabled"])
	}
	cmd, ok := server["command"].([]any)
	if !ok || len(cmd) != 2 || cmd[0] != "/bin/myserver" || cmd[1] != "--flag" {
		t.Errorf("mcp.myserver.command = %v, want [/bin/myserver --flag]", server["command"])
	}
	// The stale yolo server is DROPPED (first-migration snapshot: not captured).
	if _, present := mcp["staleServer"]; present {
		t.Errorf("mcp.staleServer survived; first migration must drop it: %v", mcp)
	}
	if _, present := got["staleTopKey"]; present {
		t.Errorf("staleTopKey survived; first migration must drop it: %v", got["staleTopKey"])
	}

	// The default $schema + managed permission land.
	if got["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v, want https://opencode.ai/config.json (default)", got["$schema"])
	}
	if got["permission"] != "allow" {
		t.Errorf("permission = %v, want allow (managed)", got["permission"])
	}

	// Overlay seeded empty; obsolete managed sidecar deleted (§4.7).
	overlay := decodeJSONFile(t, prismOverlayPath(e, "opencode", "config"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Errorf("yolo-managed-mcp-servers.json should be deleted on migration, stat err = %v", err)
	}
}

// TestConfigureOpencodePrismEmptyMCPOmitsKey proves the delete-when-empty
// parity: with no MCP servers configured, the computed layer omits the "mcp"
// key entirely (there is no host layer to carry a stale block), so the rendered
// opencode.json has no "mcp" key — matching the bespoke path's
// delete-when-empty.
func TestConfigureOpencodePrismEmptyMCPOmitsKey(t *testing.T) {
	e := opencodeComputedEnv(t, "")
	if err := ConfigureOpencodePrism(e); err != nil {
		t.Fatalf("ConfigureOpencodePrism: %v", err)
	}
	got := decodeJSONFile(t, opencodeConfigPath(e))
	if _, present := got["mcp"]; present {
		t.Errorf("mcp key present with no servers configured; want omitted: %v", got["mcp"])
	}
	if got["permission"] != "allow" {
		t.Errorf("permission = %v, want allow (managed)", got["permission"])
	}
	if got["$schema"] != "https://opencode.ai/config.json" {
		t.Errorf("$schema = %v, want https://opencode.ai/config.json (default)", got["$schema"])
	}
}

// TestConfigureOpencodePrismDroppedServerDoesNotResurrect is the load-bearing
// correctness test for the computed-layer MCP port: a yolo-owned server that is
// DROPPED from config between boots must not resurrect. The last_render sidecar
// is the "what yolo owned last boot" anchor (it replaces the bespoke managed
// sidecar): because a yolo-written server matches last_render it is never
// captured into the overlay, so removing it from the computed layer evicts it
// cleanly — no explicit tombstone needed.
func TestConfigureOpencodePrismDroppedServerDoesNotResurrect(t *testing.T) {
	// Boot 1: two yolo servers configured.
	e := opencodeComputedEnv(t, `{"alpha":{"command":"/bin/alpha"},"beta":{"command":"/bin/beta"}}`)
	if err := ConfigureOpencodePrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	configPath := opencodeConfigPath(e)
	b1 := decodeJSONFile(t, configPath)
	mcp1 := b1["mcp"].(map[string]any)
	if _, ok := mcp1["alpha"]; !ok {
		t.Fatalf("boot 1 missing alpha: %v", mcp1)
	}
	if _, ok := mcp1["beta"]; !ok {
		t.Fatalf("boot 1 missing beta: %v", mcp1)
	}

	// Boot 2: beta dropped from config (only alpha remains). The user did NOT
	// touch opencode.json, so beta still sits on disk from boot 1.
	e.Vars["YOLO_MCP_SERVERS"] = `{"alpha":{"command":"/bin/alpha"}}`
	if err := ConfigureOpencodePrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	b2 := decodeJSONFile(t, configPath)
	mcp2 := b2["mcp"].(map[string]any)
	if _, ok := mcp2["alpha"]; !ok {
		t.Errorf("boot 2 missing alpha (still configured): %v", mcp2)
	}
	if _, ok := mcp2["beta"]; ok {
		t.Errorf("boot 2 resurrected beta (dropped from config): %v", mcp2)
	}
}

// TestConfigureOpencodePrismUserAddedServerSurvives proves the flip side: a
// server the USER adds to the mcp block in-jail is captured into the overlay
// and survives regeneration (it never matched last_render, so the §5 diff
// captures it), while yolo's own servers still regenerate from the computed
// layer.
func TestConfigureOpencodePrismUserAddedServerSurvives(t *testing.T) {
	e := opencodeComputedEnv(t, `{"alpha":{"command":"/bin/alpha"}}`)
	if err := ConfigureOpencodePrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	configPath := opencodeConfigPath(e)

	// User adds their own MCP server in-jail.
	edited := decodeJSONFile(t, configPath)
	mcp := edited["mcp"].(map[string]any)
	mcp["userserver"] = map[string]any{
		"type":    "local",
		"command": []any{"/usr/local/bin/userserver"},
		"enabled": true,
	}
	editedBytes, _ := json.Marshal(edited)
	if err := os.WriteFile(configPath, editedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2: steady state — user server captured + survives, yolo server regens.
	if err := ConfigureOpencodePrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeJSONFile(t, configPath)
	gotMCP := got["mcp"].(map[string]any)
	if _, ok := gotMCP["userserver"]; !ok {
		t.Errorf("userserver dropped (in-jail add must survive regen): %v", gotMCP)
	}
	if _, ok := gotMCP["alpha"]; !ok {
		t.Errorf("alpha dropped (yolo server must regen): %v", gotMCP)
	}
}
