package check

import (
	"strings"
	"testing"
)

// TestAMovedStateIsAWarningWithItsNextStep is SD-D5's `yolo check` half: a running jail's moved
// `.yolo` is one warning whose note carries the step, and a workspace without one prints nothing.
// TestEverySectionIsWired pins the section's call in Check.
func TestAMovedStateIsAWarningWithItsNextStep(t *testing.T) {
	old := stateMovedFindings
	t.Cleanup(func() { stateMovedFindings = old })
	var buf strings.Builder
	r := newReporter(&buf, false)

	stateMovedFindings = func(string) []string { return nil }
	(&Options{}).sectionStateMoved(r, "/w")
	if buf.Len() != 0 || r.warned != 0 {
		t.Fatalf("a healthy workspace printed:\n%s", buf.String())
	}

	stateMovedFindings = func(ws string) []string {
		return []string{ws + "/.yolo is not the directory this jail started with.",
			"Put it back with `mv <where you moved it> /w/.yolo`."}
	}
	(&Options{}).sectionStateMoved(r, "/w")
	if r.warned != 1 || !strings.Contains(buf.String(), "/w/.yolo is not the directory") ||
		!strings.Contains(buf.String(), "mv <where you moved it>") {
		t.Errorf("warned %d:\n%s", r.warned, buf.String())
	}
}
