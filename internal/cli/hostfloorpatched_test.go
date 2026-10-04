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
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _, _ io.Writer, _ bool) (int, bool) {
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
	floorAdvance = func(f packload.Fork, out io.Writer) {
		events = append(events, "advance")
		prevAdvance(f, out)
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
	floorAdvance = func(packload.Fork, io.Writer) { t.Error("a Mac's floor ran a patched fork's advance") }
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
	prevAdvance := floorAdvance
	floorAdvance = func(f packload.Fork, out io.Writer) { advanced = append(advanced, f); prevAdvance(f, out) }
	t.Cleanup(func() { floorAdvance = prevAdvance })
	floor := productionHostFloor(io.Discard, progs)
	if ps := floor.Patched(p); ps.Recipe == "" || ps.Good != nil {
		t.Fatalf("before any advance: %+v, want the series read and no good build", ps)
	}
	ps := floor.Advance(context.Background(), p)
	if len(advanced) != 1 || advanced[0].Key() != "forkpack/tool" || advanced[0].Root != fx.forkDir {
		t.Fatalf("the floor advanced %+v, want forkpack/tool read from %s", advanced, fx.forkDir)
	}
	if ps.Good == nil || ps.Good.Commit != v11 || ps.Good.Entry == nil || ps.Good.Recipe != ps.Recipe {
		t.Fatalf("the state after the advance = %+v, want v1.1.0's good build with its entry", ps)
	}
	if again := floor.Patched(p); again.Good == nil || again.Good.Entry.Key != ps.Good.Entry.Key {
		t.Errorf("the offline read after the advance = %+v", again)
	}
}
