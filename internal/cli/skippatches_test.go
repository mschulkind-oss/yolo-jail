package cli

// skippatches_test.go drives YOLO_SKIP_PATCHES (docs/reference/patched-forks.md §8.5, "Skip the patches") through
// its real call sites — a fresh jail launch's fork-build slot and tree arm, `yolo host -- <bin>`,
// `yolo capture` — and the background advance, which never reads it.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// checkRecordBytes is owner's check record as stored, "" when there is none.
func checkRecordBytes(t *testing.T, owner string) string {
	t.Helper()
	data, err := os.ReadFile((&packsrc.Store{Dir: paths.PacksDir()}).CheckRecordPath(owner))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// slotLaunch is one fresh launch's fork-build slot: its delivery for tool and what it printed.
func slotLaunch(t *testing.T) (deliveryOf func(string) (string, *packsrc.PatchFailure, []patchrecovery.Hatch), out string) {
	t.Helper()
	var buf syncBuffer
	got := buildForksForLaunch(forkLaunchRequest(&run.ActInterrupt{}), &buf, &buf, false)
	return func(bin string) (string, *packsrc.PatchFailure, []patchrecovery.Hatch) {
		d := got[bin]
		return d.Key, d.PatchFailure, d.Recovery
	}, buf.String()
}

// A FRESH LAUNCH WITH THE VARIABLE runs the upstream build past a recorded conflict; a second one
// reuses it with no build and no check inside the hour; and without the variable the launch is the
// series' own again, its record untouched byte for byte and its good build still in the store.
func TestSkipPatchesRunsTheUpstreamBuildAtALaunchAndLeavesTheSeriesAlone(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	fx, first := conflictAtV14(t)
	fx.later(2 * time.Hour)
	delivery, out := slotLaunch(t)
	seriesKey, seriesFailure, _ := delivery("tool")
	if seriesFailure == nil || seriesKey != first.delivery.Key {
		t.Fatalf("without the variable the launch did not refuse on the series' conflict: key %q failure %+v\n%s",
			seriesKey, seriesFailure, out)
	}
	series := checkRecordBytes(t, "forkpack/tool")
	builds := len(fx.builds)

	t.Setenv(patchrecovery.SkipPatchesEnv, "forkpack")
	delivery, out = slotLaunch(t)
	key, failure, _ := delivery("tool")
	if key == "" || key == first.delivery.Key || failure != nil || len(fx.builds) != builds+1 {
		t.Fatalf("the upstream build was not delivered: key %q failure %+v builds %d→%d\n%s", key, failure, builds,
			len(fx.builds), out)
	}
	if got := fx.builds[len(fx.builds)-1]; got != lines30(conflicting) {
		t.Errorf("the upstream build saw f.txt %q, want the v1.4.0 upstream with no series", got)
	}
	if !strings.Contains(out, "fork forkpack/tool: running upstream v1.4.0 without its 2 patches") {
		t.Errorf("the disclosure line is missing:\n%s", out)
	}
	if got := checkRecordBytes(t, "forkpack/tool"); got != series {
		t.Errorf("the series' check record was written:\nbefore %s\nafter  %s", series, got)
	}
	upstream := patchedRecordOf(t, "forkpack/tool#upstream")
	if upstream.Good == nil || upstream.Good.Entry != key || upstream.Good.Tag != "v1.4.0" || upstream.Good.Patches != 0 {
		t.Fatalf("the upstream build's own record = %+v", upstream.Good)
	}

	fx.later(time.Minute)
	delivery, out = slotLaunch(t)
	again, _, _ := delivery("tool")
	if again != key || len(fx.builds) != builds+1 {
		t.Fatalf("the second launch did not reuse the unmoved upstream's build: key %q builds %d\n%s",
			again, len(fx.builds), out)
	}
	if !strings.Contains(out, "fork forkpack/tool: running upstream v1.4.0 without its 2 patches") {
		t.Errorf("the second launch does not say the disclosure line:\n%s", out)
	}

	if got := checkRecordBytes(t, "forkpack/tool"); got != series {
		t.Errorf("the series' check record moved across the upstream launches")
	}

	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	delivery, out = slotLaunch(t)
	back, backFailure, _ := delivery("tool")
	if back != first.delivery.Key || backFailure == nil || backFailure.Target.Tag != seriesFailure.Target.Tag {
		t.Fatalf("without the variable the launch is not the series' again: key %q failure %+v\n%s", back, backFailure, out)
	}
	if strings.Contains(out, "running upstream") {
		t.Errorf("a launch without the variable says the upstream line:\n%s", out)
	}
	for _, k := range []string{first.delivery.Key, key} {
		if !storeEntryExists(k) {
			t.Errorf("the build %s was reaped", k)
		}
	}
}

// THE UPSTREAM BUILD FAILS: nothing is handed for the fork, and the hatch left is starting without
// it. Without the variable the series' refusal then no longer lists YOLO_SKIP_PATCHES, since the
// upstream build is backing off.
func TestAFailedUpstreamBuildRefusesWithTheHatchesLeft(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	delivery, out := slotLaunch(t)
	if _, _, recovery := delivery("tool"); !slices.Contains(hatchEnvs(recovery), "YOLO_SKIP_PATCHES=forkpack/tool") {
		t.Fatalf("the series' refusal does not list the hatch: %v\n%s", hatchEnvs(recovery), out)
	}
	t.Setenv(patchrecovery.SkipPatchesEnv, "tool")
	fx.rc = 2
	delivery, out = slotLaunch(t)
	key, _, recovery := delivery("tool")
	if key != "" || len(fx.builds) != 1 {
		t.Fatalf("a failed upstream build handed %q (builds %d)\n%s", key, len(fx.builds), out)
	}
	if got, want := hatchEnvs(recovery), []string{"YOLO_ALLOW_MISSING_PROGRAMS=1"}; !slices.Equal(got, want) {
		t.Errorf("the hatches left = %v, want %v\n%s", got, want, out)
	}
	if strings.Contains(out, "running upstream") {
		t.Errorf("a failed upstream build says the disclosure line:\n%s", out)
	}
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	fx.rc = 0
	delivery, out = slotLaunch(t)
	if _, failure, recovery := delivery("tool"); failure == nil || slices.Contains(hatchEnvs(recovery), "YOLO_SKIP_PATCHES=forkpack/tool") {
		t.Errorf("with the upstream build backing off the refusal = %+v, hatches %v\n%s", failure, hatchEnvs(recovery), out)
	}
}

// A PATCHED EXTENSION: the tree arm copies the upstream build where the extension's own copy goes,
// reuses it at the next launch, and without the variable refuses on the series' conflict again.
func TestSkipPatchesDeliversAnExtensionsUpstreamBuild(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	t.Setenv(patchrecovery.AllowPatchFailuresEnv, "")
	fx := newTreeFixture(t, `"f.txt"`)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	refused, out := fx.deliver(t, true)
	if refused.Dir != "" || refused.PatchFailure == nil {
		t.Fatalf("without the variable the extension was delivered: %+v\n%s", refused, out)
	}
	series := checkRecordBytes(t, treeKeyCLI)

	t.Setenv(patchrecovery.SkipPatchesEnv, treeKeyCLI)
	d, out := fx.deliver(t, true)
	if d.Dir == "" || d.PatchFailure != nil || d.Patches != 0 {
		t.Fatalf("the upstream build was not delivered: %+v\n%s", d, out)
	}
	if got, err := os.ReadFile(d.Dir + "/f.txt"); err != nil || string(got) != lines30(map[int]string{14: "fourteen", 11: "eleven"}) {
		t.Errorf("the copy's f.txt = %q (%v), want the upstream with no series", got, err)
	}
	if !strings.Contains(d.Dir, "treepack--tool-ext-") {
		t.Errorf("the copy %s is not where the extension's own copy goes", d.Dir)
	}
	if !strings.Contains(out, "extension "+treeKeyCLI+": running upstream v1.2.0 without its 2 patches") {
		t.Errorf("the disclosure line is missing:\n%s", out)
	}
	builds := len(fx.builds)
	again, out := fx.deliver(t, true)
	if again.Entry != d.Entry || len(fx.builds) != builds {
		t.Errorf("the second launch rebuilt: %+v (builds %d→%d)\n%s", again, builds, len(fx.builds), out)
	}
	if got := checkRecordBytes(t, treeKeyCLI); got != series {
		t.Errorf("the series' check record was written by the upstream launches")
	}

	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	back, out := fx.deliver(t, true)
	if back.Dir != "" || back.PatchFailure == nil {
		t.Errorf("without the variable the extension was not refused again: %+v\n%s", back, out)
	}
}

// `yolo host -- <bin>`: the floor installs the upstream build and the launch runs it, said; without
// the variable the host launch refuses on the series' conflict again.
func TestSkipPatchesAtAHostLaunch(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	t.Setenv(patchrecovery.AllowPatchFailuresEnv, "")
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("clean initial host launch: rc=%d\n%s", rc, out)
	}
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 11: "eleven"})
	fx.later(2 * time.Hour)
	if rc, _, out := fx.hostLaunch(t); rc == 0 {
		t.Fatalf("the conflict did not refuse the host launch\n%s", out)
	}
	series := checkRecordBytes(t, "forkpack/tool")

	t.Setenv(patchrecovery.SkipPatchesEnv, "forkpack/tool")
	rc, target, out := fx.hostLaunch(t)
	if rc != 0 || target == "" {
		t.Fatalf("the host launch did not run the upstream build: rc=%d target=%q\n%s", rc, target, out)
	}
	if got := fx.builds[len(fx.builds)-1]; got != lines30(map[int]string{14: "fourteen", 11: "eleven"}) {
		t.Errorf("the host's build saw %q, want the upstream with no series", got)
	}
	if !strings.Contains(out, "yolo host: fork forkpack/tool: running upstream v1.2.0 without its 2 patches") {
		t.Errorf("the host launch does not say the disclosure line:\n%s", out)
	}
	if got := checkRecordBytes(t, "forkpack/tool"); got != series {
		t.Errorf("the series' check record was written by the host's upstream launch")
	}

	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	if rc, target, out := fx.hostLaunch(t); rc == 0 || target != "" {
		t.Errorf("without the variable the host launch ran: rc=%d target=%q\n%s", rc, target, out)
	}
}

