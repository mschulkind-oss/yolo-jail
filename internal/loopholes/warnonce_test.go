package loopholes

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestAMissingModuleDirIsReportedOncePerLaunchNotOncePerDiscovery is the repetition half of
// the 2026-09-09 defect.
//
// A container launch discovers ONCE PER CONSUMER — the briefing, the broker gate, the
// argv's broker gate, the runtime args and the daemon spawn each build their own Set
// through NewHostSet — so four missing module dirs printed twenty lines on the host this
// was measured on (2026-09-09), and a reader could not tell four facts from twenty. The
// loop below repeats the construction the way those consumers do; the repeat COUNT is this
// test's own and is not a claim about how many passes a launch makes today.
//
// BOTH HALVES ARE ASSERTED, because the wrong fix here is silence: the line must be said
// (OQ-A9 — a loophole whose module dir is absent is NOT active, and the user is told) and
// said once.
func TestAMissingModuleDirIsReportedOncePerLaunchNotOncePerDiscovery(t *testing.T) {
	warnings := captureWarnings(t)
	ResetPackModules()
	ResetPackSupersessions()
	t.Cleanup(func() {
		ResetPackModules()
		ResetPackSupersessions()
	})

	// A module dir that is not there — the shape a torn-down staging root leaves behind.
	gone := filepath.Join(t.TempDir(), "packs", "_official", "journal", "loopholes", "journal")
	SetPackModules([]PackModule{{Dir: gone, HostExecApproved: true}})
	SetPackSupersessions(nil)

	// Repeated construction, as a launch's several consumers each perform.
	for i := 0; i < 5; i++ {
		NewHostSet(nil)
	}

	said := 0
	for _, w := range *warnings {
		if strings.Contains(w, gone) && strings.Contains(w, "is not a directory") {
			said++
		}
	}
	switch {
	case said == 0:
		t.Errorf("a missing loophole module dir was not reported at all (%d warnings: %v) — "+
			"an absent module dir means the loophole is NOT active, and silence there is the "+
			"failure the warning exists to prevent", len(*warnings), *warnings)
	case said != 1:
		t.Errorf("a missing loophole module dir was reported %d times across repeated "+
			"discovery, want 1 — a launch banner that repeats four facts twenty times "+
			"buries them\nwarnings: %v", said, *warnings)
	}
}
