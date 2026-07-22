package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// geminiComputedEnv builds an Env with a fake home + workspace and the given
// MCP-server env so ConfigureGeminiPrism's computed layer has content to render.
func geminiComputedEnv(t *testing.T, mcpServersJSON string) *Env {
	t.Helper()
	vars := map[string]string{}
	if mcpServersJSON != "" {
		vars["YOLO_MCP_SERVERS"] = mcpServersJSON
	}
	return &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars}
}

// TestConfigureGeminiPrismFirstMigration is the reference port proving the
// COMPUTED layer: gemini's settings.json carries a DYNAMIC mcpServers table
// (reconciled from live config), not just static managed keys. On first
// migration the engine renders the computed mcpServers, the managed general.*
// force-offs, and the default security posture; the sidecars seed with an empty
// overlay; and the obsolete yolo-managed-mcp-servers.json orphan is deleted.
func TestConfigureGeminiPrismFirstMigration(t *testing.T) {
	e := geminiComputedEnv(t, `{"myserver":{"command":"/bin/myserver","args":["--flag"]}}`)

	// Pre-existing bespoke state: a settings.json with a stale yolo server and the
	// obsolete managed sidecar the bespoke path wrote.
	dir := e.GeminiDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settingsPath,
		[]byte(`{"mcpServers":{"staleServer":{"command":"/gone"}},"staleTopKey":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := e.GeminiManagedMCPPath()
	if err := os.WriteFile(sidecar, []byte(`["staleServer"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureGeminiPrism(e); err != nil {
		t.Fatalf("ConfigureGeminiPrism: %v", err)
	}

	got := decodeJSONFile(t, settingsPath)

	// The computed mcpServers table lands (myserver from live config).
	mcp, ok := got["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers missing or not an object: %v", got["mcpServers"])
	}
	if _, present := mcp["myserver"]; !present {
		t.Errorf("mcpServers.myserver missing (computed layer): %v", mcp)
	}
	// The stale yolo server is DROPPED (first-migration snapshot: not captured).
	if _, present := mcp["staleServer"]; present {
		t.Errorf("mcpServers.staleServer survived; first migration must drop it: %v", mcp)
	}
	if _, present := got["staleTopKey"]; present {
		t.Errorf("staleTopKey survived; first migration must drop it: %v", got["staleTopKey"])
	}

	// Managed general.* force-offs land.
	general, ok := got["general"].(map[string]any)
	if !ok {
		t.Fatalf("general missing: %v", got["general"])
	}
	if general["enableAutoUpdate"] != false {
		t.Errorf("general.enableAutoUpdate = %v, want false (managed)", general["enableAutoUpdate"])
	}
	// Default security posture lands.
	security, ok := got["security"].(map[string]any)
	if !ok {
		t.Fatalf("security missing: %v", got["security"])
	}
	if security["approvalMode"] != "yolo" {
		t.Errorf("security.approvalMode = %v, want yolo (default)", security["approvalMode"])
	}

	// Overlay seeded empty; obsolete managed sidecar deleted (§4.7).
	overlay := decodeJSONFile(t, prismOverlayPath(e, "gemini", "settings"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Errorf("yolo-managed-mcp-servers.json should be deleted on migration, stat err = %v", err)
	}
}

// TestConfigureGeminiPrismDroppedServerDoesNotResurrect is the load-bearing
// correctness test for the computed-layer MCP port: a yolo-owned server that is
// DROPPED from config between boots must not resurrect. The last_render sidecar
// is the "what yolo owned last boot" anchor (it replaces the bespoke managed
// sidecar): because a yolo-written server matches last_render it is never
// captured into the overlay, so removing it from the computed layer evicts it
// cleanly — no explicit tombstone needed.
func TestConfigureGeminiPrismDroppedServerDoesNotResurrect(t *testing.T) {
	// Boot 1: two yolo servers configured.
	e := geminiComputedEnv(t, `{"alpha":{"command":"/bin/alpha"},"beta":{"command":"/bin/beta"}}`)
	if err := ConfigureGeminiPrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	settingsPath := filepath.Join(e.GeminiDir(), "settings.json")
	b1 := decodeJSONFile(t, settingsPath)
	mcp1 := b1["mcpServers"].(map[string]any)
	if _, ok := mcp1["alpha"]; !ok {
		t.Fatalf("boot 1 missing alpha: %v", mcp1)
	}
	if _, ok := mcp1["beta"]; !ok {
		t.Fatalf("boot 1 missing beta: %v", mcp1)
	}

	// Boot 2: beta dropped from config (only alpha remains). The user did NOT
	// touch settings.json, so beta still sits on disk from boot 1.
	e.Vars["YOLO_MCP_SERVERS"] = `{"alpha":{"command":"/bin/alpha"}}`
	if err := ConfigureGeminiPrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	b2 := decodeJSONFile(t, settingsPath)
	mcp2 := b2["mcpServers"].(map[string]any)
	if _, ok := mcp2["alpha"]; !ok {
		t.Errorf("boot 2 missing alpha (still configured): %v", mcp2)
	}
	if _, ok := mcp2["beta"]; ok {
		t.Errorf("boot 2 resurrected beta (dropped from config): %v", mcp2)
	}
}

// TestConfigureGeminiPrismUserAddedServerSurvives proves the flip side: a server
// the USER adds to mcpServers in-jail is captured into the overlay and survives
// regeneration (it never matched last_render, so the §5 diff captures it), while
// yolo's own servers still regenerate from the computed layer.
func TestConfigureGeminiPrismUserAddedServerSurvives(t *testing.T) {
	e := geminiComputedEnv(t, `{"alpha":{"command":"/bin/alpha"}}`)
	if err := ConfigureGeminiPrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	settingsPath := filepath.Join(e.GeminiDir(), "settings.json")

	// User adds their own MCP server in-jail.
	edited := decodeJSONFile(t, settingsPath)
	mcp := edited["mcpServers"].(map[string]any)
	mcp["userserver"] = map[string]any{"command": "/usr/local/bin/userserver"}
	editedBytes, _ := json.Marshal(edited)
	if err := os.WriteFile(settingsPath, editedBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2: steady state — user server captured + survives, yolo server regens.
	if err := ConfigureGeminiPrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeJSONFile(t, settingsPath)
	gotMCP := got["mcpServers"].(map[string]any)
	if _, ok := gotMCP["userserver"]; !ok {
		t.Errorf("userserver dropped (in-jail add must survive regen): %v", gotMCP)
	}
	if _, ok := gotMCP["alpha"]; !ok {
		t.Errorf("alpha dropped (yolo server must regen): %v", gotMCP)
	}
}
