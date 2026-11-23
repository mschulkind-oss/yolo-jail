package cli

// yolostepfatal_test.go drives PD-NF1 (the maintainer's ruling of 2026-10-16,
// docs/design/program-delivery.md) through the advance's real call sites: when yolo has found a newer
// candidate of a patched fork or extension and ONE OF YOLO'S OWN BUILD STEPS keeps it from building —
// the runtime not starting a build jail, a lock wait that runs out, an error replaying the series —
// the act STOPS with the one refusal shape, as for a failed build (OQ-PPX3), and never runs the older
// build on its own. Only a hatch the user set or an `agent_updates` hold runs it. Being unable to
// reach upstream at all is no failure: what is here runs, and the act says it could not check.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// clearHatches unsets every scoped recovery hatch for the test.
func clearHatches(t *testing.T) {
	t.Helper()
	for _, env := range patchrecovery.Scoped {
		t.Setenv(env, "")
	}
}

// jailThatNeverStarts is the state several tests start from: a good build at v1.1.0, then v1.3.0,
// which the series takes but whose build jail exits before its build line runs, and the launch that
// met it.
func jailThatNeverStarts(t *testing.T) (*patchedAdvanceFixture, advanceResult, advanceResult, string) {
	t.Helper()
	clearHatches(t)
	fx, _, _, good, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", fitting)
	fx.rc, fx.ran, fx.said = 125, false, "Error: the fixture's runtime refused the container"
	fx.later(2 * time.Hour)
	stopped, out, _ := fx.launch(t, "podman")
	if pf := stopped.delivery.PatchFailure; !stopped.failed || pf == nil || pf.Cause != packsrc.BuildCauseJail ||
		pf.Target.Commit != v13 || stopped.delivery.Key != good.delivery.Key {
		t.Fatalf("a build jail that never started did not stop the launch: %+v\n%s", stopped, out)
	}
	return fx, good, stopped, v13
}

// THE WAIT FOR ANOTHER BUILD'S LOCK RUNS OUT (#3): with a good build serving the launch stops, the
// error block naming the wait and its repair; nothing is recorded, since the build holding the lock
// records its own result, so the next launch, once the lock is free, builds it.
func TestALockWaitThatRunsOutStopsTheLaunch(t *testing.T) {
	clearHatches(t)
	fx, _, _, good, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", fitting)
	fx.later(2 * time.Hour)
	b := forkBuild{Fork: fx.fork(t), Commit: v13, Platform: patchedTestPlatform, Series: mustSeries(t, fx)}
	if err := os.MkdirAll(filepath.Dir(b.lockPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	held, err := pidlock.Acquire(b.lockPath(), pidlock.NoWait, nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := forkBuildLockBound
	forkBuildLockBound = 100 * time.Millisecond
	t.Cleanup(func() { forkBuildLockBound = prev })
	got, out, _ := fx.launch(t, "podman")
	pf := got.delivery.PatchFailure
	if !got.failed || pf == nil || pf.Kind != packsrc.PatchFailureBuild || pf.Cause != packsrc.BuildCauseLock ||
		pf.Target.Commit != v13 || got.delivery.Key != good.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("the lock wait that ran out handed %+v after %d builds, want the launch stopped\n%s", got, len(fx.builds), out)
	}
	for _, w := range []string{"ERROR: forkpack/tool: yolo could not build upstream v1.3.0 (" + shortSHA(v13) + ")",
		"  Cause: waited ", "once the build holding its lock ends", "    yolo capture tool\n",
		"YOLO_ALLOW_PATCH_FAILURES=forkpack/tool yolo"} {
		if !strings.Contains(out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, out)
		}
	}
	for _, w := range []string{"still running v1.1.0", "YOLO_ALLOW_OLDER_UPSTREAM"} {
		if strings.Contains(out, w) {
			t.Errorf("the refusal says %q:\n%s", w, out)
		}
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("a lock wait was recorded as this launch's failed build: %+v", o)
		}
	}
	held.Release()
	if moved, out, _ := fx.launch(t, "podman"); !moved.built || moved.failed || len(fx.builds) != 2 {
		t.Errorf("once the lock was free the next launch did not build: %+v\n%s", moved, out)
	}
}

