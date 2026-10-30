//go:build linux

// Package ttyproxy is the in-process TTY proxy that wraps `podman run` so ^Z
// suspends the PROXY (not the container), SIGWINCH resizes propagate, and
// Ctrl-C, window-close, and SIGTERM tear the jail down cleanly. All signal teardown stays
// in one process.
//
// Frozen behavior (from docs/reference/ctrl-z-and-the-tty-proxy.md):
//   - non-TTY stdin -> transparent plain spawn (no pty), as is a run whose Observer names a
//     Stdout of its own.
//   - ^Z suspends the PROXY via TARGETED SIGTSTP to self (NEVER a pgroup-wide
//     signal — that would stop podman, a jail-visible change); the terminal is
//     restored to cooked mode and reset (cursor shown, mouse tracking disabled,
//     attributes cleared) before stopping; the keypress never reaches the
//     child; bytes after it in the same read are queued and flushed on resume.
//     WHICH BYTES ARE A ^Z is suspendkey.go's job and is no longer just 0x1A —
//     a terminal running the kitty keyboard protocol sends an escape sequence
//     instead, and missing it is a live wedge.
//   - NO Setsid (setsid broke `podman -it`); NEVER signal.Notify(SIGTSTP)
//     (default disposition required to actually stop).
//   - SIGCONT -> re-raw the host TTY, and resync the window size (a resize while
//     stopped raises no signal we will see) — only while we own the terminal. A
//     SIGCONT in the background (`bg`, or `kill %1` on the stopped job) leaves the
//     shell's termios alone, since the write would raise SIGTTOU and stop us again. SIGWINCH -> TIOCSWINSZ to the pty,
//     then a TARGETED SIGWINCH at the child pid — the runtime shares our process
//     group on the host tty and reads its size from the proxy pty, so without the
//     poke it can read a stale size and nothing ever corrects it (resyncWinsize).
//   - SIGINT, SIGHUP or SIGTERM -> restore cooked termios and reset the terminal
//     (in the foreground only, and with SIGTTOU/SIGTTIN ignored, so the arm reaches
//     its exit from the background too), run onTerminate, SIGKILL the child at its
//     pid, exit 128+n. A Ctrl-C keypress is not one of these: raw mode delivers it
//     as a byte, and the byte is forwarded to the jail (proxyLoop states the ruling).
//     Only while the run is the proxy's: once the child has exited and the proxy has
//     begun returning, a signal is left to the caller. A caller that runs its own arm
//     for the whole run (Observer.Arm) gets no arm from the proxy at all, and a Handle
//     that does the proxy's half of the teardown instead.
//   - stdin EOF -> stop reading stdin, keep pumping the master until child exit
//     (the decided semantics).
//
// Stage observation (StageHook/RunWithProxyHooked): the proxy is where the
// shutdown-delay question lives — the gap between the child exiting and the
// drain finishing is a named gap (docs/reference/perf-logging.md, Known gaps) — so
// the proxy reports its own transitions to whoever wants them, without this
// package learning what a timing span is.
package ttyproxy

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const (
	readChunk = 65536
	// interruptByte is no longer INTERCEPTED: ^C forwards to the jail like any
	// other byte (proxyLoop states the ruling). It is still RECOGNIZED, by
	// classifyInput, so the Window A log can name a forwarded ^C.
	interruptByte = 0x03 // ^C
	suspByte      = 0x1a // ^Z
)

// getWinsize reads the terminal window size from fd.
func getWinsize(fd int) (*unix.Winsize, error) {
	return unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
}

// resyncWinsize copies the host tty's window size onto the proxy pty and then
// POKES THE CHILD, in that order. It is the fix for size drift — the ptys in a
// jail chain disagreeing about rows and columns, which shows up as an agent TUI
// wrapping in the wrong place until something forces a redraw.
//
// THE RACE. yolo does not setsid the runtime child (it cannot — setsid leaves
// podman with no controlling tty and breaks `-it`'s pty allocation; see the
// frozen-behavior notes above), so podman stays in this process's group ON THE
// HOST TTY and the kernel signals it with the same SIGWINCH, simultaneously. Its
// STDIO, though, is the proxy pty. If podman's handler runs before ours it reads
// the size we have not written yet and pushes that stale value into the
// container, and nothing corrects it: TIOCSWINSZ on the proxy pty raises no
// SIGWINCH because that pty has no foreground process group to raise it at
// (verified — tcgetpgrp on it returns ENOTTY). A coin flip with no recovery path,
// decided per resize.
//
// Measured 2026-09-09 across eight live jails on one Linux host: both outer ptys agreed
// and several containers were a row short, three of them re-drifting on their own
// within fifteen minutes. A bare `kill -WINCH` at the podman pid fixed each
// instantly, which is what places the fault at this boundary.
//
// TARGETED AT THE PID, never the process group: a pgroup signal from here would
// be jail-visible, which the frozen-behavior block rules out for the same reason
// it rules out the ^Z broadcast. Unconditional rather than compared against a
// last-sent size — a redundant signal costs one redraw, while tracking the last
// size adds a second source of truth that can itself go stale.
//
// This does not suppress the racing first signal; podman may still act once on a
// stale size and then correct itself. And it cannot help a window that is garbled
// while every pty already AGREES: the kernel raises no SIGWINCH for a TIOCSWINSZ
// that writes the size already there, so there is nothing to re-signal. That case
// is a repaint fault in the TUI or the emulator, and no amount of this fixes it.
func resyncWinsize(inFd, master int, c *exec.Cmd) {
	ws, err := getWinsize(inFd)
	if err != nil {
		return
	}
	setWinsize(master, ws)
	// AFTER the write, which is the whole point: podman re-reads from the proxy
	// pty, so it must be authoritative before we ask. Errors are ignored in the
	// existing style of this file — after the child exits this is a signal to a
	// dead pid, which is the normal way a session ends.
	if c != nil && c.Process != nil {
		_ = c.Process.Signal(syscall.SIGWINCH)
	}
}

// setWinsize writes the window size to fd (TIOCSWINSZ).
func setWinsize(fd int, ws *unix.Winsize) {
	_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
}

// RunWithProxy spawns cmd under a TTY proxy and returns its exit code.
//
//   - onStarted (if non-nil) runs on a goroutine after spawn (post-launch
//     housekeeping, e.g. release a lock) without blocking the pump loop.
//   - onTerminate (if non-nil) is host-side teardown run when the proxy is
//     interrupted (Ctrl-C / SIGHUP window-close / SIGTERM) rather than exiting on its own.
//
// Non-TTY stdin falls back to a plain spawn (no pty), matching Python.
func RunWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func()) (int, error) {
	return RunWithProxyHooked(cmd, onStarted, onTerminate, nil)
}

