package run

// jailmain.go is the launcher's half of a container jail whose MAIN PROCESS is a hold and
// whose every session enters by exec (entrypoint/jailmain.go is the jail's half;
// docs/design/jail-lifetime-last-session-wins.md §4.1, step 2 of its §7).
//
// A fresh launch used to run `<rt> run -it … yolo-entrypoint '<the first session's command>'`
// under the TTY proxy, so the first session was the container's pid 1 and its runtime client
// held the terminal. Now two processes run the jail's two halves:
//
//  1. THE MAIN PROCESS, `<rt> run … yolo-entrypoint --yolo-hold-main '<the stage>'`, is started by
//     the jail's KEEPER (keeper.go), as a child with no terminal and in a process group of its
//     own, so nothing typed at a terminal and no signal a terminal's job gets reaches its client
//     (startJailMain). Its output crosses the keeper's progress pipe to the fresh launch, which
//     prints it as the boot always read; BootReadyLine is not printed (readyRelay). It holds until
//     a SIGTERM: nothing a session does ends it.
//  2. THE FIRST SESSION, `<rt> exec -i [-t] <cname> yolo-entrypoint --yolo-first-session
//     '<command>'`, is the fresh launch's own, under the TTY proxy, which takes the terminal
//     (firstSessionExecCmd). It is an ordinary session: its end is its quit (endSession,
//     keeperspawn.go), and the jail ends when its keeper sees the last session gone.
//
// ONE SIGNAL ARM covers the fresh launch's window (launchSignalArm): until the jail is ready it
// ends the launch alone, which has the keeper unwind; from ready on it is retargeted to the
// session's own teardown, whose hangup ends that session and nothing else (JL-D4). The proxy
// installs none for the first session and hands the arm its Handle instead, so no signal falls
// between two arms. An attach runs its session under an arm of the same kind.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// jailMain is a started main-process client: the `<rt> run` of the hold.
type jailMain struct {
	cmd *exec.Cmd
	// ready is closed when the relay sees BootReadyLine; exited when the client has exited
	// and its output is drained. exitCode is valid once exited is closed.
	ready    chan struct{}
	exited   chan struct{}
	exitCode int
}

// readyRelay copies the main process's stderr to w line by line, and on the first line that
// is exactly entrypoint.BootReadyLine calls onReady instead of printing it. After that it is
// a plain copy. Whole lines only until then, so the ready line is never split across writes;
// every boot line ends in a newline, and a partial one is flushed when the stream ends.
type readyRelay struct {
	mu      sync.Mutex
	w       io.Writer
	onReady func()
	ready   bool
	pending []byte
}

func (r *readyRelay) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ready {
		return len(p), writeAll(r.w, p)
	}
	r.pending = append(r.pending, p...)
	for !r.ready {
		i := bytes.IndexByte(r.pending, '\n')
		if i < 0 {
			break
		}
		line := r.pending[:i+1]
		if string(bytes.TrimRight(line, "\r\n")) == entrypoint.BootReadyLine {
			r.ready = true
			if r.onReady != nil {
				r.onReady()
			}
		} else if err := writeAll(r.w, line); err != nil {
			r.pending = r.pending[i+1:]
			return len(p), err
		}
		r.pending = r.pending[i+1:]
	}
	if r.ready && len(r.pending) > 0 {
		rest := r.pending
		r.pending = nil
		return len(p), writeAll(r.w, rest)
	}
	return len(p), nil
}

// flush writes out a partial last line, once the stream has ended.
func (r *readyRelay) flush() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.pending) > 0 {
		_ = writeAll(r.w, r.pending)
		r.pending = nil
	}
}

// writeAll is w.Write that treats a short write as the error it is.
func writeAll(w io.Writer, p []byte) error {
	n, err := w.Write(p)
	if err == nil && n < len(p) {
		return io.ErrShortWrite
	}
	return err
}

