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
	release := t.TempDir() + "/release"
	script := `echo "boot says hello" >&2; echo "stdout line"; printf '%s\n' "$READY" >&2; ` +
		`while [ ! -e "$RELEASE" ]; do sleep 0.02; done; exit 3`
	t.Setenv("READY", entrypoint.BootReadyLine)
	t.Setenv("RELEASE", release)
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
// terminal, the detach sequence off on podman (JL-D27) and on no other runtime, the entrypoint
// by absolute path, and the two-argument first-session form.
func TestTheFirstSessionIsAnExecOfTheFirstSessionForm(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	got := o.firstSessionExecCmd("podman", "yolo-ws-1", "the command")
	want := []string{"podman", "exec", "-i", "--detach-keys=", "yolo-ws-1", JailEntrypointPath,
		entrypoint.FirstSessionArg, "the command"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("no tty: %q, want %q", got, want)
	}
	o.IsTTYStdout = func() bool { return true }
	got = o.firstSessionExecCmd("container", "yolo-ws-1", "c")
	if len(got) < 4 || got[0] != "container" || got[2] != "-i" || got[3] != "-t" {
		t.Errorf("tty: %q, want -i -t", got)
	}
	for _, a := range got {
		if strings.HasPrefix(a, "--detach-keys") {
			t.Errorf("Apple Container got podman's detach-keys flag, unmeasured there: %q", got)
		}
	}
}

// TestTheJailEndsWithTheFirstSessionEvenWhenTheHoldDoesNotFollow: a main process that exits
// by itself is simply waited for; one that does not within the grace is stopped, since this
// launch still ends the jail with its own session.
func TestTheJailEndsWithTheFirstSessionEvenWhenTheHoldDoesNotFollow(t *testing.T) {
	saved := jailMainEndGrace
	jailMainEndGrace = 50 * time.Millisecond
	t.Cleanup(func() { jailMainEndGrace = saved })
	home := t.TempDir()
	t.Setenv("HOME", home)

	o := goldenOptions("/ws", home)
	var stops []string
	m := &jailMain{exited: make(chan struct{})}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		switch {
		case len(argv) > 1 && argv[1] == "ps":
			return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
		case len(argv) > 1 && argv[1] == "stop":
			stops = append(stops, argv[len(argv)-1])
			close(m.exited)
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: false}
	}
	if !o.awaitJailMainEnd(m, "yolo-ws-1", "podman") {
		t.Error("awaitJailMainEnd stopped the jail and did not say so")
	}
	if len(stops) != 1 || stops[0] != "yolo-ws-1" {
		t.Errorf("stops %v, want one stop of the jail the hold did not end", stops)
	}

	stops = nil
	followed := &jailMain{exited: make(chan struct{})}
	close(followed.exited)
	if o.awaitJailMainEnd(followed, "yolo-ws-1", "podman") {
		t.Error("awaitJailMainEnd said it stopped a jail whose main process followed its session out")
	}
	if len(stops) != 0 {
		t.Errorf("a main process that followed its first session out was stopped: %v", stops)
	}
}

