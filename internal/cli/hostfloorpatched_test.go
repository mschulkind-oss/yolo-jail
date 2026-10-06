package cli

// hostfloorpatched_test.go drives a PATCHED fork at the HOST notch (docs/design/patched-forks.md §9,
// §14 step 4; PF-D14, PF-D19, PF-D25) through the call sites a user reaches: `yolo host -- <bin>`
// runs the fork's advance — the fresh jail launch's own, against a real local upstream repository —
// then installs the good build it leaves and runs the floor's copy; a new upstream version moves it,
// waiting interruptibly; `agent_updates` holds it; `yolo host apply --assert` advances it and the
// dry run does not; the host-render gate's observe pass runs before the advance and never contains
// it; and on a Mac the launch names the jail that runs it. Only the build jail (patchedAdvanceFixture's
// fake), the floor's Node, Linux as the floor's platform and the exec are the test's.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// patchedFloorFixture is newPatchedAdvanceFixture at the host: the HOME resolved past any link in
// the temp path (a confined materialize compares real paths, and a Mac's temp dir is a link), a
// working directory outside the checkout, the build jail's manifest on this host's platform (which
// a materialize on the host takes), the production floor on Linux, and the launch's stubs.
func patchedFloorFixture(t *testing.T) *patchedAdvanceFixture {
	t.Helper()
	fx := newPatchedAdvanceFixture(t, "")
	fx.platform, fx.relocatable = capture.Platform(), true
	real, err := filepath.EvalSymlinks(fx.home)
	if err != nil {
		t.Fatal(err)
	}
	fx.home = real
	t.Setenv("HOME", real)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(real, ".config"))
	t.Chdir(floortest.ResolvedTemp(t))
	withForkFloor(t)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	return fx
}

// hostLaunch runs one `yolo host -- tool` and returns its rc, what it exec'd, and its stderr.
func (fx *patchedAdvanceFixture) hostLaunch(t *testing.T) (int, string, string) {
	t.Helper()
	got := captureHostExec(t)
	var errw syncBuffer
	rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil)
	return rc, got.target, errw.String()
}

