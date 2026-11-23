package cli

// patchrecoveryadvance_test.go drives the advance's half of the way back from a broken patched build
// (docs/reference/patched-forks.md §8.5) through its real call sites — a fresh jail launch's
// advance, `yolo capture`, `yolo pack update`, `yolo host -- <bin>` and a launch's tree arm:
//
//   - A RECORDED FAILURE FOLLOWS UPSTREAM'S NEWEST (§6.2, OQ-PBR1): Appendix A's scenarios 1, 5 and 6
//     build the newer release that fits, and a newer one that conflicts becomes the failure.
//   - A HOLD HOLDS (§6.3, OQ-PBR5): `agent_updates` off runs the good build past a recorded failure.
//   - THE WALK-BACK (§6.4, OQ-PBR2/3/4): YOLO_ALLOW_OLDER_UPSTREAM builds an older fit, at most three
//     builds, and its result sticks until upstream lists something newer.
//   - The scope grammar of YOLO_ALLOW_PATCH_FAILURES, and the hatch list the advance hands (§6.8).

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
)

// conflicting is an upstream edit that does not take the series' first member (line 11 is its
// context), fitting one that does.
var (
	conflicting = map[int]string{14: "fourteen", 11: "eleven-again"}
	fitting     = map[int]string{14: "fourteen", 20: "twenty"}
)

// hatchEnvs are a hatch list's variables, in order.
func hatchEnvs(hs []patchrecovery.Hatch) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Env+"="+h.Value)
	}
	return out
}

// heldSuffix is the fork's held suffix as a launch's line reads it from the record.
func heldSuffix(t *testing.T, fx *patchedAdvanceFixture) string {
	t.Helper()
	f := fx.fork(t)
	series, _ := f.ReadSeries()
	in, _, _, _ := f.CheckWant(series).Inputs()
	return run.HeldSuffix(f, fx.record(t), in, series.Digest, forkBuild{Fork: f, Series: series}.recipe())
}

// conflictAtV14 is scenario 1's first half: a good build at v1.1.0, then v1.4.0, which conflicts, and
// the launch that records it.
func conflictAtV14(t *testing.T) (*patchedAdvanceFixture, advanceResult) {
	t.Helper()
	t.Setenv(patchrecovery.AllowPatchFailuresEnv, "")
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx, _, _, r, _, _ := firstAdvance(t)
	fx.commit(t, "v1.4.0", conflicting)
	fx.later(2 * time.Hour)
	failed, out, _ := fx.launch(t, "podman")
	if failed.patchFailure == nil || failed.patchFailure.Target.Tag != "v1.4.0" || !failed.failed {
		t.Fatalf("v1.4.0 was not recorded as the failure: %+v\n%s", failed, out)
	}
	return fx, r
}

// SCENARIO 1: a release the series takes, tagged after a recorded conflict, is built by the next
// launch whose check runs; a newer conflicting one then becomes the failure the launch names.
func TestARecordedConflictFollowsANewerReleaseAtALaunch(t *testing.T) {
	fx, r := conflictAtV14(t)
	fx.commit(t, "v1.5.0", fitting)
	fx.later(2 * time.Hour)
	moved, out, _ := fx.launch(t, "podman")
	if !moved.built || moved.failed || moved.patchFailure != nil || moved.delivery.PatchFailure != nil ||
		moved.delivery.Key == "" || moved.delivery.Key == r.delivery.Key {
		t.Fatalf("v1.5.0 was not built past the recorded v1.4.0 failure: %+v\n%s", moved, out)
	}
	if g := fx.record(t).Good; g == nil || g.Tag != "v1.5.0" {
		t.Fatalf("the good build = %+v, want v1.5.0", g)
	}
	fx.commit(t, "v1.6.0", conflicting)
	fx.later(2 * time.Hour)
	again, out, _ := fx.launch(t, "podman")
	if again.patchFailure == nil || again.patchFailure.Target.Tag != "v1.6.0" || again.delivery.Key != moved.delivery.Key {
		t.Fatalf("a newer conflicting release did not become the failure: %+v\n%s", again, out)
	}
	if !strings.Contains(out, "v1.6.0") {
		t.Errorf("the refusal does not name v1.6.0:\n%s", out)
	}
}

