// Package notty runs a command with NO CONTROLLING TERMINAL and /dev/null on its stdin, which is
// how yolo runs every vendor installer (docs/design/provisioner-sets.md PS-D1).
//
// # Why both halves
//
// A vendor installer that has to survive `curl | sh` reads its questions from /dev/tty rather
// than from stdin, so a /dev/null stdin alone does not stop it asking: codex's installer asked
// `Start Codex now? [y/N]` on /dev/tty mid-probe, measured on a Mac
// (docs/plans/runbooks/mac-provisioner-measurements.md). /dev/tty is the process's controlling
// terminal, so the child is started in a session of its own (setsid), which has none: opening
// /dev/tty then fails, and a prompt either takes its default or fails. The accepted cost is
// PS-D1's: an installer that needs an answer fails instead of asking.
//
// Its stdout and stderr are the caller's, so the installer's progress still reaches whoever
// is watching; writing to a terminal does not need it to be the controlling one.
//
// # Signals
//
// A child in its own session is out of the terminal's foreground process group, so a Ctrl-C at
// the terminal no longer reaches it. Run forwards the interrupt, terminate and hangup signals
// this process receives to the child's process group while it waits, so stopping yolo still
// stops the installer rather than leaving it running unattended.
//
// AND A STOP IS STILL A STOP. This process catches those signals to forward them, so it no longer
// dies of them, and a caller that reads only an exit status sees "the installer failed" where it
// used to see "I was stopped". Run therefore reports a child that died of a signal it forwarded as
// Stopped. A caller that runs more work afterwards stops there (the host's dependency gate), and a
// process whose only job is the child (`yolo internal no-terminal`) ends with WrapperExit, which
// dies of the same signal, because the shell that started it tells the two apart by exactly that:
// bash waiting on a command when a Ctrl-C arrives abandons its script only when the command died of
// SIGINT, and goes on to the next line when the command exited, even with status 130.
//
// # The bound
//
// RunBounded adds a wall-clock bound, which is how the jail's launchers run an agent's update
// (docs/design/program-delivery.md §3.5): SIGTERM once Bound.Timeout has passed, and SIGKILL once
// the child has outlived that, or a forwarded signal, by Bound.KillAfter. Both go to the child's
// whole process group, each signal followed by a SIGCONT, because a STOPPED process acts on
// nothing but SIGKILL and SIGCONT: its SIGTERM waits, pending, for something to continue it. The
// SIGKILL goes to the group even when the child itself exited in time, so a grandchild that
// ignored the SIGTERM does not outlive the bound; RunBounded returns once the group is empty or
// the grace is over. A process that left the group (setsid, setpgid) is out of its reach. That
// is GNU timeout(1)'s `-k` semantics, built here rather than borrowed, for two reasons. A stock
// macOS has no timeout(1). And timeout(1) caused the 2026-10-01 hang: it runs its command in a
// process group of its own on the user's terminal, so `claude install` switching that terminal to
// raw mode was stopped by SIGTTOU before it did anything, and a Ctrl-C at the terminal never
// reached it. A child of this package has no terminal to be stopped by.
package notty

import (
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Prepare sets c to start with no controlling terminal and a /dev/null stdin. It keeps whatever
// else c's SysProcAttr says. Run calls it; a caller that must Start c itself calls it first.
func Prepare(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setsid = true
	// Setctty would give the new session a terminal back; a caller's leftover must not.
	c.SysProcAttr.Setctty = false
	// A nil Stdin is /dev/null (os/exec), and a caller's stdin must not survive either.
	c.Stdin = nil
}

// forwarded is the set of signals Run passes on to the child's process group. Each one's Go
// default is to end the process, which WrapperExit relies on to die of it.
var forwarded = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}

// Stopped is Run's error for a child that died of a signal Run received and forwarded to it: what
// stopped this process stopped the child too. It wraps the child's *exec.ExitError, so ExitCode and
// errors.As read through it.
type Stopped struct {
	// Signal is the signal the child died of, which this process received.
	Signal syscall.Signal
	// Err is the child's exit error.
	Err error
}

func (s *Stopped) Error() string { return s.Err.Error() }
func (s *Stopped) Unwrap() error { return s.Err }

// Run prepares c (Prepare), starts it, forwards the signals above to its process group until it
// exits, and returns c.Wait's error: nil on a zero exit, a *Stopped when the child died of a
// signal Run forwarded, and otherwise the *exec.ExitError. It is RunBounded with no bound.
func Run(c *exec.Cmd) error { return RunBounded(c, Bound{}) }

