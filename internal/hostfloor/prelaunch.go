package hostfloor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/sigyield"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/notty"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
)

// prelaunch.go is the HOST half of a program's PRE-LAUNCH REFRESH (packdecl.Refresh, a term coined
// for that field): the argv a launcher runs the installed program with, before exec'ing it, to bring
// what the program manages beside itself up to date. Pi's `update --extensions` is the case that
// bought it (docs/reference/pi-extensions.md §17.2). The jail's launchers run it
// (internal/entrypoint's prelaunchrefresh.go); `yolo host -- pi` did not, so at the host pi's
// extensions stayed as old as the user's last hand-run update, and pi kept showing its "Package
// Updates Available" box there (docs/reference/host-agent-environment.md HP-D19).
//
// # What it keeps of the jail's launcher, and why
//
// Every guarantee packdecl.Refresh lists: at most once per update interval on a stamp of its own,
// sooner when a DueOnChange file holds content no refresh has succeeded for; only where
// `agent_updates` lets the pack move (UpdatesAllowed); bounded by the jail's UPDATE_TIMEOUT
// (pollTimeout) with its UPDATE_GRACE (RefreshKillAfter) before the kill; stdin from /dev/null and
// no controlling terminal (notty; program-delivery.md OQ-PD22), its output on the launch's stderr; one refresh at a time
// per program. Its lines are the launcher's, under the floor's prefix. One guarantee is NOT kept: a
// failure no longer launches the program anyway. By PD-NF1 a refresh that reached upstream and then
// failed or timed out stops the launch unless the user's patchrecovery.UseInstalledEnv hatch covers
// it, and an upstream it cannot reach skips it with a could-not-check line (PD-NF3).
//
// # What differs, and why
//
//   - THE LOCK IS THE FLOOR'S, not the pack's. Refresh.Lock names a directory inside the store the
//     JAILS share (`~/.pi-shared-npm` in a jail's home, under yolo's state dir on the host), which
//     is how every jail on a machine sees one another's refresh. A host pi writes its own store
//     under the real home (`~/.pi/agent/npm`), so a lock in the jails' would order the host
//     against nothing. The host's lock is a flock under the floor prefix (refreshLockPath): the
//     kernel drops it with its holder, so it needs neither the launcher's stale-lock break nor its
//     heartbeat.
//   - THE STAMP AND THE SEEN MARKERS ARE THE FLOOR'S TOO, under the prefix (0700, never mounted by
//     any jail): a jail's stamp says when the jails' shared store was refreshed, which says nothing
//     about the host's.
//   - IT IS NOT A STEP OF Ensure. It runs for whichever copy the launch execs — the floor's, one found
//     on the launch PATH, or one given as a path — because what it refreshes is the program's
//     add-ons, not the program; and `yolo host apply --assert`, which runs Ensure, must not run a
//     program. The launch calls it, with the environment it builds for the refresh (internal/cli).

// RefreshKillAfter is how long a refresh, and what is left of its process group, may outlive the
// SIGTERM its bound sends before the group is killed: the jail launcher's UPDATE_GRACE.
const RefreshKillAfter = 5 * time.Second

// RefreshOutcome is what one pre-launch refresh came to.
type RefreshOutcome int

