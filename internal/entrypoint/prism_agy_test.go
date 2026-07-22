package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigureAgyPrismFirstMigration proves agy's settings.json port: agy is
// born directly on the prism (no bespoke writer, no gate). The prism renders the
// force-managed permissionMode:"allow", seeds the sidecars with an empty overlay
// (§3.2 first migration), and the dynamic mcp_config.json sibling is written
// (it stays a pure-overwrite bespoke sibling — the prism owns only settings.json).
func TestConfigureAgyPrismFirstMigration(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	e := &Env{Home: home, Workspace: ws, Vars: map[string]string{}}

	if err := ConfigureAgyPrism(e); err != nil {
		t.Fatalf("ConfigureAgyPrism: %v", err)
	}

	// settings.json rendered by the prism: the managed permissionMode lands.
	settings := decodeJSONFile(t, filepath.Join(e.AgyDir(), "settings.json"))
	if settings["permissionMode"] != "allow" {
		t.Errorf("settings.json permissionMode = %v, want \"allow\" (managed)", settings["permissionMode"])
	}

	// Sidecars seeded (proves the surface went through the stateful harness).
	if _, err := os.Stat(prismLastRenderPath(e, "agy", "settings")); err != nil {
		t.Errorf("last_render sidecar missing: %v", err)
	}
	overlay := decodeJSONFile(t, prismOverlayPath(e, "agy", "settings"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}

	// The dynamic mcp_config.json sibling is written (bespoke pure-overwrite).
	mcp := decodeJSONFile(t, filepath.Join(e.AgyDir(), "mcp_config.json"))
	if _, ok := mcp["mcpServers"]; !ok {
		t.Errorf("mcp_config.json missing mcpServers key: %v", mcp)
	}
}

// TestConfigureAgyPrismManagedReverts proves the managed posture: a user who
// edits permissionMode to something permissive-off has it forced back to "allow"
// on the next boot (managed layer wins — the container is the trust boundary).
func TestConfigureAgyPrismManagedReverts(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	e := &Env{Home: home, Workspace: ws, Vars: map[string]string{}}
	if err := os.MkdirAll(e.AgyDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// First boot to seed the sidecars.
	if err := ConfigureAgyPrism(e); err != nil {
		t.Fatalf("ConfigureAgyPrism (boot 1): %v", err)
	}
	// User flips permissionMode off between boots.
	if err := os.WriteFile(filepath.Join(e.AgyDir(), "settings.json"),
		[]byte("{\"permissionMode\": \"ask\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Second boot: managed reasserts "allow".
	if err := ConfigureAgyPrism(e); err != nil {
		t.Fatalf("ConfigureAgyPrism (boot 2): %v", err)
	}
	settings := decodeJSONFile(t, filepath.Join(e.AgyDir(), "settings.json"))
	if settings["permissionMode"] != "allow" {
		t.Errorf("settings.json permissionMode = %v, want \"allow\" (managed reverts user edit)", settings["permissionMode"])
	}
}