// Stage names handed to a StageHook, in the order a healthy pty session sees
// them. A path that skips a stage (the plain fallback has no drain, no
// termios) simply never reports it — the report shows what happened.
const (
	StageSpawned         = "spawned"
	StageExited          = "exited"
	StageDrainDone       = "drain_done"
	StageTermiosRestored = "termios_restored"
	// StageSuspended and StageResumed bracket a ^Z self-suspend attempt. StageSuspended is a
	// historical marker emitted only after the targeted SIGTSTP syscall returns (ignored or
	// after continuation); a process killed while stopped emits nothing, and this marker is
	// not an acknowledgment that the kernel actually stopped it. External observers own that
	// proof.
	StageSuspended       = "suspended"
	StageResumed         = "resumed"
	StageCancelRequested = "cancel_requested"
)

// StageHook observes the proxy's own transitions as they happen. It must not
// block, must not error, and runs on whatever goroutine got there first
// (including the signal goroutine) — observers that can panic are wrapped by
// the caller, not by this package.
type StageHook func(stage string)

func callStage(hook StageHook, stage string) {
	if hook == nil {
		return
	}
	defer func() { _ = recover() }()
	hook(stage)
}

// RunWithProxyHooked is RunWithProxy plus a stage observer. The hook is the
// seam the timing collector hangs `child.*` marks on; a nil hook is the
// everyday no-observer case and costs nothing.
func RunWithProxyHooked(cmd []string, onStarted func(*os.Process), onTerminate func(), hook StageHook) (int, error) {
	return RunWithProxyObserved(cmd, onStarted, onTerminate, Observer{Stage: hook})
}

// KeyCtrlC is the one control key Observer.Input names.
const KeyCtrlC = "ctrl-c"

// Observer is everything a caller may watch the proxy do. Caller-supplied fields are
// optional, and callbacks must not block or panic their way into the pump (each call is
// recover-wrapped). The package-private resume-input seams below are test-only.
type Observer struct {
	Stage StageHook
	// resumeInputObserve records boundary receipts; resumeInputWrite injects transient
	// write results for those receipts.
	resumeInputObserve func(resumeInputObservation)
	resumeInputWrite   func(int, []byte) (int, error)
	suspendBoundary    func(uint64)
	ownsTTY            func(int) bool
	beforeSuspend      func()
	afterSuspendCommit func()
	cancelWake         <-chan os.Signal
	// outputBlocked is told each time host stdout refuses output with EAGAIN, just
	// before the writer parks in poll: a test's exact witness that output is stalled.
	outputBlocked func()
	outputSink    *outputSink
	run           *session
	// Input sees every chunk read from the host stdin and forwarded to the child:
	// its LENGTH, and — for a chunk that is exactly one ^C, as a raw 0x03 or a
	// kitty/modifyOtherKeys escape — KeyCtrlC. NEVER the content: what a user
	// types can be a password. The Window A sampler uses it to tell whether a
	// lingering podman exits right after a keystroke (it was blocked reading
	// stdin) or regardless of one.
	Input func(n int, key string)
	// Pty is handed, once and before the pump starts, a function that reports the
	// proxy pty's line discipline as the child last set it — "icanon=on isig=on
	// echo=off" — read with TCGETS through the MASTER, which on Linux answers
	// with the slave's termios. Safe from any goroutine; "" once the master is
	// closed. Whether podman put the pty back in cooked mode on its way out
	// decides whether a forwarded ^C is data it reads or a SIGINT it may ignore.
	Pty func(mode func() string)
	// Arm, when set, says the CALLER runs the SIGINT/SIGHUP/SIGTERM arm for the whole of this
	// run, so exactly one arm acts on each signal at every instant: the proxy installs none of
	// its own (it still handles SIGWINCH and SIGCONT), never calls onTerminate, and instead
	// hands Arm, once, the Handle through which the caller's arm puts the host terminal back
	// and ends the child. Called on the calling goroutine once the child is started and, on the
	// pty path, once the host tty is raw, so a Terminate through it always undoes the raw mode.
	// A caller whose arm fired before Arm ran must call Terminate itself: the handle is the
	// first moment it can. The fresh launch's arm is one (run.launchSignalArm), which covers
	// the stretches before and after this run as well.
	Arm func(Handle)
	// Env is appended to the environment the child inherits from this process, and set on the
	// child alone: nothing the proxy spawns later sees it. The one caller is a launch in a herdr
	// pane, which puts HERDR_AGENT on the runtime client herdr reads (run's herdragent.go).
	Env []string
	// Stdout and Stderr, when set, replace the corresponding child streams. A named stream runs
	// the child without the proxy because its pty merges stdout and stderr; stdin remains this
	// process's. The launch capture/build callers use these to send sub-launch output to their
	// own streams instead of the user's command payload.
	Stdout io.Writer
	Stderr io.Writer
}

// command builds the child's exec.Cmd, with Env layered over the inherited environment.
func (obs Observer) command(cmd []string) *exec.Cmd {
	c := exec.Command(cmd[0], cmd[1:]...)
	if len(obs.Env) > 0 {
		c.Env = append(os.Environ(), obs.Env...)
	}
	return c
}

func (obs Observer) input(n int, key string) {
	if obs.Input == nil {
		return
	}
	defer func() { _ = recover() }()
	obs.Input(n, key)
}

// ptyMode reads the slave's termios through the master, guarded so a call after
// the master is closed can never read whatever fd reused its number.
type ptyMode struct {
	mu     sync.Mutex
	master int
	closed bool
}

func (m *ptyMode) read() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ""
	}
	t, err := unix.IoctlGetTermios(m.master, unix.TCGETS)
	if err != nil {
		return ""
	}
	return termiosMode(t)
}

func (m *ptyMode) close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// termiosMode renders the three bits that decide what a forwarded byte means to
// the child: ICANON (line-buffered), ISIG (^C becomes SIGINT), ECHO.
func termiosMode(t *unix.Termios) string {
	return "icanon=" + onOff(t.Lflag&unix.ICANON != 0) + " isig=" + onOff(t.Lflag&unix.ISIG != 0) +
		" echo=" + onOff(t.Lflag&unix.ECHO != 0)
}

// session is one proxied child's teardown state, shared by whichever signal arm acts — the
// proxy's own or a caller's (Observer.Arm) — and by the proxy's own return, so exactly one of
// them owns the end of the run. Every host-termios write after the start goes through it
// under mu: the terminate's restore, and the SIGCONT re-raw, which must never land after it.
type session struct {
	mu                 sync.Mutex
	state              sessionState
	restored           chan struct{}
	childDone          chan struct{}
	childOnce          sync.Once
	childLifecycle     atomic.Uint32
	killOnce           sync.Once
	suspended          bool
	ownsTTY            func(int) bool
	beforeSuspend      func()
	afterSuspendCommit func()
	output             *outputSink
	inFd               int
	cooked             *unix.Termios // nil on the plain path, which has no terminal to put back
	c                  *exec.Cmd
	hook               StageHook
}

type sessionState int

const (
	sessionRunning sessionState = iota
	sessionTerminating
	sessionReturning
)

func newSession(inFd int, cooked *unix.Termios, c *exec.Cmd, hook StageHook) *session {
	return &session{restored: make(chan struct{}), childDone: make(chan struct{}), ownsTTY: ownsTerminal, inFd: inFd, cooked: cooked, c: c, hook: hook}
}

