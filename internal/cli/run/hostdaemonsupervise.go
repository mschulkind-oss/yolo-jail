package run

// hostdaemonsupervise.go is the HOST-SIDE SUPERVISOR of a jail's host daemons
// (docs/design/host-daemon-ownership.md OQ-HD5, ruled 2026-10-15, and HD-D25): the process that
// spawned a host daemon — the jail's keeper, the macos-user keeperless arm, `yolo host`'s
// doorways, all through startExternalService — watches it, and a death it did not cause is
// reported and restarted, bounded.
//
// WHAT A RESTART IS: the same argv, the same environment, the same socket or upstream path and
// the same log, after the stale artifact the dead process left is removed, then the same
// readiness wait the first spawn passed. The jail's front is not touched: it dials the daemon's
// socket per connection (svcendpoint's front), so the jail is served again from its first request
// after the restart, and a self-publishing daemon's endpoint file is re-read by its clients per
// dial. The first spawn's startup-reason channel is a launch's diagnostic and is not reopened.
//
// THE POLICY (HD-D25) is the in-jail supervisor's backoff, its two constants exported so one
// policy governs both sides (supervisor.RestartBackoffInitial, RestartBackoffMax): 1 s doubling to
// a 30 s ceiling. Bounded where the jail side is not: five restarts in a row (after 1, 2, 4, 8 and
// 16 s, the steps the backoff takes before its ceiling), and the death after the fifth is a
// GIVE-UP, a death that stands, reported naming the daemon and its log; the front then behaves as
// mode 1 has it. A daemon that stays up longer than the ceiling resets the count. A restart that
// fails its readiness wait is a death like any other.
//
// NOT A DEATH, and never restarted:
//   - a teardown's own stop, the detached straggler included (stop marks the supervisor stopping,
//     under its lock, before any signal, so a restart in flight either never spawns or spawns the
//     process the teardown then stops);
//   - the deliberate exit a credential daemon makes when its state directory is retired under it
//     (hostservice.ExitStateDirRetired, HD-D3): reported once and left down, since a restart would
//     recreate the directory a launch just archived. A daemon that does not use the status is
//     simply restarted;
//   - a clean exit that leaves the service reachable: the daemonizing wrapper the readiness wait
//     accepts, whose service lives on in a child this process cannot see. Supervision ends there,
//     as the keeper's watch always did;
//   - a stop by `yolo host-daemon stop` (or `yolo broker stop`), which writes a marker beside the
//     copy's front socket naming the pids it signals before it signals them
//     (hostservice.WriteStopMarker). A death whose pid the marker names is reported once, not
//     restarted, and ends supervision with the service down: stop means stop. Every spawn at the
//     socket removes a marker an earlier copy left, so none outlives the copy it was written for.
//
// WHERE IT IS SAID (an implementation decision under HD-D25 (4)): every death, restart and give-up
// is one line in the daemon's host log (logs/host-service-<name>.log) and one in the workspace's
// launch.log, and mid-session NOT on the terminal, where the agent's interface owns the screen and
// a line would land inside it. launchLogOnly picks the launch.log half of the launch's writer. A
// death before the keeper's ready still reaches the launch's terminal through the keeper's progress
// pipe, as every keeper line does then. A give-up, a retired exit and the verb's stop close the
// handle's end, so the keeper records them down (recordServiceDown) and tells each session; a
// restarted death is not down and is not recorded.
//
// NOTHING SUPERVISES THE SUPERVISOR (OQ-HD5): if this yolo process dies, its daemons' deaths stand
// until the next launch, which spawns its own.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

// daemonRestartPolicy is HD-D25's bound: the backoff's first wait and its ceiling, and how many
// restarts in a row are made before a death is a give-up.
type daemonRestartPolicy struct {
	initial  time.Duration
	ceiling  time.Duration
	restarts int
}

// hostDaemonRestartLimit is HD-D25 (2)'s five: the restarts the backoff takes from its first wait
// to its ceiling (1, 2, 4, 8, 16 s).
const hostDaemonRestartLimit = 5