// floorToolBody is the floor's copy of tool, which the fake build made from the patched f.txt it saw.
func floorToolBody(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(floorRecord(t, "tool").Entry)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// THE MOTIVATING PATH AT THE HOST: `yolo host -- tool` of a patched fork never built on this machine
// runs its first advance — the newest fit, v1.1.0, built once in the sealed jail — then installs that
// good build and runs the floor's copy; its lines name `yolo host`, and the fork's line names the
// series and the build. The next launch inside the hour builds nothing; a new upstream version past
// the hour moves the good build, the launch waiting for that build as a fresh jail launch does (the
// build run as a child, a Ctrl-C starting tool on the good build), and runs the new build. No fork
// lock entry is written at any point (PF-D16).
func TestHostLaunchOfAPatchedForkRunsTheGoodBuildItsAdvanceMakes(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "tool")

	rc, target, out := fx.hostLaunch(t)
	if rc != 0 || target != launcher || len(fx.builds) != 1 {
		t.Fatalf("rc=%d target=%s builds=%d, want the floor's copy of one build\n%s", rc, target, len(fx.builds), out)
	}
	rec := floorRecord(t, "tool")
	label := "v1.1.0 (" + shortSHA(v11) + ") + 2 patches"
	if rec.Revision != v11 || rec.Version != label || rec.Declared != "git+file://"+fx.repo {
		t.Errorf("the floor's record is %+v, want the good build %s", rec, label)
	}
	if g := fx.record(t).Good; g == nil || g.Commit != v11 || g.Entry != rec.Capture {
		t.Errorf("the check record's good build is %+v, want the build the floor installed (%s)", g, rec.Capture)
	}
	if want := fx.builds[0]; !strings.Contains(floorToolBody(t), sha256Hex([]byte(want))) {
		t.Errorf("the floor's copy is not the build of the patched source:\n%s", floorToolBody(t))
	}
	for _, w := range []string{"built fork forkpack/tool: " + label + "; `yolo host` runs it",
		"yolo host: fork forkpack: tool (in place of pack basepack's) is a patched fork of git+file://" + fx.repo +
			"?ref=main + 2 patches (series ", ", at v1.1.0 (" + shortSHA(v11) + ")",
		"yolo host: starting tool (yolo's floor copy"} {
		if !strings.Contains(out, w) {
			t.Errorf("the first launch lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(out, "this jail") {
		t.Errorf("a host launch's advance talks about a jail:\n%s", out)
	}
	if got := pinnedCommit(t); got != "" {
		t.Errorf("the host launch pinned the patched fork at %s", got)
	}

	fx.later(10 * time.Minute)
	if rc, target, out := fx.hostLaunch(t); rc != 0 || target != launcher || len(fx.builds) != 1 ||
		strings.Contains(out, "checking fork") {
		t.Fatalf("inside the hour: rc=%d target=%s builds=%d, want the same copy, no check and no build\n%s",
			rc, target, len(fx.builds), out)
	}

	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	rc, target, out = fx.hostLaunch(t)
	if rc != 0 || target != launcher || len(fx.builds) != 2 || fx.child != 1 {
		t.Fatalf("past the hour with v1.3.0: rc=%d target=%s builds=%d child=%d\n%s", rc, target, len(fx.builds),
			fx.child, out)
	}
	if rec := floorRecord(t, "tool"); rec.Revision != v13 {
		t.Errorf("after the move the floor runs %s, want v1.3.0 (%s)", rec.Version, v13)
	}
	for _, w := range []string{"`yolo host` waits for it, at most 20m0s, and a Ctrl-C starts tool on the good build v1.1.0",
		"updated fork forkpack/tool: v1.1.0 (" + shortSHA(v11) + ") → v1.3.0 (" + shortSHA(v13) + "), 2 patches; " +
			"`yolo host` runs the new build"} {
		if !strings.Contains(out, w) {
			t.Errorf("the moving launch lacks %q:\n%s", w, out)
		}
	}
}

// A CTRL-C WHILE `yolo host` WAITS FOR A NEWER BUILD (PF-D25) ends the advance, not the launch: the
// floor's copy of the good build runs, the build is not recorded as failed, and the next launch
// builds it.
func TestACtrlCDuringAHostLaunchsBuildStartsTheGoodBuild(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ jailStreams, _ bool) (int, bool) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			t.Error("the Ctrl-C did not reach the host launch's advance")
		}
		return 130, false
	}
	rc, target, out := fx.hostLaunch(t)
	forkBuildChild = prev
	if rc != 0 || target != filepath.Join(paths.HostFloorDir(), "bin", "tool") || floorRecord(t, "tool").Revision != v11 {
		t.Fatalf("after the Ctrl-C: rc=%d target=%s, want the floor's good build v1.1.0\n%s", rc, target, out)
	}
	if !strings.Contains(out, "the advance was interrupted — `yolo host` starts tool on the good build v1.1.0 ("+
		shortSHA(v11)+") + 2 patches; the next `yolo host -- tool` tries again") {
		t.Errorf("the interrupted advance does not say what runs:\n%s", out)
	}
	for _, o := range fx.record(t).Outcomes {
		if o.Commit == v13 && o.Kind == packsrc.OutcomeBuildFailed {
			t.Errorf("an interrupted build was recorded as failed: %+v", o)
		}
	}
	fx.later(time.Minute)
	if rc, _, out := fx.hostLaunch(t); rc != 0 || floorRecord(t, "tool").Revision != v13 {
		t.Errorf("the launch after the Ctrl-C did not install v1.3.0: rc=%d\n%s", rc, out)
	}
}

// `agent_updates` OFF HOLDS A PATCHED FORK AT THE HOST (PF-D19), through the fork pack or its base:
// a current entry runs no check and builds nothing however far the upstream moved, and the fork's
// line says what holds it.
func TestAgentUpdatesOffHoldsAPatchedForkAtTheHost(t *testing.T) {
	for _, pack := range []string{"forkpack", "basepack"} {
		t.Run(pack, func(t *testing.T) {
			fx := patchedFloorFixture(t)
			v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
			if rc, _, out := fx.hostLaunch(t); rc != 0 {
				t.Fatalf("the first launch: rc=%d\n%s", rc, out)
			}
			fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
			fx.later(2 * time.Hour)
			fx.writeUserConfig(t, `,"agent_updates":{"`+pack+`":false}`)
			rc, _, out := fx.hostLaunch(t)
			if rc != 0 || len(fx.builds) != 1 || strings.Contains(out, "checking fork") || floorRecord(t, "tool").Revision != v11 {
				t.Fatalf("held by %s: rc=%d builds=%d, want no check and the good build\n%s", pack, rc, len(fx.builds), out)
			}
			if want := "held at v1.1.0 (" + shortSHA(v11) + "): `agent_updates` holds pack " + pack; !strings.Contains(out, want) {
				t.Errorf("the fork's line lacks %q:\n%s", want, out)
			}
		})
	}
}