// A HATCH RUNS THE GOOD BUILD PAST ONE OF YOLO'S OWN STEPS (#1): YOLO_USE_CACHED_GOOD, through the
// launch's build slot, hands the good build with no check and no build while the jail's failure is
// recorded; without it the slot hands the failure beside the good build, which the launch refuses.
func TestCachedGoodRunsTheGoodBuildPastAJailThatDidNotStart(t *testing.T) {
	fx, good, _, _ := jailThatNeverStarts(t)
	plain, out := askForCachedGood(t, fx, "")
	if plain.PatchFailure == nil || plain.PatchFailure.Cause != packsrc.BuildCauseJail || plain.Key != good.delivery.Key {
		t.Fatalf("the slot handed %+v, want the jail's failure beside the good build\n%s", plain, out)
	}
	builds := len(fx.builds)
	cached, out := askForCachedGood(t, fx, "forkpack/tool")
	if cached.Key != good.delivery.Key || cached.PatchFailure != nil || len(fx.builds) != builds {
		t.Fatalf("cached-good recovery handed %+v after %d builds, want the good build\n%s", cached, len(fx.builds), out)
	}
}

// AN `agent_updates` HOLD NEVER REACHES ONE OF YOLO'S OWN STEPS: a held launch runs no check and no
// build, so a build jail that would not start is never met, and a failure an earlier launch recorded
// for the newer version has no bearing.
func TestAHoldNeverReachesAYoloStepFailure(t *testing.T) {
	fx, good, _, _ := jailThatNeverStarts(t)
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	children := fx.child
	fx.later(2 * time.Hour)
	held, out, _ := fx.launch(t, "podman")
	if held.failed || held.delivery.PatchFailure != nil || held.delivery.Key != good.delivery.Key || fx.child != children {
		t.Fatalf("the hold refused, or started a build jail: %+v (%d build jails)\n%s", held, fx.child-children, out)
	}
	if strings.Contains(out, "ERROR") {
		t.Errorf("the held launch printed an error:\n%s", out)
	}
}

// ON APPLE CONTAINER, A NEWER CANDIDATE THE RUNTIME WILL NOT BUILD NOW STOPS THE LAUNCH (#2): beside a
// running jail no build child starts; the launch refuses naming the running jail and `container ls`;
// nothing is recorded, so once the runtime can build, the next launch builds it.
func TestAppleContainerThatCannotBuildNowStopsTheLaunch(t *testing.T) {
	clearHatches(t)
	fx, _, _, good, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", fitting)
	fx.later(2 * time.Hour)
	prev := appleContainerBuildCapability
	appleContainerBuildCapability = func(context.Context) (bool, string) {
		return false, "Apple Container has running yolo jail(s): yolo-another-session"
	}
	t.Cleanup(func() { appleContainerBuildCapability = prev })
	children := fx.child
	got, out, _ := fx.launch(t, "container")
	pf := got.delivery.PatchFailure
	if !got.failed || pf == nil || pf.Cause != packsrc.BuildCauseRuntime || pf.Target.Commit != v13 ||
		got.delivery.Key != good.delivery.Key || fx.child != children || len(fx.builds) != 1 {
		t.Fatalf("the deferred candidate handed %+v (%d build jails), want the launch stopped\n%s", got, fx.child-children, out)
	}
	for _, w := range []string{"Cause: Apple Container has running yolo jail(s): yolo-another-session",
		"once `container ls` shows no running yolo jail, retry it:", "    yolo capture tool\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("the refusal lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "left it pending") {
		t.Errorf("the launch still says it ran the older build with the candidate pending:\n%s", out)
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("a build the runtime would not start was recorded as failed: %+v", o)
		}
	}
	allowAppleContainerBuild(t)
	if moved, out, _ := fx.launch(t, "container"); !moved.built || len(fx.builds) != 2 {
		t.Errorf("once the runtime could build, the next launch did not: %+v\n%s", moved, out)
	}
}