// startJailMain starts the main-process client. stdout is the launcher's own stream (the
// process's, never the launch-log tee: what the jail prints is not what the launcher said,
// launchlog.go), and stderr goes through the ready relay to stderr.
//
// Its OWN PROCESS GROUP (Setpgid): the terminal's Ctrl-C, Ctrl-Z and hangup go to the
// launcher's foreground group and never to this client, so the launcher's signal arm alone
// decides what a signal does to the jail. Not its own session: it stays a child of the
// launcher, which waits for it. stdin is /dev/null: the hold reads nothing.
//
// onExit runs the moment the client is reaped, before its output is flushed, so the moment
// Window A ends is recorded as it happens. May be nil.
//
// BOTH STREAMS ARE PIPES this process copies, never the terminal itself: a process in a
// background group that writes to a terminal set `tostop` is stopped by SIGTTOU, and the
// client would then hold the jail with nobody to resume it. And each pipe is one of this
// process's own rather than an exec.Cmd writer, so Wait returns the moment the client exits,
// whoever else the runtime let inherit the pipe; only the copies wait for the pipes' end, for
// at most relayDrainWait, so a refusal's last lines are printed before the teardown's first.
func startJailMain(argv []string, stdout, stderr io.Writer, onExit func()) (*jailMain, error) {
	c := exec.Command(argv[0], argv[1:]...)
	c.Stdin = nil
	m := &jailMain{cmd: c, ready: make(chan struct{}), exited: make(chan struct{})}
	var once sync.Once
	relay := &readyRelay{w: stderr, onReady: func() { once.Do(func() { close(m.ready) }) }}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}
	c.Stdout, c.Stderr = outW, errW
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		for _, f := range []*os.File{outR, outW, errR, errW} {
			_ = f.Close()
		}
		return nil, err
	}
	_ = outW.Close() // the child's copies are the only writers now
	_ = errW.Close()
	var copies sync.WaitGroup
	copies.Add(2)
	for _, p := range []struct {
		dst io.Writer
		src *os.File
	}{{stdout, outR}, {relay, errR}} {
		go func() {
			defer copies.Done()
			_, _ = io.Copy(p.dst, p.src)
			_ = p.src.Close()
		}()
	}
	drained := make(chan struct{})
	go func() { copies.Wait(); close(drained) }()
	go func() {
		m.exitCode = exitCodeOf(c.Wait())
		if onExit != nil {
			onExit()
		}
		select {
		case <-drained:
		case <-time.After(relayDrainWait):
		}
		relay.flush()
		close(m.exited)
	}()
	return m, nil
}

// relayDrainWait bounds how long the relay's last lines are waited for once the main
// process's client has exited.
var relayDrainWait = time.Second

// safeRun runs fn on its caller's goroutine and keeps a panic in it from taking the launch down
// with the jail running, as the proxy's own callback runner does (ttyproxy.safeCallback): the
// fresh launch's housekeeping slot runs under it.
func safeRun(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// exitCodeOf is an exec.Cmd.Wait error as the status a shell would report: 0, the child's
// own code, or 128+N for a death by signal N.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if e, ok := err.(*exec.ExitError); ok {
		ee = e
	}
	if ee == nil {
		return 1
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := ee.ExitCode(); code >= 0 {
		return code
	}
	return 1
}

// awaitReady blocks until the main process's boot is done (true) or its client has exited
// first (false): a boot that refused, or a runtime that never started it.
func (m *jailMain) awaitReady() bool {
	select {
	case <-m.ready:
		return true
	case <-m.exited:
		// A ready line and the exit can land together; the line wins.
		select {
		case <-m.ready:
			return true
		default:
			return false
		}
	}
}

// firstSessionExecCmd is the first session's argv: the attach's own shape (attachExisting),
// `-i`, `-t` on a terminal, the jail's entrypoint by absolute path, with the first-session form
// of its argv, which makes this exec the one that runs provisioning on its terminal. The
// command is sessionCmd's. No NO_COLOR argument: this launch composed the container's
// environment, so it already carries the launch's own (noColorEnvArgs). No detach sequence, as
// on every session's exec (runtime.DetachKeysArgs, JL-D27). sessionID names the session in the
// jail, for its signal arm's hangup, as an attach's does (sessionhangup.go): this launch froze the
// session-hangup contract tag into the container it starts, so the jail always knows the form.
func (o *Options) firstSessionExecCmd(rt, cname, command, sessionID string) []string {
	argv := []string{rt, "exec", "-i"}
	if o.IsTTYStdout() {
		argv = append(argv, "-t")
	}
	argv = append(argv, runtime.DetachKeysArgs(rt)...)
	argv = append(argv, sessionEnvArgs(sessionID)...)
	return append(argv, cname, JailEntrypointPath, entrypoint.FirstSessionArg, command)
}