// `yolo host apply`: the dry run says the install checks and builds the patched fork, and runs no
// advance; --assert runs it, installs the good build, and names it.
func TestHostApplyAdvancesAPatchedForkOnlyUnderAssert(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	rc, report := applyWith(t, false, nil)
	if rc != 0 || len(fx.builds) != 0 || !strings.Contains(report, "tool: would install (not installed yet: the "+
		"install checks fork pack forkpack's upstream, replays its patch series and builds it)") {
		t.Fatalf("dry run rc=%d builds=%d:\n%s", rc, len(fx.builds), report)
	}
	if _, err := patchedForkStore().LoadCheckRecord("forkpack/tool"); err == nil {
		t.Error("the dry run checked the patched fork's upstream")
	}
	rc, report = applyWith(t, true, nil)
	if rc != 0 || len(fx.builds) != 1 || !strings.Contains(report, "tool v1.1.0 ("+shortSHA(v11)+") + 2 patches, installed") {
		t.Fatalf("--assert rc=%d builds=%d:\n%s", rc, len(fx.builds), report)
	}
}

// THE FLOOR'S ADVANCE IS OUTSIDE THE HOST-RENDER GATE'S OBSERVE PASS (the plan's trap): the gate
// surveys the render first, within its one-second budget, and the advance — a fetch and a build —
// runs after that survey has returned, never inside it, so a check can neither blow the budget nor be
// compared against.
func TestTheHostFloorsAdvanceRunsAfterTheRenderGatesObservePass(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.writeUserConfig(t, `,"host_apply_on_launch":true`)
	var events []string
	prevSurvey := hostApplyGateSurvey
	hostApplyGateSurvey = func(out, errw io.Writer, color, write bool, stdin io.Reader, s *hostApplySurvey) int {
		events = append(events, "survey")
		if s.floorStage {
			t.Error("the gate's observe pass takes the floor stage, which would run the advance inside it")
		}
		return 0
	}
	prevAdvance := floorAdvance
	floorAdvance = func(f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) {
		events = append(events, "advance")
		prevAdvance(f, out, installed, act)
	}
	t.Cleanup(func() { hostApplyGateSurvey, floorAdvance = prevSurvey, prevAdvance })
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}
	if strings.Join(events, ",") != "survey,advance" {
		t.Errorf("events = %v, want the gate's survey, then the advance", events)
	}
}

// ON A MAC `yolo host` HOLDS NO FORK'S BUILD (§9, FP-D16): the launch runs no advance, says the copy
// on PATH runs, and names the next step — a jail on a container backend, whose fresh launch builds it.
// The floor's platform decides, so this runs on every CI host.
func TestHostLaunchOfAPatchedForkOnAMacNamesTheJailThatRunsIt(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	withFloorPlatform(t, "darwin")
	prevAdvance := floorAdvance
	floorAdvance = func(packload.Fork, io.Writer, *installedCopy, *run.ActInterrupt) {
		t.Error("a Mac's floor ran a patched fork's advance")
	}
	t.Cleanup(func() { floorAdvance = prevAdvance })
	stub := filepath.Join(stubBins(t, "tool"), "tool")
	rc, target, out := fx.hostLaunch(t)
	if rc != 0 || target != stub || len(fx.builds) != 0 {
		t.Fatalf("rc=%d target=%s builds=%d, want the PATH copy and no build\n%s", rc, target, len(fx.builds), out)
	}
	for _, w := range []string{"yolo host: yolo has no copy of tool on this Mac (",
		"run it in a jail instead (`yolo -- tool`, on a container backend: Apple Container or podman), whose fresh " +
			"launch builds it"} {
		if !strings.Contains(out, w) {
			t.Errorf("the Mac's line lacks %q:\n%s", w, out)
		}
	}
}