// terminate is the proxy's half of a signal teardown. It returns true exactly once, to the
// first arm to ask while the child's run is still the proxy's, having made the rest of the
// exit reachable from the background (SIGTTOU and SIGTTIN ignored: `kill %1` finds a job the
// user ^Z'd out of a lingering quit there, and a tty write or read would stop it again) and
// put the host terminal back in cooked mode when this process still owns it. It returns false,
// doing nothing, to every later arm, and to one that asks after the proxy began returning on
// its own: the child has exited and the caller is carrying on, so the signal is not the
// proxy's to act on any more. That one waits for the proxy's own restore first.
func (s *session) terminate() bool {
	s.requestCancel()
	s.mu.Lock()
	switch s.state {
	case sessionReturning:
		s.mu.Unlock()
		<-s.restored
		return false
	case sessionTerminating:
		s.mu.Unlock()
		return false
	}
	s.state = sessionTerminating
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	if s.output != nil {
		s.output.cancel()
	}
	if s.cooked != nil && s.ownsTTY(s.inFd) {
		restoreTerminalForCancellation(s.inFd, s.cooked, s.output)
	}
	s.mu.Unlock()
	if s.cooked != nil {
		callStage(s.hook, StageTermiosRestored)
	}
	return true
}

// claimReturn is the proxy's own return claiming the end of the run. False means an arm has
// begun its teardown and owns the exit: the caller must never return then.
func (s *session) claimReturn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == sessionTerminating {
		return false
	}
	s.state = sessionReturning
	return true
}

// reRaw puts the host tty back in raw mode after a resume, unless a teardown has begun: the
// terminal it restored must stay restored.
func (s *session) reRaw() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == sessionRunning && !s.cancellationPublished() {
		setRaw(s.inFd, s.cooked)
		s.suspended = false
		s.clearSuspendStop()
	}
}

func (s *session) isSuspended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == sessionRunning && s.suspended
}

// kill SIGKILLs the child, TARGETED at its pid: a runtime client that outlived its container
// ignores the SIGTERM a shell sends the job's group (the podman client forwards it to a
// container that is gone), and an exit would leave it running with nobody waiting on it. An
// error means it already exited.
func (s *session) kill() {
	s.killOnce.Do(func() { _ = s.c.Process.Kill() })
	<-s.childDone
}

func (s *session) requestCancel() {
	s.updateChildLifecycle(lifecycleCancelled)
}

func (s *session) childHasExited() bool {
	return s.childLifecycle.Load()&lifecycleChildExited != 0
}

func (s *session) cancellationPublished() bool {
	return s.childLifecycle.Load()&lifecycleCancelled != 0
}

func (s *session) cancellationOrExitPublished() bool {
	return s.childLifecycle.Load()&(lifecycleCancelled|lifecycleChildExited) != 0
}

func (s *session) commitSuspendStop() bool {
	return s.childLifecycle.CompareAndSwap(0, lifecycleStopCommitted)
}

// claimSuspendSignal is the final exclusive atomic stop decision. It orders a
// suspension attempt against later cancellation/reaping; it does not mean the
// kernel accepted SIGTSTP or that this process actually stopped.
func (s *session) claimSuspendSignal() bool {
	return s.childLifecycle.CompareAndSwap(lifecycleStopCommitted, lifecycleStopCommitted|lifecycleStopClaimed)
}

func (s *session) clearSuspendStop() {
	const stopBits = lifecycleStopCommitted | lifecycleStopClaimed
	for {
		state := s.childLifecycle.Load()
		cleared := state &^ stopBits
		if state == cleared || s.childLifecycle.CompareAndSwap(state, cleared) {
			return
		}
	}
}

func (s *session) updateChildLifecycle(bit uint32) {
	for {
		state := s.childLifecycle.Load()
		if s.childLifecycle.CompareAndSwap(state, state|bit) {
			return
		}
	}
}

const (
	lifecycleChildExited uint32 = 1 << iota
	lifecycleCancelled
	lifecycleStopCommitted // provisional eligibility; not the stop decision
	lifecycleStopClaimed   // final ordering point for one targeted SIGTSTP attempt
)

func (s *session) childReaped() {
	s.updateChildLifecycle(lifecycleChildExited)
	s.childOnce.Do(func() { close(s.childDone) })
}

func (s *session) canDrainOutput() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == sessionRunning && !s.suspended && s.ownsTTY(s.inFd)
}

func (s *session) canReadInput() bool {
	if s == nil {
		return true
	}
	if s.cancellationOrExitPublished() {
		return false
	}
	return s.canDrainOutput()
}

// Handle is a running child's half of a caller-owned signal teardown (Observer.Arm). Its
// methods are safe from any goroutine, and harmless once the run is over.
type Handle struct{ s *session }

// Terminate prepares the run for the caller's exit: SIGTTOU and SIGTTIN ignored, the host
// terminal back in cooked mode when this process owns it, and the proxy's own return blocked
// for good, so the caller's arm alone decides how the process ends. False, having done
// nothing, when the child already exited and the proxy is returning on its own (it then waits
// until the proxy has put the terminal back), or when it was already called.
func (h Handle) Terminate() bool { return h.s.terminate() }

// Kill SIGKILLs the child at its pid.
func (h Handle) Kill() { h.s.kill() }

// resumeInputObservation reports boundary order and byte counts only; it never includes input contents.
type resumeInputObservation struct {
	Event                       string
	CancellationOrExitPublished bool
	PendingBytes                int
	BytesAttempted              int
	BytesWritten                int
	BytesQueued                 int
}

func observeResumeInput(obs Observer, event string, attempted, written, queued int) {
	if obs.resumeInputObserve == nil {
		return
	}
	cancelOrExit := obs.run != nil && obs.run.cancellationOrExitPublished()
	observeResumeInputState(obs, event, cancelOrExit, 0, attempted, written, queued)
}

func observeResumePendingInput(obs Observer, event string, pending int) {
	if obs.resumeInputObserve == nil {
		return
	}
	cancelOrExit := obs.run != nil && obs.run.cancellationOrExitPublished()
	observeResumeInputState(obs, event, cancelOrExit, pending, 0, 0, 0)
}

func observeResumeInputState(obs Observer, event string, cancelOrExit bool, pending, attempted, written, queued int) {
	if obs.resumeInputObserve == nil {
		return
	}
	observation := resumeInputObservation{Event: event, CancellationOrExitPublished: cancelOrExit, PendingBytes: pending, BytesAttempted: attempted, BytesWritten: written, BytesQueued: queued}
	defer func() { _ = recover() }()
	obs.resumeInputObserve(observation)
}

func publishCancelWake(obs Observer) {
	if obs.run == nil || obs.cancelWake == nil {
		return
	}
	// Drain notifications already published to Go's channel after the callback and before
	// the lifecycle snapshot. A kernel-pending signal not yet delivered to this channel is
	// outside this publication boundary.
	for {
		select {
		case <-obs.cancelWake:
			obs.run.requestCancel()
		default:
			return
		}
	}
}

