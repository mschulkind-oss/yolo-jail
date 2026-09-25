package cli

import (
	"strings"
	"testing"
)

// internalnodefloor_test.go pins `yolo internal node-floor-satisfied`'s one output: when it
// answers "no", stdout carries the interpreters that ARE available, because OQ-AR3's refusal
// (docs/reference/agent-program-runtimes.md) must name them and the generated bootstrap quotes
// this line rather than walking the mise store a second time in shell.
//
// Driven through runInternal, not runNodeFloorSatisfied, so the dispatcher's own argument — the
// stream it hands the function — is under test too: passing io.Discard there would pass a
// callee-only test and leave every refusal saying "none".

// A floor nothing on any machine satisfies: exit 1, and exactly one non-empty line on stdout.
// Its content is machine-dependent (whatever node this host has, or "none (…)"), so the test
// asserts the shape; entrypoint.TestAvailableNodes* pin the content against a fixture store.
func TestNodeFloorSatisfiedNamesWhatIsAvailableWhenItSaysNo(t *testing.T) {
	var rc int
	out := captureStdout(t, func() { rc = runInternal([]string{"node-floor-satisfied", "999"}) })
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (not satisfied)", rc)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("an unsatisfied floor printed nothing on stdout, so the bootstrap's refusal " +
			"cannot say what IS available")
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Errorf("want exactly one line on stdout for the refusal to quote, got %q", out)
	}
}

// Misuse keeps stdout empty: its diagnostics are stderr's, and a line on stdout would be
// quoted into a refusal as though it were a list of interpreters.
func TestNodeFloorSatisfiedMisuseLeavesStdoutEmpty(t *testing.T) {
	for _, args := range [][]string{
		{"node-floor-satisfied"},
		{"node-floor-satisfied", ">=22.19"},
	} {
		var rc int
		out := captureStdout(t, func() { rc = runInternal(args) })
		if rc != 2 {
			t.Errorf("%v: rc = %d, want 2 (misuse)", args, rc)
		}
		if out != "" {
			t.Errorf("%v: misuse wrote to stdout: %q", args, out)
		}
	}
}
