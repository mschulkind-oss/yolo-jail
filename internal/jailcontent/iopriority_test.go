package jailcontent

import (
	"strings"
	"testing"
)

// TestBriefingStatesThePassedIOPriorityOnItsOwnLine: a passed class gets its own line,
// called advisory, and never joins the "kernel-enforced" limits line — the agent can raise
// it, and the disk may ignore it (docs/design/io-priority.md §5.2). Nothing passed, or a
// value that names no class, means no line.
func TestBriefingStatesThePassedIOPriorityOnItsOwnLine(t *testing.T) {
	body := BriefingContent(BriefingInput{
		Workspace:  "/w",
		Resources:  map[string]any{"memory": "8g"},
		IOPriority: "idle",
	})
	var ioLine, limits string
	for _, l := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(l, "- **Disk I/O priority**"):
			ioLine = l
		case strings.HasPrefix(l, "- **Resource limits**"):
			limits = l
		}
	}
	if !strings.Contains(ioLine, "`idle` (idle class)") || !strings.Contains(ioLine, "Advisory, not a limit") {
		t.Errorf("the I/O priority line is %q", ioLine)
	}
	if strings.Contains(limits, "io=") || strings.Contains(limits, "idle") {
		t.Errorf("the kernel-enforced limits line names the priority: %q", limits)
	}
	for _, v := range []string{"", "normal", "realtime"} {
		if body := BriefingContent(BriefingInput{Workspace: "/w", IOPriority: v}); strings.Contains(body, "Disk I/O priority") {
			t.Errorf("IOPriority=%q gained a line", v)
		}
	}
}
