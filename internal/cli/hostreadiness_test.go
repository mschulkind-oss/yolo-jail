package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// declaredNoCopyRefusal is the clause HNR-D2's refusal adds after the floor's reason: a program a
// selected pack declares is never run from the caller's PATH.
const declaredNoCopyRefusal = "; yolo host runs a program a selected pack declares only from yolo's floor, " +
	"never a copy on your PATH"

// twoProgramFixture is a host whose user config selects one pack declaring two npm programs,
// floorcli and othercli, both published, plus a stub `sometool` on PATH that no pack declares. extra
// is appended to the config object.
func twoProgramFixture(t *testing.T, extra string) (dist *floortest.Dist, sometool string) {
	t.Helper()
	d, _ := floorHostFixtureWith(t, `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"},`+
		`{"kind":"program","bin":"othercli","via":"npm","package":"othercli-pkg"}`, extra)
	d.Publish("othercli-pkg", "1.0.0", "bin=othercli")
	return d, filepath.Join(stubBins(t, "sometool"), "sometool")
}

func floorHolds(bin string) bool {
	_, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", bin))
	return err == nil
}

// HNR-D1 at the call site: a launch whose command no pack declares still installs every program the
// selected packs declare, before it runs the command, and the command runs from the PATH as before.
// Delete the act's call in hostLaunch and neither program is installed.
func TestEveryHostLaunchInstallsEveryDeclaredProgramBeforeTheCommand(t *testing.T) {
	_, sometool := twoProgramFixture(t, "")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"sometool"}, io.Discard, &errw, nil); rc != 0 || got.target != sometool {
		t.Fatalf("rc=%d target=%s, want the PATH's %s\n%s", rc, got.target, sometool, errw.String())
	}
	for _, bin := range []string{"floorcli", "othercli"} {
		if !floorHolds(bin) {
			t.Errorf("the launch did not install %s, which a selected pack declares:\n%s", bin, errw.String())
		}
		if !strings.Contains(errw.String(), "installing "+bin+" into yolo's floor") {
			t.Errorf("the install of %s was not said:\n%s", bin, errw.String())
		}
	}
	// The act comes before the hand-over: the starting line is the last thing said.
	if i, j := strings.Index(errw.String(), "installed othercli"), strings.Index(errw.String(), "yolo host: starting sometool"); i < 0 || j < i {
		t.Errorf("the command was handed over before the act finished:\n%s", errw.String())
	}
}

// The agent's own launch installs the OTHER declared programs too, and its own once, by target
// resolution: the act does not install it a second time.
func TestAnAgentLaunchInstallsTheOtherDeclaredProgramsAndItselfOnce(t *testing.T) {
	dist, _ := twoProgramFixture(t, "")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if got.target != filepath.Join(paths.HostFloorDir(), "bin", "floorcli") {
		t.Errorf("exec'd %s, want the floor's floorcli", got.target)
	}
	if !floorHolds("othercli") {
		t.Errorf("othercli, which a selected pack declares, was not installed:\n%s", errw.String())
	}
	n := 0
	for _, call := range dist.NpmCalls("install") {
		if strings.Contains(call, "floorcli-pkg") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("floorcli's package was installed %d times on one launch, want once", n)
	}
}

// A declared program that cannot be installed REFUSES the launch, before the command, naming the
// program, its pack, the error, the ways to fix it and the hatch.
func TestAHostLaunchRefusesWhenADeclaredProgramCannotBeInstalled(t *testing.T) {
	dist, _ := twoProgramFixture(t, "")
	dist.Publish("othercli-pkg", "1.0.0", "bin=othercli", "fail")
	got := captureHostExec(t)
	var errw bytes.Buffer
	rc := hostExec(nil, []string{"sometool", "--x"}, io.Discard, &errw, nil)
	if rc == 0 || got.execed {
		t.Fatalf("rc=%d execed=%v: the launch ran without a program its config selects\n%s", rc, got.execed, errw.String())
	}
	for _, want := range []string{
		"yolo host: REFUSING to launch: a program a selected pack declares could not be installed.\n",
		"      program othercli (pack floorpack): ",
		"npm ERR! 404",
		"`\"host_floor\": {\"floorpack\": false}`",
		"          YOLO_ALLOW_MISSING_PROGRAMS=1 yolo host -- sometool --x\n",
	} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, errw.String())
		}
	}
	if strings.Contains(errw.String(), "program floorcli (pack") {
		t.Errorf("the refusal listed a program that installed:\n%s", errw.String())
	}
}

