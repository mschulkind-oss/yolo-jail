package ghbroker

import (
	"slices"
	"strings"
	"testing"
)

// menuCommands is the commands a pending message puts alone on their own indented lines.
func menuCommands(msg string) []string {
	var out []string
	for _, line := range strings.Split(msg, "\n") {
		if strings.HasPrefix(line, "    ") && strings.TrimSpace(line) != "" {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// A pending write's message lists every answer the host can give, each command alone on its
// own line, and the waiter that wakes the agent; a creation adds the project request that
// makes it last.
func TestPendingMessageListsEveryAnswerOnItsOwnLine(t *testing.T) {
	const id = "r-0123456789abcdef0123456789abcdef"
	for _, c := range []struct {
		name      string
		t         writeTarget
		want, not []string
	}{
		{"pr comment", writeTarget{Kind: targetPR, Repo: "o/r", Number: 7},
			[]string{"yolo approve " + id, "yolo approve " + id + " --for 15m", "yolo approve " + id + " --add", "yolo wait " + id},
			[]string{"yolo config apply"}},
		{"merge", writeTarget{Kind: targetMerge, Repo: "o/r", Number: 7},
			[]string{"yolo approve " + id, "yolo approve " + id + " --for 15m", "yolo wait " + id},
			[]string{"yolo approve " + id + " --add"}},
		{"create pr", writeTarget{Kind: targetCreatePR, Repo: "o/r"},
			[]string{"yolo approve " + id, "yolo request github create_pr", "yolo config apply", "yolo wait " + id},
			[]string{"yolo approve " + id + " --add"}},
	} {
		msg := pendingMessage(c.t, nil, id, "")
		cmds := menuCommands(msg)
		for _, w := range c.want {
			if !slices.Contains(cmds, w) {
				t.Errorf("%s: %q is not alone on its own line:\n%s", c.name, w, msg)
			}
		}
		for _, n := range c.not {
			if slices.Contains(cmds, n) {
				t.Errorf("%s: the menu offers %q:\n%s", c.name, n, msg)
			}
		}
		if !strings.Contains(msg, "not through `yolo host-run`") {
			t.Errorf("%s: the menu does not say the human runs it directly:\n%s", c.name, msg)
		}
	}
}