// THE NEXT LAUNCH PROBES (LPC-D6): a newer release published after the recorded conflict is found at once, not
// after an hour, and builds.
func TestANewerReleaseOvertakesARecordedConflictAtTheNextLaunch(t *testing.T) {
	fx, _ := conflictAtV14(t)
	fx.commit(t, "v1.5.0", fitting)
	fx.later(time.Minute)
	held, out, _ := fx.launch(t, "podman")
	if held.patchFailure != nil || !held.built {
		t.Fatalf("the next launch did not find the newer release past the recorded conflict: %+v\n%s", held, out)
	}
}

// SCENARIO 6: `yolo capture` (a forced check) builds the release that fits.
func TestCaptureFollowsTheNewestPastARecordedConflict(t *testing.T) {
	fx, _ := conflictAtV14(t)
	fx.commit(t, "v1.5.0", fitting)
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if g := fx.record(t).Good; g == nil || g.Tag != "v1.5.0" {
		t.Fatalf("capture left the good build at %+v, want v1.5.0\n%s%s", g, out.String(), errw.String())
	}
}

// SCENARIO 5: `yolo pack update` reports the fitting release, a held launch runs the good build past
// the recorded failure, and back on updates the launch builds the newer release.
func TestPackUpdateAndAHeldLaunchFollowTheNewest(t *testing.T) {
	fx, r := conflictAtV14(t)
	fx.commit(t, "v1.5.0", fitting)
	rc, out, errw := packVerb(t, "update")
	if rc != 0 || !strings.Contains(out, "v1.5.0") {
		t.Fatalf("update rc=%d at the recorded v1.4.0 failure\n%s\n%s", rc, out, errw)
	}
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	builds := len(fx.builds)
	held, launched, _ := fx.launch(t, "podman")
	if held.failed || held.patchFailure != nil || held.delivery.Key != r.delivery.Key || len(fx.builds) != builds {
		t.Fatalf("the held launch did not run the good build: %+v\n%s", held, launched)
	}
	fx.writeUserConfig(t, "")
	moved, launched, _ := fx.launch(t, "podman")
	if !moved.built || fx.record(t).Good.Tag != "v1.5.0" {
		t.Fatalf("back on updates the launch did not build v1.5.0: %+v\n%s", moved, launched)
	}
}

// A HOLD HOLDS (§6.3): under `agent_updates` off, a recorded failure never refuses; the held suffix
// names the release skipped; an edit is built at the good build's commit, the failure for another
// version ignored.
func TestAHoldRunsTheGoodBuildPastARecordedFailure(t *testing.T) {
	fx, r := conflictAtV14(t)
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	fx.later(2 * time.Hour)
	held, out, _ := fx.launch(t, "podman")
	if held.failed || held.patchFailure != nil || held.delivery.PatchFailure != nil || held.delivery.Key != r.delivery.Key {
		t.Fatalf("the hold refused on a recorded failure: %+v\n%s", held, out)
	}
	if suffix := heldSuffix(t, fx); !strings.Contains(suffix, "upstream v1.4.0") {
		t.Errorf("the held suffix does not name the release skipped: %q", suffix)
	}
	writeFile(t, fx.manifest, strings.Replace(mustRead(t, fx.manifest), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1))
	edited, out, _ := fx.launch(t, "podman")
	if edited.failed || edited.patchFailure != nil || edited.delivery.Key == "" || fx.record(t).Good.Tag != "v1.1.0" {
		t.Fatalf("the edit under the hold was not built at the good build's commit: %+v\n%s", edited, out)
	}
}

