package cli

// patchedeverylaunch_test.go pins the patched fork's upstream check at EVERY FRESH LAUNCH, through the
// production fork-build slot (patchedadvance.go; docs/design/local-pack-clones.md §4.3, §5, LPC-D6): a
// launch inside the old hour probes with one ls-remote; an unmoved upstream replays and builds nothing; a
// moved one advances as before; a check that cannot finish runs the good build with the §4.3 warning and
// never refuses; and an `agent_updates` hold, which runs no check, still runs none.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
)

func gitRuns(t *testing.T, log, sub string) int {
	t.Helper()
	data, _ := os.ReadFile(log)
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		for _, w := range strings.Fields(line) {
			if w == sub {
				n++
				break
			}
		}
	}
	return n
}

// A LAUNCH TEN MINUTES AFTER THE LAST probes the upstream, and an unmoved one replays and builds nothing.
func TestALaunchInsideTheOldHourProbesAndAnUnmovedUpstreamBuildsNothing(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	log := patchedGitWrapper(t, "")
	fx.later(10 * time.Minute)
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != first.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("an unmoved upstream handed %+v (first %+v) after %d builds, %d child builds, want the good build and no new one\n%s", r.delivery, first.delivery, len(fx.builds), fx.child, out)
	}
	if gitRuns(t, log, "ls-remote") != 1 || gitRuns(t, log, "fetch") != 0 || gitRuns(t, log, "am") != 0 {
		data, _ := os.ReadFile(log)
		t.Errorf("an unmoved upstream ran git:\n%s\nwant one ls-remote, no fetch, no replay", data)
	}
	if rec := fx.record(t); rec.CheckedAt != fx.now.Unix() {
		t.Errorf("the stamp = %d, want the last attempt %d", rec.CheckedAt, fx.now.Unix())
	}
}

// A MOVED UPSTREAM ADVANCES AS BEFORE, inside the old hour.
func TestALaunchInsideTheOldHourAdvancesToAMovedUpstream(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	log := patchedGitWrapper(t, "")
	fx.later(10 * time.Minute)
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key == "" || r.delivery.Key == first.delivery.Key || len(fx.builds) != 2 {
		t.Fatalf("a moved upstream handed %+v after %d builds, want a new build\n%s", r.delivery, len(fx.builds), out)
	}
	if gitRuns(t, log, "fetch") == 0 {
		t.Error("a moved upstream was never fetched")
	}
}

// A CHECK THAT TIMES OUT RUNS THE GOOD BUILD with the §4.3 warning, and never refuses.
func TestATimedOutCheckRunsTheGoodBuildWithTheWarning(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	prev := patchedCheckStart
	patchedCheckStart = func() (*packsrc.PackCheck, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the deadline has fired before the probe starts
		return &packsrc.PackCheck{Ctx: ctx, Limit: packsrc.PackCheckDeadline}, cancel
	}
	t.Cleanup(func() { patchedCheckStart = prev })
	fx.later(10 * time.Minute)
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != first.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("a timed-out check handed %+v after %d builds, want the good build\n%s", r.delivery, len(fx.builds), out)
	}
	for _, want := range []string{"could not check origin main (", "timed out after 5s) — still running v1.1.0",
		"Nothing to do until the network is back; `yolo pack update` checks on demand."} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "in an hour") {
		t.Errorf("the warning still promises an hourly check:\n%s", out)
	}
}

// AN `agent_updates` HOLD STILL RUNS NO CHECK: not a probe, not a fetch.
func TestAHeldForkNeverChecks(t *testing.T) {
	fx, _, _, first, _, _ := firstAdvance(t)
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	log := patchedGitWrapper(t, "")
	fx.later(2 * time.Hour)
	r, out, _ := fx.launch(t, "podman")
	if r.delivery.Key != first.delivery.Key {
		t.Fatalf("a held launch handed %+v, want the good build\n%s", r.delivery, out)
	}
	if gitRuns(t, log, "ls-remote") != 0 || gitRuns(t, log, "fetch") != 0 {
		data, _ := os.ReadFile(log)
		t.Errorf("a held launch checked its upstream:\n%s", data)
	}
}