// TestAJailStoppedFromOutsideReturnsWhatItDidBefore: the kernel SIGKILLs a session whose jail's
// main process ended, so `yolo stop` leaves the first session's exec at 137, or fails the exec
// outright when it lands as the exec starts; over a hold that a SIGTERM ended, which this
// launcher did not send, the launch returns 143 as it did when the session was the main
// process, and the OOM hint keyed on 137 stays quiet. Every other pairing keeps the session's
// own status.
func TestAJailStoppedFromOutsideReturnsWhatItDidBefore(t *testing.T) {
	const killed, termed = 128 + int(syscall.SIGKILL), 128 + int(syscall.SIGTERM)
	for _, tc := range []struct {
		name            string
		session, main   int
		launcherStopped bool
		want            int
	}{
		{"stopped from outside", killed, termed, false, termed},
		{"stopped as the exec started: the runtime's own failure", 255, termed, false, termed},
		{"stopped from outside after the command succeeded", 0, termed, false, 0},
		{"killed while its jail ran (the OOM killer): the hold followed it out", killed, 0, false, killed},
		{"the launcher stopped it after its grace", killed, termed, true, killed},
		{"the command's own status", 7, 0, false, 7},
		{"a command that died of SIGTERM itself", termed, 0, false, termed},
	} {
		if got := firstSessionStatus(tc.session, tc.main, tc.launcherStopped); got != tc.want {
			t.Errorf("%s: firstSessionStatus(%d, %d, %v) = %d, want %d",
				tc.name, tc.session, tc.main, tc.launcherStopped, got, tc.want)
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
	codes := make(chan int, 1)
	arm := armLaunchSignalsWith(onTerminate, func(code int) { codes <- code })
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

// TestTheLaunchSignalArmStaysArmedOnceTheFirstSessionReturns is the stretch the first
// session's return leaves: the main process's client lingering (Window A) or the launcher's
// grace before it stops a hold that did not follow. The arm must still run the teardown there,
// and it must leave the detached session's client alone.
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
	var arm *launchSignalArm
	_, code := armAndHangUp(t, func() {
		arm.attach(h)
		h.record("onTerminate done")
	}, func(a *launchSignalArm) { arm = a })
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
// which no unit test can drive (they start a real container): the main process's argv ends in
// the hold form carrying this launch's stage; the session lock and the signal arm are taken
// before the main process starts; the first session is the first-session run, handed the arm,
// and its command is sessionCmd's; the arm stays armed from the first session's return through
// the main process's end, and is disarmed after it and before the teardown; the first session's
// end is recorded for the jail's other sessions before the main process's end is waited for, from
// the session's status, and an end that status leaves undecided is settled from the main
// process's after it (stopreason.go); the launch's status is firstSessionStatus's. The deferred session-lock release
// at Run's top is pinned too. Deleting any of them fails here.
func TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	pos := map[string]token.Pos{}
	var disarms []token.Pos
	firstPos := func(name string, p token.Pos) {
		if _, seen := pos[name]; !seen {
			pos[name] = p
		}
	}
	ast.Inspect(fd, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // onStarted and onTerminate are pinned by their own tests
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := skelCallee(call)
		switch name {
		case "provisionStage", "sessionCmd", "firstSessionExecCmd", "holdSessionLock",
			"armLaunchSignals", "startJailMain", "awaitReady", "runArmedSession", "detach",
			"recordFirstSessionEnd", "awaitJailMainEnd", "settleFirstSessionEnd", "firstSessionStatus",
			"teardownAfterExit":
			firstPos(name, call.Pos())
		case "disarm":
			disarms = append(disarms, call.Pos())
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
		if name == "append" && len(call.Args) == 3 {
			if sel, ok := call.Args[1].(*ast.SelectorExpr); ok && sel.Sel.Name == "HoldMainArg" {
				firstPos("append HoldMainArg", call.Pos())
			}
		}
		// The first session's end is judged on the session's own status, and an undecided one on
		// the main process's: a jail ended from outside is not the first session's end.
		if name == "recordFirstSessionEnd" && len(call.Args) == 3 && skelIdent(call.Args[2]) != "rc" {
			t.Errorf("the first session's end is recorded from %v, want the session's status (rc)", call.Args[2])
		}
		if name == "settleFirstSessionEnd" && len(call.Args) == 3 {
			if sel, ok := call.Args[2].(*ast.SelectorExpr); !ok || sel.Sel.Name != "exitCode" {
				t.Errorf("an undecided first session's end is settled from %v, want the main process's "+
					"status (jm.exitCode)", call.Args[2])
			}
		}
		return true
	})
	order := []string{"append HoldMainArg", "provisionStage", "firstSessionExecCmd", "sessionCmd",
		"holdSessionLock", "armLaunchSignals", "startJailMain", "awaitReady", "runArmedSession",
		"detach", "recordFirstSessionEnd", "awaitJailMainEnd", "settleFirstSessionEnd",
		"firstSessionStatus", "teardownAfterExit"}
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
	// The arm is live from the first session's return through the main process's end: no
	// disarm between them, and one after it, before the normal teardown.
	disarmedAfterTheEnd := false
	for _, p := range disarms {
		if p > pos["runArmedSession"] && p < pos["awaitJailMainEnd"] {
			t.Error("runContainer disarms the signal arm between the first session's return and the " +
				"main process's end, where a hangup would then kill the launcher with no teardown")
		}
		if p > pos["awaitJailMainEnd"] && p < pos["teardownAfterExit"] {
			disarmedAfterTheEnd = true
		}
	}
	if !disarmedAfterTheEnd {
		t.Error("runContainer does not disarm the signal arm after the main process's end and before the teardown")
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

// TestTheTerminateArmKillsTheMainProcessClient pins onTerminate's kill of the main process's
// `<rt> run` client, TARGETED at its pid. That client leads a process group of its own, so no
// signal to the launcher's group reaches it, and without the kill an arm's teardown would
// leave it running with nobody waiting on it.
func TestTheTerminateArmKillsTheMainProcessClient(t *testing.T) {
	var onTerminate *ast.FuncLit
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if as, ok := n.(*ast.AssignStmt); ok && len(as.Lhs) == 1 && skelIdent(as.Lhs[0]) == "onTerminate" {
			if fl, ok := as.Rhs[0].(*ast.FuncLit); ok {
				onTerminate = fl
			}
		}
		return true
	})
	if onTerminate == nil {
		t.Fatal("runContainer's onTerminate closure moved; re-anchor this pin, do not delete it")
	}
	killsIt := false
	ast.Inspect(onTerminate, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || skelCallee(call) != "Kill" {
			return true
		}
		// jm.cmd.Process.Kill()
		if proc, ok := call.Fun.(*ast.SelectorExpr).X.(*ast.SelectorExpr); ok && proc.Sel.Name == "Process" {
			if cmd, ok := proc.X.(*ast.SelectorExpr); ok && cmd.Sel.Name == "cmd" && skelIdent(cmd.X) == "jm" {
				killsIt = true
			}
		}
		return true
	})
	if !killsIt {
		t.Error("onTerminate no longer kills the main process's client (jm.cmd.Process.Kill)")
	}
}