// A LOST CHECK RECORD ON A MACHINE THAT CANNOT BUILD costs a lookup, not the program (§6.2): the
// floor's offline read recovers the good build from the capture store, writing nothing, and the
// launch installs it with no advance — here after the floor itself was wiped too.
func TestAHostWithNoRecordAndNoRuntimeInstallsTheGoodBuildItsStoreHolds(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	recPath := patchedForkStore().CheckRecordPath("forkpack/tool")
	if err := os.Remove(recPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(paths.HostFloorDir()); err != nil {
		t.Fatal(err)
	}
	orig := newHostFloor
	newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
		f := orig(out, progs)
		f.CaptureUnavailable = func() string { return "no container runtime (podman) is on PATH" }
		return f
	}
	t.Cleanup(func() { newHostFloor = orig })
	rc, target, out := fx.hostLaunch(t)
	if rc != 0 || target != filepath.Join(paths.HostFloorDir(), "bin", "tool") || len(fx.builds) != 1 ||
		floorRecord(t, "tool").Revision != v11 {
		t.Fatalf("rc=%d target=%s builds=%d, want the store's v1.1.0 installed with no build\n%s", rc, target,
			len(fx.builds), out)
	}
	if _, err := os.Stat(recPath); err == nil {
		t.Error("the floor's offline read wrote a check record")
	}
	// THE FORK'S LINE NAMES WHAT THE FLOOR RUNS (PF-D53): the recovered build, never "no build".
	if want := ", at v1.1.0 (" + shortSHA(v11) + ")"; !strings.Contains(out, want) ||
		strings.Contains(out, "no build of it on this machine yet") {
		t.Errorf("the fork's line does not name the build the floor runs (%q):\n%s", want, out)
	}
}

// removeStoreEntry takes a build out of the capture store, as a prune or a wiped store does.
func removeStoreEntry(t *testing.T, key string) {
	t.Helper()
	if key == "" || !storeEntryExists(key) {
		t.Fatalf("the store holds no entry %q to remove", key)
	}
	if err := os.RemoveAll(filepath.Join(paths.CapturesDir(), "entries", key)); err != nil {
		t.Fatal(err)
	}
}

// floorServesGoneEntry is the state PF-D55 is about: the floor's copy of the good build v1.1.0 is
// current and that build's store entry is gone (a prune, a wiped store), and every build of v1.1.0 or
// newer fails while the series' base would build — so an advance that took the copy for missing would
// build the base and move the floor, and the good build under every later jail, backward. It returns
// v1.1.0's commit and an assertion that a launch ran the floor's copy of it, with wantBuilds builds
// so far, the good build left where it was, and nothing said missing; it returns the launch's output.
func floorServesGoneEntry(t *testing.T) (*patchedAdvanceFixture, string, func(when string, wantBuilds int) string) {
	t.Helper()
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	removeStoreEntry(t, fx.record(t).Good.Entry)
	fx.failBuildsOf(t, "fourteen")
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "tool")
	return fx, v11, func(when string, wantBuilds int) string {
		t.Helper()
		rc, target, out := fx.hostLaunch(t)
		if rc != 0 || target != launcher || len(fx.builds) != wantBuilds {
			t.Fatalf("%s: rc=%d target=%s builds=%d, want the floor's copy and %d builds\n%s", when, rc, target,
				len(fx.builds), wantBuilds, out)
		}
		if rec := floorRecord(t, "tool"); rec.Revision != v11 {
			t.Errorf("%s: the floor runs %s, want its copy of v1.1.0 kept", when, rec.Version)
		}
		if g := fx.record(t).Good; g == nil || g.Commit != v11 {
			t.Errorf("%s: the good build moved to %+v, want v1.1.0", when, g)
		}
		for _, bad := range []string{"yolo's floor has no tool", "built at its base", "nothing runs"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: the advance treated the floor's copy as missing (%q):\n%s", when, bad, out)
			}
		}
		return out
	}
}

// THE FLOOR'S COPY OF THE GOOD BUILD SERVES WHEN ITS STORE ENTRY IS GONE (PF-D55): the floor holds
// the good build itself, so its advance treats that copy as what serves. It builds nothing to put the
// entry back, never builds the series' base in its place, and says nothing is missing. A newer
// upstream is built as one with a good build serving: through the interruptible child, its lines
// saying the copy keeps running; when it fails, the next launch inside the back-off builds nothing;
// and `agent_updates` holding the fork builds nothing either.
func TestTheFloorsCopyOfTheGoodBuildServesWhenItsStoreEntryIsGone(t *testing.T) {
	fx, v11, assertServes := floorServesGoneEntry(t)
	fx.later(10 * time.Minute)
	assertServes("inside the hour", 1)
	fx.later(2 * time.Hour)
	assertServes("past the hour, nothing newer", 1)

	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	out := assertServes("a newer upstream that fails", 2)
	if fx.child != 1 {
		t.Errorf("the newer upstream's build ran outside the interruptible child (%d child builds)", fx.child)
	}
	label11 := "v1.1.0 (" + shortSHA(v11) + ")"
	for _, w := range []string{
		"upstream moved — v1.3.0 (" + shortSHA(v13) + ") is newer than the good build " + label11,
		"`yolo host` waits for it, at most 20m0s, and a Ctrl-C starts tool on the good build " + label11 + " + 2 patches instead",
		"still running " + label11 + " + 2 patches",
		"`yolo capture tool` retries it now; `yolo host -- tool` retries it after",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("the failed build lacks %q:\n%s", w, out)
		}
	}
	fx.later(10 * time.Minute)
	assertServes("inside the back-off", 2)
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	fx.later(2 * time.Hour)
	assertServes("held by agent_updates", 2)
}

