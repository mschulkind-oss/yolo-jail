package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

func allowAppleContainerBuild(t testing.TB) {
	t.Helper()
	prev := appleContainerBuildCapability
	appleContainerBuildCapability = func(context.Context) (bool, string) { return true, "" }
	t.Cleanup(func() { appleContainerBuildCapability = prev })
}

// THE BACKGROUND ADVANCE ON APPLE CONTAINER ONLY CHECKS, and the launch that meets the candidate it
// left, beside a running jail, starts no build child and STOPS (PD-NF1): a newer candidate it cannot
// build now is not a reason to run the older build on its own. Each extension is handed the runtime's
// refusal beside the admitted tree a hatch would run; nothing is recorded, so the next launch asks the
// runtime again.
func TestAppleContainerCheckOnlyDoesNotStartBuildChildAndStopsBesideTheAdmittedTree(t *testing.T) {
	t.Setenv("YOLO_ALLOW_PATCH_FAILURES", "")
	pf := newPoolFixture(t)
	first, _ := pf.launch(t, "podman", &run.ActInterrupt{})
	if first[poolKeyA].Entry == "" {
		t.Fatalf("fixture did not establish admitted trees: %+v", first)
	}
	var children atomic.Int32
	pf.child = func(context.Context, forkBuild) int {
		children.Add(1)
		return 0
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	pf.setNow(pf.now.Add(2 * time.Hour))
	prev := appleContainerBuildCapability
	appleContainerBuildCapability = func(context.Context) (bool, string) {
		return false, "Apple Container has running yolo jail(s): yolo-another-session"
	}
	t.Cleanup(func() { appleContainerBuildCapability = prev })

	built := children.Load()
	// The launch consumes a candidate discovered by the check-only act; a warm
	// launch itself must not do that upstream work before the timer runs.
	for _, f := range packload.PatchedTrees(selectConfiguredHostPacks().packs) {
		treeAdvance(f, advanceOptions{platform: patchedTestPlatform, runtime: "container", launch: true,
			checkOnly: true, checkOnlyReason: "Apple Container background preparation is check-only", out: io.Discard, errw: io.Discard})
	}
	got, out := pf.launch(t, "container", &run.ActInterrupt{})
	if children.Load() != built {
		t.Fatalf("Apple Container started %d build children while another jail was live:\n%s", children.Load()-built, out)
	}
	d := got[poolKeyA]
	if d.Entry != first[poolKeyA].Entry || d.Dir == "" {
		t.Errorf("the prior admitted tree is not what a hatch would run: before=%+v after=%+v", first[poolKeyA], d)
	}
	if pf := d.PatchFailure; pf == nil || pf.Kind != packsrc.PatchFailureBuild || pf.Cause != packsrc.BuildCauseRuntime ||
		!strings.Contains(pf.Detail, "yolo-another-session") || pf.Target.Tag != "v1.1.0" {
		t.Errorf("the launch did not stop on the runtime's refusal of the candidate: %+v\n%s", d, out)
	}
	if !strings.Contains(out, "deferred") {
		t.Errorf("the build's line does not say it was deferred:\n%s", out)
	}
	for _, o := range patchedRecordOf(t, poolKeyA).Outcomes {
		if o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("a build the runtime would not start was recorded as failed: %+v", o)
		}
	}
	block := d.PatchFailure.Render("extension "+poolKeyA, packsrc.RebaseRecipe{}, packsrc.PatchBypass{Key: poolKeyA}, false)
	for _, want := range []string{"yolo could not build upstream v1.1.0", "Cause: Apple Container has running yolo jail(s): yolo-another-session",
		"once `container ls` shows no running yolo jail, retry it:"} {
		if !strings.Contains(block, want) {
			t.Errorf("the error block lacks %q:\n%s", want, block)
		}
	}
}

func TestAppleContainerRequiredColdBuildRefusesWhenLivenessIsUnknown(t *testing.T) {
	pf := newPoolFixture(t)
	var children atomic.Int32
	pf.child = func(context.Context, forkBuild) int {
		children.Add(1)
		return 0
	}
	prev := appleContainerBuildCapability
	appleContainerBuildCapability = func(context.Context) (bool, string) {
		return false, "`container ls` could not establish Apple Container liveness (daemon unavailable); no build child was started"
	}
	t.Cleanup(func() { appleContainerBuildCapability = prev })

	got, out := pf.launch(t, "container", &run.ActInterrupt{})
	if children.Load() != 0 {
		t.Fatalf("Apple Container started %d build children with unknown liveness:\n%s", children.Load(), out)
	}
	if got[poolKeyA].Dir != "" || !strings.Contains(got[poolKeyA].Reason, "left it pending") {
		t.Errorf("a required cold build did not refuse with its pending reason: %+v\n%s", got[poolKeyA], out)
	}
	for _, want := range []string{"no build child was started", "repair the Apple Container listing", "retry a fresh launch"} {
		if !strings.Contains(out+got[poolKeyA].Reason, want) {
			t.Errorf("the cold-build refusal lacks %q:\n%s\nreason: %s", want, out, got[poolKeyA].Reason)
		}
	}
}