// hostDaemonRestarts is the policy every supervised host daemon takes, read once at its spawn. A
// variable only so this package's tests can run the same policy on a millisecond scale
// (fastHostDaemonRestarts); production never writes it.
var hostDaemonRestarts = daemonRestartPolicy{
	initial:  time.Duration(supervisor.RestartBackoffInitial * float64(time.Second)),
	ceiling:  time.Duration(supervisor.RestartBackoffMax * float64(time.Second)),
	restarts: hostDaemonRestartLimit,
}

// daemonSupervisor watches one spawned host daemon for the life of its handle.
type daemonSupervisor struct {
	name    string
	logPath string
	policy  daemonRestartPolicy
	// start spawns a fresh process with the first spawn's argv, env and log. It runs under mu, so a
	// stop either comes first (and nothing spawns) or sees the process it spawned.
	start func() (*exec.Cmd, <-chan struct{}, error)
	// ready is the first spawn's readiness wait for a restarted process: "" once it serves, a
	// failure clause otherwise. It ends early when stopCh closes, and restart then reports the
	// stop, not the clause.
	ready func(cmd *exec.Cmd, exited <-chan struct{}) string
	// abandon ends a restarted process that failed its readiness wait, as the first spawn's
	// failure path does (detach or kill).
	abandon func(cmd *exec.Cmd)
	// wrapperExit reports whether a clean exit left the service reachable: the daemonizing wrapper.
	wrapperExit func(cmd *exec.Cmd) bool
	// say writes one line to the daemon's host log and the launch's log (sayTo).
	say func(line string)
	// stoppedByVerb reports, and consumes, the marker `yolo host-daemon stop` leaves for the pid
	// it signals (hostservice.TakeStopMarker). nil for a daemon the verb never stops.
	stoppedByVerb func(pid int) bool

	mu       sync.Mutex
	cmd      *exec.Cmd
	exited   <-chan struct{}
	upSince  time.Time
	stopping bool
	stopCh   chan struct{}

	done chan struct{}
	// inputUnknown ends the private-input observer without cleanup if a wrapper
	// leaves an unobservable child serving after the wrapper itself exits.
	inputUnknown     chan struct{}
	inputUnknownOnce sync.Once
	endOnce          sync.Once
	how              string
}

// newDaemonSupervisor is the supervisor of a daemon that has passed its first readiness wait.
func newDaemonSupervisor(name, logPath string, cmd *exec.Cmd, exited <-chan struct{}) *daemonSupervisor {
	return &daemonSupervisor{name: name, logPath: logPath, policy: hostDaemonRestarts,
		cmd: cmd, exited: exited, upSince: time.Now(), stopCh: make(chan struct{}),
		done: make(chan struct{})}
}

// end is the serviceEnd the keeper watches: it closes once supervision has ended with the service
// down (a give-up, a retired exit) or stopped by the teardown, never for a death it restarted.
func (s *daemonSupervisor) end() serviceEnd {
	return serviceEnd{done: s.done, how: func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.how
	}}
}

// inputExitObserved closes only after supervision ends AND the process current at
// that end has been reaped. In a restart readiness wait, done can close before
// the detached process exits; the first process's exited channel cannot stand
// in for the restarted one. Unknown closes instead when a wrapper's child exit
// cannot be observed, so neither observer waits forever for that wrapper.
func (s *daemonSupervisor) inputExitObserved() (observed, unknown <-chan struct{}) {
	closed := make(chan struct{})
	unobservable := make(chan struct{})
	s.inputUnknown = unobservable
	go func() {
		select {
		case <-s.done:
		case <-unobservable:
			return
		}
		s.mu.Lock()
		exited := s.exited
		s.mu.Unlock()
		if exited == nil {
			return
		}
		<-exited
		select {
		case <-unobservable:
			return
		default:
		}
		close(closed)
	}()
	return closed, unobservable
}

// finish ends supervision, once, with how as the end's account.
func (s *daemonSupervisor) finish(how string) {
	s.endOnce.Do(func() {
		s.mu.Lock()
		s.how = how
		s.mu.Unlock()
		close(s.done)
	})
}

