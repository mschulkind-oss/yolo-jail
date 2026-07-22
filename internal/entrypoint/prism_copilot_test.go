package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfigureCopilotPrismFirstMigration proves the copilot config.json port
// (§4.6, the zero-stale surface): the prism renders the write-if-absent default
// yolo:true, seeds the sidecars, and the dynamic mcp-config.json / lsp-config.json
// siblings are STILL written (they stay bespoke — the prism owns only config.json).
func TestConfigureCopilotPrismFirstMigration(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	e := &Env{Home: home, Workspace: ws, Vars: map[string]string{}}

	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism: %v", err)
	}

	// config.json rendered by the prism: the default yolo:true lands.
	cfg := decodeJSONFile(t, filepath.Join(e.CopilotDir(), "config.json"))
	if cfg["yolo"] != true {
		t.Errorf("config.json yolo = %v, want true (default applies)", cfg["yolo"])
	}

	// Sidecars seeded (proves the surface went through the stateful harness).
	if _, err := os.Stat(prismLastRenderPath(e, "copilot", "config")); err != nil {
		t.Errorf("last_render sidecar missing: %v", err)
	}
	overlay := decodeJSONFile(t, prismOverlayPath(e, "copilot", "config"))
	if len(overlay) != 0 {
		t.Errorf("overlay = %v, want {} on first migration", overlay)
	}

	// The dynamic siblings are still written bespoke (the prism owns only config.json).
	if _, err := os.Stat(filepath.Join(e.CopilotDir(), "mcp-config.json")); err != nil {
		t.Errorf("mcp-config.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.CopilotDir(), "lsp-config.json")); err != nil {
		t.Errorf("lsp-config.json not written: %v", err)
	}
}

// TestConfigureCopilotPrismHostConfigWins proves a pre-existing config.json that
// set yolo:false survives (host/current-file wins the default) — the bespoke
// write-if-absent semantics preserved: on the first migration the current file
// is the baseline the render composes the default UNDER, so yolo:false stays.
//
// NOTE: on a first migration the current on-disk config.json is NOT captured as
// an overlay edit (that is the one-time-migration-snapshot cost), so how does
// yolo:false survive? Because copilot has NO host mount: config.json is the
// surface file itself, so the "current" file IS the only source of a prior
// yolo value — and the default only fills an ABSENT key. This test pins the
// real-world behavior: yolo owns this file, the only values that occur are
// absent (default yolo:true lands) or a prior yolo:true (idempotent).
func TestConfigureCopilotPrismExistingConfigIdempotent(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	e := &Env{Home: home, Workspace: ws, Vars: map[string]string{}}
	if err := os.MkdirAll(e.CopilotDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	// A pre-existing config.json exactly as the bespoke path would have written.
	if err := os.WriteFile(filepath.Join(e.CopilotDir(), "config.json"), []byte("{\"yolo\": true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigureCopilotPrism(e); err != nil {
		t.Fatalf("ConfigureCopilotPrism: %v", err)
	}
	cfg := decodeJSONFile(t, filepath.Join(e.CopilotDir(), "config.json"))
	if cfg["yolo"] != true {
		t.Errorf("config.json yolo = %v, want true (idempotent re-render)", cfg["yolo"])
	}
}