// Bound limits how long RunBounded lets the child live. The zero Bound is no limit at all.
type Bound struct {
	// Timeout is how long the child may run before its process group is sent SIGTERM. Zero is
	// no timeout.
	Timeout time.Duration
	// KillAfter is how long the child, and every process left in its process group, may outlive
	// that SIGTERM, or a signal RunBounded forwarded to it, before the group is sent SIGKILL.
	// Zero never kills.
	KillAfter time.Duration
}

// ExitTimedOut is the status a wrapper exits with when its child ran out of time: GNU timeout(1)'s,
// so a caller that already reads 124 as "timed out" reads this the same.
const ExitTimedOut = 124

// TimedOut is RunBounded's error for a child still running when its Bound.Timeout passed. It wraps
// the child's own exit error (nil when it exited 0 after the SIGTERM).
type TimedOut struct {
	// After is the timeout that passed.
	After time.Duration
	// Killed says the child outlived the SIGTERM too, and was killed after Bound.KillAfter.
	Killed bool
	// Err is the child's exit error, nil for a zero exit.
	Err error
}

func (t *TimedOut) Error() string {
	if t.Killed {
		return fmt.Sprintf("timed out after %s, and killed when it outlived the SIGTERM", t.After)
	}
	return fmt.Sprintf("timed out after %s", t.After)
}
func (t *TimedOut) Unwrap() error { return t.Err }

// outcome is what the watcher saw while the child ran.
type outcome struct {
	seen     map[syscall.Signal]bool
	first    syscall.Signal // the first signal forwarded, 0 for none
	timedOut bool
	killed   bool
}

// RunBounded is Run under b: it prepares c (Prepare), starts it, and until it exits forwards the
// signals above to its process group and enforces b (see Bound and the package comment). It
// returns nil on a zero exit; a *Stopped when the child died of a signal RunBounded forwarded, or
// was killed after outliving one by b.KillAfter; a *TimedOut when b.Timeout passed first; and
// otherwise c.Wait's error.
func RunBounded(c *exec.Cmd, b Bound) error {
	Prepare(c)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, forwarded...)
	defer signal.Stop(sigs)
	if err := c.Start(); err != nil {
		return err
	}
	// The child leads its own session, so its pid is its process group.
	group := -c.Process.Pid
	var deadline *time.Timer
	if b.Timeout > 0 {
		deadline = time.NewTimer(b.Timeout)
		defer deadline.Stop()
	}
	done := make(chan struct{})
	result := make(chan outcome, 1)
	go func() {
		o := outcome{seen: map[syscall.Signal]bool{}}
		var expired <-chan time.Time
		if deadline != nil {
			expired = deadline.C
		}
		var kill *time.Timer
		var killNow <-chan time.Time
		// signalGroup sends sig, then SIGCONT so a stopped child can act on it, and starts the
		// grace once: the FIRST signal is when the child's time to exit begins.
		signalGroup := func(sig syscall.Signal) {
			_ = syscall.Kill(group, sig)
			_ = syscall.Kill(group, syscall.SIGCONT)
			if b.KillAfter > 0 && kill == nil {
				kill = time.NewTimer(b.KillAfter)
				killNow = kill.C
			}
		}
		for {
			select {
			case s := <-sigs:
				if sig, ok := s.(syscall.Signal); ok {
					// Recorded BEFORE it is sent, so a child that dies of it is always found here.
					o.seen[sig] = true
					if o.first == 0 {
						o.first = sig
					}
					signalGroup(sig)
				}
			case <-expired:
				expired = nil
				o.timedOut = true
				signalGroup(syscall.SIGTERM)
			case <-killNow:
				killNow = nil
				o.killed = true
				_ = syscall.Kill(group, syscall.SIGKILL)
			case <-done:
				// THE BOUND COVERS THE GROUP, NOT JUST THE COMMAND. Once a signal went out, a
				// member the command started can outlive it: a grandchild that ignores SIGTERM
				// stays behind when its parent dies of it, still writing to the install prefix
				// after the launcher has dropped its lock. So until the grace runs out, wait for
				// the group to empty, and SIGKILL whatever of it is left then. A process that
				// left the group (setsid, setpgid) is out of reach, as it is for timeout(1).
				killed := o.killed
				for killNow != nil && groupAlive(group) {
					select {
					case s := <-sigs:
						if sig, ok := s.(syscall.Signal); ok {
							signalGroup(sig)
						}
					case <-killNow:
						killNow = nil
						killed = true
						_ = syscall.Kill(group, syscall.SIGKILL)
					case <-time.After(20 * time.Millisecond):
					}
				}
				// A SIGKILL is not synchronous, and a member is gone only once its parent (by now
				// init) has reaped it. A moment for that, so the caller that releases its lock
				// next releases it on an empty group.
				for wait := time.Now().Add(time.Second); killed && groupAlive(group) && time.Now().Before(wait); {
					time.Sleep(10 * time.Millisecond)
				}
				if kill != nil {
					kill.Stop()
				}
				result <- o
				return
			}
		}
	}()
	err := c.Wait()
	close(done)
	o := <-result
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && o.seen[ws.Signal()] {
			return &Stopped{Signal: ws.Signal(), Err: err}
		}
	}
	if o.timedOut {
		return &TimedOut{After: b.Timeout, Killed: o.killed, Err: err}
	}
	// Killed for outliving a forwarded signal: what stopped this process stopped the child.
	if o.killed && o.first != 0 && err != nil {
		return &Stopped{Signal: o.first, Err: err}
	}
	return err
}