const (
	// RefreshNone: nothing ran and nothing was said — the program declares no refresh,
	// `agent_updates` holds its pack, none is due, or a refresh another launch held finished
	// exactly this launch's content while it waited.
	RefreshNone RefreshOutcome = iota
	// RefreshHeld: another launch holds the refresh lock; nothing ran, and the launch runs what is
	// installed.
	RefreshHeld
	// RefreshNoLock: the lock could not be taken at all (the prefix is not writable); nothing ran,
	// and the next launch, which most likely cannot write the prefix either, says so again.
	RefreshNoLock
	// RefreshRan: the refresh ran and exited 0.
	RefreshRan
	// RefreshFailed: the refresh exited non-zero, or could not start.
	RefreshFailed
	// RefreshTimedOut: the refresh outlived its bound and was stopped.
	RefreshTimedOut
	// RefreshInterrupted: a Ctrl-C (SIGINT) reached this process while the refresh ran, whatever
	// the refresh then exited with, 0 included: an interrupted refresh is not a successful one. The
	// program still launches, as the jail's launcher's does.
	RefreshInterrupted
	// RefreshStopped: a SIGTERM or SIGHUP reached this process while the refresh ran, whatever the
	// refresh then exited with: one that handles the signal and exits with a status is stopped too.
	// The lock is released and the caller ends the launch, as that signal asked.
	RefreshStopped
	// RefreshUnchecked: the refresh failed or timed out, and the probe that followed (PD-NF3) could
	// not reach its upstream, so the launch runs what is installed: a refresh that cannot reach
	// upstream cannot know there is anything newer, which PD-NF1 does not count as a failure.
	// Stamped, so an offline hour asks once. Status keeps the failed refresh's status.
	RefreshUnchecked
)

// RefreshResult is one PrelaunchRefresh: its outcome, and what a caller needs to say or do about it.
type RefreshResult struct {
	Outcome RefreshOutcome
	// Signal is the signal that ended a RefreshStopped or RefreshInterrupted refresh, 0 otherwise.
	Signal syscall.Signal
	// Status is a RefreshFailed refresh's exit status (notty.ExitCode), 0 otherwise.
	Status int
	// Refused is set for a RefreshFailed or RefreshTimedOut automatic refresh the user's
	// patchrecovery.UseInstalledEnv hatch does not cover: its refusal block is printed, and the
	// caller ends the launch (PD-NF1) rather than running the program with its add-ons as they were.
	Refused bool
	// Err is the refusal, an *UpdateRefusal, when Refused is set.
	Err error
}

// PrelaunchRefresh runs p's declared pre-launch refresh against exe, the binary the launch is about
// to exec, in env, when it is due — and prints, through the floor's Out under its Prefix, the
// launcher's own lines for each outcome. Two outcomes stop the launch: RefreshStopped, a signal that
// asked for exactly that, and a Refused failure (PD-NF1): a refresh that reached upstream and then
// failed or timed out is a failed update, which runs the program only under the user's
// patchrecovery.UseInstalledEnv hatch. A Ctrl-C still runs it: the interrupt was the user's.
//
// env is the refresh's whole environment, PATH included; the caller builds it (the launch's own
// environment with none of the credentials it composes for the program, packdecl.Refresh's "none
// of those needs a credential"). The refresh runs in this process's working directory, which is the
// launch's, as the jail's launcher runs it in the shell's: a program may refresh what the workspace
// names too.
func (f *Floor) PrelaunchRefresh(p Program, exe string, env []string) RefreshResult {
	return f.prelaunchRefresh(p, exe, env, false)
}

// PrelaunchRefreshManual runs the same declared owner-program refresh, explicitly requested by
// --update, without automatic policy/stamp holds. It remains the pack's declared argv and environment.
func (f *Floor) PrelaunchRefreshManual(p Program, exe string, env []string) RefreshResult {
	return f.prelaunchRefresh(p, exe, env, true)
}

