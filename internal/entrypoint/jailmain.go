package entrypoint

// jailmain.go makes a container jail's MAIN PROCESS a hold, and every session an exec
// (docs/design/jail-lifetime-last-session-wins.md §4.1 and JL-D1; step 2 of its §7).
//
// # The shape
//
// A container used to run its first session's command AS its main process: the entrypoint
// booted and then exec'd bash on that command, so the jail ended the moment the command did,
// and every other session with it, because the kernel SIGKILLs a pid namespace whose init
// dies. Now the launch starts pid 1 with the HOLD form of the argv (HoldMainArg): it boots,
// prints BootReadyLine, and then does nothing but keep the container running. Every session,
// the first included, enters with `<runtime> exec … yolo-entrypoint`, as an attach always has.
//
// # What ends the hold
//
// SIGTERM, which is what `podman stop` and `yolo stop` send. SIGHUP and SIGINT are caught and
// dropped, through signal.Notify and never signal.Ignore, whose SIG_IGN every later child
// would inherit: a stray hangup must not end every session (JL-D15).
//
// And nothing else. Until the design's keeper existed (its step 3) the hold also ended when the
// first session's process did, so a jail lived with the terminal that launched it; now the
// host-side session count decides, and the jail's keeper stops the jail when its last session is
// gone (docs/design/jail-lifetime-last-session-wins.md §9.5), so no session is special here.
//
// # Provisioning, once per container, on the first session's terminal
//
// The provisioning stage asks "continue anyway? [Y/n]" only at a terminal (provision.Script's
// `[ -t 0 ]`), and pid 1 has none. So pid 1 records the stage the launch handed it and the
// FIRST session runs it on its own tty, under an in-jail flock, and records ONE OUTCOME
// (JL-D33): done, which includes a failure the terminal chose to continue past, or refused,
// which is provision.RefusedStatus or a failure the terminal declined. Every other session
// waits for that outcome before its own boot pass, never for a done marker: a refusal refuses
// it too, and a run abandoned part-way (the flock free, a claim recorded, no outcome) is run
// again on a waiter's terminal, or refuses a waiter that has none, since that waiter would
// otherwise continue where a person is asked.
//
// Everything here is in-jail state on the container's own /run tmpfs, so nothing carries over
// from a previous container, and nothing here is anything the host trusts: a jail process that
// rewrote these files would change only what its own jail's sessions do.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// JailMainEnv is set to JailMainHold in the environment of every container whose main process
// is a hold. It is the one marker of that shape, with two readers: a session's entrypoint,
// which then waits for the boot and for provisioning before its own pass, and the host, which
// reads it off `inspect` to know that the container's main process is not a session
// (run.jailSessionCount). A jail without it booted its first session as pid 1.
const (
	JailMainEnv  = "YOLO_JAIL_MAIN"
	JailMainHold = "hold"
)

// The two argv forms the launcher gives the entrypoint besides a session's one command.
//
// Each is exactly TWO arguments, a flag and one payload, and that arity is the whole of what
// keeps them apart from a session: every command a launcher passes is ONE argument (the
// target, shell-quoted and joined), so `yolo -- --yolo-hold-main x` arrives as the single
// argument "--yolo-hold-main x" and is a command like any other.
const (
	// HoldMainArg: this process is the container's main process. Its payload is the
	// provisioning stage, as one shell command, for the first session to run.
	HoldMainArg = "--yolo-hold-main"
	// FirstSessionArg: this exec is the jail's first session, the one that runs provisioning
	// on its terminal. Its payload is the session's command.
	FirstSessionArg = "--yolo-first-session"
)

// BootReadyLine is the line pid 1 prints on its stderr, and nowhere else, once its boot is
// done and the container is ready for its first session. The launcher reads pid 1's stderr
// through the attached runtime client, prints every other line as it arrives, and starts the
// first session's exec on this one, which it does not print. On stderr because every line of
// the boot is, so nothing the boot printed can arrive after it.
const BootReadyLine = "yolo-entrypoint: boot done; the jail's main process is holding it open"

