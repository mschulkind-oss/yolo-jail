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

// TestTheBriefingNamesTheSchedulersThatIgnoreTheValue: the ignore clause follows the grading
// the launch line and `yolo check` use (ioprio.Grade), so an agent on an mq-deadline host is
// never told "low" makes its builds yield — mq-deadline keeps one queue per class and ignores
// the level, and honors "idle" only.
func TestTheBriefingNamesTheSchedulersThatIgnoreTheValue(t *testing.T) {
	for _, tc := range []struct{ p, want, never string }{
		{"low", "disks whose scheduler is mq-deadline, kyber or none ignore it", ""},
		{"idle", "disks whose scheduler is kyber or none ignore it", "mq-deadline"},
	} {
		body := BriefingContent(BriefingInput{Workspace: "/w", IOPriority: tc.p})
		var line string
		for _, l := range strings.Split(body, "\n") {
			if strings.HasPrefix(l, "- **Disk I/O priority**") {
				line = l
			}
		}
		if !strings.Contains(line, tc.want) {
			t.Errorf("%s: the line is %q, want it to say %q", tc.p, line, tc.want)
		}
		if tc.never != "" && strings.Contains(line, tc.never) {
			t.Errorf("%s: the line names %s, which honors it: %q", tc.p, tc.never, line)
		}
	}
}

// TestTheBriefingScopesTheIOPriorityToTheJail: the class reaches the jail's own processes and
// nothing a host process does for it. Wherever the host nix daemon is mounted the jail runs
// with NIX_REMOTE=daemon, so a `nix build` and its store writes run in the daemon's builders
// on the host, at the host's priority; an agent told "builds yield" would count those in
// (docs/design/io-priority.md §2, Non-Goal 6).
func TestTheBriefingScopesTheIOPriorityToTheJail(t *testing.T) {
	body := BriefingContent(BriefingInput{Workspace: "/w", IOPriority: "low"})
	var line string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(l, "- **Disk I/O priority**") {
			line = l
		}
	}
	for _, want := range []string{"every process in this jail", "keeps the host's priority", "`nix build` through the host nix daemon"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line is %q, want it to say %q", line, want)
		}
	}
}