// YOLO_ALLOW_PATCH_FAILURES TAKES NAMES: a key, a pack or a bin covers the fork; another name does
// not; `1` still does.
func TestAllowPatchFailuresIsScopedByName(t *testing.T) {
	fx, r := conflictAtV14(t)
	for _, tc := range []struct {
		value string
		runs  bool
	}{{"otherpack", false}, {"forkpack/tool", true}, {"forkpack", true}, {"tool", true}, {"1", true}} {
		t.Setenv(patchrecovery.AllowPatchFailuresEnv, tc.value)
		got, out, _ := fx.launch(t, "podman")
		if got.failed == tc.runs || got.delivery.Key != r.delivery.Key {
			t.Errorf("%s=%s: failed=%v, want runs=%v\n%s", patchrecovery.AllowPatchFailuresEnv, tc.value, got.failed,
				tc.runs, out)
		}
	}
}

// THE HATCH LIST (§6.8): with the good build serving a conflict, the patch failure's own bypass naming
// what it runs, then skip-patches; no walk-back, since nothing newer than the serving build is left.
func TestTheHatchListOfAServingConflict(t *testing.T) {
	fx, _ := conflictAtV14(t)
	fx.later(2 * time.Hour)
	failed, out, _ := fx.launch(t, "podman")
	if got, want := hatchEnvs(failed.delivery.Recovery), []string{"YOLO_ALLOW_PATCH_FAILURES=forkpack/tool",
		"YOLO_SKIP_PATCHES=forkpack/tool"}; !slices.Equal(got, want) {
		t.Fatalf("the hatch list = %v, want %v\n%s", got, want, out)
	}
	if h := failed.delivery.Recovery[0]; h.Command != "yolo" || !strings.HasPrefix(h.Label, "run v1.1.0 (") {
		t.Errorf("the bypass hatch = %+v", h)
	}
}

// SCENARIO 2 WITHOUT THE VARIABLE refuses, listing the walk-back first; WITH IT the older fit is
// built and said, and it STICKS: the next launch without the variable runs it held, with no refusal,
// until a release that fits replaces it.
func TestTheWalkBackBuildsAnOlderFitAndSticksUntilUpstreamMoves(t *testing.T) {
	t.Setenv(patchrecovery.AllowPatchFailuresEnv, "")
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	refused, out, _ := fx.launch(t, "podman")
	if refused.delivery.Key != "" || refused.patchFailure == nil || len(fx.builds) != 0 {
		t.Fatalf("without the variable the launch did not refuse: %+v\n%s", refused, out)
	}
	if got, want := hatchEnvs(refused.delivery.Recovery), []string{
		"YOLO_ALLOW_OLDER_UPSTREAM=forkpack/tool", "YOLO_SKIP_PATCHES=forkpack/tool", "YOLO_ALLOW_MISSING_PROGRAMS=1"}; !slices.Equal(got, want) {
		t.Errorf("the hatch list = %v, want %v", got, want)
	}
	if h := refused.delivery.Recovery; len(h) > 0 && (h[0].Command != "yolo" || h[0].Label != "build on an older release") {
		t.Errorf("the walk-back hatch = %+v", h[0])
	}

	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "forkpack")
	fx.later(2 * time.Hour)
	walked, out, _ := fx.launch(t, "podman")
	if !walked.built || walked.failed || walked.patchFailure != nil || walked.delivery.PatchFailure != nil || walked.delivery.Key == "" {
		t.Fatalf("the walk-back did not build the older fit: %+v\n%s", walked, out)
	}
	if !strings.Contains(out, "fork forkpack/tool: walked back to v1.1.0; upstream v1.2.0 does not take the series") {
		t.Errorf("the walk-back's disclosure line is missing:\n%s", out)
	}
	rec := fx.record(t)
	if rec.Good == nil || rec.Good.Tag != "v1.1.0" || rec.WalkBack == nil || rec.WalkBack.Newest.Tag != "v1.2.0" {
		t.Fatalf("the record = good %+v, walk-back hold %+v", rec.Good, rec.WalkBack)
	}

	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx.later(2 * time.Hour)
	builds := len(fx.builds)
	sticks, out, _ := fx.launch(t, "podman")
	if sticks.failed || sticks.patchFailure != nil || sticks.delivery.Key != walked.delivery.Key || len(fx.builds) != builds {
		t.Fatalf("the walked-back build did not stick without the variable: %+v\n%s", sticks, out)
	}
	if suffix := heldSuffix(t, fx); !strings.Contains(suffix, "held at v1.1.0") ||
		!strings.Contains(suffix, "upstream v1.2.0 does not take the series") {
		t.Errorf("the held suffix = %q", suffix)
	}

	fx.commit(t, "v1.3.0", fitting)
	fx.later(2 * time.Hour)
	moved, out, _ := fx.launch(t, "podman")
	rec = fx.record(t)
	if !moved.built || rec.Good.Tag != "v1.3.0" || rec.WalkBack != nil {
		t.Fatalf("a release that fits did not replace the walked-back build: %+v, good %+v, hold %+v\n%s",
			moved, rec.Good, rec.WalkBack, out)
	}
}

