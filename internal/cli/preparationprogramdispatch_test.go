package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func programPreparationSlot(t *testing.T, fx *patchedAdvanceFixture) {
	t.Helper()
	f := fx.fork(t)
	req := run.ForkBuildRequest{Pins: []packload.ForkPin{{Fork: f}}, Platform: patchedTestPlatform, Runtime: "podman", Interrupt: &run.ActInterrupt{}}
	d, _ := runBuildSlot(run.BuildSlotRequest{Forks: &req, MaxChecks: 1, MaxBuilds: 1}, io.Discard, false)
	if d[f.Bin].Key == "" {
		t.Fatalf("slot failed: %+v", d)
	}
}

// The production slot must subscribe even when deliverPatchedFork has nothing due.
// The channel drives the real scheduler; no subsequent launch supplies work.
func TestPreparationProgramTimerAdvancesWithoutAnotherLaunch(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.launch(t, "podman")
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
	stubSpawn(t)
	programPreparationSlot(t, fx)
	old := fx.record(t).Good.Entry
	subs, err := readPreparationSubscriptions(preparationRoot())
	if err != nil || len(subs) != 1 {
		t.Fatalf("slot did not register: %v %+v", err, subs)
	}
	for _, sub := range subs {
		f, err := currentPreparationTree(context.Background(), sub)
		if err != nil || f.IsTree() || f.Build != fx.fork(t).Build || len(f.Produces) != 1 {
			t.Fatalf("lost program declaration: %+v %v", f, err)
		}
	}
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.later(2 * time.Hour)
	runProgramPreparationTick(t, func() bool {
		for _, sub := range subs {
			snap, err := readPreparationSnapshotFor(preparationRoot(), sub)
			if err != nil || snap.ServingEntry == "" || snap.ServingEntry == old {
				return false
			}
		}
		return true
	})
	d, g := admittedProgramServing(fx.fork(t), "podman", patchedTestPlatform)
	if g == nil || d.Key == "" || d.Key == old || g.Entry != d.Key {
		t.Fatalf("timer did not admit exact Good: %+v %+v", d, g)
	}
}

func runProgramPreparationTick(t *testing.T, complete func() bool) {
	t.Helper()
	previousNow := preparationNow
	preparationNow = func() time.Time { return patchedNow() }
	defer func() { preparationNow = previousNow }()
	subs, err := readPreparationSubscriptions(preparationRoot())
	if err != nil {
		t.Fatal(err)
	}
	previous := map[string]string{}
	for id, sub := range subs {
		snap, _ := readPreparationSnapshotFor(preparationRoot(), sub)
		previous[id] = snap.WorkerIdentity
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	done := make(chan error, 1)
	go func() {
		defer close(done)
		done <- runPreparationWorker(ctx, preparationRoot(), run.SelfExecPath(), nil, func(ownerCtx context.Context) error {
			return runPreparationScheduler(ownerCtx, ticks, io.Discard)
		})
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil && err != context.Canceled {
			t.Error(err)
		}
	}()
	owner := awaitPreparationOwner(t, preparationRoot(), done, cancel)
	if err := controlPreparationWorker(context.Background(), preparationRoot(), owner.Identity, "wake"); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		published := len(subs) > 0
		for id, sub := range subs {
			snap, err := readPreparationSnapshotFor(preparationRoot(), sub)
			published = published && err == nil && snap.WorkerIdentity != "" && snap.WorkerIdentity != previous[id]
		}
		if published && complete() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	state, err := loadPreparationState()
	t.Fatalf("timer did not publish expected program outcome: state=%+v err=%v", state, err)
}

func TestPreparationProgramRegistrationExcludesUnauthorizedAndUnsupportedInputs(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
	stubSpawn(t)
	f := fx.fork(t)
	for _, tc := range []struct {
		name              string
		fork              packload.Fork
		runtime, platform string
	}{
		{"plain", func() packload.Fork { v := f; v.Patches = ""; return v }(), "podman", patchedTestPlatform},
		{"tree", func() packload.Fork { v := f; v.Into = ".tool/ext"; return v }(), "podman", patchedTestPlatform},
		{"darwin", f, "podman", "darwin/arm64"},
		{"arch", f, "podman", "linux/other"},
		{"runtime", f, "macos-user", patchedTestPlatform},
		{"platform-excluded", func() packload.Fork { v := f; v.Platforms = []string{"darwin-arm64"}; return v }(), "podman", patchedTestPlatform},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := registerProgramPreparation([]packload.ForkPin{{Fork: tc.fork}}, run.ForkBuildRequest{Runtime: tc.runtime, Platform: tc.platform}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			subs, err := readPreparationSubscriptions(preparationRoot())
			if err != nil || len(subs) != 0 {
				t.Fatalf("excluded input registered: %+v %v", subs, err)
			}
		})
	}
	fx.writeUserConfig(t, `,"agent_updates":false`)
	if err := registerProgramPreparation([]packload.ForkPin{{Fork: f}}, run.ForkBuildRequest{Runtime: "podman", Platform: patchedTestPlatform}, io.Discard); err != nil {
		t.Fatal(err)
	}
	subs, err := readPreparationSubscriptions(preparationRoot())
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range subs {
		if sub.Active {
			t.Fatalf("unauthorized input admitted: %+v", sub)
		}
	}
}