// RunWithProxyObserved is RunWithProxy plus an Observer.
func RunWithProxyObserved(cmd []string, onStarted func(*os.Process), onTerminate func(), obs Observer) (int, error) {
	hook := obs.Stage
	inFd := int(os.Stdin.Fd())
	if obs.Stdout != nil || obs.Stderr != nil || !isatty(inFd) {
		return runPlain(cmd, onStarted, obs)
	}

	// Save cooked attrs to restore on suspend/exit.
	cooked, err := unix.IoctlGetTermios(inFd, unix.TCGETS)
	if err != nil {
		return runPlain(cmd, onStarted, obs)
	}

	master, slave, err := openPty()
	if err != nil {
		return 0, err
	}

	// Match the pty window to the host TTY at startup.
	if ws, err := getWinsize(inFd); err == nil {
		setWinsize(slave, ws)
	}

	ptyFile := os.NewFile(uintptr(slave), "pty-slave")
	c := obs.command(cmd)
	c.Stdin, c.Stdout, c.Stderr = ptyFile, ptyFile, ptyFile
	if err := c.Start(); err != nil {
		_ = ptyFile.Close()
		unix.Close(master)
		return 0, err
	}
	_ = ptyFile.Close() // one owner for the slave fd; avoid competing finalizers after fd reuse
	mode := &ptyMode{master: master}
	if obs.Pty != nil {
		func() {
			defer func() { _ = recover() }()
			obs.Pty(mode.read)
		}()
	}
	callStage(hook, StageSpawned)
	if onStarted != nil {
		go safeCallback(onStarted, c.Process)
	}

	// Raw mode on the host TTY.
	setRaw(inFd, cooked)

	sink := &outputSink{fd: 1, blocked: obs.outputBlocked}
	obs.outputSink = sink
	restoreCooked := func() { restoreTerminal(inFd, cooked) }

	// The run's teardown state. Whichever arm acts first owns the exit: it kills the child
	// before it exits the process, and that kill ends proxyLoop on the main goroutine, so
	// without the claim below the ordinary return path could run — and return an exit code,
	// and let the caller tear down — before the arm's exit. And a signal that arrives once the
	// child has exited and the return is under way is not an arm's to act on
	// (session.terminate).
	sess := newSession(inFd, cooked, c, hook)
	if obs.ownsTTY != nil {
		sess.ownsTTY = obs.ownsTTY
	}
	sess.beforeSuspend = obs.beforeSuspend
	sess.afterSuspendCommit = obs.afterSuspendCommit
	sess.output = sink
	obs.run = sess
	// Reap before exposing a kill-waiting Handle to the synchronous Arm callback.
	exitedCh := reapChild(c, sess)
	callerArms := obs.Arm != nil
	if callerArms {
		func() {
			defer func() { _ = recover() }()
			obs.Arm(Handle{sess})
		}()
	}

	// Signal handlers. Note: we DO NOT Notify SIGTSTP (default disposition must
	// stop us); we handle WINCH/CONT/INT/HUP/TERM, and INT/HUP/TERM only when no caller
	// runs that arm itself (Observer.Arm). Ctrl-C arrives as a byte while the host TTY is
	// raw, and is forwarded (proxyLoop states the ruling).
	sigCh := make(chan os.Signal, 8)
	if callerArms {
		signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGCONT)
	} else {
		signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGCONT, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM)
	}
	defer signal.Stop(sigCh)
	if !callerArms {
		cancelWake := make(chan os.Signal, 8)
		signal.Notify(cancelWake, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM)
		defer signal.Stop(cancelWake)
		obs.cancelWake = cancelWake
	}

	go func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGWINCH:
				resyncWinsize(inFd, master, c)
			case syscall.SIGCONT:
				// Resumed in the BACKGROUND — `bg`, or the SIGCONT bash's `kill %1`
				// sends a stopped job — the terminal is the shell's: a termios write
				// from here would raise SIGTTOU and stop us again, and would put the
				// shell's terminal in raw mode if it got through. `fg` sends another
				// SIGCONT, from the foreground, and that one re-raws.
				if !sess.ownsTTY(inFd) {
					continue
				}
				sess.reRaw()
				// AND RESIZE. A window resized while we were stopped changed the
				// host tty behind our back, and the resulting SIGWINCH is only
				// delivered if one was pending — a resize that happened before the
				// stop, or several that coalesced, leaves us resumed at the wrong
				// size with no further signal coming. Same drift, different
				// trigger.
				resyncWinsize(inFd, master, c)
			case syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM:
				sess.requestCancel()
				observeResumeInputState(obs, "resume-signal-cancellation-published", sess.cancellationOrExitPublished(), 0, 0, 0, 0)
				callStage(hook, StageCancelRequested)
				// The terminate arm must reach os.Exit from the background too: that is
				// where `kill %1` finds a job the user ^Z'd out of a lingering quit, and
				// session.terminate ignores SIGTTOU/SIGTTIN and leaves the termios, which is
				// the shell's while we are in the background, alone. Only the first signal
				// acts, and none once the return has claimed the run.
				if !sess.terminate() {
					continue
				}
				if onTerminate != nil {
					onTerminate()
				}
				// Then the child, TARGETED at its pid (session.kill).
				sess.kill()
				n := int(s.(syscall.Signal))
				if beforeTerminateExit != nil {
					beforeTerminateExit()
				}
				os.Exit(128 + n)
			}
		}
	}()

	rc, proxyErr := proxyLoop(inFd, master, c, cooked, obs, exitedCh)
	if !sess.claimReturn() {
		select {} // an arm is exiting the process; never race it
	}
	if afterReturnClaimed != nil {
		afterReturnClaimed()
	}

	if proxyErr != nil {
		restoreTerminalForCancellation(inFd, cooked, sink)
	} else if !sess.isSuspended() {
		restoreCooked()
	}
	close(sess.restored)
	callStage(hook, StageTermiosRestored)
	mode.close()
	unix.Close(master)
	return rc, proxyErr
}

// beforeTerminateExit is a test seam: it runs in the terminate arm between killing the
// child and os.Exit, so a test can hold that window open and prove the main goroutine
// waits for the arm instead of returning. Nil in production.
var beforeTerminateExit func()

// afterReturnClaimed is a test seam: it runs once the proxy's own return has claimed the end
// of the run and before it puts the terminal back, so a test can deliver a signal inside that
// window and prove the arm leaves it alone. Nil in production.
var afterReturnClaimed func()

// runPlain is the no-terminal spawn. It installs no signal arm; a caller that runs its own
// (Observer.Arm) is handed the child's Handle, whose Terminate has no terminal to put back.
func runPlain(cmd []string, onStarted func(*os.Process), obs Observer) (int, error) {
	hook := obs.Stage
	c := obs.command(cmd)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, io.Writer(os.Stdout), os.Stderr
	if obs.Stdout != nil {
		c.Stdout = obs.Stdout
	}
	if obs.Stderr != nil {
		c.Stderr = obs.Stderr
	}
	if err := c.Start(); err != nil {
		return 0, err
	}
	sess := newSession(-1, nil, c, hook)
	exitedCh := reapChild(c, sess)
	if obs.Arm != nil {
		func() {
			defer func() { _ = recover() }()
			obs.Arm(Handle{sess})
		}()
	}
	callStage(hook, StageSpawned)
	if onStarted != nil {
		go safeCallback(onStarted, c.Process)
	}
	rc := <-exitedCh
	callStage(hook, StageExited)
	if !sess.claimReturn() {
		select {} // the caller's arm is exiting the process; never race it
	}
	close(sess.restored)
	return rc, nil
}

