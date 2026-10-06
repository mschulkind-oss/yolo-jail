package run

// macosuserarm_test.go pins the macos-user arm (macosuserarm.go) phase by phase, with real signals
// sent to this test process, and its two call sites in Run: installed after the config-change
// prompt and before the first host service, and asked before the dispatch.

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// keepAlive catches sig on a channel of the test's own, so a defect that left the arm not taking it
// fails the assertion instead of ending the whole test binary by the default action.
func keepAlive(t *testing.T, sigs ...os.Signal) {
	t.Helper()
	c := make(chan os.Signal, 8)
	signal.Notify(c, sigs...)
	t.Cleanup(func() { signal.Stop(c) })
}

// installArm installs a fresh arm on log and disarms it at cleanup.
func installArm(t *testing.T, log *perf.Log) (*MacosUserArm, *[]string) {
	t.Helper()
	var notices []string
	arm := NewMacosUserArm()
	disarm := arm.install(log, func(line string) { notices = append(notices, line) })
	t.Cleanup(disarm)
	return arm, &notices
}

// awaitEnding waits for the arm to report a signal ending the launch.
func awaitEnding(t *testing.T, arm *MacosUserArm) int {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if status, ending := arm.Ending(); ending {
			return status
		}
	}
	t.Fatal("the arm never reported the signal ending the launch")
	return 0
}

// awaitForeground waits for the arm to publish a setup child as its foreground process.
func awaitForeground(t *testing.T, arm *MacosUserArm) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		arm.mu.Lock()
		foreground := arm.foreground
		arm.mu.Unlock()
		if foreground != nil {
			return
		}
	}
	t.Fatal("the arm never published the foreground child")
}

