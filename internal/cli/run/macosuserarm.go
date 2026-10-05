package run

// macosuserarm.go is a macos-user launch's SIGNAL ARM — "the macos-user arm", a term coined here:
// the one handler this process has for SIGINT, SIGQUIT, SIGHUP and SIGTERM from the moment the
// launch starts its first host process until every teardown it deferred has run
// (docs/design/jail-lifetime-last-session-wins.md JL-D40 and §9.9.6).
//
// WHAT IT REPLACED, which was no handler at all. Past its nix builds' own stop
// (nixchildren.StopOnSignal) the backend installed none, and darwin has no TTY proxy: the session
// ran as a plain foreground child (proxy_other.go). So a SIGTERM, or the SIGHUP of a closed window,
// took Go's default action, which ends the process WITHOUT running a deferred function (measured on
// Linux with a stand-in of that run: its deferred write never happened, and its child slept on). The
// session's env files, which hold its credentials, its guest supervisor and its host services were
// all left to the next launch's sweep, and a sandboxed command that survives SIGINT ran on after its
// launcher was gone.
//
// THREE PHASES, by what the launch is doing when a signal lands:
//
//   - SETUP, from the install until the session's command starts. The first SIGINT, SIGHUP or
//     SIGTERM ENDS THE LAUNCH: Ending reports 128+N from then on, the nix this process has running is
//     stopped (nixchildren.Stop), and a SIGTERM or SIGHUP is forwarded to the child the backend has in
//     the foreground (macosuser.SetForegroundWatch), which a signal sent to yolo alone never reaches.
//     The launch then returns 128+N at its next step boundary — Run's check before the dispatch and
//     RunMacosUser's after each step (macosuser.Deps.Ending) — so every deferred teardown runs, and a
//     Ctrl-C during setup never continues to the agent. SIGQUIT is absorbed.
//   - SESSION, while the command runs: launchservice.RunAgent's shape. SIGINT and SIGQUIT are
//     absorbed, since the terminal delivers them to the command's process group itself; SIGTERM and
//     SIGHUP are forwarded to the command's sudo, which relays them (sudo(8)). The launch returns the
//     command's status, 128+N when a signal ended it.
//   - TEARDOWN, from the command's exit until the disarm: every signal is absorbed, so the teardown
//     under way finishes.
//
// A signal ignored when the arm is installed stays ignored (nohup's SIGHUP): notifying for it would
// undo what the user asked for.
//
// Installed by Run's macos-user arm after the config-change prompt — a Ctrl-C there must still end
// the launch at once — and before its first host service, and disarmed by the defer Run registers
// first, so it outlasts every teardown and the timing report. The front door makes the arm
// (NewMacosUserArm) and hands its session runner, its Ending and its AgentStarting to the backend
// (internal/cli's macosUserRun); Run installs that same arm.

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/nixchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// macosUserPhase is where a launch the arm covers is.
type macosUserPhase int

const (
	macosUserUnarmed macosUserPhase = iota
	macosUserSetup
	macosUserSession
	macosUserTeardown
)

// MacosUserArm is one macos-user launch's signal arm. The zero value is not usable: make one with
// NewMacosUserArm. Every method is safe from any goroutine.
type MacosUserArm struct {
	mu    sync.Mutex
	phase macosUserPhase
	// ending is 128+N of the first signal that ended the launch in setup, or that was forwarded to
	// the session; 0 for none.
	ending int
	// foreground is the child the backend has running in the foreground during setup, registered
	// through macosuser.SetForegroundWatch; session is the session's command once started.
	foreground *os.Process
	session    *os.Process
	perf       *perf.Log
	notice     func(string)
	starts     []func()
}

// NewMacosUserArm makes an arm, not yet installed: it handles no signal until Run installs it.
func NewMacosUserArm() *MacosUserArm { return &MacosUserArm{} }

// macosUserArmSignals are the signals the arm takes.
var macosUserArmSignals = []os.Signal{syscall.SIGINT, syscall.SIGQUIT, syscall.SIGHUP, syscall.SIGTERM}

// install arms a for one launch: log is the launch's collector (o.Perf) and notice prints the one
// line a launch ended in setup says. It returns the disarm, idempotent, which stops taking signals
// and waits for a signal being handled. An arm is installed once; a second install arms nothing.
func (a *MacosUserArm) install(log *perf.Log, notice func(string)) (disarm func()) {
	a.mu.Lock()
	if a.phase != macosUserUnarmed {
		a.mu.Unlock()
		return func() {}
	}
	a.phase, a.perf, a.notice = macosUserSetup, log, notice
	a.mu.Unlock()
	var armed []os.Signal
	for _, s := range macosUserArmSignals {
		if !signal.Ignored(s) {
			armed = append(armed, s)
		}
	}
	sigs, stop, done := make(chan os.Signal, 4), make(chan struct{}), make(chan struct{})
	if len(armed) > 0 {
		signal.Notify(sigs, armed...)
	}
	unwatch := macosuser.SetForegroundWatch(a.watchForeground)
	go func() {
		defer close(done)
		for {
			select {
			case s := <-sigs:
				a.take(s)
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(sigs)
			unwatch()
			close(stop)
			<-done
		})
	}
}