func reapChild(c *exec.Cmd, sess *session) <-chan int {
	exitedCh := make(chan int, 1)
	go func() {
		rc := exitCode(c.Wait())
		if sess != nil {
			sess.childReaped()
		}
		exitedCh <- rc
	}()
	return exitedCh
}

// maxForwardQueue is the backpressure threshold for host input queued for the
// child's pty master. When the in-memory queue reaches this ceiling, reading from
// host stdin is paused so a flood of pasted input cannot grow memory without bound
// or outpace the child. Output draining from the child continues uninterrupted.
const (
	maxForwardQueue = 1024 * 1024 // 1 MiB
	maxOutputQueue  = 1024 * 1024 // 1 MiB
)

type outputWriteRequest struct {
	fd    int
	data  []byte
	reset bool
}

type outputWriteResult struct {
	bytes int
	reset bool
	err   error
}

func writeOutputChunk(fd int, data []byte, blocked func()) error {
	for len(data) > 0 {
		n, err := unix.Write(fd, data)
		if n > 0 {
			data = data[n:]
		}
		if err == nil {
			if n == 0 {
				return io.ErrShortWrite
			}
			continue
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
			if blocked != nil {
				blocked()
			}
			fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
			if _, pollErr := unix.Poll(fds, -1); pollErr != nil && !errors.Is(pollErr, syscall.EINTR) {
				return pollErr
			}
			continue
		}
		return err
	}
	return nil
}

type outputSink struct {
	fd       int
	mu       sync.Mutex
	canceled atomic.Bool
	blocked  func()
}

func (s *outputSink) writeTo(fd int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.canceled.Load() {
		return nil
	}
	return writeOutputChunk(fd, data, s.blocked)
}

func (s *outputSink) cancel() { s.canceled.Store(true) }

func (s *outputSink) cancelAndReset(inFd int) {
	s.cancel()
	fd := terminalResetFD(inFd)
	if fd < 0 {
		return
	}
	if fd == s.fd {
		if !s.mu.TryLock() {
			return
		}
		defer s.mu.Unlock()
	}
	writeTerminalResetFD(fd)
}

func writeTerminalResetIfReady(inFd int) {
	if fd := terminalResetFD(inFd); fd >= 0 {
		writeTerminalResetFD(fd)
	}
}

func writeTerminalResetFD(fd int) {
	reset := []byte(termReset)
	for len(reset) > 0 {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		n, err := unix.Poll(fds, 0)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return
		}
		if n == 0 || fds[0].Revents&unix.POLLOUT == 0 {
			return
		}
		written, err := unix.Write(fd, reset[:1])
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || written != 1 {
			return
		}
		reset = reset[1:]
	}
}

func restoreTerminalForCancellation(inFd int, cooked *unix.Termios, sink *outputSink) {
	if cooked != nil {
		_ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked)
	}
	if sink != nil {
		sink.cancelAndReset(inFd)
	} else {
		writeTerminalResetIfReady(inFd)
	}
}

