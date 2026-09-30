package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
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
// client's, and the refusal's lines are all printed by the time exited closes.
func TestAMainProcessThatExitsBeforeReadyIsARefusal(t *testing.T) {
	var stderr lockedBuffer
	m, err := startJailMain([]string{"sh", "-c", `echo "BOOT REFUSED here" >&2; exit 1`},
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
}

// TestTheFirstSessionIsAnExecOfTheFirstSessionForm: the attach's argv shape, -t only on a
// terminal, the entrypoint by absolute path, and the two-argument first-session form.
func TestTheFirstSessionIsAnExecOfTheFirstSessionForm(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	got := o.firstSessionExecCmd("podman", "yolo-ws-1", "the command")
	want := []string{"podman", "exec", "-i", "yolo-ws-1", JailEntrypointPath, entrypoint.FirstSessionArg, "the command"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("no tty: %q, want %q", got, want)
	}
	o.IsTTYStdout = func() bool { return true }
	got = o.firstSessionExecCmd("container", "yolo-ws-1", "c")
	if len(got) < 4 || got[0] != "container" || got[2] != "-i" || got[3] != "-t" {
		t.Errorf("tty: %q, want -i -t", got)
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
	o.awaitJailMainEnd(m, "yolo-ws-1", "podman")
	if len(stops) != 1 || stops[0] != "yolo-ws-1" {
		t.Errorf("stops %v, want one stop of the jail the hold did not end", stops)
	}

	stops = nil
	followed := &jailMain{exited: make(chan struct{})}
	close(followed.exited)
	o.awaitJailMainEnd(followed, "yolo-ws-1", "podman")
	if len(stops) != 0 {
		t.Errorf("a main process that followed its first session out was stopped: %v", stops)
	}
}

// TestTheLaunchSignalArmTearsDownAndStepsAsideForTheProxy drives the arm with real signals to
// this process: armed, a SIGHUP runs the teardown and exits 128+1; handed off to a proxy that
// arms itself, it does nothing; and once it has begun, disarm says so, so the launch's own
// goroutine leaves the rest to it.
func TestTheLaunchSignalArmTearsDownAndStepsAsideForTheProxy(t *testing.T) {
	// Handed off: nothing runs, and disarm is clean.
	quiet := armLaunchSignalsWith(func() { t.Error("a handed-off arm ran the teardown") }, true,
		func(int) { t.Error("a handed-off arm exited") })
	quiet.handOff()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if !quiet.disarm() {
		t.Error("disarm of an arm that never fired reported a teardown under way")
	}

	// Armed: the teardown, then the exit, with 128+SIGHUP.
	tore := make(chan struct{})
	codes := make(chan int, 1)
	var armed *launchSignalArm
	armed = armLaunchSignalsWith(func() { close(tore) }, false, func(code int) { codes <- code })
	// A proxy that does not arm (no terminal) leaves this one armed after handOff.
	armed.handOff()
	if err := syscall.Kill(os.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-codes:
		if code != 128+int(syscall.SIGHUP) {
			t.Errorf("exit %d, want %d", code, 128+int(syscall.SIGHUP))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the armed arm did not exit on SIGHUP")
	}
	select {
	case <-tore:
	default:
		t.Error("the arm exited without running the teardown")
	}
	if armed.disarm() {
		t.Error("disarm after the arm began the teardown must report it, so the launch does not run it twice")
	}
}

// TestTheFreshLaunchRunsTheJailAsAHoldAndItsFirstSessionByExec pins runContainer's call sites,
// which no unit test can drive (they start a real container): the main process's argv ends in
// the hold form carrying this launch's stage; the session lock and the signal arm are taken
// before the main process starts; the first session is the proxy's child and its command is
// sessionCmd's; the jail's end is awaited before the teardown. Deleting any of them fails here.
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
			return false // onStarted and onTerminate are pinned by their own tests
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := skelCallee(call)
		switch name {
		case "provisionStage", "sessionCmd", "firstSessionExecCmd", "holdSessionLock",
			"armLaunchSignals", "startJailMain", "awaitReady", "handOff", "runWithProxy",
			"awaitJailMainEnd", "teardownAfterExit":
			firstPos(name, call.Pos())
		}
		if name == "runWithProxy" && len(call.Args) > 0 {
			if id, ok := call.Args[0].(*ast.Ident); !ok || id.Name != "firstExec" {
				t.Errorf("the fresh launch's proxy runs %v, want the first session's exec (firstExec)", call.Args[0])
			}
		}
		if name == "append" && len(call.Args) == 3 {
			if sel, ok := call.Args[1].(*ast.SelectorExpr); ok && sel.Sel.Name == "HoldMainArg" {
				firstPos("append HoldMainArg", call.Pos())
			}
		}
		return true
	})
	order := []string{"append HoldMainArg", "provisionStage", "firstSessionExecCmd", "sessionCmd",
		"holdSessionLock", "armLaunchSignals", "startJailMain", "awaitReady", "handOff",
		"runWithProxy", "awaitJailMainEnd", "teardownAfterExit"}
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
}