// groupAlive says whether any process is still in the process group whose id is -group: signal 0
// checks without sending. EPERM is a member this process may not signal, which is still a member.
func groupAlive(group int) bool {
	err := syscall.Kill(group, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// WrapperExit is how a process whose whole job is running the child ends after Run returned err
// (`yolo internal no-terminal`, the jail launcher's installer run). For a Stopped child it does not
// return: it dies of the same signal, so the shell that started it sees what it would have seen
// running the child itself (the package comment says why that is the difference between a Ctrl-C
// and "carry on"). Otherwise, or if the signal cannot end this process (one it was started with
// ignored), it returns ExitCode(err) for the caller to exit with.
func WrapperExit(err error) int {
	var to *TimedOut
	if errors.As(err, &to) {
		return ExitTimedOut
	}
	var st *Stopped
	if errors.As(err, &st) {
		// Every Notify for it is undone, so the runtime's default, ending the process by the
		// signal, is what the kill meets.
		signal.Reset(st.Signal)
		if kerr := syscall.Kill(os.Getpid(), st.Signal); kerr == nil {
			// Delivery to a multi-threaded process is asynchronous; it lands within this wait.
			time.Sleep(2 * time.Second)
		}
	}
	return ExitCode(err)
}

// ExitCode is the status a wrapper should exit with after Run returned err: 0 for nil, the
// child's own status for an exit, 128+N for a death by signal N (the shell's convention),
// ExitTimedOut for a *TimedOut, and 127 when the command could not be started at all.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var to *TimedOut
	if errors.As(err, &to) {
		return ExitTimedOut
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 127
}

// Main is the whole `yolo internal <verb> [--timeout=SECONDS] [--kill-after=SECONDS] -- <command>
// [args...]` verb: it runs the command with no controlling terminal and a /dev/null stdin, its
// stdout and stderr this process's, under the bound the flags give (none without them), and
// returns the status to exit with (WrapperExit, which for a command stopped by a signal this verb
// forwarded does not return but dies of that signal). 2 is misuse, 127 a command that could not
// start. It lives here rather than beside the verb's dispatch so that a test can run exactly what
// the verb runs, the launchers' tests included.
func Main(verb string, args []string) int {
	usage := "usage: yolo internal " + verb + " [--timeout=SECONDS] [--kill-after=SECONDS] -- <command> [args...]"
	var b Bound
	i := 0
	for ; i < len(args) && args[i] != "--"; i++ {
		var dst *time.Duration
		var val string
		switch {
		case strings.HasPrefix(args[i], "--timeout="):
			dst, val = &b.Timeout, strings.TrimPrefix(args[i], "--timeout=")
		case strings.HasPrefix(args[i], "--kill-after="):
			dst, val = &b.KillAfter, strings.TrimPrefix(args[i], "--kill-after=")
		default:
			fmt.Fprintln(os.Stderr, usage)
			return 2
		}
		d, ok := seconds(val)
		if !ok {
			fmt.Fprintf(os.Stderr, "yolo internal %s: %q is not a number of seconds\n%s\n", verb, args[i], usage)
			return 2
		}
		*dst = d
	}
	if i+1 >= len(args) {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	c := exec.Command(args[i+1], args[i+2:]...)
	c.Stdout, c.Stderr = os.Stdout, os.Stderr
	err := RunBounded(c, b)
	var ee *exec.ExitError
	var to *TimedOut
	if err != nil && !errors.As(err, &ee) && !errors.As(err, &to) {
		fmt.Fprintf(os.Stderr, "yolo internal %s: %v\n", verb, err)
	}
	return WrapperExit(err)
}

// seconds reads a flag's value: a non-negative, finite number of seconds, fractions allowed.
func seconds(v string) (time.Duration, bool) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || math.IsInf(f, 0) || math.IsNaN(f) || f > float64(math.MaxInt64)/float64(time.Second) {
		return 0, false
	}
	return time.Duration(f * float64(time.Second)), true
}
