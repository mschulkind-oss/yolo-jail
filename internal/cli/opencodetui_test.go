package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHostApplyLeavesOpencodesThemeMigrationAlone: the opencode footer's plugin list must not be
// the file that stops opencode moving a user's TUI settings. opencode 1.18.32 moves `theme`,
// `keybinds` and `tui` out of the global opencode.json into tui.json on its next start, but only
// while tui.json does not exist, and after that its TUI reads them from the tui files alone
// (docs/design/agent-footer.md §2.1). So over a home that still carries them in opencode.json,
// `yolo host apply` must create no tui.json, must leave those keys where opencode will look for
// them, and lists the plugin in tui.jsonc, which opencode reads beside tui.json.
func TestHostApplyLeavesOpencodesThemeMigrationAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs": ["opencode"]}`)
	dir := filepath.Join(home, ".config", "opencode")
	legacy := `{"theme": "tokyonight", "keybinds": {"leader": "ctrl+a"}}`
	writeFile(t, filepath.Join(dir, "opencode.json"), legacy)
	stubDeclaredBins(t)

	if rc, report := applyWith(t, true, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("host apply --assert rc=%d\n%s", rc, report)
	}

	if _, err := os.Lstat(filepath.Join(dir, "tui.json")); err == nil {
		t.Errorf("host apply created %s: opencode now skips moving the theme and keybinds out of "+
			"opencode.json, and its TUI stops applying them", filepath.Join(dir, "tui.json"))
	}
	var cfg map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, "opencode.json"))
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatalf("opencode.json after host apply: %v\n%s", err, raw)
	}
	if cfg["theme"] != "tokyonight" || cfg["keybinds"] == nil {
		t.Errorf("host apply moved the user's legacy TUI keys out of opencode.json (%s): that is opencode's "+
			"migration to make, into the tui.json it writes", raw)
	}
	var tui struct {
		Plugin []string `json:"plugin"`
	}
	raw, err = os.ReadFile(filepath.Join(dir, "tui.jsonc"))
	if err != nil || json.Unmarshal(raw, &tui) != nil {
		t.Fatalf("host apply wrote no readable tui.jsonc: %v\n%s", err, raw)
	}
	if strings.Join(tui.Plugin, ",") != "./yolo/footer.js" {
		t.Errorf("tui.jsonc plugin = %q, want yolo's footer plugin alone", tui.Plugin)
	}
}
