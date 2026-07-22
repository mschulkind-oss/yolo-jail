package entrypoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// codexComputedEnv builds an Env with a fake home + workspace and the given
// MCP-server env so ConfigureCodexPrism's computed layer has content to render.
// It mirrors geminiComputedEnv — the codex port is the TOML-codec analogue of
// the gemini reference port.
func codexComputedEnv(t *testing.T, mcpServersJSON string) *Env {
	t.Helper()
	vars := map[string]string{}
	if mcpServersJSON != "" {
		vars["YOLO_MCP_SERVERS"] = mcpServersJSON
	}
	return &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars}
}

// decodeCodexTOML reads and TOML-decodes config.toml into a generic object. The
// prism writes the surface through the toml codec, so the render is read back
// the way codex reads it — as decoded TOML, NOT compared byte-for-byte (the
// documented env-sub-table byte-shape gap means bytes may differ from the
// bespoke emitter while the decoded value is identical).
func decodeCodexTOML(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	m, err := tomlx.Decode(raw)
	if err != nil {
		t.Fatalf("decode %s: %v\n---\n%s", path, err, raw)
	}
	return m
}

// TestConfigureCodexPrismFirstMigration is the TOML-codec analogue of the gemini
// reference port: codex's config.toml carries a DYNAMIC mcp_servers table
// (translated from live config), plus the static force-managed scalars. On first
// migration the engine renders the computed mcp_servers, the managed
// approval_policy/sandbox_mode, seeds the sidecars with an empty overlay, and
// deletes the obsolete yolo-managed-mcp-servers.json orphan.
func TestConfigureCodexPrismFirstMigration(t *testing.T) {
	e := codexComputedEnv(t, `{"myserver":{"command":"/bin/myserver","args":["--flag"]}}`)

	// Pre-existing bespoke state: a config.toml with a stale yolo server + a loose
	// posture, plus the obsolete managed sidecar the bespoke path wrote.
	dir := e.CodexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "config.toml")
	pre := "approval_policy = \"on-request\"\n" +
		"sandbox_mode = \"read-only\"\n" +
		"\n[mcp_servers.staleServer]\ncommand = \"/gone\"\n"
	if err := os.WriteFile(configPath, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}
	sidecar := filepath.Join(dir, "yolo-managed-mcp-servers.json")
	if err := os.WriteFile(sidecar, []byte(`["staleServer"]`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureCodexPrism(e); err != nil {
		t.Fatalf("ConfigureCodexPrism: %v", err)
	}

	got := decodeCodexTOML(t, configPath)

	// The computed mcp_servers table lands (myserver from live config).
	mcp, ok := got["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers missing or not a table: %v", got["mcp_servers"])
	}
	srv, ok := mcp["myserver"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers.myserver missing (computed layer): %v", mcp)
	}
	if srv["command"] != "/bin/myserver" {
		t.Errorf("myserver.command = %v, want /bin/myserver", srv["command"])
	}
	args, ok := srv["args"].([]any)
	if !ok || len(args) != 1 || args[0] != "--flag" {
		t.Errorf("myserver.args = %v, want [--flag]", srv["args"])
	}
	// The stale yolo server is DROPPED (first-migration snapshot: not captured).
	if _, present := mcp["staleServer"]; present {
		t.Errorf("mcp_servers.staleServer survived; first migration must drop it: %v", mcp)
	}

	// Managed scalars win over the loose host posture.
	if got["approval_policy"] != "never" {
		t.Errorf("approval_policy = %v, want never (managed)", got["approval_policy"])
	}
	if got["sandbox_mode"] != "danger-full-access" {
		t.Errorf("sandbox_mode = %v, want danger-full-access (managed)", got["sandbox_mode"])
	}

	// Overlay seeded empty; obsolete managed sidecar deleted (§4.7).
	overlay := decodeJSONFile(t, prismOverlayPath(e, "codex", "config"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Errorf("yolo-managed-mcp-servers.json should be deleted on migration, stat err = %v", err)
	}
}

// TestConfigureCodexPrismDroppedServerDoesNotResurrect is the load-bearing
// correctness test: a yolo-owned server DROPPED from config between boots must
// not resurrect. The last_render sidecar is the "what yolo owned last boot"
// anchor (it replaces the bespoke managed sidecar): a yolo-written server matches
// last_render so it is never captured into the overlay, and removing it from the
// computed layer evicts it cleanly — no explicit tombstone needed.
func TestConfigureCodexPrismDroppedServerDoesNotResurrect(t *testing.T) {
	// Boot 1: two yolo servers configured.
	e := codexComputedEnv(t, `{"alpha":{"command":"/bin/alpha"},"beta":{"command":"/bin/beta"}}`)
	if err := ConfigureCodexPrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	configPath := filepath.Join(e.CodexDir(), "config.toml")
	b1 := decodeCodexTOML(t, configPath)
	mcp1 := b1["mcp_servers"].(map[string]any)
	if _, ok := mcp1["alpha"]; !ok {
		t.Fatalf("boot 1 missing alpha: %v", mcp1)
	}
	if _, ok := mcp1["beta"]; !ok {
		t.Fatalf("boot 1 missing beta: %v", mcp1)
	}

	// Boot 2: beta dropped from config (only alpha remains). The user did NOT
	// touch config.toml, so beta still sits on disk from boot 1.
	e.Vars["YOLO_MCP_SERVERS"] = `{"alpha":{"command":"/bin/alpha"}}`
	if err := ConfigureCodexPrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	b2 := decodeCodexTOML(t, configPath)
	mcp2 := b2["mcp_servers"].(map[string]any)
	if _, ok := mcp2["alpha"]; !ok {
		t.Errorf("boot 2 missing alpha (still configured): %v", mcp2)
	}
	if _, ok := mcp2["beta"]; ok {
		t.Errorf("boot 2 resurrected beta (dropped from config): %v", mcp2)
	}
}

// TestConfigureCodexPrismUserAddedServerSurvives proves the flip side: a server
// the USER adds to mcp_servers in-jail is captured into the overlay and survives
// regeneration (it never matched last_render, so the §5 diff captures it), while
// yolo's own servers still regenerate from the computed layer.
func TestConfigureCodexPrismUserAddedServerSurvives(t *testing.T) {
	e := codexComputedEnv(t, `{"alpha":{"command":"/bin/alpha"}}`)
	if err := ConfigureCodexPrism(e); err != nil {
		t.Fatalf("boot 1: %v", err)
	}
	configPath := filepath.Join(e.CodexDir(), "config.toml")

	// User adds their own MCP server in-jail by appending a TOML sub-table. This
	// is read back cleanly by the surface's toml codec (the render reads the
	// on-disk config.toml the same way).
	existing, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(existing) + "\n[mcp_servers.userserver]\ncommand = \"/usr/local/bin/userserver\"\n"
	if err := os.WriteFile(configPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	// Boot 2: steady state — user server captured + survives, yolo server regens.
	if err := ConfigureCodexPrism(e); err != nil {
		t.Fatalf("boot 2: %v", err)
	}
	got := decodeCodexTOML(t, configPath)
	gotMCP := got["mcp_servers"].(map[string]any)
	if _, ok := gotMCP["userserver"]; !ok {
		t.Errorf("userserver dropped (in-jail add must survive regen): %v", gotMCP)
	}
	if _, ok := gotMCP["alpha"]; !ok {
		t.Errorf("alpha dropped (yolo server must regen): %v", gotMCP)
	}
}