// ProvisionMillisEnv carries how long the first session's provisioning took, in
// milliseconds, into that session's shell, for the in-container timing block
// (run.buildSessionCmd's timing branch), which used to time the stage itself.
const ProvisionMillisEnv = "YOLO_PROVISION_MS"

// jailMainDir holds the main process's state and provisioning's, on the container's /run
// tmpfs. A var so tests can relocate it.
var jailMainDir = "/run/yolo/main"

// The files in jailMainDir. Each is written whole (writeMainState), so a reader sees the old
// value or the new one and never half of one.
const (
	// bootStateFile is pid 1's boot: bootBooting, bootReady or bootRefused.
	bootStateFile = "boot"
	// provisionScriptFile is the stage pid 1 was handed, for whichever session runs it.
	provisionScriptFile = "provision.sh"
	// provisionLockFile is flocked by the session running provisioning, for as long as it
	// runs, and by nobody otherwise.
	provisionLockFile = "provision.lock"
	// provisionClaimFile exists once some session has begun provisioning. A free lock with a
	// claim and no outcome is a run that was abandoned; without the claim it is a first
	// session that has not arrived yet.
	provisionClaimFile = "provision.claimed"
	// provisionOutcomeFile is provisioning's one outcome: "done", or "refused <status>".
	provisionOutcomeFile = "provision.outcome"
	// firstSessionFile names the first session's pid: whichever exec registered it first is the
	// one that runs provisioning.
	firstSessionFile = "first-session"
)

const (
	bootBooting = "booting"
	bootReady   = "ready"
	bootRefused = "refused"

	outcomeDone    = "done"
	outcomeRefused = "refused"
)

// The bounds on a session's waits (JL-D34: nothing waits without one). Vars so tests can
// shrink them.
var (
	// bootWaitLimit bounds a session's wait for pid 1's boot. The boot is seconds; its one
	// open-ended step is the in-jail daemons' readiness, which has no bound of its own.
	bootWaitLimit = 10 * time.Minute
	// provisionWaitLimit bounds a wait on a provisioning run someone else holds. A first
	// boot installs toolchains and npm packages, which can take many minutes.
	provisionWaitLimit = time.Hour
	// claimWaitLimit bounds a wait for a first session that has not begun provisioning.
	// Its launcher starts it the moment the boot is done, so this is seconds unless that
	// launcher died in between, and then the run is treated as abandoned.
	claimWaitLimit = 2 * time.Minute
	// sessionPoll is how often a waiting session looks again.
	sessionPoll = 200 * time.Millisecond
	// waitNoticeAfter is how long a wait stays silent before it says what it waits for.
	waitNoticeAfter = 2 * time.Second
)

// entryMode is which of the three things this entrypoint invocation is.
type entryMode int

const (
	// modeSession is an ordinary session: an attach, or any exec naming one command.
	modeSession entryMode = iota
	// modeHold is the container's main process.
	modeHold
	// modeFirstSession is the session that runs provisioning.
	modeFirstSession
)

// parseEntryArgs reads Main's argv: one of the two-argument forms above, or a session's
// command, which is its arguments joined by spaces ("bash" when there are none), as it always
// was.
func parseEntryArgs(args []string) (entryMode, string) {
	if len(args) == 2 {
		switch args[0] {
		case HoldMainArg:
			return modeHold, args[1]
		case FirstSessionArg:
			return modeFirstSession, args[1]
		}
	}
	if len(args) == 0 {
		return modeSession, "bash"
	}
	return modeSession, strings.Join(args, " ")
}

// ExitStatus is an entrypoint refusal that carries the exit status the session must end
// with — provisioning's own, so `yolo -- …` returns what the stage returned, as it did when
// the stage ran in the container's main process. cmd/yolo-entrypoint exits with Code and
// prints Message when there is one.
type ExitStatus struct {
	Code    int
	Message string
}

func (e *ExitStatus) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

func mainStatePath(name string) string { return filepath.Join(jailMainDir, name) }

// writeMainState replaces one state file whole: a temporary file beside it, then a rename.
func writeMainState(name, value string) error {
	if err := os.MkdirAll(jailMainDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(jailMainDir, "."+name+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(value); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), mainStatePath(name))
}