// `yolo capture <bin>` with the variable builds the upstream build into its own record.
func TestSkipPatchesAtCapture(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	fx, _ := conflictAtV14(t)
	series := checkRecordBytes(t, "forkpack/tool")
	t.Setenv(patchrecovery.SkipPatchesEnv, "1")
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"tool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("capture rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	g := patchedRecordOf(t, "forkpack/tool#upstream").Good
	if g == nil || g.Tag != "v1.4.0" || !storeEntryExists(g.Entry) || fx.builds[len(fx.builds)-1] != lines30(conflicting) {
		t.Fatalf("capture did not build the upstream build: %+v\n%s%s", g, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "fork forkpack/tool: built upstream v1.4.0 without its 2 patches") {
		t.Errorf("capture does not say what it built:\n%s", out.String())
	}
	if got := checkRecordBytes(t, "forkpack/tool"); got != series {
		t.Errorf("capture of the upstream build wrote the series' check record")
	}
}

// THE BACKGROUND ADVANCE NEVER READS IT: with the variable in its environment it advances the
// series' key alone, and no upstream build is checked or built. The detached advance's environment
// does not carry it at all.
func TestTheBackgroundAdvanceNeverSkipsPatches(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "1")
	fx := newPatchedAdvanceFixture(t, "")
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	var bg bytes.Buffer
	runBackgroundAdvanceUnder(context.Background(), backgroundArgs{runtime: "podman", platform: patchedTestPlatform,
		keys: []string{"forkpack/tool"}}, &bg)
	if g := fx.record(t).Good; g == nil || g.Patches != 2 {
		t.Fatalf("the background advance did not build the series: %+v\n%s", g, bg.String())
	}
	if got := checkRecordBytes(t, "forkpack/tool#upstream"); got != "" {
		t.Errorf("the background advance wrote an upstream build's record:\n%s", got)
	}
	if len(fx.builds) != 1 || fx.builds[0] == lines30(map[int]string{14: "fourteen"}) {
		t.Errorf("the background advance built %q", fx.builds)
	}
	for _, kv := range preparationEnvironment([]string{patchrecovery.SkipPatchesEnv + "=1", "HOME=/h"}, nil, nil) {
		if strings.HasPrefix(kv, patchrecovery.SkipPatchesEnv+"=") {
			t.Errorf("the detached environment carries %s", kv)
		}
	}
}