func publishForeground(t *testing.T, arm *MacosUserArm, p *os.Process) func() {
	t.Helper()
	done, err := arm.startForeground(func() (*os.Process, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	return done
}

type backendRunFixture struct {
	result   chan int
	finished chan struct{}
	stopPath string
}

func startBackendRunFixture(t *testing.T, run func([]string) int, argv []string, stopPath string) *backendRunFixture {
	t.Helper()
	fixture := &backendRunFixture{
		result:   make(chan int, 1),
		finished: make(chan struct{}),
		stopPath: stopPath,
	}
	t.Cleanup(func() {
		select {
		case <-fixture.finished:
			return
		default:
		}
		if err := os.WriteFile(fixture.stopPath, []byte("stop\n"), 0o600); err != nil && !os.IsNotExist(err) {
			t.Errorf("stop backend fixture: %v", err)
		}
		<-fixture.finished
	})
	go func() {
		fixture.result <- run(argv)
		close(fixture.finished)
	}()
	return fixture
}

func (f *backendRunFixture) wait() int { return <-f.result }

// trappingChild starts a shell that records each INT and TERM it gets in marks/seen, says it is
// ready once its traps are set, and exits 3 on a TERM.
func trappingChild(t *testing.T, marks string) *exec.Cmd {
	t.Helper()
	seen, ready := filepath.Join(marks, "seen"), filepath.Join(marks, "ready")
	cmd := exec.Command("sh", "-c", `trap 'echo INT >> "$1"' INT; trap 'echo TERM >> "$1"; exit 3' TERM; : > "$2"; `+
		`i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; exit 9`, "sh", seen, ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	awaitFile(t, ready)
	return cmd
}

func awaitFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Fatalf("%s never appeared", path)
}

func readSeen(marks string) string {
	b, _ := os.ReadFile(filepath.Join(marks, "seen"))
	return strings.TrimSpace(string(b))
}

// SETUP: a SIGTERM to yolo alone ends the launch 143, stops the nix this process runs, and is
// forwarded to the foreground child the backend registered — which a signal sent to yolo alone
// never reaches. The notice is printed once, and the runner refuses to start the session.
func TestASetupSignalEndsTheLaunchStopsNixAndReachesTheForegroundChild(t *testing.T) {
	s := nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGTERM)
	log := perf.New(nil)
	arm, notices := installArm(t, log)

	nixMarks := t.TempDir()
	nixDone := make(chan error, 1)
	go func() {
		nixDone <- nixchildren.Run(exec.Command("sh", "-c", testsupport.UntilInterrupted(
			"touch "+shquote.Quote(filepath.Join(nixMarks, "interrupted")), filepath.Join(nixMarks, "started"))))
	}()
	awaitFile(t, filepath.Join(nixMarks, "started"))

	marks := t.TempDir()
	child := trappingChild(t, marks)
	done := publishForeground(t, arm, child.Process)
	defer done()

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if got := awaitEnding(t, arm); got != 143 {
		t.Errorf("Ending = %d, want 143", got)
	}
	select {
	case <-nixDone:
	case <-time.After(15 * time.Second):
		t.Fatal("the nix this process runs was not stopped")
	}
	if _, err := os.Stat(filepath.Join(nixMarks, "interrupted")); err != nil {
		t.Error("the stand-in nix ended without the interrupt the stop gives it")
	}
	if s.Running() != 0 {
		t.Errorf("%d nix still running", s.Running())
	}
	_ = child.Wait()
	if got := readSeen(marks); got != "TERM" {
		t.Errorf("the foreground child saw %q, want the forwarded TERM", got)
	}
	if len(*notices) != 1 || !strings.Contains((*notices)[0], "SIGTERM") || !strings.Contains((*notices)[0], "`yolo` again") {
		t.Errorf("the notice is %q, want one line naming the signal and the next step", *notices)
	}
	if _, ok := log.LastEvent("terminate.signal"); !ok {
		t.Error("no terminate.signal mark on the launch's collector")
	}
	if rc := arm.RunSession([]string{"sh", "-c", "exit 0"}); rc != 143 {
		t.Errorf("the runner started the session of a launch the signal ended (rc %d)", rc)
	}
}

// SETUP, SIGINT: the launch ends 130, and the INT is NOT forwarded — a terminal's Ctrl-C reached the
// foreground group already, and a second one would hit a child mid-teardown.
func TestASetupInterruptEndsTheLaunchWithoutForwarding(t *testing.T) {
	nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGINT)
	arm, _ := installArm(t, nil)
	marks := t.TempDir()
	child := trappingChild(t, marks)
	defer publishForeground(t, arm, child.Process)()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if got := awaitEnding(t, arm); got != 130 {
		t.Errorf("Ending = %d, want 130", got)
	}
	time.Sleep(200 * time.Millisecond)
	if got := readSeen(marks); got != "" {
		t.Errorf("the foreground child was sent %q", got)
	}
	// And a child the teardown starts once the launch is ending is never its target.
	later := trappingChild(t, t.TempDir())
	if _, err := arm.startForeground(func() (*os.Process, error) { return later.Process, nil }); err != nil {
		t.Fatal(err)
	}
	arm.mu.Lock()
	registered := arm.foreground == later.Process
	arm.mu.Unlock()
	if registered {
		t.Error("a child started after the ending became the forward target")
	}
}

// SETUP, SIGQUIT: absorbed. It does not end the launch, is not forwarded, prints nothing and stops
// no nix — a stray Ctrl-\ during setup is not a request to stop.
func TestASetupQuitIsAbsorbed(t *testing.T) {
	nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGQUIT)
	arm, notices := installArm(t, nil)
	marks := t.TempDir()
	child := exec.Command("sh", "-c", `trap 'echo QUIT >> "$1"' QUIT; : > "$2"; `+
		`i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done`, "sh",
		filepath.Join(marks, "seen"), filepath.Join(marks, "ready"))
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	awaitFile(t, filepath.Join(marks, "ready"))
	defer publishForeground(t, arm, child.Process)()
	if err := syscall.Kill(os.Getpid(), syscall.SIGQUIT); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if status, ending := arm.Ending(); ending {
		t.Errorf("a SIGQUIT in setup ended the launch %d", status)
	}
	if len(*notices) != 0 {
		t.Errorf("a SIGQUIT in setup said %q", *notices)
	}
	if got := readSeen(marks); got != "" {
		t.Errorf("the foreground child was sent %q", got)
	}
	if nixchildren.Stopped() {
		t.Error("a SIGQUIT in setup stopped this process's nix")
	}
}