func (f *Floor) prelaunchRefresh(p Program, exe string, env []string, manual bool) RefreshResult {
	r := p.Install.Refresh
	if r == nil || len(r.Argv) == 0 {
		return RefreshResult{}
	}
	if !manual && f.UpdatesAllowed != nil && !f.UpdatesAllowed(p.Pack) {
		return RefreshResult{}
	}
	bin := p.Bin()
	key := refreshContentKey(f.home(), r.DueOnChange)
	if !manual && !f.refreshDue(bin, key) {
		return RefreshResult{}
	}
	lockPath := f.refreshLockPath(bin)
	lk, err := f.takeRefreshLock(bin)
	if errors.Is(err, pidlock.ErrHeld) && f.contentUnseen(bin, key) {
		// THE ONE CASE A HELD LOCK IS WAITED ON, the launcher's _wait_for_refresh_lock: this
		// launch's watched content has never been refreshed with, so the program is about to
		// install what it names, and doing that beside the holder's refresh of the same store is
		// the first-install race DueOnChange exists to close. Bounded by the refresh's own bound.
		f.say("%s: another refresh holds %s and this workspace's add-ons are new — waiting for it (up to %s)...",
			bin, lockPath, f.pollTimeout())
		lk, err = pidlock.Acquire(lockPath, pidlock.Mode{Wait: true, Bound: f.pollTimeout()}, nil)
		if err == nil && !f.contentUnseen(bin, key) {
			// The holder refreshed exactly this content while this launch waited.
			lk.Release()
			return RefreshResult{}
		}
		if errors.Is(err, pidlock.ErrTimedOut) {
			err = pidlock.ErrHeld
		}
	}
	switch {
	case errors.Is(err, pidlock.ErrHeld):
		// No stamp: the holder stamps when it finishes, and a launch after that sees it.
		f.say("%s: another refresh holds %s — running what is installed", bin, lockPath)
		return RefreshResult{Outcome: RefreshHeld}
	case err != nil:
		f.say("%s: cannot take the refresh lock %s (%v) — skipping the pre-launch refresh; once %s "+
			"is a directory you can write, a later launch runs it", bin, lockPath, err, f.refreshDir())
		// Stamped and recorded as the launcher's are, BEST EFFORT. The launcher's stamp sits in a
		// directory of its own, apart from the store whose lock failed, so there a refresh that
		// cannot run says so once an interval. Here the stamp and the record live in the same
		// prefix's refresh/, so in the usual case, a prefix that cannot be written, both writes fail
		// too and the line is said on every launch, which keeps the broken prefix in view. A lock
		// that fails beside a writable refresh/ (a directory where the lock file should be, say) is
		// said once an interval, its change trigger with it.
		f.touchRefreshStamp(bin)
		f.recordSeen(bin, key)
		return RefreshResult{Outcome: RefreshNoLock}
	}
	defer lk.Release()

	// SAID BEFORE IT RUNS (OQ-RO3): the refresh is the network, and a launch has no quiet mode.
	f.say("Refreshing %s (%s)...", bin, strings.Join(r.Argv, " "))
	res := f.runRefresh(exe, r.Argv, env)
	var failure error
	switch res.Outcome {
	case RefreshTimedOut:
		failure = fmt.Errorf("it timed out after %s", f.pollTimeout())
	case RefreshFailed:
		failure = fmt.Errorf("it exited with status %d", res.Status)
	}
	refuse := failure != nil && !manual && !f.useInstalled(p)
	act := "the pre-launch refresh (" + strings.Join(r.Argv, " ") + ")"
	if refuse {
		// THE PROBE AFTER THE FAILURE (PD-NF3), as the jail's launcher probes (_yolo_failed_act): the
		// refresh is itself the check for newer add-ons, so a failure whose upstream cannot be reached
		// is a check that could not run — said, stamped, and the program runs what is installed. After
		// rather than before: a refresh that succeeds costs no probe.
		if url := refreshUpstream(env); url != "" {
			if why := f.upstreamUnreachable(context.Background(), url); why != nil {
				f.say("%s: %s failed (%v), and could not check for newer add-ons: %s is unreachable (%v) — running what is installed",
					bin, act, failure, url, why)
				f.touchRefreshStamp(bin)
				res.Outcome = RefreshUnchecked
				return res
			}
		}
	}
	// Stamped on every outcome but a refusal, as the launcher's is: the stamp throttles retries,
	// and a refused refresh must be retried by the next launch, not skipped for an interval while
	// the program runs with its add-ons as they were (PD-NF1).
	if !refuse {
		f.touchRefreshStamp(bin)
	}
	switch {
	case res.Outcome == RefreshRan:
		// The content key only on SUCCESS: a failed refresh leaves the change due, so the next
		// launch retries the install under the lock instead of leaving it to the program.
		f.recordSeen(bin, key)
	case res.Outcome == RefreshInterrupted:
		f.say("%s: the pre-launch refresh was interrupted (Ctrl-C) — running what is installed", bin)
	case refuse:
		res.Refused = true
		res.Err = f.refuseRefresh(p, act, failure)
	case manual && res.Outcome == RefreshTimedOut:
		f.say("%s: the pre-launch refresh timed out after %s", bin, f.pollTimeout())
	case manual && res.Outcome == RefreshFailed:
		f.say("%s: the pre-launch refresh failed (status %d)", bin, res.Status)
	case failure != nil:
		f.sayUsingInstalled(p, "add-ons", act, failure)
	}
	return res
}