// AN APPLY ERROR IN THE BUILD'S OWN REPLAY (#4): the walk found the fit, and the replay that puts the
// series in the build's src/ failed; with a good build serving, the launch stops, and the error is
// kept, so the next launch inside the check's window stops on it with no replay.
func TestAnApplyErrorInTheBuildsOwnReplayStopsTheLaunch(t *testing.T) {
	clearHatches(t)
	fx, _, _, good, _, _ := firstAdvance(t)
	v13 := fx.commit(t, "v1.3.0", fitting)
	failFile := filepath.Join(t.TempDir(), "fail")
	countFile := filepath.Join(t.TempDir(), "count")
	writeFile(t, failFile, v13)
	// The walk reads v1.3.0's tree once (its prefetch's probe, whose listing the missing-file check and the
	// pick reuse); the build's own replay reads it again, and fails.
	patchedGitWrapper(t, "fail=$(cat "+shquote.Quote(failFile)+" 2>/dev/null)\n"+
		"if [ -n \"$fail\" ]; then for a in \"$@\"; do if [ \"$a\" = \"$fail^{tree}\" ]; then "+
		"n=$(cat "+shquote.Quote(countFile)+" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "+shquote.Quote(countFile)+"; "+
		"if [ $n -ge 2 ]; then echo \"fatal: simulated: cannot read $fail\" >&2; exit 128; fi; fi; done; fi")
	fx.later(2 * time.Hour)
	got, out, _ := fx.launch(t, "podman")
	pf := got.delivery.PatchFailure
	if !got.failed || pf == nil || pf.Cause != packsrc.BuildCauseReplay || pf.Target.Commit != v13 ||
		got.delivery.Key != good.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("the build's own replay failure handed %+v after %d builds, want the launch stopped\n%s",
			got, len(fx.builds), out)
	}
	if !strings.Contains(pf.Detail, "replaying the series onto v1.3.0") || !strings.Contains(out, "simulated: cannot read") ||
		strings.Contains(out, "still running v1.1.0") {
		t.Errorf("the refusal does not carry the replay's error, or says the older build runs:\n%s", out)
	}
	fx.later(time.Minute)
	if again, out, _ := fx.launch(t, "podman"); again.delivery.PatchFailure == nil ||
		again.delivery.PatchFailure.Cause != packsrc.BuildCauseReplay || len(fx.builds) != 1 {
		t.Errorf("inside the check's window the next launch did not stop on the kept error: %+v\n%s", again, out)
	}
}

// UPSTREAM UNREACHABLE AT A JAIL LAUNCH IS NOT A FAILURE (#9): with a candidate known from an earlier
// check and the upstream gone, the launch runs the good build, saying it could not check, with no
// refusal and no hatch.
func TestAnUnreachableUpstreamRunsTheGoodBuildAtAJailLaunch(t *testing.T) {
	clearHatches(t)
	fx, _, _, good, _, _ := firstAdvance(t)
	if err := os.Rename(fx.repo, fx.repo+".gone"); err != nil {
		t.Fatal(err)
	}
	fx.later(2 * time.Hour)
	got, out, _ := fx.launch(t, "podman")
	if got.delivery.PatchFailure != nil || got.delivery.Key != good.delivery.Key || len(fx.builds) != 1 {
		t.Fatalf("an unreachable upstream handed %+v after %d builds, want the good build\n%s", got, len(fx.builds), out)
	}
	if !strings.Contains(out, "could not check origin main") || !strings.Contains(out, "still running v1.1.0") ||
		strings.Contains(out, "ERROR") {
		t.Errorf("the launch does not say it could not check and runs the good build:\n%s", out)
	}
}