func TestAppleContainerFreshCheckRefusesPendingCandidateDespiteServingGood(t *testing.T) {
	pf := newPoolFixture(t)
	initial, out := pf.launch(t, "podman", &run.ActInterrupt{})
	if initial[poolKeyA].Entry == "" {
		t.Fatalf("fixture did not establish an admitted tree: %+v\n%s", initial[poolKeyA], out)
	}
	before := patchedRecordOf(t, poolKeyA)
	if before.Good == nil {
		t.Fatal("fixture has no previous admitted good build")
	}
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	pf.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	pf.setNow(pf.now.Add(2 * time.Hour))
	var children atomic.Int32
	pf.child = func(context.Context, forkBuild) int {
		children.Add(1)
		return 0
	}
	prevCapability := appleContainerBuildCapability
	appleContainerBuildCapability = func(context.Context) (bool, string) {
		return false, "Apple Container has running yolo jail(s): yolo-another-session"
	}
	t.Cleanup(func() { appleContainerBuildCapability = prevCapability })

	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	req := run.TreeBuildRequest{FreshUpdate: true, Trees: trees, Platform: patchedTestPlatform, Runtime: "container",
		Workspace: "/ws", Build: true, CopyRoot: filepath.Join(t.TempDir(), "tree.patched"), Interrupt: &run.ActInterrupt{}}
	var report syncBuffer
	got := deliverTreesForLaunch(req, &report, &report, false)
	if children.Load() != 0 {
		t.Fatalf("fresh check started an Apple Container build child despite live-jail liveness:\n%s", report.String())
	}
	if got[poolKeyA].Dir != "" || !strings.Contains(got[poolKeyA].FreshFailure, "candidate remains pending") {
		t.Fatalf("freshness request did not preserve its pending failure independently of the old good build: delivery=%+v\n%s",
			got[poolKeyA], report.String())
	}
	if !strings.Contains(got[poolKeyA].Reason, "candidate remains pending") || !strings.Contains(got[poolKeyA].Reason, "yolo-another-session") {
		t.Fatalf("strict refusal did not retain the Apple pending/liveness explanation: %+v\n%s", got[poolKeyA], report.String())
	}
	after := patchedRecordOf(t, poolKeyA)
	if after.Good == nil || after.Good.Commit != before.Good.Commit || after.Good.Recipe != before.Good.Recipe {
		t.Fatalf("check-only freshness damaged the old admitted good build: before=%+v after=%+v", before.Good, after.Good)
	}
	if after.Check == nil || len(after.Check.List) == 0 || after.Check.List[0].Tag != "v1.1.0" {
		t.Fatalf("freshness did not record the checked candidate as pending: %+v", after.Check)
	}
}