// WHAT A WALK STOPS ON, WHILE THE FLOOR'S COPY SERVES (PF-D55): an upstream that does not take the
// series, and one the series cannot be replayed onto, each leave the floor's copy running, said as
// such, with no build of the series' base in its place; and an apply error is replayed again only by
// the next check (PF-D45), never by every launch inside the hour.
func TestAWalkThatStopsLeavesTheFloorsCopyServing(t *testing.T) {
	t.Run("a conflict", func(t *testing.T) {
		fx, v11, assertServes := floorServesGoneEntry(t)
		fx.commit(t, "v1.4.0", map[int]string{10: "upstream ten"})
		fx.later(2 * time.Hour)
		out := assertServes("an upstream that does not take the series", 1)
		for _, w := range []string{"upstream v1.4.0 (", "does not take the patch series",
			"still running v1.1.0 (" + shortSHA(v11) + ") + 2 patches"} {
			if !strings.Contains(out, w) {
				t.Errorf("the conflict lacks %q:\n%s", w, out)
			}
		}
	})
	t.Run("an apply error", func(t *testing.T) {
		fx, v11, assertServes := floorServesGoneEntry(t)
		v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
		failFile := filepath.Join(t.TempDir(), "fail")
		writeFile(t, failFile, v13)
		patchedGitWrapper(t, failReadingFilesAt(failFile))
		fx.later(2 * time.Hour)
		out := assertServes("an apply error", 1)
		for _, w := range []string{"could not replay the series", "still running v1.1.0 (" + shortSHA(v11) +
			") + 2 patches; the next check, in an hour, retries it, or `yolo pack update` now"} {
			if !strings.Contains(out, w) {
				t.Errorf("the apply error lacks %q:\n%s", w, out)
			}
		}
		fx.later(time.Minute)
		if out := assertServes("inside the hour after it", 1); strings.Contains(out, "could not replay") {
			t.Errorf("a launch inside the hour replayed the apply error again:\n%s", out)
		}
	})
	t.Run("a failed fetch", func(t *testing.T) {
		// A CANDIDATE STILL PENDING WHEN THE NEXT CHECK'S FETCH FAILS (§6.2) is not built until a check
		// fetches: here v1.3.0, whose build a Ctrl-C ended, and an upstream that has since gone away.
		fx, _, assertServes := floorServesGoneEntry(t)
		fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
		fx.later(2 * time.Hour)
		prev := forkBuildChild
		forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ jailStreams, _ bool) (int, bool) {
			fx.child++
			if ctx.Done() == nil {
				t.Error("the floor's advance built outside an interrupt scope")
				return 1, false
			}
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			<-ctx.Done()
			return 130, false
		}
		assertServes("a Ctrl-C during v1.3.0's build", 1)
		forkBuildChild = prev
		if err := os.Rename(fx.repo, fx.repo+".gone"); err != nil {
			t.Fatal(err)
		}
		fx.later(2 * time.Hour)
		if out := assertServes("a failed fetch", 1); !strings.Contains(out, "could not check its upstream") {
			t.Errorf("the failed fetch is not said:\n%s", out)
		}
	})
}

// movedPastTheFloor leaves the floor's copy at the fixture's v1.1.0 while the good build moves to
// v1.3.0 — as a jail launch's advance moves it — and v1.3.0's store entry then goes, so the floor
// cannot install it. It returns v1.1.0's and v1.3.0's commits.
func movedPastTheFloor(t *testing.T, fx *patchedAdvanceFixture) (string, string) {
	t.Helper()
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	v13 := fx.commit(t, "v1.3.0", map[int]string{14: "fourteen", 20: "twenty"})
	fx.later(2 * time.Hour)
	saved := paths.HostFloorDir() + ".saved" // beside it, so the rename never crosses a filesystem
	if err := os.Rename(paths.HostFloorDir(), saved); err != nil {
		t.Fatal(err)
	}
	if rc, _, out := fx.hostLaunch(t); rc != 0 || fx.record(t).Good.Commit != v13 {
		t.Fatalf("the moving launch: rc=%d good=%+v\n%s", rc, fx.record(t).Good, out)
	}
	if err := os.RemoveAll(paths.HostFloorDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, paths.HostFloorDir()); err != nil {
		t.Fatal(err)
	}
	removeStoreEntry(t, fx.record(t).Good.Entry)
	if rec := floorRecord(t, "tool"); rec.Revision != v11 {
		t.Fatalf("the floor holds %s, want v1.1.0", rec.Version)
	}
	return v11, v13
}