// AN UNMODIFIED EXTENSION has nothing to skip, and an upstream build's identity is its own.
func TestSkipPatchesCoversOnlyPatchedBuilds(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "1")
	fx := newTreeFixture(t, `"f.txt"`)
	f := fx.tree(t)
	if !skipsPatches(f) {
		t.Fatal("YOLO_SKIP_PATCHES=1 does not cover a patched extension")
	}
	u := f.UpstreamBuild()
	if skipsPatches(u) || u.Key() != treeKeyCLI+"#upstream" || u.SeriesKey() != treeKeyCLI || u.Unmodified() ||
		!u.Patched() || u.CaptureArg() != treeKeyCLI {
		t.Errorf("the upstream build's identity = key %q series key %q", u.Key(), u.SeriesKey())
	}
	if s, err := u.ReadSeries(); err != nil || s.Replays() {
		t.Errorf("the upstream build's series = %+v (%v), want the empty one", s, err)
	}
	unmodified := f
	unmodified.Patches = ""
	if skipsPatches(unmodified) {
		t.Error("YOLO_SKIP_PATCHES covers an unmodified extension")
	}
	if (forkBuild{Fork: u, Series: packsrc.EmptySeries()}).recipe() == "" {
		t.Error("the upstream build has no recipe")
	}
}