func TestAppleProductionProbeParsesHumanMemoryTableBeforeStartingChild(t *testing.T) {
	pf := newPoolFixture(t)
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "container"), "#!/bin/sh\nprintf 'ID IMAGE OS ARCH STATE IP CPUS MEMORY STARTED\\nyolo-ac-builder image linux arm64 running - 4 12288 MB Tue\\n'\n")
	if err := os.Chmod(filepath.Join(bin, "container"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prevCapability := appleContainerBuildCapability
	appleContainerBuildCapability = appleContainerCanBuildNow
	t.Cleanup(func() { appleContainerBuildCapability = prevCapability })
	var children atomic.Int32
	pf.child = func(context.Context, forkBuild) int { children.Add(1); return 0 }

	got, out := pf.launch(t, "container", &run.ActInterrupt{})
	if children.Load() != 0 {
		t.Fatalf("the production Apple probe started %d build children with a running yolo-ac-builder:\n%s", children.Load(), out)
	}
	if !strings.Contains(got[poolKeyA].Reason, "yolo-ac-builder") || strings.Contains(got[poolKeyA].Reason, "unrecognized listing") {
		t.Fatalf("the production Apple probe did not parse the real table and retain its yolo- classification: delivery=%+v\n%s", got[poolKeyA], out)
	}
}

func TestAppleProductionProbeRejectsLiveRowBeforeHeaderWithoutStartingChild(t *testing.T) {
	pf := newPoolFixture(t)
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "container"), "#!/bin/sh\nprintf 'yolo-live image linux arm64 running\\nID IMAGE OS ARCH STATE\\n'\n")
	if err := os.Chmod(filepath.Join(bin, "container"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	prevCapability := appleContainerBuildCapability
	appleContainerBuildCapability = appleContainerCanBuildNow
	t.Cleanup(func() { appleContainerBuildCapability = prevCapability })
	var children atomic.Int32
	pf.child = func(context.Context, forkBuild) int { children.Add(1); return 0 }

	got, out := pf.launch(t, "container", &run.ActInterrupt{})
	if children.Load() != 0 {
		t.Fatalf("the production Apple probe discarded a live row and started %d build children:\n%s", children.Load(), out)
	}
	if !strings.Contains(out, "unrecognized listing") || !strings.Contains(got[poolKeyA].Reason, "left it pending") {
		t.Fatalf("malformed liveness did not leave the cold candidate pending:\n%s\ndelivery: %+v", out, got[poolKeyA])
	}
}

func TestAppleContainerRechecksLivenessAtEachActualChildStart(t *testing.T) {
	for _, tc := range []struct {
		name        string
		denyAt      int32
		failedBuild bool
		wantChild   int32
	}{
		{name: "candidate", denyAt: 2},
		{name: "base fallback", denyAt: 3, failedBuild: true, wantChild: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTreeFixture(t, `"f.txt"`)
			if tc.failedBuild {
				fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			}
			fx.noFile = tc.failedBuild
			f := fx.tree(t)
			var probes, children atomic.Int32
			prevCapability := appleContainerBuildCapability
			appleContainerBuildCapability = func(context.Context) (bool, string) {
				if probes.Add(1) >= tc.denyAt {
					return false, "Apple Container has running yolo jail(s): yolo-live-session"
				}
				return true, ""
			}
			t.Cleanup(func() { appleContainerBuildCapability = prevCapability })
			prevChild := forkBuildChild
			forkBuildChild = func(ctx context.Context, bound time.Duration, staging string, b forkBuild,
				streams captureStreams, color bool) (int, bool) {
				children.Add(1)
				return forkBuildRunJail(staging, b, streams, color), false
			}
			t.Cleanup(func() { forkBuildChild = prevChild })

			req := run.TreeBuildRequest{FreshUpdate: true, Trees: []packload.Fork{f}, Platform: patchedTestPlatform,
				Runtime: "container", Workspace: "/ws", Build: true,
				CopyRoot: filepath.Join(t.TempDir(), "tree.patched"), Interrupt: &run.ActInterrupt{}}
			var report syncBuffer
			got := deliverTreesForLaunch(req, &report, &report, false)
			if probes.Load() != tc.denyAt {
				t.Errorf("capability probes = %d, want denial at probe %d:\n%s", probes.Load(), tc.denyAt, report.String())
			}
			if children.Load() != tc.wantChild {
				t.Errorf("build children = %d, want %d:\n%s", children.Load(), tc.wantChild, report.String())
			}
			if got[f.Key()].Dir != "" || !strings.Contains(got[f.Key()].Reason, "candidate remains pending") {
				t.Errorf("late liveness denial did not preserve the pending fresh candidate: %+v\n%s", got[f.Key()], report.String())
			}
			record := patchedRecordOf(t, f.Key())
			if record.Good != nil {
				t.Errorf("a deferred candidate changed the good build: %+v", record.Good)
			}
			if record.Check == nil || len(record.Check.List) == 0 {
				t.Errorf("the checked candidate was not retained pending in its record: %+v", record.Check)
			}
		})
	}
}

func TestAppleContainerCapabilityTreatsUnknownListingAsUnsafe(t *testing.T) {
	for _, listing := range []string{"", "yolo-one image running", "yolo-live image linux arm64 running\nID IMAGE OS ARCH STATE\n",
		"ID IMAGE OS ARCH STATE\nyolo-live image linux running\n", "ID IMAGE OS ARCH STATE\nID IMAGE OS ARCH STATE\n",
		"ID IMAGE OS ARCH STATE\nyolo-live image linux arm64 stopped\n",
		"ID IMAGE OS ARCH STATE IP CPUS MEMORY STARTED\nyolo-live image linux arm64 running - 4 twelve MB Tue\n",
		"ID IMAGE OS ARCH STATE IP CPUS MEMORY STARTED\nyolo-live image linux arm64 running - 4 12288 WHAT Tue\n",
		"ID IMAGE OS ARCH STATE IP CPUS MEMORY STARTED\nyolo-live image linux arm64 running - 4 12288 MB Tue extra\n"} {
		if safe, reason := appleContainerBuildDecision(listing); safe || !strings.Contains(reason, "liveness is unknown") {
			t.Errorf("unrecognized listing %q was treated as safe: safe=%v reason=%q", listing, safe, reason)
		}
	}
	if safe, reason := appleContainerBuildDecision("ID IMAGE OS ARCH STATE\nyolo-live image linux arm64 running\n"); safe || !strings.Contains(reason, "yolo-live") {
		t.Errorf("a live yolo jail was not a build blocker: safe=%v reason=%q", safe, reason)
	}
	if safe, reason := appleContainerBuildDecision("ID IMAGE OS ARCH STATE\n"); !safe || reason != "" {
		t.Errorf("a valid empty container listing was not safe: safe=%v reason=%q", safe, reason)
	}
}