// THE FLOOR'S INSTALLED COPY SERVES WHILE A MOVED GOOD BUILD IS BUILT AGAIN (PF-D55, PF-D8): the
// good build moved past the floor's copy and its entry went, so the floor's advance builds it again
// — as one with a good build serving, since the floor's copy keeps running whatever the build does.
// A Ctrl-C starts that copy; a failed build keeps it and never builds the series' base in its place;
// and the fork's line names the copy that runs, with the good build it does not.
func TestTheFloorsInstalledCopyServesWhileAMovedGoodBuildIsBuiltAgain(t *testing.T) {
	t.Run("a Ctrl-C", func(t *testing.T) {
		fx := patchedFloorFixture(t)
		v11, _ := movedPastTheFloor(t, fx)
		prev := forkBuildChild
		forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _ jailStreams, _ bool) (int, bool) {
			fx.child++
			if ctx.Done() == nil {
				// No interrupt scope holds the Ctrl-C, which would end the launch (and this test binary).
				t.Error("the floor's advance built outside an interrupt scope")
				return 1, false
			}
			_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			select {
			case <-ctx.Done():
			case <-time.After(10 * time.Second):
				t.Error("the Ctrl-C did not reach the floor's advance")
			}
			return 130, false
		}
		t.Cleanup(func() { forkBuildChild = prev })
		before := fx.child
		rc, target, out := fx.hostLaunch(t)
		if rc != 0 || target != filepath.Join(paths.HostFloorDir(), "bin", "tool") || fx.child != before+1 ||
			floorRecord(t, "tool").Revision != v11 {
			t.Fatalf("rc=%d target=%s child builds=%d floor=%s, want the interruptible build and the floor's "+
				"v1.1.0 started\n%s", rc, target, fx.child-before, floorRecord(t, "tool").Version, out)
		}
		installed := "the installed v1.1.0 (" + shortSHA(v11) + ") + 2 patches"
		for _, w := range []string{"and a Ctrl-C starts tool on " + installed + " instead",
			"the advance was interrupted — `yolo host` starts tool on " + installed} {
			if !strings.Contains(out, w) {
				t.Errorf("the launch does not name the copy that runs (%q):\n%s", w, out)
			}
		}
	})
	t.Run("a failed build", func(t *testing.T) {
		fx := patchedFloorFixture(t)
		v11, v13 := movedPastTheFloor(t, fx)
		fx.failBuildsOf(t, "twenty") // v1.3.0 fails to build; the series' base would build
		before := len(fx.builds)
		rc, target, out := fx.hostLaunch(t)
		if rc != 0 || target != filepath.Join(paths.HostFloorDir(), "bin", "tool") || len(fx.builds) != before+1 {
			t.Fatalf("rc=%d target=%s builds=%d, want v1.3.0 tried once and the floor's copy run\n%s", rc, target,
				len(fx.builds)-before, out)
		}
		if rec := floorRecord(t, "tool"); rec.Revision != v11 {
			t.Errorf("the floor runs %s, want its copy of v1.1.0 kept", rec.Version)
		}
		if g := fx.record(t).Good; g == nil || g.Commit != v13 {
			t.Errorf("the good build moved to %+v, want it left at v1.3.0", g)
		}
		label13 := "v1.3.0 (" + shortSHA(v13) + ")"
		for _, w := range []string{
			"the build of " + label13 + " + 2 patches failed: the capture jail exited 2 — nothing was stored — still " +
				"running v1.1.0 (" + shortSHA(v11) + ") + 2 patches",
			"running the installed v1.1.0 (" + shortSHA(v11) + ") + 2 patches",
			"`yolo capture tool` builds it",
			", at v1.1.0 (" + shortSHA(v11) + ") — its good build " + label13 + " is not installed",
		} {
			if !strings.Contains(out, w) {
				t.Errorf("the launch lacks %q:\n%s", w, out)
			}
		}
		if strings.Contains(out, ", at "+label13) {
			t.Errorf("the fork's line names the good build the floor does not run:\n%s", out)
		}
		fx.later(10 * time.Minute)
		if rc, _, out := fx.hostLaunch(t); rc != 0 || len(fx.builds) != before+1 {
			t.Errorf("inside the back-off: rc=%d builds=%d, want no build\n%s", rc, len(fx.builds)-before, out)
		}
	})
}