func (s *daemonSupervisor) markInputUnknown() {
	if s.inputUnknown != nil {
		s.inputUnknownOnce.Do(func() { close(s.inputUnknown) })
	}
}

// stop is the teardown's stop: it marks the supervisor stopping BEFORE any signal, then hands
// whichever process is current to signal (stopService: detach or kill). A restart in flight
// either sees the mark and spawns nothing, or spawned under the lock before it, in which case
// signal gets that process.
//
// The current process is handed over even when it is already reaped (a backoff wait, a give-up,
// a daemonizing wrapper's exit): the kill path signals its GROUP, which may still hold a child
// the leader left. stopService's detach path, which signals the bare pid, is the one that skips a
// reaped process.
func (s *daemonSupervisor) stop(signal func(cmd *exec.Cmd, exited <-chan struct{})) {
	s.mu.Lock()
	if !s.stopping {
		s.stopping = true
		close(s.stopCh)
	}
	cmd, exited := s.cmd, s.exited
	s.mu.Unlock()
	signal(cmd, exited)
}

func (s *daemonSupervisor) isStopping() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopping
}

// wait sleeps d, and reports false when a stop ends the wait first.
func (s *daemonSupervisor) wait(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-s.stopCh:
		return false
	}
}

// logClause names the daemon's log for a line, or nothing when its log could not be opened.
func (s *daemonSupervisor) logClause() string {
	if s.logPath == "" {
		return ""
	}
	return " Its log: " + s.logPath
}

// stoppedByTeardown is the end's account of a teardown's own stop, which the keeper never records.
const stoppedByTeardown = "stopped by its jail's end"

// stoppedByVerbAccount is the end's account of a stop by `yolo host-daemon stop`, which the keeper
// records as the service down, as it does a give-up.
const stoppedByVerbAccount = "it was stopped by `yolo host-daemon stop`, and is not restarted"

// run is the supervision loop, from the first spawn's ready to supervision's end.
func (s *daemonSupervisor) run() {
	restarts := 0
	backoff := s.policy.initial
	for {
		s.mu.Lock()
		cmd, exited, upSince := s.cmd, s.exited, s.upSince
		s.mu.Unlock()
		<-exited
		if s.isStopping() {
			// A daemonizing wrapper can exit before teardown marks stopping.
			// Its child is not this reaped process: do not remove its input.
			if st := cmd.ProcessState; st != nil && st.Success() && s.wrapperExit != nil && s.wrapperExit(cmd) {
				s.markInputUnknown()
			}
			s.finish(stoppedByTeardown)
			return
		}
		if cmd.Process != nil && s.endIfStoppedByVerb(cmd.Process.Pid) {
			return
		}
		if st := cmd.ProcessState; st != nil && st.Success() && s.wrapperExit(cmd) {
			if s.inputUnknown != nil {
				s.markInputUnknown()
			}
			return // the daemonizing wrapper: its service lives on in a child no one here can see
		}
		how := "its process " + exitPhrase(cmd)
		if st := cmd.ProcessState; st != nil && st.ExitCode() == hostservice.ExitStateDirRetired {
			s.say(fmt.Sprintf("host service '%s' stopped: its state directory was retired; not "+
				"restarted, since a restart would recreate it. The next launch that selects its "+
				"pack starts a fresh one.", s.name))
			s.finish("it stopped because its state directory was retired, and is not restarted")
			return
		}
		// UP LONGER THAN THE CEILING: a death after a long life is not a crash loop, so the count
		// and the backoff start again.
		if time.Since(upSince) > s.policy.ceiling {
			restarts, backoff = 0, s.policy.initial
		}
		for {
			if restarts >= s.policy.restarts {
				s.say(fmt.Sprintf("host service '%s' died (%s) after %d restarts in a row; giving up: "+
					"it stays down, and what uses it fails, until this launch ends and the next one "+
					"starts it.%s", s.name, how, restarts, s.logClause()))
				s.finish(fmt.Sprintf("%s, and its supervisor gave up after %d restarts in a row", how, restarts))
				return
			}
			s.say(fmt.Sprintf("host service '%s' died mid-session (%s); restarting it in %s "+
				"(restart %d of %d).%s", s.name, how, backoff, restarts+1, s.policy.restarts, s.logClause()))
			if !s.wait(backoff) {
				s.finish(stoppedByTeardown)
				return
			}
			restarts++
			backoff = min(backoff*2, s.policy.ceiling)
			failure, pid, stopped := s.restart()
			if stopped {
				s.finish(stoppedByTeardown)
				return
			}
			// A restart the verb stopped inside its readiness wait fails that wait; the stop is
			// still a stop, not a failed restart.
			if failure != "" && pid != 0 && s.endIfStoppedByVerb(pid) {
				return
			}
			if failure == "" {
				s.mu.Lock()
				pid := s.cmd.Process.Pid
				s.mu.Unlock()
				s.say(fmt.Sprintf("host service '%s' restarted (pid %d); the jail is served again from "+
					"its next request", s.name, pid))
				break
			}
			how = "its restart " + failure
		}
	}
}