// take handles one signal by the phase the launch is in (the file's comment states the rules).
func (a *MacosUserArm) take(s os.Signal) {
	sig, ok := s.(syscall.Signal)
	if !ok {
		return
	}
	forwards := sig == syscall.SIGTERM || sig == syscall.SIGHUP
	a.mu.Lock()
	var to *os.Process
	first := false
	switch a.phase {
	case macosUserSetup:
		if sig == syscall.SIGQUIT {
			a.mu.Unlock()
			return
		}
		if a.ending == 0 {
			a.ending, first = 128+int(sig), true
		}
		if forwards {
			to = a.foreground
		}
	case macosUserSession:
		if forwards {
			to = a.session
			if a.ending == 0 {
				a.ending = 128 + int(sig)
			}
		}
	}
	log, notice := a.perf, a.notice
	a.mu.Unlock()
	if to != nil {
		_ = to.Signal(sig)
	}
	if !first {
		return
	}
	log.Mark("terminate.signal")
	if notice != nil {
		notice(fmt.Sprintf("yolo: %s — ending this macos-user launch before its session starts, "+
			"and removing what it set up. Run `yolo` again to launch.", sigName(sig)))
	}
	// The nix this process runs, which a signal sent to yolo alone does not reach. Its goroutine
	// goes on at once (the hand-off is immediate), so its step returns and the launch ends at the
	// boundary after it rather than here.
	nixchildren.Stop(func() {})
}

// Ending reports the status a signal has begun ending the launch with: macosuser.Deps.Ending.
func (a *MacosUserArm) Ending() (status int, ending bool) {
	if a == nil {
		return 0, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ending, a.ending != 0
}

// RunSession runs the session's command, argv, in the foreground with the terminal it inherits, and
// returns its status (128+N for a signal's death, 1 when it cannot start). It is the backend's
// session runner (macosuser.Deps.RunWithProxy). A launch a signal already ended in setup never
// starts it: the check and the start are one step under the arm's lock, so a signal is either before
// both or forwarded to the command.
func (a *MacosUserArm) RunSession(argv []string) int {
	if len(argv) == 0 {
		return 1
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	a.mu.Lock()
	if a.phase == macosUserSetup && a.ending != 0 {
		rc := a.ending
		a.mu.Unlock()
		return rc
	}
	if err := cmd.Start(); err != nil {
		a.mu.Unlock()
		fmt.Fprintf(os.Stderr, "launch failed: %v\n", err)
		return 1
	}
	if a.phase == macosUserSetup {
		a.phase = macosUserSession
	}
	a.session = cmd.Process
	log := a.perf
	a.mu.Unlock()
	log.Mark("child.spawned")
	err := cmd.Wait()
	log.Mark("child.exited")
	a.mu.Lock()
	a.session = nil
	if a.phase == macosUserSession {
		a.phase = macosUserTeardown
	}
	a.mu.Unlock()
	return exitCodeOf(err)
}

// OnAgentStart registers f to run once when the session's command is about to start
// (AgentStarting). f must return promptly: the session waits for it.
func (a *MacosUserArm) OnAgentStart(f func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.starts = append(a.starts, f)
}

// AgentStarting runs what OnAgentStart registered, once: macosuser.Options.OnAgentStart, which the
// backend calls just before the session's command and on no other path.
func (a *MacosUserArm) AgentStarting() {
	a.mu.Lock()
	starts := a.starts
	a.starts = nil
	a.mu.Unlock()
	for _, f := range starts {
		f()
	}
}

// watchForeground is the arm's macosuser.SetForegroundWatch: p is the backend's foreground child
// until the done it returns. A child started once the launch is ending is a teardown's own removal,
// which no later signal is forwarded to.
func (a *MacosUserArm) watchForeground(p *os.Process) (done func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ending != 0 || a.phase != macosUserSetup {
		return func() {}
	}
	a.foreground = p
	return func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.foreground == p {
			a.foreground = nil
		}
	}
}

// sigName is the conventional name of the signals the arm takes.
func sigName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	}
	return sig.String()
}

// macosUserArm is this launch's arm: the front door's, or one of the launch's own for a caller that
// made none (a capture act, a test).
func (o *Options) macosUserArm() *MacosUserArm {
	if o.MacosUserArm == nil {
		o.MacosUserArm = NewMacosUserArm()
	}
	return o.MacosUserArm
}

// armMacosUser installs this launch's macos-user arm, its notice on stderr, and returns the disarm.
func (o *Options) armMacosUser() (*MacosUserArm, func()) {
	arm := o.macosUserArm()
	return arm, arm.install(o.Perf, func(line string) {
		o.pr(o.Stderr).print("[yellow]" + line + "[/yellow]")
	})
}
