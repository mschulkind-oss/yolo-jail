package run

// jailmain.go is the launcher's half of a container jail whose MAIN PROCESS is a hold and
// whose every session enters by exec (entrypoint/jailmain.go is the jail's half;
// docs/design/jail-lifetime-last-session-wins.md §4.1, step 2 of its §7).
//
// A fresh launch used to run `<rt> run -it … yolo-entrypoint '<the first session's command>'`
// under the TTY proxy, so the first session was the container's pid 1 and its runtime client
// held the terminal. Now it runs three things in order:
//
//  1. THE MAIN PROCESS, `<rt> run … yolo-entrypoint --yolo-hold-main '<the stage>'`, as a child
//     with no terminal and in a process group of its own, so nothing typed at the terminal and
//     no signal the terminal's job gets reaches its client (startJailMain). Its stdout is the
//     launcher's; its stderr is relayed line by line until BootReadyLine, which is not printed,
//     so the boot reads on the terminal as it always did (relayUntilReady).
//  2. THE FIRST SESSION, `<rt> exec -i [-t] <cname> yolo-entrypoint --yolo-first-session
//     '<command>'`, under the TTY proxy, which takes the terminal (firstSessionExecCmd). Its
//     exit status is the launch's, as the container's was.
//  3. THE END: the main process follows its first session out, and the launcher waits for its
//     client to exit, stopping the jail itself only when it did not (awaitJailMainEnd). Then
//     today's teardown chain, unchanged.
//
// WHAT A USER SEES DOES NOT CHANGE, by design: this launch still owns the jail's host services
// and still ends the jail with its own session. What changes underneath is that no session is
// the container's main process any more, which is what the design's keeper needs to let the
// first terminal go.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

// safeOnStarted runs a fresh launch's onStarted on its own goroutine's behalf, and keeps a
// panic in it from taking the launch down with the jail running, as the proxy's own
// callback runner does (ttyproxy.safeCallback).
func safeOnStarted(onStarted func(*os.Process), p *os.Process) {
	defer func() { _ = recover() }()
	onStarted(p)
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
// environment, so it already carries the launch's own (noColorEnvArgs).
func (o *Options) firstSessionExecCmd(rt, cname, command string) []string {
	argv := []string{rt, "exec", "-i"}
	if o.IsTTYStdout() {
		argv = append(argv, "-t")
	}
	return append(argv, cname, JailEntrypointPath, entrypoint.FirstSessionArg, command)
}

// jailMainEndGrace is how long the launcher waits, after its first session has returned, for
// the main process to follow it out on its own (the hold polls the first session every 100 ms)
// before it stops the jail itself. A var so a test need not wait it out.
var jailMainEndGrace = 10 * time.Second

// awaitJailMainEnd ends the jail with this launch's own session, as the launch always has:
// the hold normally exits by itself within a poll of the first session's end. When it has not
// within jailMainEndGrace and the container is still running — an exec client that died while
// its session ran on, or a first session that never registered — the launcher stops it. Then
// it waits for the client, as the proxy used to wait for it: the client's lingering exit after
// its container is gone is Window A, and it is measured, not cut short.
func (o *Options) awaitJailMainEnd(m *jailMain, cname, rt string) {
	select {
	case <-m.exited:
		return
	case <-time.After(jailMainEndGrace):
	}
	if o.findRunningContainer(cname, rt) != "" {
		o.stopJail(cname, rt)
	}
	<-m.exited
}

// launchSignalArm is the fresh launch's signal arm for the stretch the TTY proxy does not
// cover: from the main process's start until the first session's exec takes the terminal, and
// through that exec when stdin is no terminal (the proxy's plain path installs no arm). A
// SIGINT, SIGHUP or SIGTERM runs the launch's own teardown (onTerminate, then the embedded
// pack tree's release, as the proxy's arm does) and exits 128+N, which is what the proxy's arm
// did when it held the terminal from the container's start.
type launchSignalArm struct {
	mu          sync.Mutex
	signals     chan os.Signal
	done        chan struct{}
	proxyArms   bool
	deferred    bool // the proxy's own arm has the terminal
	stopped     bool
	terminating bool
}

// armLaunchSignals installs the arm. proxyArms says the proxy will install its own arm when
// it takes the terminal (a terminal on stdin), so this one steps aside then (handOff).
func armLaunchSignals(onTerminate func(), proxyArms bool) *launchSignalArm {
	return armLaunchSignalsWith(onTerminate, proxyArms, os.Exit)
}

// armLaunchSignalsWith is armLaunchSignals with the process exit as a parameter, so a test can
// drive the arm to its end without ending the test binary.
func armLaunchSignalsWith(onTerminate func(), proxyArms bool, exit func(int)) *launchSignalArm {
	a := &launchSignalArm{signals: make(chan os.Signal, 4), done: make(chan struct{}),
		proxyArms: proxyArms}
	signal.Notify(a.signals, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM)
	terminate := func() {
		if onTerminate != nil {
			onTerminate()
		}
		// The process's embedded pack tree, which the exit below would otherwise leak, for the
		// reason the proxy's arm releases it (proxy_linux.go's withEmbeddedRelease).
		packload.ReleaseEmbedded()
	}
	go func() {
		for {
			select {
			case s := <-a.signals:
				a.mu.Lock()
				if a.deferred || a.stopped || a.terminating {
					a.mu.Unlock()
					continue
				}
				a.terminating = true
				a.mu.Unlock()
				terminate()
				exit(128 + int(s.(syscall.Signal)))
				return
			case <-a.done:
				return
			}
		}
	}()
	return a
}

// handOff is called before the proxy takes the terminal: when the proxy installs its own arm,
// this one lets every signal through to it and does nothing.
func (a *launchSignalArm) handOff() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.proxyArms {
		a.deferred = true
	}
}

// disarm ends the arm, after which a signal has its default effect, as it always had once the
// proxy returned. It returns false when the arm has already begun the teardown: the caller —
// the launch's own goroutine, woken because that teardown stopped the jail — must then leave
// the rest to the arm, which exits the process, rather than run the teardown a second time.
// Idempotent.
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
	signal.Stop(a.signals)
	close(a.done)
	return true
}