// APPLE CONTAINER, with no build jail known safe: the upstream build's candidate is checked and left
// pending in its own record, nothing is handed, and the hatch left is starting without it; once a
// build jail is safe the next launch with the variable builds it. The series' record is never touched.
func TestSkipPatchesOnAppleContainerDefersTheUpstreamBuild(t *testing.T) {
	t.Setenv(patchrecovery.SkipPatchesEnv, "")
	fx, _ := conflictAtV14(t)
	series := checkRecordBytes(t, "forkpack/tool")
	prev := appleContainerBuildCapability
	canBuild := false
	appleContainerBuildCapability = func(context.Context) (bool, string) {
		return canBuild, "Apple Container liveness is unknown"
	}
	t.Cleanup(func() { appleContainerBuildCapability = prev })
	request := forkLaunchRequest(&run.ActInterrupt{})
	request.Runtime = "container"
	t.Setenv(patchrecovery.SkipPatchesEnv, "forkpack/tool")
	builds := len(fx.builds)
	var out syncBuffer
	got := buildForksForLaunch(request, &out, &out, false)
	if d := got["tool"]; d.Key != "" || len(fx.builds) != builds ||
		!slices.Equal(hatchEnvs(d.Recovery), []string{"YOLO_ALLOW_MISSING_PROGRAMS=1"}) {
		t.Fatalf("the deferred upstream build handed %+v (builds %d→%d)\n%s", d, builds, len(fx.builds), out.String())
	}
	if !strings.Contains(out.String(), "left it pending") {
		t.Errorf("the deferral is not said:\n%s", out.String())
	}
	canBuild = true
	var again syncBuffer
	got = buildForksForLaunch(request, &again, &again, false)
	if d := got["tool"]; d.Key == "" || len(fx.builds) != builds+1 {
		t.Fatalf("once a build jail is safe the upstream build was not built: %+v\n%s", d, again.String())
	}
	if got := checkRecordBytes(t, "forkpack/tool"); got != series {
		t.Errorf("the series' check record was written by the Apple Container upstream launches")
	}
}