// SESSION: SIGINT and SIGQUIT are absorbed (the terminal gives them to the command's group itself),
// SIGTERM is forwarded, and the runner returns the command's own status.
func TestTheSessionForwardsTerminateAbsorbsInterruptAndReturnsTheCommandsStatus(t *testing.T) {
	keepAlive(t, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	log := perf.New(nil)
	arm, _ := installArm(t, log)
	marks := t.TempDir()
	seen, ready := filepath.Join(marks, "seen"), filepath.Join(marks, "ready")
	go func() {
		awaitFile(t, ready)
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		_ = syscall.Kill(os.Getpid(), syscall.SIGQUIT)
		time.Sleep(200 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()
	rc := arm.RunSession([]string{"sh", "-c", `trap 'echo INT >> "$1"' INT; trap 'echo QUIT >> "$1"' QUIT; ` +
		`trap 'echo TERM >> "$1"; exit 3' TERM; : > "$2"; i=0; while [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; exit 9`,
		"sh", seen, ready})
	if rc != 3 {
		t.Errorf("rc = %d, want the command's 3", rc)
	}
	if got := readSeen(marks); got != "TERM" {
		t.Errorf("the command saw %q, want TERM alone (INT and QUIT absorbed)", got)
	}
	for _, mark := range []string{"child.spawned", "child.exited"} {
		if _, ok := log.LastEvent(mark); !ok {
			t.Errorf("no %s mark on the launch's collector", mark)
		}
	}
	// A command a signal killed returns 128+N, where the plain exec this replaced returned 255.
	if rc := NewMacosUserArm().RunSession([]string{"sh", "-c", `kill -TERM $$`}); rc != 143 {
		t.Errorf("a command killed by SIGTERM returned %d, want 143", rc)
	}
}

// TEARDOWN: once the command has exited, a signal is absorbed — the teardown under way finishes —
// and it does not mark the launch as ending.
func TestTheTeardownAbsorbsALateSignal(t *testing.T) {
	keepAlive(t, syscall.SIGTERM)
	arm, notices := installArm(t, nil)
	if rc := arm.RunSession([]string{"sh", "-c", "exit 5"}); rc != 5 {
		t.Fatalf("rc = %d", rc)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if status, ending := arm.Ending(); ending {
		t.Errorf("a signal in the teardown marked the launch ending %d", status)
	}
	if len(*notices) != 0 {
		t.Errorf("the teardown said %q", *notices)
	}
}

// A SIGNAL IGNORED AT THE INSTALL STAYS IGNORED (nohup's SIGHUP): notifying for it would undo it.
// In a child process of its own, because signal.Ignore removes EVERY channel's registration for the
// signal process-wide, so run here it would silence the launch arms' handler for the tests after it.
func TestTheArmLeavesAnIgnoredSignalIgnored(t *testing.T) {
	if os.Getenv(ignoredArmHelperEnv) == "1" {
		signal.Ignore(syscall.SIGHUP)
		disarm := NewMacosUserArm().install(nil, nil)
		fmt.Printf("ignored=%v\n", signal.Ignored(syscall.SIGHUP))
		disarm()
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTheArmLeavesAnIgnoredSignalIgnored$")
	cmd.Env = append(os.Environ(), ignoredArmHelperEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the helper failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ignored=true") {
		t.Errorf("installing the arm un-ignored SIGHUP:\n%s", out)
	}
}

// ignoredArmHelperEnv runs TestTheArmLeavesAnIgnoredSignalIgnored's child half.
const ignoredArmHelperEnv = "YOLO_TEST_MACOS_USER_ARM_IGNORED_HELPER"

// The session-start hooks run once, in order, and only through AgentStarting.
func TestTheArmRunsItsSessionStartHooksOnce(t *testing.T) {
	arm := NewMacosUserArm()
	var got []int
	arm.OnAgentStart(func() { got = append(got, 1) })
	arm.OnAgentStart(func() { got = append(got, 2) })
	arm.AgentStarting()
	arm.AgentStarting()
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Errorf("hooks ran as %v, want [1 2] once", got)
	}
}

// A TERM during child start is serialized with foreground publication. The explicit TryLock witness
// is the regression oracle: it fails deterministically if startForeground stops holding arm.mu while
// invoking start, regardless of when the signal-delivery goroutine happens to run.
func TestSignalDuringForegroundStartIsForwardedAfterPublication(t *testing.T) {
	nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGTERM)
	arm, _ := installArm(t, nil)
	marks := t.TempDir()
	seen, ready := filepath.Join(marks, "seen"), filepath.Join(marks, "ready")
	cmd := exec.Command("sh", "-c", `trap 'echo TERM >> "$1"; exit 3' TERM; : > "$2"; `+
		`i=0; while [ $i -lt 40 ]; do sleep 0.05; i=$((i+1)); done; exit 9`, "sh", seen, ready)
	started, publish := make(chan struct{}), make(chan struct{}, 1)
	var startHeldArmLock bool
	type result struct {
		done func()
		err  error
	}
	startedResult := make(chan result, 1)
	workerDone := make(chan struct{})
	go func() {
		done, err := arm.startForeground(func() (*os.Process, error) {
			startErr := cmd.Start()
			startHeldArmLock = !arm.mu.TryLock()
			if !startHeldArmLock {
				arm.mu.Unlock()
			}
			close(started)
			if startErr != nil {
				return nil, startErr
			}
			<-publish
			return cmd.Process, nil
		})
		startedResult <- result{done: done, err: err}
		close(workerDone)
	}()
	t.Cleanup(func() {
		select {
		case publish <- struct{}{}:
		default:
		}
		<-workerDone
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	<-started
	if cmd.Process == nil {
		if got := <-startedResult; got.err != nil {
			t.Fatal(got.err)
		}
		t.Fatal("startForeground returned without starting the child")
	}
	if !startHeldArmLock {
		t.Error("child start callback did not hold arm.mu; serialization was removed")
	}
	awaitFile(t, ready)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	publish <- struct{}{}
	got := <-startedResult
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.done()
	if status := awaitEnding(t, arm); status != 143 {
		t.Errorf("Ending = %d, want 143", status)
	}
	_ = cmd.Wait()
	if got := readSeen(marks); got != "TERM" {
		t.Errorf("foreground child saw %q, want TERM delivered after atomic publication", got)
	}
}

// TestTheBackendsForegroundChildGetsTheForwardedSignal exercises the actual RealDeps.Run caller:
// TERM reaches a published setup child, teardown work is allowed to finish, and after disarm no child
// is signaled. Each asynchronous invocation owns its stop-and-join cleanup before its temp directory.
func TestTheBackendsForegroundChildGetsTheForwardedSignal(t *testing.T) {
	nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGTERM)
	run := macosuser.RealDeps(nil, nil, false).Run
	script := func(marks, stop string) []string {
		return []string{"sh", "-c", `trap 'echo TERM >> "$1"; exit 3' TERM; : > "$2"; ` +
			`i=0; while [ $i -lt 40 ] && [ ! -e "$3" ]; do sleep 0.05; i=$((i+1)); done; exit 9`,
			"sh", filepath.Join(marks, "seen"), filepath.Join(marks, "ready"), stop}
	}

	arm := NewMacosUserArm()
	disarm := arm.install(nil, nil)
	t.Cleanup(disarm)
	marks := t.TempDir()
	stop := filepath.Join(marks, "stop")
	fixture := startBackendRunFixture(t, run, script(marks, stop), stop)
	awaitFile(t, filepath.Join(marks, "ready"))
	awaitForeground(t, arm)
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if rc := fixture.wait(); rc != 3 || readSeen(marks) != "TERM" {
		t.Errorf("the backend's child returned %d and saw %q; want the forwarded TERM (3)", rc, readSeen(marks))
	}
	teardown := t.TempDir()
	stop = filepath.Join(teardown, "stop")
	fixture = startBackendRunFixture(t, run, script(teardown, stop), stop)
	awaitFile(t, filepath.Join(teardown, "ready"))
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if rc := fixture.wait(); rc != 9 || readSeen(teardown) != "" {
		t.Errorf("a teardown child returned %d and saw %q; want it to finish without forwarding (9)",
			rc, readSeen(teardown))
	}
	disarm()
	after := t.TempDir()
	stop = filepath.Join(after, "stop")
	fixture = startBackendRunFixture(t, run, script(after, stop), stop)
	awaitFile(t, filepath.Join(after, "ready"))
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if rc := fixture.wait(); rc != 9 || readSeen(after) != "" {
		t.Errorf("after the disarm the backend's child returned %d and saw %q; want nothing forwarded (9)",
			rc, readSeen(after))
	}
}

// THE CALL SITES, which no unit test can drive to a session: Run installs the arm after the
// config-change prompt and before the first host service, defers its disarm before the timing
// report's defer (so the disarm runs after it), and asks the arm before the dispatch. Pinned in
// source order; deleting any of them fails here.
func TestRunInstallsTheMacosUserArmAndAsksItBeforeTheDispatch(t *testing.T) {
	run := funcDecl(t, "run.go", "Run")
	pos := map[string]token.Pos{}
	ast.Inspect(run, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name := skelCallee(call); name != "" {
				if _, seen := pos[name]; !seen {
					pos[name] = call.Pos()
				}
			}
		}
		return true
	})
	order := []string{"checkConfigChanges", "armMacosUser", "emitTimingReport", "startLoopholesDisclosed",
		"Ending", "MacosUserRun"}
	last := token.NoPos
	for _, name := range order {
		p, ok := pos[name]
		if !ok {
			t.Fatalf("Run no longer calls %s; re-anchor this pin, do not delete it", name)
		}
		if p < last {
			t.Errorf("Run calls %s out of order; want %v", name, order)
		}
		last = p
	}
	// The arm's Ending must gate a return: an asked-and-ignored answer is no boundary.
	gated := false
	ast.Inspect(run, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok && ifs.Init != nil {
			ast.Inspect(ifs.Init, func(c ast.Node) bool {
				if call, ok := c.(*ast.CallExpr); ok && skelCallee(call) == "Ending" {
					for _, stmt := range ifs.Body.List {
						if _, ok := stmt.(*ast.ReturnStmt); ok {
							gated = true
						}
					}
				}
				return true
			})
		}
		return true
	})
	if !gated {
		t.Error("Run asks the arm's Ending without returning on it")
	}
}

// A LAUNCH WHOSE CALLER MADE NO ARM IS NOT ARMED. The capture act's launch (internal/cli's
// capturehost.go, which the host floor's capture reaches too) hands Run a MacosUserRun that never
// asks the arm's Ending, so an arm Run installed for it would take the signal, print a notice naming
// `yolo`, stop this process's nix for good, and then let the handler go on to its next step and
// return success. Its signals keep the action they had before the arm existed.
func TestALaunchWhoseCallerMadeNoArmIsNotArmed(t *testing.T) {
	nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGINT)
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	if o.MacosUserArm != nil {
		t.Fatal("the fixture already carries an arm; this test needs a caller that made none")
	}
	steps := 0
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay,
		macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
		steps++
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Error(err)
		}
		time.Sleep(200 * time.Millisecond)
		steps++
		return 0
	}
	if rc := Run(*o); rc != 0 || steps != 2 {
		t.Fatalf("Run() = %d after %d steps, want the handler's 0 after both\n%s", rc, steps, stderr.String())
	}
	if out := stdout.String() + stderr.String(); strings.Contains(out, "ending this macos-user launch") {
		t.Errorf("a launch whose caller made no arm was armed, and its notice names a `yolo` the user never ran:\n%s", out)
	}
	if nixchildren.Stopped() {
		t.Error("a signal to a launch whose caller made no arm stopped this process's nix")
	}
}