// A BUILD THAT LEFT THE STORE BEFORE ITS MOVE, AT THE HOST (PF-D46), names the floor's copy as what
// runs (PF-D55), never a missing program: the floor's v1.1.0 keeps serving whatever the advance does.
func TestABuildThatLeftTheStoreAtTheHostNamesTheFloorsCopy(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	removeStoreEntry(t, fx.record(t).Good.Entry)
	store := &capture.Store{Dir: paths.CapturesDir()}
	staged, err := store.Stage("gone")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(capture.TreeDir(staged), ".local", "bin", "tool"), "gone")
	entry, err := store.AdmitEntry(staged)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReapEntry(entry.Key); err != nil {
		t.Fatal(err)
	}
	var out syncBuffer
	a, early := newAdvance(fx.fork(t), advanceOptions{platform: floorPatchedPlatform(), out: &out, errw: &out,
		launch: true, host: true, installed: floorServingCopy(floorRecord(t, "tool"))})
	if early != nil {
		t.Fatal(early)
	}
	a.seq = a.rec.Seq + 1
	b := forkBuild{Fork: a.f, Commit: v11, Platform: floorPatchedPlatform(), Series: a.series,
		Entry: packsrc.ListEntry{Commit: v11, Tag: "v1.1.0", Version: "1.1.0"}}
	a.moved(b, entry, baseNone, false)
	if want := "`yolo host` runs v1.1.0 (" + shortSHA(v11) + ") + 2 patches"; !strings.Contains(out.String(), want) ||
		strings.Contains(out.String(), "yolo's floor has no tool") {
		t.Errorf("the line does not name the floor's copy as what runs (%q):\n%s", want, out.String())
	}
}

// THE FLOOR'S COPY SERVES ONLY AS A BUILD OF THE SERIES AS IT STANDS (PF-D55): a copy handed to the
// advance whose recipe is not the manifest's — the series edited since the floor read it — serves
// nothing, and the advance runs as one with nothing serving.
func TestAFloorCopyOfAnotherRecipeServesNoAdvance(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	for _, tc := range []struct {
		recipe string
		serves bool
	}{{fx.recipe(t), true}, {"another-recipe", false}} {
		a, early := newAdvance(fx.fork(t), advanceOptions{platform: floorPatchedPlatform(), out: io.Discard,
			errw: io.Discard, launch: true, host: true, installed: &installedCopy{commit: v11, recipe: tc.recipe, label: "x"}})
		if early != nil {
			t.Fatal(early)
		}
		if a.serves() != tc.serves {
			t.Errorf("a floor copy of recipe %q: serves = %v, want %v", tc.recipe, a.serves(), tc.serves)
		}
	}
}

// A HELD FORK'S ADVANCE OVER THE FLOOR'S COPY OF THE GOOD BUILD BUILDS NOTHING (PF-D19, PF-D55): under
// `agent_updates` off the floor runs an advance only for an entry it reinstalls (a raised node_floor,
// say), and with the good build's store entry gone that advance does not build the good build again,
// held or not: the floor keeps its copy, and its stop names `yolo capture <bin>`.
func TestAHeldAdvanceOverTheFloorsCopyOfTheGoodBuildBuildsNothing(t *testing.T) {
	fx, _, _ := floorServesGoneEntry(t)
	fx.writeUserConfig(t, `,"agent_updates":{"forkpack":false}`)
	fx.later(2 * time.Hour)
	var out syncBuffer
	advancePatchedFork(fx.fork(t), advanceOptions{platform: floorPatchedPlatform(), out: &out, errw: &out, launch: true,
		host: true, installed: floorServingCopy(floorRecord(t, "tool"))})
	if len(fx.builds) != 1 || strings.Contains(out.String(), "checking fork") {
		t.Errorf("a held advance over the floor's copy checked or built (%d builds):\n%s", len(fx.builds), out.String())
	}
}