// ONCE A NEWER RELEASE CONFLICTS TOO, the hold no longer covers it: the launch refuses, listing the
// bypass that runs the walked-back build.
func TestAWalkedBackBuildRefusesOnceANewerReleaseConflicts(t *testing.T) {
	t.Setenv(patchrecovery.AllowPatchFailuresEnv, "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "forkpack/tool")
	walked, out, _ := fx.launch(t, "podman")
	if !walked.built {
		t.Fatalf("the walk-back built nothing: %+v\n%s", walked, out)
	}
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx.commit(t, "v1.3.0", conflicting)
	fx.later(2 * time.Hour)
	refused, out, _ := fx.launch(t, "podman")
	if refused.patchFailure == nil || refused.patchFailure.Target.Tag != "v1.3.0" || refused.delivery.Key != walked.delivery.Key {
		t.Fatalf("the newer conflict did not refuse: %+v\n%s", refused, out)
	}
	if got := hatchEnvs(refused.delivery.Recovery); len(got) == 0 || got[0] != "YOLO_ALLOW_PATCH_FAILURES=forkpack/tool" {
		t.Errorf("the hatch list = %v, want the bypass first", got)
	}
}

// SCENARIO 3: an edited series that the newest does not take lists cached-good and the walk-back;
// with the walk-back it is built at the older release that takes it.
func TestTheWalkBackBuildsAnEditedSeriesOnAnOlderRelease(t *testing.T) {
	fx, _ := conflictAtV14(t)
	patches, _ := filepath.Glob(filepath.Join(fx.forkDir, "patches", "*.patch"))
	data, err := os.ReadFile(patches[0])
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, patches[0], strings.Replace(string(data), "Subject: [PATCH 1/2]", "Subject: [PATCH 1/2] edited", 1))
	builds := len(fx.builds)
	refused, out, _ := fx.launch(t, "podman")
	if refused.delivery.Key != "" || refused.patchFailure == nil {
		t.Fatalf("the edited series was not refused: %+v\n%s", refused, out)
	}
	got := hatchEnvs(refused.delivery.Recovery)
	if !slices.Contains(got, "YOLO_USE_CACHED_GOOD=forkpack/tool") || !slices.Contains(got, "YOLO_ALLOW_OLDER_UPSTREAM=forkpack/tool") {
		t.Errorf("the hatch list = %v, want cached-good and the walk-back", got)
	}
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "1")
	walked, out, _ := fx.launch(t, "podman")
	g := fx.record(t).Good
	if !walked.built || walked.delivery.Key == "" || g.Tag != "v1.1.0" || g.Recipe != fx.recipe(t) || len(fx.builds) != builds+1 {
		t.Fatalf("the edited series was not built at v1.1.0: %+v, good %+v\n%s", walked, g, out)
	}
}