// readMainState reads one state file, trimmed; ok is false when it does not exist or cannot
// be read.
func readMainState(name string) (string, bool) {
	b, err := os.ReadFile(mainStatePath(name))
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

// markBoot records pid 1's boot state. A failure is dropped: it can only make a session wait
// its bound and then say so, which is the visible form of it.
func markBoot(state string) { _ = writeMainState(bootStateFile, state) }

// holdJail is the main process's life after a boot that succeeded: record the stage for the
// first session, say the boot is done (the state file for sessions, BootReadyLine for the
// launcher), and hold until SIGTERM. terminated says a SIGTERM ended it, and Main then exits
// 128+SIGTERM (holdExitStatus). A channel that never closes stands where the first session's end
// was until the keeper: nothing a session does ends the hold.
func holdJail(stage string, stderr io.Writer) (terminated bool) {
	announceReady(stage, stderr)
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	return holdUntil(signals, nil)
}

// holdExitStatus is the main process's end once its hold is over. A hold that a SIGTERM ended
// exits 128+SIGTERM, the status of a process the signal ended; one that ended otherwise exits 0.
func holdExitStatus(terminated bool) error {
	if terminated {
		return &ExitStatus{Code: 128 + int(syscall.SIGTERM)}
	}
	return nil
}

// announceReady records the stage, then the ready state, then prints BootReadyLine: in that
// order, because the launcher starts the first session on the line, and that session reads
// both files.
func announceReady(stage string, stderr io.Writer) {
	_ = writeMainState(provisionScriptFile, stage)
	markBoot(bootReady)
	fmt.Fprintln(stderr, BootReadyLine)
}

// holdUntil blocks until a SIGTERM arrives (terminated) or released closes; a nil released never
// does. SIGHUP and SIGINT are received and dropped.
func holdUntil(signals <-chan os.Signal, released <-chan struct{}) (terminated bool) {
	for {
		select {
		case s := <-signals:
			if s == syscall.SIGTERM {
				return true
			}
		case <-released:
			return false
		}
	}
}

// sessionGate is one session's side of pid 1's boot and of provisioning.
type sessionGate struct {
	// tty is whether this session has a terminal on stdin, which is what decides whether it
	// may run an abandoned provisioning again.
	tty bool
	// out is where the waits say what they wait for: the session's own stderr.
	out io.Writer
	// lock is the provisioning flock, held from claim or await until provision or abandon.
	lock *os.File
	// noticed records which waits have already said so, so each says it once.
	noticed map[string]bool
}

func newSessionGate(tty bool, out io.Writer) *sessionGate {
	return &sessionGate{tty: tty, out: out, noticed: map[string]bool{}}
}

// notice says once, after waitNoticeAfter, what this session is waiting for.
func (g *sessionGate) notice(key string, waited time.Duration, msg string) {
	if waited < waitNoticeAfter || g.noticed[key] {
		return
	}
	g.noticed[key] = true
	fmt.Fprintln(g.out, "yolo-entrypoint: "+msg)
}

// registerFirst records pid as the jail's first session. False when a first session is
// already recorded, which makes this one an ordinary session: there is one first session per
// container, and it is whichever exec registered first.
func (g *sessionGate) registerFirst(pid int) bool {
	if err := os.MkdirAll(jailMainDir, 0o755); err != nil {
		return false
	}
	f, err := os.OpenFile(mainStatePath(firstSessionFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false
	}
	_, werr := f.WriteString(strconv.Itoa(pid) + "\n")
	cerr := f.Close()
	return werr == nil && cerr == nil
}

// awaitBoot waits for pid 1's boot to finish. A boot that refused refuses this session.
func (g *sessionGate) awaitBoot() error {
	start := time.Now()
	for {
		switch st, _ := readMainState(bootStateFile); st {
		case bootReady:
			return nil
		case bootRefused:
			return &ExitStatus{Code: 1, Message: "this jail's boot refused, so no session can " +
				"enter it; the reason is in /workspace/.yolo/boot.log"}
		}
		waited := time.Since(start)
		if waited >= bootWaitLimit {
			return &ExitStatus{Code: 1, Message: fmt.Sprintf("this jail's boot has not finished "+
				"after %s; its progress is in /workspace/.yolo/boot.log", bootWaitLimit)}
		}
		g.notice("boot", waited, "waiting for this jail's boot to finish…")
		time.Sleep(sessionPoll)
	}
}

// openLock opens the provisioning lock file.
func (g *sessionGate) openLock() (*os.File, error) {
	if err := os.MkdirAll(jailMainDir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(mainStatePath(provisionLockFile), os.O_RDWR|os.O_CREATE, 0o644)
}

// claim is the first session's side: take the provisioning lock and record the claim, and
// return provisioner=true holding it. The lock is normally free: nothing else takes it until a
// waiter gives up on a first session that never arrived (claimWaitLimit) and runs the stage
// itself. A first session that arrives after all then waits for that run, within
// provisionWaitLimit, and acts on its outcome as any waiter does instead of running the stage
// a second time.
func (g *sessionGate) claim() (provisioner bool, err error) {
	f, err := g.openLock()
	if err != nil {
		return false, fmt.Errorf("open the provisioning lock: %w", err)
	}
	start := time.Now()
	for {
		lerr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if lerr == nil {
			break
		}
		if !errors.Is(lerr, syscall.EWOULDBLOCK) && !errors.Is(lerr, syscall.EAGAIN) {
			_ = f.Close()
			return false, fmt.Errorf("take the provisioning lock: %w", lerr)
		}
		waited := time.Since(start)
		if waited >= provisionWaitLimit {
			_ = f.Close()
			return false, &ExitStatus{Code: 1, Message: fmt.Sprintf("this jail's provisioning "+
				"has not finished after %s; its log is /workspace/.yolo/startup.log", provisionWaitLimit)}
		}
		g.notice("provision", waited, "waiting for this jail's provisioning, which another session took over…")
		time.Sleep(sessionPoll)
	}
	if done, err := outcomeVerdict(); done || err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return false, err
	}
	g.lock = f
	if err := writeMainState(provisionClaimFile, strconv.Itoa(os.Getpid())); err != nil {
		g.abandon()
		return false, fmt.Errorf("record the provisioning claim: %w", err)
	}
	return true, nil
}

// await is every other session's side. It returns once provisioning is done, refuses on a
// recorded refusal, and on an abandoned run returns provisioner=true, holding the lock, when
// this session has a terminal to run it again on.
func (g *sessionGate) await() (provisioner bool, err error) {
	f, err := g.openLock()
	if err != nil {
		return false, fmt.Errorf("open the provisioning lock: %w", err)
	}
	start := time.Now()
	unclaimedSince := time.Time{}
	for {
		if done, err := outcomeVerdict(); done || err != nil {
			_ = f.Close()
			return false, err
		}
		if lerr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); lerr != nil {
			// Someone is provisioning now.
			unclaimedSince = time.Time{}
			waited := time.Since(start)
			if waited >= provisionWaitLimit {
				_ = f.Close()
				return false, &ExitStatus{Code: 1, Message: fmt.Sprintf("this jail's provisioning "+
					"has not finished after %s; its log is /workspace/.yolo/startup.log", provisionWaitLimit)}
			}
			g.notice("provision", waited, "waiting for this jail's first session to finish provisioning…")
			time.Sleep(sessionPoll)
			continue
		}
		// The lock is ours. An outcome recorded just before its holder let go is read again.
		if done, err := outcomeVerdict(); done || err != nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			return false, err
		}
		if _, claimed := readMainState(provisionClaimFile); claimed {
			// ABANDONED: begun, not finished, and nobody running it.
			if !g.tty {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
				return false, &ExitStatus{Code: 1, Message: "this jail's provisioning was " +
					"abandoned before it finished, and this session has no terminal to run it " +
					"again on; run `yolo` in a terminal to finish it"}
			}
			fmt.Fprintln(g.out, "yolo-entrypoint: this jail's provisioning was abandoned before it "+
				"finished; running it again here")
			g.lock = f
			return true, nil
		}
		// Not begun: the first session has not arrived. Let go and wait for it.
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		if unclaimedSince.IsZero() {
			unclaimedSince = time.Now()
		}
		waited := time.Since(unclaimedSince)
		if waited >= claimWaitLimit {
			// Its launcher is gone: nobody will ever begin it. Treat it as abandoned.
			if err := writeMainState(provisionClaimFile, "abandoned-unclaimed"); err != nil {
				_ = f.Close()
				return false, fmt.Errorf("record the provisioning claim: %w", err)
			}
			continue
		}
		g.notice("claim", time.Since(start), "waiting for this jail's first session to begin provisioning…")
		time.Sleep(sessionPoll)
	}
}