// THE BACKGROUND ADVANCE'S BUILD JAIL THAT NEVER STARTED STOPS THE NEXT LAUNCH (#8): it is recorded
// as the candidate's failed build, so the next fresh launch in the next-launch mode, which builds
// nothing, hands it beside the good build's copy, which refuses — never the older copy silently.
func TestABackgroundJailThatNeverStartedStopsTheNextLaunch(t *testing.T) {
	clearHatches(t)
	pf := readyForABackgroundAdvance(t)
	jails := 0
	withFakeCaptureJail(t, func(o run.Options) int {
		jails++
		return 125 // no toolchain witness: the build line never ran
	})
	if rc := runBackgroundAdvance(backgroundAdvanceArgv([]string{poolKeyA}, "podman", patchedTestPlatform)[2:]); rc != 0 {
		t.Fatalf("the background advance returned %d", rc)
	}
	if jails != 1 {
		t.Fatalf("the background advance started %d build jails, want one", jails)
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	stubSpawn(t)
	got, out := pf.launch(t, "podman", &run.ActInterrupt{})
	d := got[poolKeyA]
	if d.PatchFailure == nil || d.PatchFailure.Kind != packsrc.PatchFailureBuild || d.PatchFailure.Cause != packsrc.BuildCauseJail ||
		d.Dir == "" || jails != 1 {
		t.Fatalf("the next launch handed %+v after %d build jails, want the background's failure beside the copy\n%s",
			d, jails, out)
	}
}

// AT THE HOST, ONE OF YOLO'S OWN STEPS STOPS `yolo host -- <bin>` AND THEN `yolo host apply --assert`
// (#1, #5, #6): the launch that meets a build jail that never started, or an apply error, refuses with
// the floor's copy kept. After a jail that never started, the apply reads the record with no advance
// (hostPatchPreflight) and refuses before its first write, and YOLO_ALLOW_PATCH_FAILURES runs the
// floor's copy. After an apply error, the floor's read of the record finds an error no local replay
// classifies, which it refuses as unresolved authority that no hatch waives (PF-D94).
func TestAtTheHostAYoloStepFailureStopsTheLaunchAndTheApply(t *testing.T) {
	for _, tc := range []struct {
		name, head, applyHead string
		cause                 string
		hatchRuns             bool
		set                   func(t *testing.T, fx *patchedAdvanceFixture, v13 string)
	}{
		{"a build jail that never started", "yolo could not build upstream v1.3.0", "yolo could not build upstream v1.3.0",
			packsrc.BuildCauseJail, true,
			func(t *testing.T, fx *patchedAdvanceFixture, _ string) { fx.rc, fx.ran = 125, false }},
		{"an apply error", "the patch series could not be replayed onto upstream v1.3.0", "patch-failure authority is unresolved",
			packsrc.BuildCauseReplay, false,
			func(t *testing.T, fx *patchedAdvanceFixture, v13 string) {
				failFile := filepath.Join(t.TempDir(), "fail")
				writeFile(t, failFile, v13)
				patchedGitWrapper(t, failReadingFilesAt(failFile))
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearHatches(t)
			fx := patchedFloorFixture(t)
			fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			fx.writeUserConfig(t, `,"host_management":"own"`)
			if rc, _, out := fx.hostLaunch(t); rc != 0 {
				t.Fatalf("the first launch: rc=%d\n%s", rc, out)
			}
			v13 := fx.commit(t, "v1.3.0", fitting)
			tc.set(t, fx, v13)
			fx.later(2 * time.Hour)
			rc, target, out := fx.hostLaunch(t)
			if rc == 0 || target != "" || !strings.Contains(out, tc.head) || strings.Contains(out, "still running v1.1.0") {
				t.Fatalf("the host launch did not stop: rc=%d target=%q\n%s", rc, target, out)
			}
			if pf := currentPatchFailure(fx.fork(t)); pf == nil || pf.Cause != tc.cause {
				t.Fatalf("the record does not hold what stops the next act: %+v", pf)
			}
			var applyOut, applyErr strings.Builder
			rc = hostApplyRefreshAndRender(&applyOut, &applyErr, false, true, nil, "", "")
			if said := applyOut.String() + applyErr.String(); rc == 0 || !strings.Contains(said, tc.applyHead) {
				t.Errorf("the apply did not stop: rc=%d\n%s", rc, said)
			}
			if tc.hatchRuns && (strings.Contains(applyOut.String(), "host apply — applying into") ||
				!strings.Contains(applyErr.String(), "YOLO_ALLOW_PATCH_FAILURES=forkpack/tool yolo host apply --assert ")) {
				t.Errorf("the apply did not stop before its render, naming the hatch:\n%s\n%s", applyOut.String(), applyErr.String())
			}
			t.Setenv(patchrecovery.AllowPatchFailuresEnv, "forkpack/tool")
			rc, target, out = fx.hostLaunch(t)
			if ran := rc == 0 && target != "" && strings.Contains(out, "Continuing with "); ran != tc.hatchRuns {
				t.Errorf("the hatch ran the floor's copy %v, want %v: rc=%d target=%q\n%s", ran, tc.hatchRuns, rc, target, out)
			}
		})
	}
}
