package config

// hostfilesselected_test.go pins OQ-BH15 (docs/design/base-home-legacy-state.md): host_files'
// surface-path reservation covers only the SELECTED packs, resolved the way writable_home_dirs
// resolves them, because an unselected pack can never have an impact (DIR-BH1). Driven through
// ValidateConfig, the call both `yolo check` and the launch preflight make, so each test fails
// if checkHostFiles stops resolving the selection.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hostFilesErrsNaming(t *testing.T, cfg, path string) []string {
	t.Helper()
	errs, _ := ValidateConfig(decode(t, cfg), t.TempDir(), nil)
	var out []string
	for _, e := range errs {
		if strings.Contains(e, path) {
			out = append(out, e)
		}
	}
	return out
}

// THE RULING'S OWN EXAMPLE: ~/.codex/config.toml is codex's composed surface, so a host_files
// entry there is refused once codex is selected, and is an ordinary file while it is not.
func TestHostFilesCodexSurfaceIsLegalUntilCodexIsSelected(t *testing.T) {
	cfg := `{"host_files": [{"path": "~/.codex/config.toml", "content": "x"}]}`

	selectionHome(t, `["claude"]`)
	if errs := hostFilesErrsNaming(t, cfg, ".codex/config.toml"); len(errs) != 0 {
		t.Errorf("host_files ~/.codex/config.toml was refused in a claude-only workspace — codex "+
			"is not selected, so nothing composes that file (OQ-BH15): %v", errs)
	}

	selectionHome(t, `["claude", "codex"]`)
	if errs := hostFilesErrsNaming(t, cfg, ".codex/config.toml"); len(errs) != 1 {
		t.Errorf("with codex selected, want exactly one refusal of ~/.codex/config.toml, got %v", errs)
	}
}

// A CONFIGURED pack's surface is reserved too once it is selected, which the shipped-set list
// could never do: resolving a configured pack needed the pack store, so the old reservation
// covered embedded packs only and left this to SurfaceCollisions at launch.
func TestHostFilesReservesASelectedConfiguredPacksSurface(t *testing.T) {
	root := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "acme", "contributes": [{"kind": "config", "config": [{"agent": "acme",
	  "name": "settings", "codec": "json", "path": "~/.config/acme/settings.json",
	  "managed": {"a": 1}}]}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `{"host_files": [{"path": "~/.config/acme/settings.json", "content": "x"}]}`

	selectionHome(t, `["claude"]`)
	if errs := hostFilesErrsNaming(t, cfg, ".config/acme/settings.json"); len(errs) != 0 {
		t.Errorf("an unselected configured pack reserved its surface: %v", errs)
	}

	selectionHome(t, `["claude", {"source": "`+root+`"}]`)
	if errs := hostFilesErrsNaming(t, cfg, ".config/acme/settings.json"); len(errs) != 1 {
		t.Errorf("a selected configured pack's surface must be refused as a host_files dest, got %v", errs)
	}
}

// Core's own surfaces are nobody's pack, so they stay reserved with no pack selected.
func TestHostFilesCoreSurfaceIsReservedWithNoPackSelected(t *testing.T) {
	selectionHome(t, `[]`)
	cfg := `{"host_files": [{"path": "~/.config/mise/config.toml", "content": "x"}]}`
	if errs := hostFilesErrsNaming(t, cfg, ".config/mise/config.toml"); len(errs) != 1 {
		t.Errorf("core's mise surface must stay reserved whatever the selection, got %v", errs)
	}
}
