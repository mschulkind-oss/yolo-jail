package run

// macosuserarm_test.go pins the macos-user arm (macosuserarm.go) phase by phase, with real signals
// sent to this test process, and its two call sites in Run: installed after the config-change
// prompt and before the first host service, and asked before the dispatch.

import (
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

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
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
	done := arm.watchForeground(child.Process)
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
	defer arm.watchForeground(child.Process)()
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
	arm.watchForeground(later.Process)
	arm.mu.Lock()
	registered := arm.foreground == later.Process
	arm.mu.Unlock()
	if registered {
		t.Error("a child started after the ending became the forward target")
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

// THE FOREGROUND WATCH IS PUBLISHED while the arm is installed and withdrawn at its disarm: a child
// the backend runs through its real Run (macosuser's runReal) is the one a SIGTERM to yolo alone is
// forwarded to during setup, and after the disarm none is.
func TestTheBackendsForegroundChildGetsTheForwardedSignal(t *testing.T) {
	nixchildren.Isolate(t)
	keepAlive(t, syscall.SIGTERM)
	run := macosuser.RealDeps(nil, nil, false).Run
	script := func(marks string) []string {
		return []string{"sh", "-c", `trap 'echo TERM >> "$1"; exit 3' TERM; : > "$2"; ` +
			`i=0; while [ $i -lt 40 ]; do sleep 0.05; i=$((i+1)); done; exit 9`,
			"sh", filepath.Join(marks, "seen"), filepath.Join(marks, "ready")}
	}
	signalWhenReady := func(marks string) {
		go func() {
			awaitFile(t, filepath.Join(marks, "ready"))
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}()
	}

	arm := NewMacosUserArm()
	disarm := arm.install(nil, nil)
	marks := t.TempDir()
	signalWhenReady(marks)
	if rc := run(script(marks)); rc != 3 || readSeen(marks) != "TERM" {
		t.Errorf("the backend's child returned %d and saw %q; want the forwarded TERM (3)", rc, readSeen(marks))
	}
	disarm()

	after := t.TempDir()
	signalWhenReady(after)
	if rc := run(script(after)); rc != 9 || readSeen(after) != "" {
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