// proxyLoop pumps bytes between the host TTY and the master pty until the child
// exits.
func proxyLoop(inFd, master int, c *exec.Cmd, cooked *unix.Termios, obs Observer, exitedCh <-chan int) (int, error) {
	hook := obs.Stage
	var pending []byte
	var toMaster []byte
	var enqueueToMaster func([]byte, bool) (attempted, written, queued int)
	resumeInputTracked := false
	resumeInputPrefix := 0
	resumeInputRemaining := 0
	writeMaster := func(data []byte, resumed bool) (int, error) {
		if resumed && obs.resumeInputWrite != nil {
			return obs.resumeInputWrite(master, data)
		}
		return unix.Write(master, data)
	}
	stdinClosed := false

	// Set master to non-blocking so writes to the pty never hang proxyLoop in the
	// kernel when child input buffers fill up (e.g. large image paste) while the child
	// is producing output or blocked.
	_ = unix.SetNonblock(master, true)
	buf := make([]byte, readChunk)

	// A blocked host stdout must not stop polling host input. One writer owns all
	// stdout writes; the pump bounds outstanding output and keeps forwarding input
	// while that writer is backpressured. No shared stdout status flags are changed.
	sink := obs.outputSink
	if sink == nil {
		sink = &outputSink{fd: 1, blocked: obs.outputBlocked}
	}
	outputWrites := make(chan outputWriteRequest)
	outputResults := make(chan outputWriteResult, 1)
	outputStopped := make(chan struct{})
	go func() {
		defer close(outputStopped)
		for request := range outputWrites {
			err := sink.writeTo(request.fd, request.data)
			outputResults <- outputWriteResult{bytes: len(request.data), reset: request.reset, err: err}
		}
	}()
	var pendingOutput []outputWriteRequest
	outputBytes := 0
	var acceptedOffset uint64
	outputBusy := false
	outputFailed := false
	var outputErr error
	pendingSuspend := false
	suspendResetQueued := false
	var suspendOffset uint64
	suspendResumePending := false
	var resumeSuspendedInput func()
	acceptOutput := func(data []byte) {
		if outputFailed || len(data) == 0 {
			return
		}
		copyOfData := append([]byte(nil), data...)
		pendingOutput = append(pendingOutput, outputWriteRequest{fd: sink.fd, data: copyOfData})
		outputBytes += len(copyOfData)
		acceptedOffset += uint64(len(copyOfData))
	}
	dispatchOutput := func() {
		if outputBusy || len(pendingOutput) == 0 {
			return
		}
		outputWrites <- pendingOutput[0]
		pendingOutput = pendingOutput[1:]
		outputBusy = true
	}
	completeOutput := func(result outputWriteResult) {
		outputBusy = false
		outputBytes -= result.bytes
		if result.err != nil {
			outputFailed = true
			outputErr = &OutputError{Err: result.err}
			pendingOutput = nil
			outputBytes = 0
			pendingSuspend = false
			// This command is the child owned by this proxy invocation. The
			// reaper goroutine remains the sole caller of Wait.
			if obs.run != nil {
				obs.run.kill()
			} else {
				_ = c.Process.Kill()
			}
		} else if result.reset && pendingSuspend {
			suspend, err := suspendAfterReset(inFd, cooked, obs.run, hook)
			if err != nil {
				outputFailed = true
				outputErr = err
				pendingSuspend = false
				if obs.run != nil {
					obs.run.kill()
				} else {
					_ = c.Process.Kill()
				}
				return
			}
			if !suspend {
				pendingSuspend = false
				return
			}
			if obs.run != nil && obs.run.isSuspended() {
				suspendResumePending = true
				return
			}
			if resumeSuspendedInput != nil {
				resumeSuspendedInput()
			}
		}
	}
	waitOutput := func() {
		if !outputBusy {
			dispatchOutput()
		}
		if outputBusy {
			completeOutput(<-outputResults)
		}
	}
	finish := func(rc int) (int, error) {
		callStage(hook, StageExited)
		for {
			if obs.run != nil && !obs.run.canDrainOutput() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			for !outputFailed && outputBytes+readChunk > maxOutputQueue {
				if obs.run != nil && !obs.run.canDrainOutput() {
					break
				}
				waitOutput()
			}
			if obs.run != nil && !obs.run.canDrainOutput() {
				continue
			}
			n, err := unix.Read(master, buf)
			if n > 0 {
				acceptOutput(buf[:n])
			}
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
				break
			}
			if err != nil || n == 0 {
				break
			}
		}
		for outputBytes > 0 {
			if obs.run != nil && !obs.run.canDrainOutput() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			waitOutput()
		}
		callStage(hook, StageDrainDone)
		close(outputWrites)
		<-outputStopped
		return rc, outputErr
	}

	flushToMaster := func() {
		if len(toMaster) == 0 {
			return
		}
		attempted, written, queued := 0, 0, 0
		prefixBeforeWrite := resumeInputPrefix
		if resumeInputTracked {
			attempted = resumeInputRemaining
		}
		n, err := writeMaster(toMaster, resumeInputTracked)
		if n > 0 {
			toMaster = toMaster[n:]
			if len(toMaster) == 0 {
				toMaster = nil
			}
		}
		writeFailed := err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR)
		if writeFailed {
			// Master write error (e.g. EIO if slave closed): discard remaining queue.
			toMaster = nil
		}
		if resumeInputTracked {
			writeCount := n
			if writeCount < 0 {
				writeCount = 0
			}
			written = writeCount - prefixBeforeWrite
			if written < 0 {
				written = 0
			}
			if written > resumeInputRemaining {
				written = resumeInputRemaining
			}
			if writeFailed {
				resumeInputRemaining = 0
				resumeInputPrefix = 0
			} else {
				resumeInputPrefix = prefixBeforeWrite - writeCount
				if resumeInputPrefix < 0 {
					resumeInputPrefix = 0
				}
				resumeInputRemaining -= written
			}
			queued = resumeInputRemaining
			observeResumeInput(obs, "resume-flush-result", attempted, written, queued)
			resumeInputTracked = queued > 0
		}
	}

	enqueueToMaster = func(data []byte, resumed bool) (attempted, written, queued int) {
		attempted = len(data)
		if len(data) == 0 {
			return attempted, written, queued
		}
		queuedBeforeWrite := len(toMaster)
		if queuedBeforeWrite == 0 {
			if resumed {
				observeResumeInput(obs, "resume-write-attempt", len(data), 0, 0)
			}
			n, err := writeMaster(data, resumed)
			written = n
			if written < 0 {
				written = 0
			}
			if n > 0 {
				data = data[n:]
			}
			if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
				if resumed {
					observeResumeInput(obs, "resume-write-error", attempted, written, 0)
				}
				return attempted, written, 0
			}
		}
		if len(data) > 0 {
			toMaster = append(toMaster, data...)
		}
		queued = len(data)
		if resumed && obs.resumeInputObserve != nil {
			resumeInputPrefix = queuedBeforeWrite
			resumeInputRemaining = queued
			resumeInputTracked = queued > 0
			observeResumeInput(obs, "resume-enqueue-result", attempted, written, queued)
		}
		return attempted, written, queued
	}
	resumeSuspendedInput = func() {
		if obs.cancelWake != nil {
			select {
			case <-obs.cancelWake:
				if obs.run != nil {
					obs.run.requestCancel()
				}
				select {}
			default:
			}
		}
		if obs.run != nil && obs.run.cancellationOrExitPublished() {
			suspendResumePending = false
			pendingSuspend = false
			return
		}
		suspendResumePending = false
		pendingSuspend = false
		suspendResetQueued = false
		observeResumePendingInput(obs, "resume-callback-entry", len(pending))
		callStage(hook, StageResumed)
		observeResumePendingInput(obs, "resume-callback-return", len(pending))
		publishCancelWake(obs)
		cancelOrExit := obs.run != nil && obs.run.cancellationOrExitPublished()
		observeResumeInputState(obs, "resume-cancellation-check", cancelOrExit, len(pending), 0, 0, 0)
		if cancelOrExit {
			pending = nil
			return
		}
		if len(pending) > 0 {
			_, _, _ = enqueueToMaster(pending, true)
			pending = nil
		}
	}

	if exitedCh == nil {
		exitedCh = reapChild(c, obs.run)
	}

	for {
		if obs.cancelWake != nil {
			select {
			case <-obs.cancelWake:
				if obs.run != nil {
					obs.run.requestCancel()
				}
				// The signal arm owns restoration, the owned-child kill and exit.
				// Stop pumping immediately; the arm will end this process.
				select {}
			default:
			}
		}
		select {
		case rc := <-exitedCh:
			return finish(rc)
		default:
		}
		select {
		case result := <-outputResults:
			completeOutput(result)
		default:
		}
		if suspendResumePending && obs.run != nil && !obs.run.isSuspended() {
			resumeSuspendedInput()
		}
		if pendingSuspend && !suspendResetQueued && outputBytes == 0 && acceptedOffset == suspendOffset && !outputBusy && !outputFailed {
			fd := terminalResetFD(inFd)
			if fd >= 0 {
				reset := []byte(termReset)
				pendingOutput = append(pendingOutput, outputWriteRequest{fd: fd, data: reset, reset: true})
				outputBytes += len(reset)
				suspendResetQueued = true
			}
		}
		if obs.run == nil || obs.run.canDrainOutput() {
			dispatchOutput()
		}

		masterEvents := int16(0)
		if (obs.run == nil || obs.run.canDrainOutput()) && !pendingSuspend && (outputFailed || outputBytes+readChunk <= maxOutputQueue) {
			masterEvents |= unix.POLLIN
		}
		readInputAllowed := obs.run == nil || obs.run.canReadInput()
		if readInputAllowed && len(toMaster) > 0 {
			masterEvents |= unix.POLLOUT
		}
		fds := []unix.PollFd{{Fd: int32(master), Events: masterEvents}}
		if readInputAllowed && !stdinClosed && len(toMaster)+len(pending) < maxForwardQueue {
			fds = append(fds, unix.PollFd{Fd: int32(inFd), Events: unix.POLLIN})
		}
		_, err := unix.Poll(fds, 100)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			// keep going; the exit check handles teardown
			continue
		}

		// master readable
		if (obs.run == nil || obs.run.canDrainOutput()) && !pendingSuspend && fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			n, rerr := unix.Read(master, buf)
			if n > 0 {
				acceptOutput(buf[:n])
			}
			if rerr != nil || n == 0 {
				if rerr == nil || (!errors.Is(rerr, unix.EAGAIN) && !errors.Is(rerr, unix.EWOULDBLOCK) && !errors.Is(rerr, syscall.EINTR)) {
					// master closed (child exited) — wait for the reaper.
					rc := <-exitedCh
					return finish(rc)
				}
			}
		}

		// master writable
		if (obs.run == nil || obs.run.canReadInput()) && fds[0].Revents&unix.POLLOUT != 0 {
			flushToMaster()
		}

		// stdin readable
		if (obs.run == nil || obs.run.canReadInput()) && !stdinClosed && len(fds) > 1 && fds[1].Revents&unix.POLLIN != 0 {
			n, rerr := unix.Read(inFd, buf)
			if n == 0 || (rerr != nil && !errors.Is(rerr, syscall.EINTR) && !errors.Is(rerr, unix.EAGAIN) && !errors.Is(rerr, unix.EWOULDBLOCK)) {
				// EOF on host stdin: stop reading stdin, keep pumping master
				// until child exit (the decided semantics). We do NOT close the
				// master (that would kill an interactive child prematurely);
				// just stop polling stdin.
				stdinClosed = true
				continue
			}
			if n <= 0 {
				continue
			}
			data := append([]byte(nil), buf[:n]...)
			// The observer gets the chunk's SIZE and at most the name of the one
			// control key it is — never a byte of it.
			obs.input(n, classifyInput(data))
			if pendingSuspend {
				pending = append(pending, stripSuspendKeys(data)...)
				continue
			}
			// ^C IS FORWARDED LIKE ANY OTHER BYTE, and that is a 2026-09-19 ruling
			// reversing what this loop used to do.
			//
			// It used to scan for 0x03, drop it, and raise a targeted SIGINT at the
			// proxy — "keep it out of the jail and signal only this proxy". The host
			// TTY is raw, so that was the only way Ctrl-C could mean anything at all
			// here; what it meant was QUIT THE LAUNCHER. Measured on a real host: at
			// a jail's bash prompt, Ctrl-C to clear the line tore the whole session
			// down instead. It is wrong for an agent too — Claude Code uses ^C to
			// interrupt generation, and under the old rule that killed the jail.
			//
			// Forwarded, the byte reaches the pty and the JAIL's own line discipline
			// raises SIGINT at the jail's foreground process group: bash clears the
			// line, an agent in raw mode gets 0x03 as input and decides for itself.
			// That is the ordinary `podman exec -it` contract, and it is what a user
			// expects from every other terminal they have.
			//
			// WHAT THIS COSTS is the escape hatch: there is no longer a keystroke
			// that quits the launcher from outside the jail. Leaving is exiting the
			// shell, or the child exiting. ^Z still suspends the proxy (suspendkey.go),
			// and an explicit `kill -INT` still reaches the signal arm below, which is
			// why that arm stays.
			if len(pending) > 0 {
				data = append(pending, data...)
				pending = nil
			}
			// NOT a byte scan: a terminal asked for the kitty keyboard
			// protocol sends Ctrl-Z as an escape sequence holding no 0x1A at
			// all, and forwarding that is the wedge this package exists to
			// prevent (suspendkey.go, THE WEDGE OF 2026-09-10).
			start, stop, found := findSuspendKey(data)
			if !found {
				_, _, _ = enqueueToMaster(data, false)
				continue
			}
			if start > 0 {
				_, _, _ = enqueueToMaster(data[:start], false)
			}
			pending = append(pending, stripSuspendKeys(data[stop:])...)
			if obs.suspendBoundary != nil {
				func() {
					defer func() { _ = recover() }()
					obs.suspendBoundary(acceptedOffset)
				}()
			}
			pendingSuspend = true
			suspendOffset = acceptedOffset
		}
	}
}