// sessionHandle is the first session's runtime client's half of the launch arm's teardown:
// the TTY proxy's Handle on Linux (ttyproxy.Observer.Arm), the plain spawn's elsewhere.
// Terminate puts the host terminal back and keeps the proxy from returning on its own; Kill
// ends the client at its pid.
type sessionHandle interface {
	Terminate() bool
	Kill()
}

// launchSignalArm is the fresh launch's ONE signal arm for its whole window: from the keeper's
// spawn, through the boot it relays and the first session's exec, until that session has returned,
// so exactly one arm acts on each signal at every instant. The TTY proxy runs the first session
// with no arm of its own and hands this one its Handle (attach), through which it restores the
// terminal first and kills the exec client last. A SIGINT, SIGHUP or SIGTERM runs the arm's
// teardown (then the embedded pack tree's release, as the proxy's arm does) and exits 128+N. The
// teardown is the pre-ready one until the jail is ready, and the session's from then on (retarget,
// keeperspawn.go): neither ever stops the jail, which is its keeper's (JL-D4).
//
// An attach runs its one session under an arm of the same kind (attachSignalArm), whose teardown
// hangs up that session's processes in the jail. Before either, from its pack staging, a container
// launch runs under its LAUNCH GUARD, an arm of the same kind again (launchguard.go), which the
// keeper's arm or the attach's takes over from. Only the innermost installed arm acts on a signal
// (armstack.go), so a guard still installed under the arm that took over never acts with it.
type launchSignalArm struct {
	mu          sync.Mutex
	signals     chan os.Signal
	done        chan struct{}
	exited      chan struct{} // closed once the exit returns, which os.Exit never does
	session     sessionHandle // the first session's, while its run is in progress
	onTerminate func()        // the teardown a signal runs (retarget replaces it)
	// outer is what this arm's launch needs done when a launch running inside it, in this process,
	// ends the process on a signal (armstack.go): nil but for a launch guard's.
	outer       func()
	stopped     bool
	terminating bool
}

// armLaunchSignals installs the arm.
func armLaunchSignals(onTerminate func()) *launchSignalArm {
	return armLaunchSignalsWith(onTerminate, launchArmExit)
}

// launchArmExit is how every arm the pipeline installs ends the process: os.Exit. A var so a test
// that drives a whole launch to a signal can see the exit without ending the test binary.
var launchArmExit = os.Exit

// endingSignal is the signal the arm now ending this process took, set before its teardown runs,
// for a teardown whose last words depend on it: a line that its jail stays up is for a terminal
// that outlives the signal, which a SIGHUP's closed pane does not (endingOnAHangup). One
// arm ends a process, the innermost and once (armstack.go), so the signal is the process's. Unset
// while no arm is ending it, as for a teardown called by anything but its arm; only a test's exit
// returns, and the arm unsets it then.
var endingSignal atomic.Pointer[syscall.Signal]

// signalEndingTheProcess is endingSignal's signal, and false when no arm is ending the process.
func signalEndingTheProcess() (syscall.Signal, bool) {
	if s := endingSignal.Load(); s != nil {
		return *s, true
	}
	return 0, false
}

// endingOnAHangup reports that an arm is ending this process on a SIGHUP: a closed pane or window,
// which leaves no terminal to read what a teardown would say of the jail it leaves. Each teardown a
// launch's arm runs says nothing of the jail staying up then (JL-D76): the session's
// (noteJailStaysUpOnSignal) and the one before ready (awaitKeeperUnwind).
func endingOnAHangup() bool {
	sig, ending := signalEndingTheProcess()
	return ending && sig == syscall.SIGHUP
}

// armLaunchSignalsWith is armLaunchSignals with the process exit as a parameter, so a test can
// drive the arm to its end without ending the test binary.
func armLaunchSignalsWith(onTerminate func(), exit func(int)) *launchSignalArm {
	return armLaunchSignalsOuter(onTerminate, nil, exit)
}

