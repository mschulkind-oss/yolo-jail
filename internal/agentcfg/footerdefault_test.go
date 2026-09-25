package agentcfg

import (
	"reflect"
	"testing"
)

// The agent footer's adapters sit in each agent pack's DEFAULTS (docs/design/agent-footer.md
// OQ-FT1), and layers merge per key. These tests compose the real surfaces to pin what that
// buys (§3): a user's own statusLine — from the host file or from an in-jail edit — replaces
// every key yolo wrote, and agy's stack flag is the one key that deliberately reaches a
// user's statusLine too.

// composeStatusLine composes one pack surface with the given host bytes and capture
// overlay, and returns the composed statusLine.
func composeStatusLine(t *testing.T, agent, name string, host []byte, overlay any) any {
	t.Helper()
	s, ok := packManifest(t).Lookup(agent, name)
	if !ok {
		t.Fatalf("no %s/%s surface", agent, name)
	}
	res, err := Compose(Inputs{Surface: s, HostBytes: host, Overlay: overlay})
	if err != nil {
		t.Fatalf("composing %s/%s: %v", agent, name, err)
	}
	return res.ConfigMap()["statusLine"]
}

func TestClaudeFooterDefaultYieldsToTheUsersStatusLine(t *testing.T) {
	// With nothing of the user's, the composed file carries yolo's default.
	got, ok := composeStatusLine(t, "claude", "settings", nil, nil).(map[string]any)
	if !ok || got["type"] != "command" || got["command"] == "" {
		t.Fatalf("claude settings without a user statusLine = %#v, want yolo's footer command", got)
	}

	mine := map[string]any{"type": "command", "command": "~/.claude/statusline.sh", "padding": float64(1)}
	// The host file's statusLine: replaced whole, nothing of yolo's left behind.
	host := []byte(`{"statusLine": {"type": "command", "command": "~/.claude/statusline.sh", "padding": 1}}`)
	if got := composeStatusLine(t, "claude", "settings", host, nil); !reflect.DeepEqual(got, mine) {
		t.Errorf("host statusLine composed to %#v, want exactly the user's %#v", got, mine)
	}
	// An in-jail edit (Claude's own /statusline writes the file; the capture overlay carries
	// it across boots): the same.
	if got := composeStatusLine(t, "claude", "settings", nil, map[string]any{"statusLine": mine}); !reflect.DeepEqual(got, mine) {
		t.Errorf("in-jail statusLine composed to %#v, want exactly the user's %#v", got, mine)
	}
}

func TestAgyFooterStacksWithAUsersStatusLineUntilTheySayNot(t *testing.T) {
	mine := map[string]any{"type": "command", "command": "~/agy-line.sh"}
	got, _ := composeStatusLine(t, "agy", "settings", nil, map[string]any{"statusLine": mine}).(map[string]any)
	if got["command"] != "~/agy-line.sh" {
		t.Errorf("agy composed command = %v, want the user's", got["command"])
	}
	// DIR-FT2 applied to the user's footer too (§3): agy's own line stays beside theirs.
	if got["stack_with_default"] != true {
		t.Errorf("agy composed stack_with_default = %v, want true beside a user's statusLine", got["stack_with_default"])
	}
	// One key to undo.
	off := map[string]any{"type": "command", "command": "~/agy-line.sh", "stack_with_default": false}
	got, _ = composeStatusLine(t, "agy", "settings", nil, map[string]any{"statusLine": off}).(map[string]any)
	if !reflect.DeepEqual(got, off) {
		t.Errorf("agy statusLine with stack_with_default=false composed to %#v, want exactly %#v", got, off)
	}
}

// TestOpencodeFooterPluginYieldsToTheUsersPluginList is §3's rule for opencode's adapter, whose
// footer key is tui.jsonc's `plugin` list. A list replaces wholesale in the merge (it is not an
// object), so a `plugin` list of the user's own IN THAT FILE replaces yolo's entry, exactly as a
// user's statusLine replaces yolo's command; keeping both means listing yolo's spec in theirs. A
// tui.jsonc of the user's that sets no `plugin` keeps yolo's beside their other keys. (A list in
// the user's tui.json is not this surface's to merge: opencode concatenates the two files' lists.)
func TestOpencodeFooterPluginYieldsToTheUsersPluginList(t *testing.T) {
	s, ok := packManifest(t).Lookup("opencode", "tui")
	if !ok {
		t.Fatal("no opencode/tui surface")
	}
	compose := func(overlay any) map[string]any {
		t.Helper()
		res, err := Compose(Inputs{Surface: s, Overlay: overlay})
		if err != nil {
			t.Fatalf("composing opencode/tui: %v", err)
		}
		return res.ConfigMap()
	}
	yolos := []any{"./yolo/footer.js"}

	if got := compose(nil)["plugin"]; !reflect.DeepEqual(got, yolos) {
		t.Errorf("tui.jsonc with nothing of the user's: plugin = %#v, want yolo's %#v", got, yolos)
	}
	got := compose(map[string]any{"theme": "tokyonight"})
	if got["theme"] != "tokyonight" || !reflect.DeepEqual(got["plugin"], yolos) {
		t.Errorf("a user's tui.jsonc with only a theme composed to %#v, want their theme and yolo's plugin", got)
	}
	mine := []any{"opencode-plugin-of-mine"}
	if got := compose(map[string]any{"plugin": mine})["plugin"]; !reflect.DeepEqual(got, mine) {
		t.Errorf("a user's plugin list composed to %#v, want exactly theirs %#v", got, mine)
	}
}