func stripSuspendKeys(data []byte) []byte {
	var filtered []byte
	copiedUntil := 0
	for i := 0; i < len(data); {
		skip := 0
		if data[i] == suspByte {
			skip = 1
		} else if data[i] == esc {
			if n, matched := matchEscapedSuspend(data[i:]); matched {
				skip = n
			}
		}
		if skip == 0 {
			i++
			continue
		}
		if filtered == nil {
			filtered = make([]byte, 0, len(data))
		}
		filtered = append(filtered, data[copiedUntil:i]...)
		i += skip
		copiedUntil = i
	}
	if filtered == nil {
		return data
	}
	return append(filtered, data[copiedUntil:]...)
}

// termReset contains ANSI escape sequences that restore the terminal to a
// clean state without clearing the screen, altering scrollback, or moving the cursor:
//   - \x1b[0m: reset SGR attributes (colors, bold, underline, etc.)
//   - \x1b[?25h: show cursor (DECTCEM)
//   - \x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l: disable mouse tracking (X10, button-event, any-event, SGR)
//   - \x1b[?2004l: disable bracketed paste mode
//   - \x1b[?1l: normal cursor keys mode (DECCKM)
//   - \x1b>: normal keypad mode (DECKPNM)
//   - \x1b[?1004l: disable focus reporting
//   - \x1b[<u: pop the kitty keyboard protocol flags
//   - \x1b[>4;0m: disable xterm modifyOtherKeys
//
// THE LAST THREE ARE THE KEYSTROKE ONES, and they are why this constant exists
// in the shape it does rather than as "whatever `reset` sends". Under an enhanced
// keyboard protocol the terminal encodes every key as a CSI sequence, so a shell
// that inherits the mode echoes a burst of punctuation for each keypress — the
// state a user escapes by pasting `reset`. Claude Code turns one on (its own
// defaults carry `kittyKeyboard:!0`) and probes with `CSI ? u` and `CSI ? 6 n`,
// whose REPLY is `CSI ? <row> ; <col> R` — the stray `32;87` fragments that show
// up in the wreckage. None of the mouse or paste disables above touch any of it.
//
// It bites hardest on the ATTACH arm. A second terminal's `exec` dies when the
// container does, which is whenever the launching terminal is interrupted, and
// the agent inside it is killed with no chance to pop what it pushed. This
// process survives that, so it is the only thing that can.
//
// NOT A COLOR DECISION, so NO_COLOR does not gate it: the \x1b[0m here CLEARS attributes a
// child left set rather than adding any, and the convention (https://no-color.org) is about
// the second. Every other byte is terminal-mode restoration.
//
// ⚠ DELIBERATELY ABSENT: \x1b[?1049l (leave the alternate screen). restoreTerminal
// is also the Ctrl-Z path (selfSuspend), where dropping the alt screen would wipe
// the suspended program's display and `fg` would not bring it back. Screen state
// is the one thing this must not touch.
const termReset = "\x1b[0m\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?1l\x1b>" +
	"\x1b[?1004l\x1b[<u\x1b[>4;0m"

// resetTerminal sends escape sequences to restore cursor visibility, reset
// text attributes, and disable mouse tracking, bracketed paste, and application
// keypad/cursor modes. It writes to the first available terminal among stdout,
// stderr, and inFd.
var (
	terminalStdoutFD = 1
	terminalStderrFD = 2
)

func terminalResetFD(inFd int) int {
	switch {
	case isatty(terminalStdoutFD):
		return terminalStdoutFD
	case isatty(terminalStderrFD):
		return terminalStderrFD
	case isatty(inFd):
		return inFd
	default:
		return -1
	}
}