// Registration and the actual timer must check Linux inputs on Apple without
// authorizing even a build against an apparently empty container listing.
func TestPreparationAppleSchedulerChecksLinuxWithoutBuild(t *testing.T) {
	checkAppleSchedulerWithoutBuild(t, false)
}

func TestPreparationAppleSchedulerChecksLinuxWithoutBuildAfterClockRollback(t *testing.T) {
	checkAppleSchedulerWithoutBuild(t, true)
}

func checkAppleSchedulerWithoutBuild(t *testing.T, rollback bool) {
	t.Helper()
	pf := readyForABackgroundAdvance(t)
	pf.agentUpdates(t, `{"treepool":"next-launch"}`)
	stubPreparationWorkerStart(t)
	before := patchedRecordOf(t, poolKeyA)
	if rollback {
		// A future check stamp is due after clock rollback. Completing the new check
		// must not require its wall-clock stamp to exceed the prior attempt's.
		pf.setNow(time.Unix(before.CheckedAt, 0).Add(-time.Second))
	}
	// The fixture's initial Podman launch registered its own runtime. This case
	// exercises an Apple-only machine, retaining the admitted bytes but no Podman timer.
	if err := withPreparationState(func(state *preparationState) error { state.Subscriptions = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	calls := pf.countBuilds()
	trees := packload.PatchedTrees(selectConfiguredHostPacks().packs)
	if err := registerTreePreparation(trees, run.TreeBuildRequest{Runtime: "container", Platform: patchedTestPlatform, Build: true}, io.Discard); err != nil {
		t.Fatal(err)
	}
	state, err := loadPreparationState()
	i := preparationRecordIndex(state, poolKeyA, "container", patchedTestPlatform)
	if err != nil || i < 0 || !state.Subscriptions[i].Active {
		t.Fatalf("Apple Linux checks not registered: %+v %v", state, err)
	}
	// Observe the recorded result, not the check's attempt stamp or an in-flight
	// snapshot. The scheduler calls this before its next dispatch pass.
	type settlement struct {
		snapshot preparationSnapshot
		err      error
	}
	settled := make(chan settlement, 1)
	previousSettled := preparationTaskSettled
	preparationTaskSettled = func(key string) {
		if key == poolKeyA {
			snapshot, err := readPreparationSnapshot(key)
			settled <- settlement{snapshot, err}
		}
	}
	t.Cleanup(func() { preparationTaskSettled = previousSettled })
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time, 1)
	ticks <- pf.clock()
	done := make(chan error, 1)
	go func() { done <- runPreparationScheduler(ctx, ticks, io.Discard) }()
	stopped := false
	defer func() {
		cancel()
		if !stopped {
			if err := <-done; err != nil && err != context.Canceled {
				t.Error(err)
			}
		}
	}()
	guard := time.NewTimer(testsupport.ReadinessBudget(t))
	defer guard.Stop()
	select {
	case got := <-settled:
		after := patchedRecordOf(t, poolKeyA)
		if rollback && after.CheckedAt >= before.CheckedAt {
			t.Fatalf("clock rollback did not reach the check: before stamp=%d after stamp=%d", before.CheckedAt, after.CheckedAt)
		}
		if after.Seq <= before.Seq || got.err != nil || !strings.Contains(got.snapshot.Reason, "left it pending") {
			t.Fatalf("Apple timer did not finish a check-only result: before seq=%d after seq=%d snapshot=%+v err=%v",
				before.Seq, after.Seq, got.snapshot, got.err)
		}
		if calls.Load() != 0 || after.Good == nil || before.Good == nil || after.Good.Entry != before.Good.Entry {
			t.Fatalf("check-only work built or replaced Good: builds=%d before=%+v after=%+v", calls.Load(), before.Good, after.Good)
		}
	case err := <-done:
		stopped = true
		t.Fatalf("Apple scheduler stopped before recording its check-only result: %v", err)
	case <-guard.C:
		snapshot, err := readPreparationSnapshot(poolKeyA)
		t.Fatalf("Apple scheduler never settled its check-only result: snapshot=%+v err=%v", snapshot, err)
	}
}