func TestPreparationProgramRevocationAndRecipeEditRejectAdmission(t *testing.T) {
	for _, edit := range []string{"revoke", "recipe"} {
		t.Run(edit, func(t *testing.T) {
			fx := newPatchedAdvanceFixture(t, "")
			fx.launch(t, "podman")
			fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
			stubSpawn(t)
			programPreparationSlot(t, fx)
			old := fx.record(t).Good.Entry
			fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			fx.later(2 * time.Hour)
			prior := forkBuildChild
			forkBuildChild = func(ctx context.Context, d time.Duration, staging string, b forkBuild, s captureStreams, color bool) (int, bool) {
				rc, interrupted := prior(ctx, d, staging, b, s, color)
				if edit == "revoke" {
					fx.writeUserConfig(t, `,"agent_updates":false`)
				} else {
					bytes, err := os.ReadFile(fx.manifest)
					if err != nil {
						t.Error(err)
					}
					writeFile(t, fx.manifest, strings.Replace(string(bytes), "sh build.sh", "sh changed.sh", 1))
				}
				return rc, interrupted
			}
			runProgramPreparationTick(t, func() bool {
				o, err := readBackgroundOutcome(backgroundOutcomePath("forkpack/tool"))
				return err == nil && o.At == fx.now.Unix()
			})
			if got := fx.record(t).Good.Entry; got != old {
				t.Fatalf("obsolete work admitted: %s -> %s", old, got)
			}
		})
	}
}

func TestPreparationProgramFailureBackoffPreservesGood(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.launch(t, "podman")
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
	stubSpawn(t)
	programPreparationSlot(t, fx)
	old := fx.record(t).Good.Entry
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.later(2 * time.Hour)
	fx.rc = 1
	runProgramPreparationTick(t, func() bool {
		o, err := readBackgroundOutcome(backgroundOutcomePath("forkpack/tool"))
		return err == nil && o.RetryAt > fx.now.Unix()
	})
	if fx.record(t).Good.Entry != old {
		t.Fatal("failure replaced Good")
	}
	failure, err := readBackgroundOutcome(backgroundOutcomePath("forkpack/tool"))
	if err != nil {
		t.Fatal(err)
	}
	retryAt := time.Unix(failure.RetryAt, 0)
	subs, err := readPreparationSubscriptions(preparationRoot())
	if err != nil {
		t.Fatal(err)
	}
	previous := ""
	for _, sub := range subs {
		snap, err := readPreparationSnapshotFor(preparationRoot(), sub)
		if err != nil {
			t.Fatal(err)
		}
		previous = snap.WorkerIdentity
	}
	fx.mu.Lock()
	before := fx.child
	fx.mu.Unlock()
	runProgramPreparationTick(t, func() bool {
		subs, _ := readPreparationSubscriptions(preparationRoot())
		for _, sub := range subs {
			snap, err := readPreparationSnapshotFor(preparationRoot(), sub)
			if err == nil && snap.WorkerIdentity != "" && snap.WorkerIdentity != previous {
				return true
			}
		}
		return false
	})
	fx.mu.Lock()
	after := fx.child
	fx.mu.Unlock()
	if after != before {
		t.Fatal("backoff retried build")
	}
	fx.rc = 0
	fx.setNow(retryAt.Add(2 * time.Hour))
	runProgramPreparationTick(t, func() bool {
		for _, sub := range subs {
			snap, err := readPreparationSnapshotFor(preparationRoot(), sub)
			if err == nil && snap.ServingEntry != "" && snap.ServingEntry != old {
				return true
			}
		}
		return false
	})
	if d, g := admittedProgramServing(fx.fork(t), "podman", patchedTestPlatform); g == nil || d.Key == old {
		t.Fatalf("retry did not admit exact Good: %+v %+v", d, g)
	}

}

func TestPreparationProgramPatchFailureDoesNotBuild(t *testing.T) {
	fx := newPatchedAdvanceFixture(t, "")
	fx.launch(t, "podman")
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":"next-launch"}`)
	stubSpawn(t)
	programPreparationSlot(t, fx)
	old := fx.record(t).Good.Entry
	fx.commit(t, "v1.1.0", map[int]string{11: "conflict", 14: "fourteen"})
	fx.later(2 * time.Hour)
	fx.mu.Lock()
	before := fx.child
	fx.mu.Unlock()
	var outcome backgroundOutcome
	runProgramPreparationTick(t, func() bool {
		var err error
		outcome, err = readBackgroundOutcome(backgroundOutcomePath("forkpack/tool"))
		return err == nil && outcome.At == fx.now.Unix() && outcome.State == bgFailed && outcome.PatchFailure != nil
	})
	fx.mu.Lock()
	after := fx.child
	fx.mu.Unlock()
	if after != before || fx.record(t).Good.Entry != old {
		t.Fatal("patch failure built or replaced Good")
	}
	record := fx.record(t)
	failure := outcome.PatchFailure
	current := record.CurrentPatchFailure(record.Read, failure.Series)
	if outcome.State != bgFailed || failure.Owner != fx.fork(t).Key() || failure.Kind != "conflict" ||
		record.Check == nil || record.Check.Seq != record.Seq || record.Read != failure.Inputs ||
		!sameBackgroundPatchFailureIdentity(current, failure) {
		t.Fatalf("background failure is not the exact current typed CheckRecord failure: outcome=%+v check=%+v current=%+v",
			outcome, record.Check, current)
	}
	if d, g := admittedProgramServing(fx.fork(t), "podman", patchedTestPlatform); d.PatchFailure == nil || g != nil {
		t.Fatalf("patch failure qualified: %+v %+v", d, g)
	}
}