func resetTerminal(inFd int) {
	if fd := terminalResetFD(inFd); fd >= 0 {
		_, _ = unix.Write(fd, []byte(termReset))
	}
}

// restoreTerminal restores cooked termios on the host terminal and resets
// terminal modes (shows the cursor, resets attributes, and disables mouse
// tracking, bracketed paste, application cursor/keypad modes, focus reporting
// and the enhanced keyboard protocols).
// ownsTerminal reports whether this process's group is the terminal's foreground
// group — false once a ^Z has handed the terminal back to the shell. Reading the
// foreground group is allowed from the background; an fd that is not a terminal
// answers true, because a termios write to it cannot stop us.
func ownsTerminal(fd int) bool {
	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return true
	}
	return pgrp == unix.Getpgrp()
}

func restoreTerminal(inFd int, cooked *unix.Termios) {
	_ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked)
	resetTerminal(inFd)
}

// suspendAfterReset runs after the ordered terminal reset. Its lifecycle CAS is provisional;
// claimSuspendSignal is the final atomic ordering point before the targeted syscall. Neither
// that claim nor a successful syscall return proves that the kernel stopped this process.
func suspendAfterReset(inFd int, cooked *unix.Termios, sess *session, hook StageHook) (bool, error) {
	if sess == nil {
		if cooked != nil {
			_ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked)
		}
		if err := raiseStop(); err != nil {
			if cooked != nil {
				setRaw(inFd, cooked)
			}
			return false, err
		}
		callStage(hook, StageSuspended)
		return true, nil
	}

	sess.mu.Lock()
	if sess.state != sessionRunning || sess.cancellationPublished() || sess.childHasExited() {
		sess.mu.Unlock()
		return false, nil
	}
	if cooked != nil {
		_ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked)
	}
	sess.suspended = true
	if sess.beforeSuspend != nil {
		sess.beforeSuspend()
	}
	if sess.cancellationPublished() || sess.childHasExited() {
		if sess.childHasExited() && sess.ownsTTY(inFd) {
			setRaw(inFd, cooked)
			sess.suspended = false
		}
		sess.mu.Unlock()
		return false, nil
	}
	if !sess.commitSuspendStop() {
		if sess.ownsTTY(inFd) {
			setRaw(inFd, cooked)
			sess.suspended = false
		}
		sess.mu.Unlock()
		return false, nil
	}
	sess.mu.Unlock()

	// This test seam is deliberately before the final claim. Cancellation/reaping publish
	// without this transition lock, so either event can deterministically revoke the claim.
	if sess.afterSuspendCommit != nil {
		func() {
			defer func() { _ = recover() }()
			sess.afterSuspendCommit()
		}()
	}
	if !sess.claimSuspendSignal() {
		sess.mu.Lock()
		if sess.state == sessionRunning && sess.suspended && !sess.cancellationPublished() && sess.ownsTTY(inFd) {
			setRaw(inFd, cooked)
			sess.suspended = false
		}
		sess.mu.Unlock()
		return false, nil
	}

	// No callback, mutex, or wait between the final claim and this targeted attempt. The
	// process is not holding the terminal-transition mutex while SIGTSTP can stop it.
	err := raiseStop()
	if err != nil {
		sess.clearSuspendStop()
		sess.mu.Lock()
		if sess.state == sessionRunning && sess.suspended && !sess.cancellationPublished() && sess.ownsTTY(inFd) {
			setRaw(inFd, cooked)
			sess.suspended = false
		}
		sess.mu.Unlock()
		return false, err
	}
	// The syscall returned only after an ignored signal or a continuation; this marker is
	// not proof of the kernel stop, which tests observe independently from the owned process.
	callStage(hook, StageSuspended)
	sess.mu.Lock()
	if sess.state == sessionRunning && sess.suspended && !sess.cancellationPublished() && sess.ownsTTY(inFd) {
		// SIGTSTP was ignored, or a foreground continuation raced the signal arm.
		sess.suspended = false
		setRaw(inFd, cooked)
		sess.clearSuspendStop()
	}
	sess.mu.Unlock()
	return true, nil
}

// selfSuspend restores cooked termios and resets the terminal, then raises
// SIGTSTP on OUR pid only — TARGETED, never pgroup-wide (that would stop podman).
func selfSuspend(inFd int, cooked *unix.Termios) {
	restoreTerminal(inFd, cooked)
	// SIGTSTP with the DEFAULT disposition stops us; the shell prints
	// "[1]+ Stopped" and `fg` later sends SIGCONT (handled -> re-raw).
	_ = raiseStop()
	// Control returns here after SIGCONT.
}

// raiseStop sends SIGTSTP to this process — TARGETED, never pgroup-wide — aimed at the
// calling THREAD, so control returns past it only once the process was stopped and
// continued, or the signal was ignored. Every ^Z stop goes through it.
//
// ⚠ NOT kill(getpid(), SIGTSTP). A process-directed signal is taken by the thread-group
// leader whenever the leader does not block it, and the Go thread that raised it returns
// from kill() and runs on until the leader's group stop reaches it. suspendAfterReset reads
// "the syscall returned" as "ignored or continued" and re-raws the terminal, so the proxy
// stopped with the shell's terminal left raw (CI run 41138698250). The kernel acts on a
// signal pending on the current thread before that thread returns to user code.
func raiseStop() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return unix.Tgkill(unix.Getpid(), unix.Gettid(), unix.SIGTSTP)
}

func safeCallback(cb func(*os.Process), p *os.Process) {
	defer func() { _ = recover() }()
	cb(p)
}

func setRaw(fd int, cooked *unix.Termios) {
	raw := *cooked
	// cfmakeraw equivalent.
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	_ = unix.IoctlSetTermios(fd, unix.TCSETS, &raw)
}

// isatty is the shared terminal probe (internal/tty, a TCGETS ioctl here), named for the
// int descriptors this file works in. Keep it a call into internal/tty: this file once held
// a private copy of the ioctl that survived the probe's first unification
// (docs/reference/cli-color.md).
func isatty(fd int) bool { return tty.IsTerminal(uintptr(fd)) }

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// openPty opens a new pty master/slave pair (no setsid — setsid broke
// `podman -it`).
func openPty() (master, slave int, err error) {
	master, err = unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return -1, -1, err
	}
	// grantpt + unlockpt: unlock the slave.
	var unlock int
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCSPTLCK,
		uintptr(unsafe.Pointer(&unlock))); e != 0 {
		unix.Close(master)
		return -1, -1, e
	}
	// TIOCGPTN: get the pty number.
	var ptn uint32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCGPTN,
		uintptr(unsafe.Pointer(&ptn))); e != 0 {
		unix.Close(master)
		return -1, -1, e
	}
	slavePath := ptsPath(ptn)
	slave, err = unix.Open(slavePath, unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		unix.Close(master)
		return -1, -1, err
	}
	return master, slave, nil
}

func ptsPath(n uint32) string {
	return "/dev/pts/" + uitoa(uint64(n))
}

func uitoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
