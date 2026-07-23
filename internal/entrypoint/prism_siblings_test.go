package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// prism_siblings_test.go pins the PURE-OVERWRITE sibling ports (copilot
// mcp-config.json / lsp-config.json, agy mcp_config.json) onto the stateless
// renderSurfaceComputed path. The defining property these tests guard is that,
// unlike the stateful surfaces, these files write NO sidecars and preserve NO
// in-jail edit — the live table wins outright every boot.

// siblingEnv builds an Env with temp Home+Workspace and the given env vars.
func siblingEnv(t *testing.T, vars map[string]string) *Env {
	t.Helper()
	if vars == nil {
		vars = map[string]string{}
	}
	return &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: vars}
}

// TestCopilotMCPComputedServerLands: a server declared via YOLO_MCP_SERVERS is
// rendered into mcp-config.json's mcpServers table via the computed layer.
func TestCopilotMCPComputedServerLands(t *testing.T) {
	e := siblingEnv(t, map[string]string{
		"YOLO_MCP_SERVERS": `{"demo":{"command":"/bin/demo","args":["--serve"]}}`,
	})
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism: %v", err)
	}
	mcp := decodeJSONFile(t, filepath.Join(e.CopilotDir(), "mcp-config.json"))
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp-config.json mcpServers not an object: %#v", mcp["mcpServers"])
	}
	demo, ok := servers["demo"].(map[string]any)
	if !ok {
		t.Fatalf("mcpServers.demo missing: %#v", servers)
	}
	if demo["command"] != "/bin/demo" {
		t.Errorf("demo.command = %v, want /bin/demo", demo["command"])
	}
}

// TestCopilotMCPEmptyStillEmitsWrapper: with no configured servers the file is
// still a well-formed {"mcpServers":{}} (the empty-wrapper default supplies the
// shape), matching the old always-emit-the-wrapper behavior.
func TestCopilotMCPEmptyStillEmitsWrapper(t *testing.T) {
	e := siblingEnv(t, nil)
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism: %v", err)
	}
	mcp := decodeJSONFile(t, filepath.Join(e.CopilotDir(), "mcp-config.json"))
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp-config.json must carry an mcpServers wrapper even when empty: %#v", mcp)
	}
	if len(servers) != 0 {
		t.Errorf("mcpServers should be empty with no configured servers, got %#v", servers)
	}
}

// TestCopilotSiblingsWriteNoSidecars is the PURE-OVERWRITE guard: the compute
// path must NOT seed last_render / overlay sidecars (those belong to the
// edit-preserving stateful surfaces). If a sibling ever grew a sidecar it would
// silently start capturing edits — the exact regression this pins against.
func TestCopilotSiblingsWriteNoSidecars(t *testing.T) {
	e := siblingEnv(t, nil)
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism: %v", err)
	}
	for _, name := range []string{"mcp", "lsp"} {
		if _, err := os.Stat(prismLastRenderPath(e, "copilot", name)); !os.IsNotExist(err) {
			t.Errorf("copilot/%s must write NO last_render sidecar (pure overwrite), stat err=%v", name, err)
		}
		if _, err := os.Stat(prismOverlayPath(e, "copilot", name)); !os.IsNotExist(err) {
			t.Errorf("copilot/%s must write NO overlay sidecar (pure overwrite), stat err=%v", name, err)
		}
	}
}

// TestCopilotMCPUserEditNotPreserved proves the pure-overwrite semantics end to
// end: a user edit to mcp-config.json between boots is DISCARDED (the live table
// wins outright) — the opposite of a stateful surface, which would capture it.
func TestCopilotMCPUserEditNotPreserved(t *testing.T) {
	e := siblingEnv(t, nil)
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism (boot 1): %v", err)
	}
	mcpPath := filepath.Join(e.CopilotDir(), "mcp-config.json")
	// User hand-adds a server.
	if err := os.WriteFile(mcpPath,
		[]byte(`{"mcpServers":{"handAdded":{"command":"/bin/x"}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism (boot 2): %v", err)
	}
	mcp := decodeJSONFile(t, mcpPath)
	servers, _ := mcp["mcpServers"].(map[string]any)
	if _, present := servers["handAdded"]; present {
		t.Errorf("hand-added server must NOT be preserved (pure overwrite), got %#v", servers)
	}
}

// TestCopilotLSPReshape: an LSP server declared via YOLO_LSP_SERVERS is reshaped
// into copilot's {command,args,fileExtensions} entry; a commandless entry drops
// its command key (RFC-7386 null-leaf: a commandless LSP server is nonfunctional
// either way) rather than emitting an explicit null.
func TestCopilotLSPReshape(t *testing.T) {
	e := siblingEnv(t, map[string]string{
		"YOLO_LSP_SERVERS": `{` +
			`"gopls":{"command":"/bin/gopls","args":["serve"],"fileExtensions":[".go"]},` +
			`"bare":{"fileExtensions":[".x"]}` +
			`}`,
	})
	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism: %v", err)
	}
	lsp := decodeJSONFile(t, filepath.Join(e.CopilotDir(), "lsp-config.json"))
	servers, ok := lsp["lspServers"].(map[string]any)
	if !ok {
		t.Fatalf("lsp-config.json lspServers not an object: %#v", lsp["lspServers"])
	}
	gopls, ok := servers["gopls"].(map[string]any)
	if !ok {
		t.Fatalf("lspServers.gopls missing: %#v", servers)
	}
	if gopls["command"] != "/bin/gopls" {
		t.Errorf("gopls.command = %v, want /bin/gopls", gopls["command"])
	}
	if _, has := gopls["args"]; !has {
		t.Error("gopls entry missing args key")
	}
	if _, has := gopls["fileExtensions"]; !has {
		t.Error("gopls entry missing fileExtensions key")
	}
	bare, ok := servers["bare"].(map[string]any)
	if !ok {
		t.Fatalf("lspServers.bare missing: %#v", servers)
	}
	if _, present := bare["command"]; present {
		t.Errorf("commandless entry must OMIT command (not emit null), got %#v", bare)
	}
}

// TestAgyMCPComputedServerLands mirrors the copilot MCP test for agy's
// mcp_config.json sibling (distinct path, same pure-overwrite compute path).
func TestAgyMCPComputedServerLands(t *testing.T) {
	e := siblingEnv(t, map[string]string{
		"YOLO_MCP_SERVERS": `{"demo":{"command":"/bin/demo"}}`,
	})
	if err := ConfigureAgyPrism(e); err != nil {
		t.Fatalf("ConfigureAgyPrism: %v", err)
	}
	mcp := decodeJSONFile(t, filepath.Join(e.AgyDir(), "mcp_config.json"))
	servers, ok := mcp["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_config.json mcpServers not an object: %#v", mcp["mcpServers"])
	}
	if _, present := servers["demo"]; !present {
		t.Errorf("agy mcp_config.json missing demo server: %#v", servers)
	}
	// Pure overwrite: no sidecars for the agy/mcp surface.
	if _, err := os.Stat(prismLastRenderPath(e, "agy", "mcp")); !os.IsNotExist(err) {
		t.Errorf("agy/mcp must write NO last_render sidecar, stat err=%v", err)
	}
}
