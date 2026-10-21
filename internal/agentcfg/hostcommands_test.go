package agentcfg

import (
	"encoding/json"
	"reflect"
	"testing"
)

// claudeHookPatterns are packs/claude's settings HostCommands, spelled here so a change to the
// shape this filter walks is noticed in the package that walks it.
var claudeHookPatterns = []string{"/hooks/*/*/hooks/*/command", "/statusLine/command"}

const testHostHome = "/Users/someone"

func decodeObject(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return m
}

// The case that motivated it: herdr's SessionStart hook, installed on a Mac, names its script
// under the host's home. The handler goes, its now-empty matcher group and event go with it,
// and the other event's hook — which names no host path — stays exactly as it was.
func TestDropHostOnlyCommandsRemovesTheHandlerAndWhatItEmptied(t *testing.T) {
	host := []byte(`{
	  "theme": "dark",
	  "hooks": {
	    "SessionStart": [{"hooks": [{"type": "command",
	      "command": "bash '/Users/someone/.claude/hooks/herdr-agent-state.sh' session"}]}],
	    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "echo done > /tmp/stop"}]}]
	  }
	}`)
	out, drops := DropHostOnlyCommands("json", host, claudeHookPatterns, testHostHome,
		func(string) bool { return false })

	want := []CommandDrop{{
		Pointer: "/hooks/SessionStart/0/hooks/0",
		Command: "bash '/Users/someone/.claude/hooks/herdr-agent-state.sh' session",
		Path:    "/Users/someone/.claude/hooks/herdr-agent-state.sh",
	}}
	if !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %#v\nwant %#v", drops, want)
	}
	got := decodeObject(t, out)
	hooks := got["hooks"].(map[string]any)
	if _, still := hooks["SessionStart"]; still {
		t.Errorf("SessionStart survived with no handler left in it: %v", hooks["SessionStart"])
	}
	if _, kept := hooks["Stop"]; !kept {
		t.Errorf("the Stop hook names no host path and was removed anyway: %v", got)
	}
	if got["theme"] != "dark" {
		t.Errorf("an unrelated key changed: theme = %v", got["theme"])
	}
}

// Only the host-only handler of a group goes; its sibling and the group stay.
func TestDropHostOnlyCommandsKeepsSiblingHandlers(t *testing.T) {
	host := []byte(`{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [
	  {"type": "command", "command": "/Users/someone/bin/audit"},
	  {"type": "command", "command": "yolo internal footer"}
	]}]}}`)
	out, drops := DropHostOnlyCommands("json", host, claudeHookPatterns, testHostHome, nil)
	if len(drops) != 1 {
		t.Fatalf("drops = %#v, want exactly the audit hook", drops)
	}
	group := decodeObject(t, out)["hooks"].(map[string]any)["PreToolUse"].([]any)[0].(map[string]any)
	handlers := group["hooks"].([]any)
	if len(handlers) != 1 || handlers[0].(map[string]any)["command"] != "yolo internal footer" {
		t.Errorf("handlers = %v, want only the footer hook", handlers)
	}
	if group["matcher"] != "Bash" {
		t.Errorf("the group lost its matcher: %v", group)
	}
}

// A statusLine naming a host-only script goes whole, so the surface's own default can apply.
func TestDropHostOnlyCommandsRemovesAHostOnlyStatusLine(t *testing.T) {
	host := []byte(`{"statusLine": {"type": "command", "command": "~/x; /Users/someone/.claude/statusline.sh"}}`)
	out, drops := DropHostOnlyCommands("json", host, claudeHookPatterns, testHostHome, nil)
	if len(drops) != 1 || drops[0].Pointer != "/statusLine" {
		t.Fatalf("drops = %#v, want /statusLine", drops)
	}
	if _, still := decodeObject(t, out)["statusLine"]; still {
		t.Errorf("statusLine survived: %s", out)
	}
}

// Everything that must NOT be dropped, because a false positive deletes a hook that works.
func TestDropHostOnlyCommandsLeavesRunnableCommandsAlone(t *testing.T) {
	cases := []struct {
		name, command string
		reachable     func(string) bool
		hostHome      string
	}{
		{name: "a path outside the host home", command: "bash /tmp/hook.sh", hostHome: testHostHome},
		{name: "a home-relative spelling the jail resolves itself",
			command: "bash ~/.claude/hooks/x.sh; bash $HOME/.claude/hooks/y.sh", hostHome: testHostHome},
		{name: "a host-home path the jail can reach (a mount at the same spelling)",
			command: "bash /Users/someone/shared/hook.sh", hostHome: testHostHome,
			reachable: func(p string) bool { return p == "/Users/someone/shared/hook.sh" }},
		{name: "a sibling of the host home that only shares its prefix",
			command: "bash /Users/someone-else/hook.sh", hostHome: testHostHome},
		{name: "a launcher older than the variable reports no host home",
			command: "bash /Users/someone/.claude/hooks/x.sh", hostHome: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host := []byte(`{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": ` +
				mustJSON(tc.command) + `}]}]}}`)
			reachable := tc.reachable
			if reachable == nil {
				reachable = func(string) bool { return false }
			}
			out, drops := DropHostOnlyCommands("json", host, claudeHookPatterns, tc.hostHome, reachable)
			if len(drops) != 0 {
				t.Errorf("dropped %#v", drops)
			}
			if string(out) != string(host) {
				t.Errorf("the bytes changed with nothing dropped:\n%s", out)
			}
		})
	}
}