// THE USER'S OWN FAILED EDIT AT THE HOST IS NOT HELD (PF-D23), through the production read: an edit
// to the fork pack's `build` that does not build leaves no good build serving the manifest, so the
// floor's copy of the old recipe leaves bin/, `yolo host` does not start it, and the stop names the
// revert.
func TestAUsersFailedEditAtTheHostRemovesTheFloorsCopy(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	launcher := filepath.Join(paths.HostFloorDir(), "bin", "tool")
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	writeFile(t, fx.manifest, strings.Replace(mustRead(t, fx.manifest), `"build":"sh build.sh"`,
		`"build":"sh build2.sh"`, 1))
	fx.rc = 2
	fx.later(10 * time.Minute)
	rc, target, out := fx.hostLaunch(t)
	if rc != 127 || target != "" {
		t.Fatalf("a failed edit: rc=%d target=%s, want the launch refused\n%s", rc, target, out)
	}
	if _, err := os.Lstat(launcher); !os.IsNotExist(err) {
		t.Errorf("the old recipe's copy is still in the floor's bin/ (%v)", err)
	}
	for _, w := range []string{"so the floor no longer runs it", "reverting the edit brings that build back"} {
		if !strings.Contains(out, w) {
			t.Errorf("the failed edit lacks %q:\n%s", w, out)
		}
	}
}

// A GOOD BUILD THAT IS GONE AND DOES NOT BUILD AGAIN STOPS WITH ITS NEXT STEP: the floor holds no
// copy, the advance's rebuild and the series' base both fail, and the launch's last line names the
// act that builds it — never an install that claims to build it after the build already failed.
func TestAGoneGoodBuildThatDoesNotBuildAgainNamesTheNextStep(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	if rc, _, out := fx.hostLaunch(t); rc != 0 {
		t.Fatalf("the first launch: rc=%d\n%s", rc, out)
	}
	removeStoreEntry(t, fx.record(t).Good.Entry)
	if err := os.RemoveAll(paths.HostFloorDir()); err != nil {
		t.Fatal(err)
	}
	fx.rc = 1
	fx.later(10 * time.Minute)
	rc, _, out := fx.hostLaunch(t)
	if rc != 127 {
		t.Fatalf("rc=%d, want the launch refused\n%s", rc, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "could not install tool") || !strings.Contains(last, "`yolo capture tool`") {
		t.Errorf("the stop does not name its next step:\n%s", last)
	}
	if strings.Contains(out, "the install builds it again") {
		t.Errorf("a line claims the install builds what the advance just failed to:\n%s", out)
	}
}

// THE PRODUCTION FLOOR'S PATCHED WIRING IS THE CALL SITE: its Advance runs floorAdvance for the fork
// as the selection declares it (its series' root among it) and returns the state after, and its
// Patched reads that state offline — the fixture's first advance's good build, from the check record.
func TestTheProductionFloorWiresThePatchedAdvanceAndRead(t *testing.T) {
	fx := patchedFloorFixture(t)
	v11 := fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	progs := floorPrograms(selectConfiguredHostPacks().packs)
	p, ok := floorProgram(progs, "tool")
	if !ok || !p.Install.IsPatchedFork() {
		t.Fatalf("the selection's floor has no patched tool: %+v", progs)
	}
	var advanced []packload.Fork
	var handed []*installedCopy
	prevAdvance := floorAdvance
	floorAdvance = func(f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) {
		advanced, handed = append(advanced, f), append(handed, installed)
		prevAdvance(f, out, installed, act)
	}
	t.Cleanup(func() { floorAdvance = prevAdvance })
	floor := productionHostFloor(io.Discard, progs)
	if ps := floor.Patched(p); ps.Recipe == "" || ps.Good != nil {
		t.Fatalf("before any advance: %+v, want the series read and no good build", ps)
	}
	ps := floor.Advance(context.Background(), p, nil)
	if len(advanced) != 1 || advanced[0].Key() != "forkpack/tool" || advanced[0].Root != fx.forkDir {
		t.Fatalf("the floor advanced %+v, want forkpack/tool read from %s", advanced, fx.forkDir)
	}
	if ps.Good == nil || ps.Good.Commit != v11 || ps.Good.Entry == nil || ps.Good.Recipe != ps.Recipe {
		t.Fatalf("the state after the advance = %+v, want v1.1.0's good build with its entry", ps)
	}
	if again := floor.Patched(p); again.Good == nil || again.Good.Entry.Key != ps.Good.Entry.Key {
		t.Errorf("the offline read after the advance = %+v", again)
	}
	// THE FLOOR'S COPY THAT SERVES reaches the advance as the advance reads it (PF-D55).
	installed := &hostfloor.Record{Revision: v11, Recipe: ps.Recipe, Version: ps.Good.Label}
	floor.Advance(context.Background(), p, installed)
	want := installedCopy{commit: v11, recipe: ps.Recipe, label: ps.Good.Label}
	if len(handed) != 2 || handed[0] != nil || handed[1] == nil || *handed[1] != want {
		t.Errorf("the advances were handed %+v, want nothing and then %+v", handed, want)
	}
}