// endIfStoppedByVerb ends supervision, saying so, when the death of pid was a stop by `yolo
// host-daemon stop` (its marker names pid), and reports whether it did.
func (s *daemonSupervisor) endIfStoppedByVerb(pid int) bool {
	if s.stoppedByVerb == nil || !s.stoppedByVerb(pid) {
		return false
	}
	s.say(fmt.Sprintf("host service '%s' was stopped by `yolo host-daemon stop`; not "+
		"restarted. What uses it fails until its jail is launched again, which starts a "+
		"fresh copy.%s", s.name, s.logClause()))
	s.finish(stoppedByVerbAccount)
	return true
}

// restart spawns the daemon again and waits for it, as the first spawn did. pid is the process it
// spawned, 0 when none was. stopped is true when a teardown began before the spawn or during its
// wait; the teardown then stops whatever is current.
func (s *daemonSupervisor) restart() (failure string, pid int, stopped bool) {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return "", 0, true
	}
	cmd, exited, err := s.start()
	if err != nil {
		s.mu.Unlock()
		return "could not start (" + err.Error() + ")", 0, false
	}
	s.cmd, s.exited, s.upSince = cmd, exited, time.Now()
	s.mu.Unlock()
	pid = cmd.Process.Pid
	failure = s.ready(cmd, exited)
	if s.isStopping() {
		return "", pid, true
	}
	if failure != "" {
		s.abandon(cmd)
		<-exited
	}
	return failure, pid, false
}

// launchLogOnly is the launch.log half of a launch's writer, for a line that belongs in the record
// and not on the terminal: the log of a teeLog (attachLaunchLog), each one a stack of them holds,
// and a keeper's stream as it is, whose own routing sends a line to its log and, once ready, to
// launch.log and nowhere a session's terminal shows it. nil for a writer with no log half (a bare
// terminal, a test's buffer), where the host log is the line's only record.
func launchLogOnly(w io.Writer) io.Writer {
	switch t := w.(type) {
	case teeLog:
		if inner := launchLogOnly(t.w); inner != nil {
			return io.MultiWriter(t.log, inner)
		}
		return t.log
	case keeperStream:
		return t
	}
	return nil
}

// supervisorSay is a supervisor's say: one line to the daemon's host log, opened for the line
// (the daemon holds its own descriptor, and a trim at a later spawn may have moved the file), and
// one to the launch's log. Both are best effort: a line that cannot be written stops nothing.
func supervisorSay(logPath string, launch io.Writer) func(string) {
	return func(line string) {
		if logPath != "" {
			if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
				_, _ = fmt.Fprintf(f, "%s yolo[%s]: %s\n", time.Now().Format(time.RFC3339),
					strconv.Itoa(os.Getpid()), line)
				_ = f.Close()
			}
		}
		if launch != nil {
			_, _ = io.WriteString(launch, line+"\n")
		}
	}
}
