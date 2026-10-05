package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// lockedBuffer is a bytes.Buffer safe to read while a relay goroutine writes it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// TestTheRelayPrintsTheBootAndSwallowsTheReadyLine: every line before the ready line reaches
// the terminal whole, the ready line is never printed and fires onReady once, and what follows
// it is a plain copy — even when the writes split lines anywhere.
func TestTheRelayPrintsTheBootAndSwallowsTheReadyLine(t *testing.T) {
	var out bytes.Buffer
	readies := 0
	r := &readyRelay{w: &out, onReady: func() { readies++ }}
	stream := "boot line one\nboot line two\n" + entrypoint.BootReadyLine + "\nafter ready\npartial"
	for i := 0; i < len(stream); i += 7 {
		end := min(i+7, len(stream))
		if _, err := r.Write([]byte(stream[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	r.flush()
	if got, want := out.String(), "boot line one\nboot line two\nafter ready\npartial"; got != want {
		t.Errorf("relayed %q, want %q", got, want)
	}
	if readies != 1 {
		t.Errorf("onReady ran %d times, want once", readies)
	}
}

// TestARelayThatNeverSeesReadyFlushesItsLastLine: a boot that refused ends its stream without
// the ready line, and its last partial line is still printed once the stream ends.
func TestARelayThatNeverSeesReadyFlushesItsLastLine(t *testing.T) {
	var out bytes.Buffer
	r := &readyRelay{w: &out, onReady: func() { t.Error("onReady fired for a boot that never finished") }}
	_, _ = r.Write([]byte("refusing to start the jail\nno newline at the end"))
	if out.String() != "refusing to start the jail\n" {
		t.Errorf("before the end, relayed %q; want only the whole line", out.String())
	}
	r.flush()
	if !strings.HasSuffix(out.String(), "no newline at the end") {
		t.Errorf("the partial last line was lost: %q", out.String())
	}
}

// TestTheMainProcessClientRunsDetachedFromTheTerminalsSignals drives startJailMain with a
// stand-in main process: its boot lines are relayed, the ready line wakes awaitReady and is not
// printed, stdout goes where the launcher's does, the client leads a process group of its own
// (so the terminal's Ctrl-C never reaches it), and its exit status and exit mark arrive once it
// is gone.
func TestTheMainProcessClientRunsDetachedFromTheTerminalsSignals(t *testing.T) {
	testsupport.UnsetLCAll(t) // the child shell's output is compared exactly
	held := heldDir(t)
	release := held + "/release"
	script := recordPID(held) + `echo "boot says hello" >&2; echo "stdout line"; printf '%s\n' "$READY" >&2; ` +
		holdUntil(release) + `; exit 3`
	t.Setenv("READY", entrypoint.BootReadyLine)
	var stdout, stderr lockedBuffer
	exited := make(chan struct{})
	m, err := startJailMain([]string{"sh", "-c", script}, &stdout, &stderr, func() { close(exited) })
	if err != nil {
		t.Fatal(err)
	}
	if !m.awaitReady() {
		t.Fatalf("awaitReady reported a boot that never finished; stderr: %q", stderr.String())
	}
	if pgid, err := syscall.Getpgid(m.cmd.Process.Pid); err != nil || pgid != m.cmd.Process.Pid {
		t.Errorf("the client's process group is %d (err %v), want its own (%d)", pgid, err, m.cmd.Process.Pid)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-m.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("the client never exited")
	}
	select {
	case <-exited:
	default:
		t.Error("onExit did not run before exited closed")
	}
	if m.exitCode != 3 {
		t.Errorf("exit code %d, want the main process's 3", m.exitCode)
	}
	if got := stderr.String(); got != "boot says hello\n" {
		t.Errorf("stderr relayed %q, want the boot line and not the ready line", got)
	}
	if got := stdout.String(); got != "stdout line\n" {
		t.Errorf("stdout %q", got)
	}
}

// TestAMainProcessThatExitsBeforeReadyIsARefusal: awaitReady is false, the status is the
// client's, and the refusal's lines are all printed by the time exited closes, a last line with
// no newline included (the relay's flush, which startJailMain must call at the stream's end).
func TestAMainProcessThatExitsBeforeReadyIsARefusal(t *testing.T) {
	var stderr lockedBuffer
	m, err := startJailMain([]string{"sh", "-c", `echo "BOOT REFUSED here" >&2; printf 'and its last words' >&2; exit 1`},
		&bytes.Buffer{}, &stderr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.awaitReady() {
		t.Fatal("awaitReady reported ready for a boot that exited without its ready line")
	}
	<-m.exited
	if m.exitCode != 1 {
		t.Errorf("exit code %d, want 1", m.exitCode)
	}
	if !strings.Contains(stderr.String(), "BOOT REFUSED here") {
		t.Errorf("the refusal's line was not relayed before exited: %q", stderr.String())
	}
	if !strings.HasSuffix(stderr.String(), "and its last words") {
		t.Errorf("the refusal's partial last line was lost: %q", stderr.String())
	}
}

// TestTheFirstSessionIsAnExecOfTheFirstSessionForm: the attach's argv shape, -t only on a
// terminal, the detach sequence off on podman (JL-D27) and on no other runtime, the session's own
// id for its hangup (JL-D4), the entrypoint by absolute path, and the two-argument first-session
// form.
func TestTheFirstSessionIsAnExecOfTheFirstSessionForm(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	got := o.firstSessionExecCmd("podman", "yolo-ws-1", "the command", "abcd")
	want := []string{"podman", "exec", "-i", "--detach-keys=", "-e", entrypoint.SessionIDEnv + "=abcd",
		"yolo-ws-1", JailEntrypointPath, entrypoint.FirstSessionArg, "the command"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("no tty: %q, want %q", got, want)
	}
	o.IsTTYStdout = func() bool { return true }
	got = o.firstSessionExecCmd("container", "yolo-ws-1", "c", "abcd")
	if len(got) < 4 || got[0] != "container" || got[2] != "-i" || got[3] != "-t" {
		t.Errorf("tty: %q, want -i -t", got)
	}
	for _, a := range got {
		if strings.HasPrefix(a, "--detach-keys") {
			t.Errorf("Apple Container got podman's detach-keys flag, unmeasured there: %q", got)
		}
	}
}

// fakeSessionHandle records what the arm asks of the first session's run, in order.
type fakeSessionHandle struct {
	mu    sync.Mutex
	calls []string
}

func (h *fakeSessionHandle) Terminate() bool { h.record("terminate"); return true }
func (h *fakeSessionHandle) Kill()           { h.record("kill") }

func (h *fakeSessionHandle) record(c string) {
	h.mu.Lock()
	h.calls = append(h.calls, c)
	h.mu.Unlock()
}

func (h *fakeSessionHandle) seen() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.calls, ",")
}

// armAndHangUp installs an arm around onTerminate, lets setup attach to it, sends this process
// a SIGHUP, and returns the exit code the arm chose.
func armAndHangUp(t *testing.T, onTerminate func(), setup func(*launchSignalArm)) (*launchSignalArm, int) {
	t.Helper()
	isolateNixSet(t)
	codes := make(chan int, 1)
	arm := armLaunchSignalsWith(onTerminate, func(code int) { codes <- code })
	// An arm that fired never disarms (its exit would have ended the process), so it leaves the
	// process's arms here instead, and no later test's signal is routed to it (armstack.go).
	t.Cleanup(func() { popLaunchArm(arm) })
	if setup != nil {
		setup(arm)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-codes:
		return arm, code
	case <-time.After(5 * time.Second):
		t.Fatal("the arm did not exit on SIGHUP")
		return arm, 0
	}
}

// TestTheLaunchSignalArmTearsDownThroughTheFirstSessionsHandle drives the arm with a real
// SIGHUP while a first session's run is attached: the terminal goes back first (the handle's
// Terminate), then the launch's teardown, then the exec client (Kill), then the exit with
// 128+SIGHUP; the arm leaves SIGTTOU ignored, so a teardown from the background is not stopped
// by its own writes; and afterwards neither detach nor disarm lets the launch's goroutine run a
// second teardown.
func TestTheLaunchSignalArmTearsDownThroughTheFirstSessionsHandle(t *testing.T) {
	h := &fakeSessionHandle{}
	arm, code := armAndHangUp(t, func() { h.record("onTerminate") }, func(a *launchSignalArm) { a.attach(h) })
	if code != 128+int(syscall.SIGHUP) {
		t.Errorf("exit %d, want %d", code, 128+int(syscall.SIGHUP))
	}
	if got := h.seen(); got != "terminate,onTerminate,kill" {
		t.Errorf("the arm ran %q, want the terminal back, then the teardown, then the client killed", got)
	}
	if !signal.Ignored(syscall.SIGTTOU) {
		t.Error("the arm's teardown left SIGTTOU at its default, which stops a teardown run from the background")
	}
	if arm.detach() {
		t.Error("detach after the arm began the teardown must report it, so the launch does not race it")
	}
	if arm.disarm() {
		t.Error("disarm after the arm began the teardown must report it, so the launch does not run it twice")
	}
}

// TestTheLaunchSignalArmStaysArmedOnceTheFirstSessionReturns: a detached arm still runs its
// teardown on a signal, until it is disarmed, and it leaves the detached session's client alone.
func TestTheLaunchSignalArmStaysArmedOnceTheFirstSessionReturns(t *testing.T) {
	h := &fakeSessionHandle{}
	tore := false
	_, code := armAndHangUp(t, func() { tore = true }, func(a *launchSignalArm) {
		a.attach(h)
		if !a.detach() {
			t.Error("detach of an arm that never fired reported a teardown under way")
		}
	})
	if code != 128+int(syscall.SIGHUP) || !tore {
		t.Errorf("after the first session returned, a SIGHUP exited %d with teardown=%v; want %d with it",
			code, tore, 128+int(syscall.SIGHUP))
	}
	if got := h.seen(); got != "" {
		t.Errorf("the arm acted on a detached session's client: %q", got)
	}
}

// TestAFirstSessionThatAttachesDuringATeardownIsTerminated: a signal that lands after the main
// process's boot but before the first session's proxy hands over its handle finds no handle;
// the handle that arrives mid-teardown is terminated at once (its raw terminal put back, its
// return blocked) and its client killed at the end.
func TestAFirstSessionThatAttachesDuringATeardownIsTerminated(t *testing.T) {
	h := &fakeSessionHandle{}
	// Handed from the setup to the arm's own goroutine, which the signal alone orders after it.
	var mu sync.Mutex
	var arm *launchSignalArm
	_, code := armAndHangUp(t, func() {
		mu.Lock()
		a := arm
		mu.Unlock()
		a.attach(h)
		h.record("onTerminate done")
	}, func(a *launchSignalArm) { mu.Lock(); arm = a; mu.Unlock() })
	if code != 128+int(syscall.SIGHUP) {
		t.Errorf("exit %d, want %d", code, 128+int(syscall.SIGHUP))
	}
	if got := h.seen(); got != "terminate,onTerminate done,kill" {
		t.Errorf("a handle attached mid-teardown saw %q, want it terminated on arrival and killed at the end", got)
	}
}

// TestADisarmedLaunchSignalArmDoesNothing: once the main process's client has exited and the
// launch disarms, the normal teardown owns the rest and a signal no longer reaches the arm.
func TestADisarmedLaunchSignalArmDoesNothing(t *testing.T) {
	quiet := armLaunchSignalsWith(func() { t.Error("a disarmed arm ran the teardown") },
		func(int) { t.Error("a disarmed arm exited") })
	if !quiet.disarm() {
		t.Error("disarm of an arm that never fired reported a teardown under way")
	}
	// Something must still catch the signal for the test binary to survive it.
	catch := make(chan os.Signal, 1)
	signal.Notify(catch, syscall.SIGHUP)
	defer signal.Stop(catch)
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	<-catch
	time.Sleep(100 * time.Millisecond)
}

// TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec pins runContainer's call sites,
// which no unit test can drive (they start a real container), in the keeper's shape (step 3 of
// docs/design/jail-lifetime-last-session-wins.md §7): the host services are disclosed and planned
// before anything spawns; the main process's argv ends in the hold form carrying this launch's
// stage; the first session is named in the jail; the plan and the keeper's line come before the
// session lock, and the lock before the keeper's spawn (JL-D16); one arm is installed after the
// spawn and retargeted at ready to the session's own teardown, whose hangup ends that session alone
// (JL-D4); the first session is the first-session run, handed that arm; and its quit is endSession's.
// runContainer itself NEVER stops the jail and never runs the teardown chain: those are the
// keeper's. The deferred session-lock release at Run's top is pinned too. Deleting any of them
// fails here.
func TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	pos := map[string]token.Pos{}
	firstPos := func(name string, p token.Pos) {
		if _, seen := pos[name]; !seen {
			pos[name] = p
		}
	}
	ast.Inspect(fd, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // the relay's event callbacks are pinned by their own tests
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := skelCallee(call)
		switch name {
		case "discloseLoopholes", "plannedLoopholeNames", "provisionStage", "newSessionID",
			"firstSessionExecCmd", "sessionCmd", "keeperPlanFor", "keeperLine", "holdSessionLock",
			"startKeeper", "armLaunchSignalsWith", "relay", "retarget", "runArmedSession",
			"detach", "endSession":
			firstPos(name, call.Pos())
		case "stopJail", "teardownAfterExit", "startLoopholes", "startLoopholesDisclosed",
			"startPlannedLoopholes", "startPortForwards", "startJailMain", "startJailMainWithEnv":
			t.Errorf("runContainer calls %s: the keeper owns the jail's host services and its end, "+
				"and the first session's launcher never stops the jail (JL-D4)", name)
		case "runWithProxy":
			t.Error("the fresh launch runs a proxy with an arm of its own; its first session must be " +
				"runArmedSession's, under the launch's one arm")
		}
		if name == "runArmedSession" && len(call.Args) > 1 {
			if skelIdent(call.Args[0]) != "firstExec" {
				t.Errorf("the fresh launch's first session runs %v, want the first session's exec (firstExec)", call.Args[0])
			}
			if skelIdent(call.Args[1]) != "arm" {
				t.Errorf("the first session is handed %v, want the launch's signal arm", call.Args[1])
			}
		}
		if name == "retarget" && len(call.Args) == 1 {
			if c, ok := call.Args[0].(*ast.CallExpr); !ok || skelCallee(c) != "attachTeardown" {
				t.Errorf("the arm is retargeted at ready to %v, want the session's own teardown "+
					"(attachTeardown), which hangs that session up and never stops the jail", call.Args[0])
			}
		}
		if name == "endSession" && len(call.Args) == 6 {
			if b, ok := call.Args[5].(*ast.Ident); !ok || b.Name != "true" {
				t.Errorf("the first session's quit is endSession's with first=%v, want true", call.Args[5])
			}
		}
		if name == "append" && len(call.Args) == 3 {
			if sel, ok := call.Args[1].(*ast.SelectorExpr); ok && sel.Sel.Name == "HoldMainArg" {
				firstPos("append HoldMainArg", call.Pos())
			}
		}
		return true
	})
	order := []string{"discloseLoopholes", "plannedLoopholeNames", "append HoldMainArg",
		"provisionStage", "newSessionID", "firstSessionExecCmd", "sessionCmd", "keeperPlanFor",
		"keeperLine", "holdSessionLock", "startKeeper", "armLaunchSignalsWith", "relay", "retarget",
		"runArmedSession", "detach", "endSession"}
	last := token.NoPos
	for _, name := range order {
		p, ok := pos[name]
		if !ok {
			t.Errorf("runContainer no longer calls %s", name)
			continue
		}
		if p < last {
			t.Errorf("%s is out of order in runContainer", name)
		}
		last = p
	}

	// Run's deferred release of the session lock: auto-capture runs this pipeline in-process,
	// so without it the process would count itself in the jail for its whole life.
	deferredRelease := false
	ast.Inspect(funcDecl(t, "run.go", "Run"), func(n ast.Node) bool {
		if d, ok := n.(*ast.DeferStmt); ok && skelCallee(d.Call) == "releaseSessionLock" {
			deferredRelease = true
		}
		return true
	})
	if !deferredRelease {
		t.Error("Run no longer defers releaseSessionLock")
	}
}

// TestNoSessionsArmStopsTheJail is JL-D4 for every session, the first included: a session's signal
// arm ends that session and never stops the jail, which is its other sessions' too. The fresh
// launch's arm runs keeperPreReadyTeardown until ready, which ends the launch alone (its keeper
// unwinds on the lifeline), and attachTeardown from then on, as an attach's does. Deleting a
// stopJail ban, or giving either teardown a stopJail, fails here.
func TestNoSessionsArmStopsTheJail(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"keeperspawn.go", "keeperPreReadyTeardown"},
		{"sessionhangup.go", "attachTeardown"},
		{"sessionhangup.go", "attachSignalArm"},
		// The launch guard's, before the keeper exists (launchguard.go): it stops nothing either.
		{"launchguard.go", "launchGuardTeardown"},
		{"launchguard.go", "abandonLaunch"},
		{"launchguard.go", "discardUnspawned"},
	} {
		calls := callsIn(funcDecl(t, tc.file, tc.fn))
		for _, forbidden := range []string{"stopJail", "teardownAfterExit", "stopLoopholes"} {
			if calls[forbidden] {
				t.Errorf("%s calls %s: a session's arm ends only its own session (JL-D4)", tc.fn, forbidden)
			}
		}
	}
	if !callsIn(funcDecl(t, "sessionhangup.go", "attachTeardown"))["hangUpAttachSession"] {
		t.Error("attachTeardown no longer hangs up its session's own processes (OQ-JL8)")
	}
	if !callsIn(funcDecl(t, "keeperspawn.go", "keeperPreReadyTeardown"))["closeLifeline"] {
		t.Error("keeperPreReadyTeardown no longer closes the lifeline, which is what has the keeper unwind")
	}
}

// TestTheKeeperKillsAMainProcessClientThatOutlivedItsContainer pins the keeper's kill of the main
// process's `<rt> run` client, TARGETED at its pid, once its chain is done: that client leads a
// process group of its own, the keeper's teardown never waits for it (so Window A stays off every
// terminal, JL-D17), and without the kill it would be left running with nobody waiting on it.
func TestTheKeeperKillsAMainProcessClientThatOutlivedItsContainer(t *testing.T) {
	killsIt := false
	ast.Inspect(funcDecl(t, "keeper.go", "finish"), func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || skelCallee(call) != "Kill" {
			return true
		}
		// k.jm.cmd.Process.Kill()
		if proc, ok := call.Fun.(*ast.SelectorExpr).X.(*ast.SelectorExpr); ok && proc.Sel.Name == "Process" {
			if cmd, ok := proc.X.(*ast.SelectorExpr); ok && cmd.Sel.Name == "cmd" {
				killsIt = true
			}
		}
		return true
	})
	if !killsIt {
		t.Error("the keeper's finish no longer kills the main process's client (k.jm.cmd.Process.Kill)")
	}
}
