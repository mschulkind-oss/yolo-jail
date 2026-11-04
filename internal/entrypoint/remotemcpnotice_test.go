package entrypoint

import (
	"reflect"
	"testing"
)

func TestRemoteMCPNoticeNamesEachAgentWithoutAForm(t *testing.T) {
	withheld := map[string]map[string]string{
		"muse-code": {"linear": "http"}, "codex": {"legacy": "sse"},
		"copilot": {"linear": "http", "legacy": "sse"},
	}
	rendered := map[string][]string{"linear": {"claude", "codex", "pi"}, "legacy": {"claude"}}
	got := remoteMCPNotices(withheld, rendered)
	want := []string{
		`legacy: codex, copilot have no "sse" MCP form in yolo yet; it is configured for claude`,
		`linear: copilot, muse-code have no remote MCP form in yolo yet; it is configured for claude, codex, pi`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("notices:\n got %q\nwant %q", got, want)
	}
	if got := remoteMCPNotices(map[string]map[string]string{"copilot": {"linear": "http"}}, nil); !reflect.DeepEqual(got,
		[]string{`linear: copilot has no remote MCP form in yolo yet, so no agent in this jail is configured for it`}) {
		t.Errorf("no-agent notice: %q", got)
	}
}