// outcomeVerdict reads the recorded outcome: done=true once there is one, with err set when
// it was a refusal.
func outcomeVerdict() (done bool, err error) {
	raw, ok := readMainState(provisionOutcomeFile)
	if !ok {
		return false, nil
	}
	if raw == outcomeDone {
		return true, nil
	}
	code := 1
	if f := strings.Fields(raw); len(f) == 2 && f[0] == outcomeRefused {
		if n, err := strconv.Atoi(f[1]); err == nil && n > 0 {
			code = n
		}
	}
	return true, &ExitStatus{Code: code, Message: fmt.Sprintf("this jail's provisioning refused "+
		"it (exit %d), so no session can enter it; the reason is in /workspace/.yolo/startup.log",
		code)}
}

// provision runs the recorded stage through run, records its outcome, and lets the lock go.
// It returns the stage's exit status: 0 is done, anything else refused.
func (g *sessionGate) provision(run func(stage string) int) int {
	stage, _ := readMainState(provisionScriptFile)
	rc := 0
	if strings.TrimSpace(stage) != "" {
		rc = run(stage)
	}
	outcome := outcomeDone
	if rc != 0 {
		outcome = outcomeRefused + " " + strconv.Itoa(rc)
	}
	_ = writeMainState(provisionOutcomeFile, outcome)
	g.abandon()
	return rc
}

