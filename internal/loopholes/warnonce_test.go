package loopholes

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
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

// TestConcurrentDiscoveriesSayEachLineOnceWithoutRacing is the concurrency half of the once
// rule: two discoveries in one process can run at the same time, and the said set is shared.
//
// The production pair is `yolo host --` with `host_apply_on_launch` on. The launch gate runs
// `yolo host apply`'s observe pass on a goroutine it abandons after its budget
// (internal/cli's surveyHostApplyWithinBudget), and that pass discovers loopholes
// (run.HostDoorwayLoopholes) while the launch, carrying on, discovers its own doorways
// (run.PlanHostDoorways). Before warnf locked its check-and-set, a module dir that warns made
// both goroutines read and write saidWarnings unguarded: `go test -race` named the pair, and
// the Go runtime may stop such a process with "concurrent map read and map write", killing the
// launch.
//
// So this runs several discoveries over a missing module dir at once, each followed by a burst
// of distinct lines, and asserts both halves: every line is said, and said once. Without the
// lock, -race reports the race, and a plain run usually dies on the runtime's concurrent-map
// check or counts a line twice (the check and the set are separate steps).
func TestConcurrentDiscoveriesSayEachLineOnceWithoutRacing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))
	var mu sync.Mutex
	said := map[string]int{}
	prev := warnSink
	resetSaidWarnings()
	warnSink = func(msg string) {
		mu.Lock()
		said[msg]++
		mu.Unlock()
	}
	t.Cleanup(func() { warnSink = prev; resetSaidWarnings() })

	gone := filepath.Join(t.TempDir(), "packs", "_official", "journal", "loopholes", "journal")
	opts := DiscoverOptions{PackModules: []PackModule{{Dir: gone, HostExecApproved: true}}}
	const workers, lines = 8, 400
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			NewSet(opts)
			for i := 0; i < lines; i++ {
				warnf("concurrency canary %d", i)
			}
		}()
	}
	close(start)
	wg.Wait()

	missing := 0
	for msg, n := range said {
		if strings.Contains(msg, gone) && strings.Contains(msg, "is not a directory") {
			missing += n
		}
		if n != 1 {
			t.Errorf("%q was said %d times by %d concurrent discoveries, want once", msg, n, workers)
		}
	}
	if missing != 1 {
		t.Errorf("the missing module dir was reported %d times, want 1 (said: %v)", missing, said)
	}
	for i := 0; i < lines; i++ {
		if msg := fmt.Sprintf("concurrency canary %d", i); said[msg] == 0 {
			t.Errorf("%q was never said — the once rule must not turn into silence", msg)
		}
	}
}
