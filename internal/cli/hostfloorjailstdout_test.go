package cli

// hostfloorjailstdout_test.go pins where the jails a HOST verb boots write their own stdout: this
// process's stderr, never its stdout (hostJailStdout; newHostFloor: "an agent's stdout is routinely
// parsed, so nothing here may write to it"). Before this, a `yolo host -- <bin>` that captured an
// installer, or built a fork, printed the installer's or the build's lines on stdout ahead of the
// agent's own: the run pipeline relayed the jail's output to the process's own stdout whatever
// writers it was handed (run.Options.JailStdout, and the session's SessionStdout). A build jail's
// act tees the jail's own lines (jailTail), so for a build the session's writer is the one read. Each test drives the production wiring and reads
// the options the run pipeline was handed (captureRunPipeline), or, for the macos-user arm, runs a
// command through the account runner the act composed.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// probetoolCaptured is what the fake capture jail files for probetool.
var probetoolCaptured = []capture.ManifestEntry{
	{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
	{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
	{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 16},
}

// THE FLOOR'S CAPTURE JAIL WRITES TO THIS PROCESS'S STDERR (the Linux floor, the capture jail's arm):
// the production floor's Capture hands the run pipeline os.Stderr as the jail's stdout, while a typed
// `yolo capture` leaves it unset, the process's own stdout, as before.
func TestTheFloorsCaptureJailWritesItsOutputToTheLaunchsStderr(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	stubContainerRuntime(t)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, probetoolCaptured))

	var floorOut bytes.Buffer
	f := productionHostFloor(&floorOut, nil)
	f.GOOS = "linux"
	if err := f.Capture("probetool"); err != nil {
		t.Fatalf("the floor's capture: %v\n%s", err, floorOut.String())
	}
	if seen.JailStdout != io.Writer(os.Stderr) || seen.SessionStdout != io.Writer(os.Stderr) {
		t.Errorf("the floor's capture jail writes its stdout to %s and its session's to %s, want this "+
			"process's stderr for both", writerName(seen.JailStdout), writerName(seen.SessionStdout))
	}

	seen = run.Options{}
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("yolo capture: rc %d\n%s", rc, errw.String())
	}
	if seen.JailStdout != nil || seen.SessionStdout != nil {
		t.Errorf("a typed `yolo capture` sends its jail's stdout to %s and its session's to %s, want "+
			"this process's own (unset)", writerName(seen.JailStdout), writerName(seen.SessionStdout))
	}
}

// ON A MAC THE FLOOR'S CAPTURE ACT RUNS THE ACCOUNT'S COMMANDS WITH THEIR STDOUT ON THIS PROCESS'S
// STDERR (HP-D2): the macos-user act's setup, bootstrap, driver and installer are commands it runs
// through deps.Run, which by default inherits this process's stdout.
func TestTheMacFloorsCaptureActRunsItsCommandsWithStdoutOnStderr(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, probetoolCaptured))
	orig := macCaptureAct
	macCaptureAct = func(deps macosuser.Deps, _ macosuser.CaptureOptions, _ string, _ bool) int {
		return deps.Run([]string{"sh", "-c", "echo MAC_CAPTURE_STDOUT"})
	}
	t.Cleanup(func() { macCaptureAct = orig })
	stderr := swapStderr(t)

	f := productionHostFloor(io.Discard, nil)
	f.GOOS = "darwin"
	if err := f.Capture("probetool"); err != nil {
		t.Fatalf("the floor's capture: %v\n%s", err, stderr())
	}
	if seen.MacosUserRun == nil {
		t.Fatal("the Mac floor's capture reached no macos-user arm")
	}
	if rc := seen.MacosUserRun(jsonx.NewOrderedMap(), "", nil, nil, "", "", macosuser.HomeOverlay{},
		macosuser.HostContext{}, false, jsonx.NewOrderedMap(), nil, macosuser.JailDaemons{}); rc != 0 {
		t.Fatalf("the macos-user capture act: rc %d", rc)
	}
	if got := stderr(); !strings.Contains(got, "MAC_CAPTURE_STDOUT\n") {
		t.Errorf("the act's command did not write its stdout to this process's stderr:\n%s", got)
	}
}

// swapStderr points os.Stderr at a file for the rest of the test and returns a func reading what
// was written to it.
func swapStderr(t *testing.T) func() string {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = orig; _ = f.Close() })
	return func() string {
		b, _ := os.ReadFile(f.Name())
		return string(b)
	}
}

// writerName names w for a message: a *bytes.Buffer's %v is its contents.
func writerName(w io.Writer) string {
	switch w {
	case nil:
		return "nothing (this process's stdout)"
	case io.Writer(os.Stdout):
		return "os.Stdout"
	case io.Writer(os.Stderr):
		return "os.Stderr"
	}
	return fmt.Sprintf("a %T", w)
}

// THE FLOOR'S FORK BUILD JAIL WRITES TO THIS PROCESS'S STDERR: the first `yolo host -- <forked bin>`
// builds the pin in the sealed jail (FP-D4), whose output is that launch's progress.
func TestTheFloorsForkBuildJailWritesItsOutputToTheLaunchsStderr(t *testing.T) {
	forkFloorHome(t)
	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("pack install rc=%d\n%s\n%s", rc, out.String(), errw.String())
	}
	dist := withForkFloor(t)
	dist.Publish("forkcli-pkg", "1.0.0", "bin=forkcli")
	runs := 0
	build := forkFloorBuildJail(t, &runs, true)
	var jailStdout io.Writer
	withFakeCaptureJail(t, func(o run.Options) int { jailStdout = o.SessionStdout; return build(o) })
	captureHostExec(t)

	errw.Reset()
	if rc := hostExec(nil, []string{"forkcli"}, io.Discard, &errw, nil); rc != 0 || runs != 1 {
		t.Fatalf("rc=%d builds=%d, want the floor's one build\n%s", rc, runs, errw.String())
	}
	if jailStdout != io.Writer(os.Stderr) {
		t.Errorf("the floor's build jail runs its session with its stdout on %s, want this process's stderr", writerName(jailStdout))
	}
}

// THE HOST'S PATCHED-FORK ADVANCE BUILDS IN A JAIL THAT WRITES TO THIS PROCESS'S STDERR: the first
// `yolo host -- <bin>` of a patched fork runs its advance in this process (PF-D14), whose build jail
// is the launch's progress too.
func TestTheHostsAdvanceBuildJailWritesItsOutputToTheLaunchsStderr(t *testing.T) {
	fx := patchedFloorFixture(t)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	build := fx.buildJail(t)
	var jailStdout io.Writer
	withFakeCaptureJail(t, func(o run.Options) int { jailStdout = o.SessionStdout; return build(o) })
	captureHostExec(t)

	var errw syncBuffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || len(fx.builds) != 1 {
		t.Fatalf("rc=%d builds=%d, want the advance's one build\n%s", rc, len(fx.builds), errw.String())
	}
	if jailStdout != io.Writer(os.Stderr) {
		t.Errorf("the advance's build jail runs its session with its stdout on %s, want this process's stderr", writerName(jailStdout))
	}
}