// refuseRefresh prints a failed refresh's refusal block, its repair the launch itself.
func (f *Floor) refuseRefresh(p Program, act string, err error) error {
	r := &UpdateRefusal{Bin: p.Bin(), Pack: p.Pack, Act: act, Err: err, Runs: "run it with what is installed",
		Repair: f.hatchCommand(p), Command: f.hatchCommand(p)}
	out := f.out()
	_, _ = io.WriteString(out, r.Render(colorFor(out)))
	return r
}

// runRefresh runs exe argv in env under the refresh's bound, its output on the floor's Out nested
// under the line that started it, and classifies how it ended.
//
// A SIGNAL THIS PROCESS RECEIVED OUTRANKS HOW THE REFRESH EXITED. notty forwards SIGINT, SIGTERM and
// SIGHUP to the refresh, but reports one (notty.Stopped) only when the refresh died of it or was
// killed for outliving it, and a refresh that handles the signal exits with a status of its own
// choosing (a shell trap, a node process.on handler followed by process.exit(143)), 0 included.
// The jail's launcher reads neither: its _shielded TERM and HUP traps end the launcher whatever
// the program then exits with, and its _bounded reads a Ctrl-C followed by a zero exit as an
// interrupted act. So the signals are recorded here, beside notty's forwarding (signal.Notify
// hands each one to every channel registered for it), for the whole run: any SIGTERM or SIGHUP is
// RefreshStopped by the first of them, and otherwise any SIGINT is RefreshInterrupted. notty's own
// report is read too, which covers a signal the runtime has not yet handed this channel when the
// refresh died of it.
func (f *Floor) runRefresh(exe string, argv, env []string) RefreshResult {
	cmd := exec.Command(exe, argv...)
	cmd.Env = env
	shown := &indentWriter{w: f.out(), indent: "    "}
	// One writer for both streams, so os/exec gives them one pipe and their order survives.
	cmd.Stdout, cmd.Stderr = shown, shown
	// A process the refresh left behind holding that pipe must not hold the launch with it.
	cmd.WaitDelay = RefreshKillAfter
	var err error
	got := signalsDuring(func() {
		err = notty.RunBounded(cmd, notty.Bound{Timeout: f.pollTimeout(), KillAfter: RefreshKillAfter})
	})
	shown.flush()
	var stopped *notty.Stopped
	if errors.As(err, &stopped) {
		got.add(stopped.Signal)
	}
	var timedOut *notty.TimedOut
	switch {
	case got.ending != 0:
		return RefreshResult{Outcome: RefreshStopped, Signal: got.ending}
	case got.interrupted:
		return RefreshResult{Outcome: RefreshInterrupted, Signal: syscall.SIGINT}
	case errors.As(err, &timedOut):
		return RefreshResult{Outcome: RefreshTimedOut}
	case err == nil:
		return RefreshResult{Outcome: RefreshRan}
	}
	return RefreshResult{Outcome: RefreshFailed, Status: notty.ExitCode(err)}
}

// receivedSignals is what signalsDuring saw: the first SIGTERM or SIGHUP, 0 for none, and whether
// a SIGINT came.
type receivedSignals struct {
	ending      syscall.Signal
	interrupted bool
}

func (r *receivedSignals) add(sig syscall.Signal) {
	switch sig {
	case syscall.SIGTERM, syscall.SIGHUP:
		if r.ending == 0 {
			r.ending = sig
		}
	case syscall.SIGINT:
		r.interrupted = true
	}
}