// C17 (§5): a failed build of an edited series, with an older good build here, lists cached-good.
func TestAFailedEditListsCachedGood(t *testing.T) {
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx, _, _, _, _, _ := firstAdvance(t)
	fx.commit(t, "v1.3.0", fitting)
	writeFile(t, fx.manifest, strings.Replace(mustRead(t, fx.manifest), `"build":"sh build.sh"`, `"build":"sh build2.sh"`, 1))
	fx.rc = 2
	fx.later(2 * time.Hour)
	failed, out, _ := fx.launch(t, "podman")
	if failed.delivery.Key != "" {
		t.Fatalf("the failed edit handed %+v\n%s", failed.delivery, out)
	}
	if got := hatchEnvs(failed.delivery.Recovery); len(got) == 0 || got[0] != "YOLO_USE_CACHED_GOOD=forkpack/tool" {
		t.Errorf("the hatch list = %v, want cached-good first", got)
	}
}

// SCENARIO 7 WITH THE VARIABLE: every build fails, and the walk-back stops at three builds.
func TestTheWalkBackBuildsAtMostThreeTimes(t *testing.T) {
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "1")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 16: "sixteen"})
	fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 18: "eighteen"})
	fx.rc = 2
	failed, out, _ := fx.launch(t, "podman")
	if failed.delivery.Key != "" || len(fx.builds) != 3 {
		t.Fatalf("the walk-back ran %d builds, want 3: %+v\n%s", len(fx.builds), failed.delivery, out)
	}
}

// THE WALK-BACK AT `yolo host -- <bin>`: nothing of the fork is here, the newest conflicts, and the
// host launch builds the older fit and runs it.
func TestTheWalkBackAtAHostLaunch(t *testing.T) {
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	rc, target, out := fx.hostLaunch(t)
	if rc == 0 || target != "" {
		t.Fatalf("without the variable the host launch ran: rc=%d\n%s", rc, out)
	}
	if !strings.Contains(out, "YOLO_ALLOW_OLDER_UPSTREAM=forkpack/tool yolo host -- tool") {
		t.Errorf("the host refusal does not list the walk-back:\n%s", out)
	}
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "tool")
	rc, target, out = fx.hostLaunch(t)
	if rc != 0 || target == "" || fx.record(t).Good.Tag != "v1.1.0" {
		t.Fatalf("the host launch did not walk back: rc=%d target=%q\n%s", rc, target, out)
	}
}

// THE WALK-BACK AT `yolo capture`: the forced advance builds the older fit.
func TestTheWalkBackAtCapture(t *testing.T) {
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "forkpack/tool")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if g := fx.record(t).Good; g == nil || g.Tag != "v1.1.0" {
		t.Fatalf("capture's good build = %+v\n%s%s", g, out.String(), errw.String())
	}
}

// A PATCHED EXTENSION walks back through the same advance, and its refusal carries the hatch list.
func TestTheWalkBackOfAPatchedExtension(t *testing.T) {
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "")
	fx := newTreeFixture(t, `"f.txt"`)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	refused, out := fx.deliver(t, true)
	if refused.Dir != "" || refused.PatchFailure == nil || !slices.Contains(hatchEnvs(refused.Recovery),
		"YOLO_ALLOW_OLDER_UPSTREAM="+treeKeyCLI) {
		t.Fatalf("the extension's refusal = %+v\n%s", refused, out)
	}
	t.Setenv(patchrecovery.AllowOlderUpstreamEnv, "treepack")
	walked, out := fx.deliver(t, true)
	if walked.Dir == "" || walked.PatchFailure != nil {
		t.Fatalf("the extension did not walk back: %+v\n%s", walked, out)
	}
	if !strings.Contains(out, "extension "+treeKeyCLI+": walked back to v1.1.0; upstream v1.2.0 does not take the series") {
		t.Errorf("the extension's disclosure line is missing:\n%s", out)
	}
}
