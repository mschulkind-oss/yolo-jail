package loopholes

// doctortimeout_test.go covers the one TIMEOUT in this package: a doctor_cmd that
// never returns.
//
// The branch used to answer the bare word "timeout", which leaves a reader unable to
// tell a self-check that HUNG from one that is merely slower than a deadline it
// cannot see — and the deadline is a caller's argument, so there is no constant to
// look up either. It is driven through RunDoctorChecks rather than runOne so the
// production CALL SITE is what's pinned: the report has to reach a reader through the
// DoctorResult a `yolo check` prints.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDoctorTimeoutNamesTheDeadlineItBurned(t *testing.T) {
	unsetJail(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	isolateModules(t)
	// Outside the jail-home tree, or the placement rule
	// (docs/reference/loophole-system.md#the-placement-rule) withholds the check and
	// this test would pass on a refusal instead of a timeout.
	tools := filepath.Join(home, "tools")
	if err := os.MkdirAll(tools, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(tools, "hang.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	mod := writeDoctorModule(t, t.TempDir(), "hanghole", script)
	set := NewSet(DiscoverOptions{PackModules: []PackModule{{Dir: mod, HostExecApproved: true}}})
	lp, ok := set.Lookup("hanghole")
	if !ok {
		t.Fatalf("fixture module %s was not discovered", mod)
	}

	start := time.Now()
	results := set.RunDoctorChecks([]*Loophole{lp}, 150*time.Millisecond)
	elapsed := time.Since(start)
	if len(results) != 1 {
		t.Fatalf("want one result, got %d", len(results))
	}
	if results[0].RC != nil {
		t.Errorf("rc = %d; a self-check that never returned has no exit status", *results[0].RC)
	}
	if results[0].Output != "timeout after 150ms" {
		t.Errorf("doctor timeout said %q; it must name the deadline it burned, because "+
			"the deadline is a caller's argument and no reader can look it up",
			results[0].Output)
	}
	// And the deadline is real: the kill happens at it, not at the process's own exit.
	if elapsed > 5*time.Second {
		t.Errorf("RunDoctorChecks waited %s for a 150ms deadline", elapsed)
	}
}

// TestDoctorTimeoutDefaultIsNamed: the zero-value branch. A launch path's timeout
// spelled as a magic literal is a duration no reader can cite, and this one is what
// the message above reports.
func TestDoctorTimeoutDefaultIsNamed(t *testing.T) {
	if doctorTimeoutDefault != 10*time.Second {
		t.Errorf("doctorTimeoutDefault = %s, want 10s", doctorTimeoutDefault)
	}
	if !strings.Contains(doctorTimeoutDefault.String(), "s") {
		t.Errorf("a duration that does not render as a unit cannot appear in a report")
	}
}