// abandon lets the provisioning lock go without recording an outcome, which a waiter reads as
// an abandoned run.
func (g *sessionGate) abandon() {
	if g.lock == nil {
		return
	}
	_ = syscall.Flock(int(g.lock.Fd()), syscall.LOCK_UN)
	_ = g.lock.Close()
	g.lock = nil
}

// provisionThisSession runs the recorded provisioning stage for the session that holds its lock
// (gate.provision, through run), and hands the session's shell how long it took, as
// ProvisionMillisEnv, for the in-container timing block that used to time the stage itself.
// Its status is the stage's.
func provisionThisSession(e *Env, gate *sessionGate, run func(*Env, string) int) int {
	start := time.Now()
	rc := gate.provision(func(stage string) int { return run(e, stage) })
	setEnvBoth(e, ProvisionMillisEnv, strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	return rc
}

// runProvisionStage runs the provisioning stage as its own shell, on this session's
// terminal, with the activation every session's shell gets (activationPrefix) and the PATH it
// gets (BootPath) — the environment the stage had when it was the first clause of the
// container's own command.
//
// SIGINT is caught for the stage's lifetime and dropped: a Ctrl-C at the terminal reaches this
// process and the stage together, the stage decides what it means, and this process records
// what the stage decided instead of dying before it can.
func runProvisionStage(e *Env, stage string) int {
	bash, err := exec.LookPath("bash")
	if err != nil {
		fmt.Fprintln(os.Stderr, "yolo-entrypoint: cannot run provisioning: "+err.Error())
		return 127
	}
	cmd := exec.Command(bash, "-c", activationPrefix(e)+stage)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// The stage's PATH is the session's (BootPath), set on the child alone: execBash applies it
	// to this process, once, as the single authority does (TestBootPathIsTheOnlyPathAuthority).
	cmd.Env = envWith(os.Environ(), "PATH", BootPath(e))
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	err = cmd.Run()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		if code := exitErr.ExitCode(); code > 0 {
			return code
		}
	}
	return 1
}
