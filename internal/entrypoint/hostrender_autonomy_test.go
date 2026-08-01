package entrypoint

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The SHIPPED claude pack, rendered at the host notch, must NOT write the jail-bypass
// permission keys into the real ~/.claude/settings.json — the concrete §4.2 fix for the
// apply --host bypass leak. RenderHostPack drives the same path apply --host --assert does.
// (The jail notch staying byte-identical is enforced by TestRenderFingerprintStable.)
func TestHostRenderClaudeDropsBypass(t *testing.T) {
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	home := t.TempDir()

	// write=true (assert) so the file actually lands, then inspect it.
	results, rerr := RenderHostPack(claude, home, false)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	// The settings surface must have rendered (not been refused).
	var rendered bool
	for _, r := range results {
		if r.Surface == "claude/settings" && r.Action == "rendered" {
			rendered = true
		}
	}
	if !rendered {
		t.Fatalf("claude/settings should render at host; results=%+v", results)
	}

	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read host settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse host settings: %v\n%s", err, data)
	}

	// The bypass keys must be absent or benign — never the jail values on a real host.
	if got["skipDangerousModePermissionPrompt"] == true {
		t.Errorf("host settings must not skip the dangerous-mode prompt:\n%s", data)
	}
	if perms, _ := got["permissions"].(map[string]any); perms != nil {
		if perms["defaultMode"] == "acceptEdits" {
			t.Errorf("host settings must not be acceptEdits:\n%s", data)
		}
		if ad, _ := perms["additionalDirectories"].([]any); len(ad) != 0 {
			t.Errorf("host settings must not allow the filesystem root:\n%s", data)
		}
	}
	// The benign always-safe key still renders (it is plain config, not autonomy-gated).
	if prefs, _ := got["preferences"].(map[string]any); prefs == nil || prefs["autoUpdaterStatus"] != "disabled" {
		t.Errorf("benign autoUpdater key should still render at host:\n%s", data)
	}
}