// armLaunchSignalsOuter installs an arm whose launch needs outer done when a launch run inside it
// ends the process (armstack.go). It is the innermost arm from here until it is disarmed or another
// is installed.
func armLaunchSignalsOuter(onTerminate, outer func(), exit func(int)) *launchSignalArm {
	a := &launchSignalArm{signals: make(chan os.Signal, 4), done: make(chan struct{}),
		exited: make(chan struct{}), onTerminate: onTerminate, outer: outer}
	go func() {
		for {
			select {
			case s := <-a.signals:
				a.mu.Lock()
				if a.stopped || a.terminating {
					a.mu.Unlock()
					continue
				}
				a.terminating = true
				h := a.session
				teardown := a.onTerminate
				a.mu.Unlock()
				sig := s.(syscall.Signal)
				endingSignal.Store(&sig)
				a.terminate(h, teardown)
				exit(128 + int(sig))
				endingSignal.Store(nil)
				close(a.exited)
				return
			case <-a.done:
				return
			}
		}
	}()
	pushLaunchArm(a)
	return a
}

// terminate is the arm's teardown, in the proxy's order. From the background too: `kill %1`
// finds a job the user ^Z'd while the main process's client lingered there, and a tty write on
// the way out would raise SIGTTOU and stop it again, so both job-control signals are ignored
// first. Then the terminal, when the first session's run holds it, before the teardown prints;
// then onTerminate; then the exec client, at its pid, read again because a run that started
// while the teardown ran attaches late (attach); then what each launch this one runs inside needs
// done, since the exit ends them too (armstack.go); then the embedded pack tree, which the exit
// would otherwise leak, for the reason the proxy's arm releases it (proxy_linux.go's
// withEmbeddedRelease).
func (a *launchSignalArm) terminate(h sessionHandle, onTerminate func()) {
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	if h != nil {
		h.Terminate()
	}
	if onTerminate != nil {
		onTerminate()
	}
	a.mu.Lock()
	h = a.session
	a.mu.Unlock()
	if h != nil {
		h.Kill()
	}
	for _, outer := range outerArms(a) {
		if outer.outer != nil {
			outer.outer()
		}
	}
	packload.ReleaseEmbedded()
}

// retarget replaces the teardown a signal runs from here on: a fresh launch's arm ends the launch
// alone until its jail is ready, and from then on its session, as an attach's does (keeperspawn.go).
// One arm for the whole window, so no signal falls between two. It returns false when the arm has
// begun a teardown, which then owns the exit.
func (a *launchSignalArm) retarget(onTerminate func()) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminating {
		return false
	}
	a.onTerminate = onTerminate
	return true
}

// attach is the first session's run handing the arm its Handle, once its exec client is
// started and the terminal is the proxy's. A teardown already under way did not have it, so
// the Terminate it skipped runs here: the terminal the proxy just made raw goes back, and the
// proxy's own return stays blocked for the arm's exit.
func (a *launchSignalArm) attach(h sessionHandle) {
	a.mu.Lock()
	a.session = h
	late := a.terminating
	a.mu.Unlock()
	if late {
		h.Terminate()
	}
}

// detach ends the first session's part in the arm once its run has returned. It returns false
// when the arm has begun the teardown, which then owns the exit: the caller must leave the rest
// to it rather than race it.
func (a *launchSignalArm) detach() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminating {
		return false
	}
	a.session = nil
	return true
}

// disarm ends the arm, after which a signal goes to the next arm out (armstack.go), or, with none,
// has its default effect, as it always had once the proxy returned: the launch calls it once the
// main process's client has exited, before the normal teardown. It returns false when the arm has
// already begun the teardown: the caller — the launch's own goroutine, woken because that teardown
// stopped the jail — must then leave the rest to the arm, which exits the process, rather than run
// the teardown a second time. Idempotent.
func (a *launchSignalArm) disarm() bool {
	a.mu.Lock()
	if a.terminating {
		a.mu.Unlock()
		return false
	}
	if a.stopped {
		a.mu.Unlock()
		return true
	}
	a.stopped = true
	a.mu.Unlock()
	popLaunchArm(a)
	close(a.done)
	return true
}

// awaitExit waits for the exit of an arm that began its teardown, which os.Exit never returns
// from: in production it blocks until the process ends, so the caller never races the arm, and a
// test that faked the exit gets its goroutine back once the exit has run.
func (a *launchSignalArm) awaitExit() { <-a.exited }