// signalsDuring runs run with SIGINT, SIGTERM and SIGHUP recorded as this process receives them,
// and returns what it received. It is read continuously rather than left in a buffer, so a burst of
// Ctrl-Cs cannot fill one and push out the SIGTERM that follows it. A signal arriving once run has
// returned and before the recording stops is recorded too: it still came during the refresh's
// step, before the launch went on.
func signalsDuring(run func()) receivedSignals {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer sigyield.Hold(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)()
	var got receivedSignals
	record := func(s os.Signal) {
		if sig, ok := s.(syscall.Signal); ok {
			got.add(sig)
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case s := <-ch:
				record(s)
			case <-stop:
				return
			}
		}
	}()
	run()
	signal.Stop(ch)
	close(stop)
	<-done
	for {
		select {
		case s := <-ch:
			record(s)
		default:
			return got
		}
	}
}

// The refresh's state, all under refresh/ in the prefix, so no bin name can collide with it.
func (f *Floor) refreshDir() string { return filepath.Join(f.Dir, "refresh") }
func (f *Floor) refreshStampPath(bin string) string {
	return filepath.Join(f.refreshDir(), bin+".stamp")
}
func (f *Floor) refreshSeenDir(bin string) string  { return filepath.Join(f.refreshDir(), bin+".seen") }
func (f *Floor) refreshLockPath(bin string) string { return filepath.Join(f.refreshDir(), bin+".lock") }

// home is the HOME the refresh's watched files are read under: the installers' (Floor.Home), else
// $HOME.
func (f *Floor) home() string {
	if f.Home != "" {
		return f.Home
	}
	return os.Getenv("HOME")
}

// refreshDue reports whether bin's refresh is due: its watched content has never been refreshed
// with here, there is no stamp, or the stamp is at least the update interval old.
func (f *Floor) refreshDue(bin, key string) bool {
	if f.contentUnseen(bin, key) {
		return true
	}
	st, err := os.Stat(f.refreshStampPath(bin))
	if err != nil {
		return true
	}
	return f.now().Sub(st.ModTime()) >= f.updateInterval()
}

// contentUnseen reports whether key names watched content no refresh of bin has succeeded for. ""
// (a refresh that watches nothing) is never unseen.
func (f *Floor) contentUnseen(bin, key string) bool {
	if key == "" {
		return false
	}
	_, err := os.Lstat(filepath.Join(f.refreshSeenDir(bin), key))
	return err != nil
}

// takeRefreshLock takes bin's refresh lock without waiting: pidlock.ErrHeld when another launch
// holds it. The prefix is made first, 0700, by the floor's one rule for it (ensureDir).
func (f *Floor) takeRefreshLock(bin string) (*pidlock.Lock, error) {
	if err := f.ensureDir("refresh"); err != nil {
		return nil, err
	}
	return pidlock.Acquire(f.refreshLockPath(bin), pidlock.NoWait, nil)
}

// touchRefreshStamp moves bin's stamp to now. Best effort: a stamp that cannot be written leaves the
// refresh due, which costs one more refresh, never a launch.
func (f *Floor) touchRefreshStamp(bin string) {
	path := f.refreshStampPath(bin)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return
	}
	now := f.now()
	_ = os.Chtimes(path, now, now)
}

// recordSeen records that a refresh of bin succeeded with the watched content key names.
func (f *Floor) recordSeen(bin, key string) {
	if key == "" {
		return
	}
	dir := f.refreshSeenDir(bin)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, key), nil, 0o600)
}

// refreshContentKey is one key for the current content of every watched file under home, "" when
// nothing is watched. A file that is absent, or not a regular file, contributes "absent", so absent
// and present differ, as the launcher's key does; one that cannot be read contributes "unreadable".
// The key is only compared, never trusted, and it names a file in the prefix, so it is hex.
func refreshContentKey(home string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	h := sha256.New()
	for _, rel := range files {
		path := filepath.Join(home, rel)
		st, err := os.Stat(path)
		if err != nil || !st.Mode().IsRegular() {
			fmt.Fprintf(h, "%s absent\n", rel)
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(h, "%s unreadable\n", rel)
			continue
		}
		fmt.Fprintf(h, "%s %x\n", rel, sha256.Sum256(data))
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}
