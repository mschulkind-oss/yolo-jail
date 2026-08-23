package integration

// handoff_test.go is the end-to-end half of the one-time host→jail carry-in
// (docs/design/host-to-jail-handoff.md): a real launch, against a real briefing file
// inside a real jail.
//
// The unit tests in internal/cli/run pin the wire from .yolo/handover.md through
// refreshJailBriefings to a staged briefing file. What they cannot see is the last hop —
// whether that staged file is the one the agent actually reads at /home/agent/.claude/
// CLAUDE.md. Mounts, staging names and inode-preserving writes all sit between the two,
// and none of them are exercised by a test that stops at the staging dir.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// handoffSentinel is deliberately not a word that appears in the briefing template, so a
// match proves the FILED CONTENT arrived rather than the section heading.
const handoffSentinel = "HANDOFF-SENTINEL-7f3a wire the OAuth broker"

// A filed handoff reaches the briefing the agent reads, exactly once. The second launch
// is the whole point: the stale-handoff bug this design exists to fix was a four-week-old
// handover surfacing as the current task, so "gone on the next launch" is the assertion
// that matters, not "present on the first".
func TestHandoffSurfacesOnceThenIsConsumed(t *testing.T) {
	requireJail(t)

	dir := writeProjectWithPacks(t, `{}`, "claude")
	yoloDir := filepath.Join(dir, ".yolo")
	if err := os.MkdirAll(yoloDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(yoloDir, "handover.md")
	if err := os.WriteFile(pointer, []byte(handoffSentinel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Launch 1: the handoff is in the briefing the agent reads.
	probe := `rg -c 'HANDOFF-SENTINEL-7f3a' /home/agent/.claude/CLAUDE.md || echo HANDOFF-ABSENT`
	first := runYolo(t, dir, probe)
	if first.rc != 0 {
		t.Fatalf("first launch failed: rc %d\nstdout: %s\nstderr: %s", first.rc, first.stdout, first.stderr)
	}
	if strings.Contains(first.stdout, "HANDOFF-ABSENT") {
		t.Errorf("filed handoff never reached the agent's briefing:\n%s", first.stdout)
	}

	// The pointer is consumed host-side, and the launch says so — the notice is the only
	// protection against a handoff carried off by a launch nobody meant to spend it on.
	if _, err := os.Stat(pointer); !os.IsNotExist(err) {
		t.Errorf("pointer survived a launch that carried it (stat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(yoloDir, "handover.md.consumed")); err != nil {
		t.Errorf("consumed marker missing: %v", err)
	}
	if !strings.Contains(first.stderr, "handover.md.consumed") {
		t.Errorf("consuming a handoff must name the restore path on stderr:\n%s", first.stderr)
	}

	// Launch 2: nothing. A stale handoff is never re-read as the task.
	second := runYolo(t, dir, probe)
	if second.rc != 0 {
		t.Fatalf("second launch failed: rc %d\nstdout: %s\nstderr: %s", second.rc, second.stdout, second.stderr)
	}
	if !strings.Contains(second.stdout, "HANDOFF-ABSENT") {
		t.Errorf("consumed handoff resurfaced on a later launch — this is the stale-task "+
			"bug the design was written for:\n%s", second.stdout)
	}
}