// HNR-D3: with YOLO_ALLOW_MISSING_PROGRAMS set the launch goes on and lists what it could not install.
func TestTheBypassStartsTheLaunchAndListsWhatIsMissing(t *testing.T) {
	dist, sometool := twoProgramFixture(t, "")
	dist.Publish("othercli-pkg", "1.0.0", "bin=othercli", "fail")
	t.Setenv(paths.AllowMissingProgramsEnv, "yes")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"sometool"}, io.Discard, &errw, nil); rc != 0 || got.target != sometool {
		t.Fatalf("rc=%d target=%s, want %s\n%s", rc, got.target, sometool, errw.String())
	}
	for _, want := range []string{
		"yolo host: ⚠ YOLO_ALLOW_MISSING_PROGRAMS is set, so this launch starts WITHOUT what a selected pack declares:\n",
		"      program othercli (pack floorpack): ",
		"The next `yolo host` launch tries each install again.",
	} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the bypass line lacks %q:\n%s", want, errw.String())
		}
	}
	if !floorHolds("floorcli") {
		t.Errorf("the bypass skipped a program that installs:\n%s", errw.String())
	}
}

// HNR-D3's other half: the bypass never covers the program the launch runs. Its failed install is
// still exit 127, with nothing exec'd.
func TestTheBypassNeverRunsTheLaunchsOwnMissingProgram(t *testing.T) {
	dist, _ := twoProgramFixture(t, "")
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli", "fail")
	t.Setenv(paths.AllowMissingProgramsEnv, "1")
	stubBins(t, "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 127 || got.execed {
		t.Fatalf("rc=%d target=%s, want 127 and no exec\n%s", rc, got.target, errw.String())
	}
	if !strings.Contains(errw.String(), "could not install floorcli into yolo's floor") {
		t.Errorf("the refusal does not name the program:\n%s", errw.String())
	}
}

// HNR-D4: a pack the user-scope `host_floor` leaves out is the user's choice, not a failure: the act
// installs nothing of it and says nothing, and the rest of the floor still installs.
func TestTheActSkipsWhatTheUserLeavesOutOfTheFloor(t *testing.T) {
	_, sometool := twoProgramFixture(t, `,"host_floor":{"floorpack":false}`)
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"sometool"}, io.Discard, &errw, nil); rc != 0 || got.target != sometool {
		t.Fatalf("rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	if floorHolds("floorcli") || floorHolds("othercli") || strings.Contains(errw.String(), "REFUSING") ||
		strings.Contains(errw.String(), "floorcli") {
		t.Errorf("the act acted on a pack `host_floor` leaves out:\n%s", errw.String())
	}
}

// A target given as a path is the user's own copy of its base name's program: the act does not
// install the floor's (HNR-D5), and still installs the others.
func TestAPathTargetsOwnProgramIsNotInstalledByTheAct(t *testing.T) {
	dist, _ := twoProgramFixture(t, "")
	mine := filepath.Join(stubBins(t, "floorcli"), "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{mine}, io.Discard, &errw, nil); rc != 0 || got.target != mine {
		t.Fatalf("rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	for _, call := range dist.NpmCalls("install") {
		if strings.Contains(call, "floorcli-pkg") {
			t.Errorf("the act installed the floor's copy of a program given as a path: %q", call)
		}
	}
	if !floorHolds("othercli") {
		t.Errorf("othercli was not installed:\n%s", errw.String())
	}
}

// Zero declared programs: no act, nothing said, no floor directory made — the jail's
// TestAJailWithNoProgramsHasNoReadinessAct case.
func TestAHostLaunchWithNoDeclaredProgramsHasNoAct(t *testing.T) {
	floorHostFixtureWith(t, `{"kind":"env","vars":{"A":"b"}}`, "")
	sometool := filepath.Join(stubBins(t, "sometool"), "sometool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"sometool"}, io.Discard, &errw, nil); rc != 0 || got.target != sometool {
		t.Fatalf("rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	if _, err := os.Stat(paths.HostFloorDir()); err == nil {
		t.Errorf("a launch with no declared program made %s:\n%s", paths.HostFloorDir(), errw.String())
	}
	if strings.Contains(errw.String(), "into yolo's floor") {
		t.Errorf("a launch with no declared program spoke of the floor:\n%s", errw.String())
	}
}

// HNR-D4's third class: a program whose vendor publishes no build for this platform is not the
// floor's to hold, as the jail writes no launcher for it. The act skips it, and a launch of it runs
// the copy on the PATH (one the user built themselves) with the no-copy line, rather than refusing.
func TestAnUnpublishedProgramRunsThePATHCopyAndIsNotAReadinessFailure(t *testing.T) {
	floorHostFixtureWith(t, `{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg",`+
		`"platforms":["plan9/amd64"]}`, "")
	mine := filepath.Join(stubBins(t, "floorcli"), "floorcli")
	sometool := filepath.Join(stubBins(t, "sometool"), "sometool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"sometool"}, io.Discard, &errw, nil); rc != 0 || got.target != sometool {
		t.Fatalf("the act refused over an unpublished program: rc=%d target=%s\n%s", rc, got.target, errw.String())
	}
	*got = execCapture{}
	errw.Reset()
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || got.target != mine {
		t.Fatalf("rc=%d target=%s, want the PATH copy %s\n%s", rc, got.target, mine, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo has no copy of floorcli") ||
		!strings.Contains(errw.String(), "looking for it on your PATH") {
		t.Errorf("the launch did not say the copy is not yolo's:\n%s", errw.String())
	}
}
