package run

// scratchremoval.go deletes a podman jail's scratch volumes AFTER the user has their
// terminal back (docs/reference/perf-logging.md#the-linger-was-the-scratch-volumes).
//
// THE BUG IT FIXES. Quitting a jail took 32 s on the maintainer's host (2026-09-28, a
// 59 h session): conmon's exit file appeared, podman's `remove` event followed 40 ms
// later, and then the attached `podman run --rm` client spent 31.9 s in unlinkat before
// it exited. That was --rm deleting the container's ANONYMOUS scratch volumes — /tmp,
// /var/tmp, /var/lib/containers, /var/cache/containers — one file at a time, in the
// client, while the launcher waited on it and the terminal waited on the launcher.
//
// THE SHAPE. The volumes are NAMED per launch now (ScratchMountArgs), which --rm does
// not delete, so the client exits as soon as the container is removed. The launcher then
// starts ONE detached process — `yolo internal scratch-rm`, in its own session, stdio on
// /dev/null — and exits without waiting for it. That process waits until podman says no
// container references the volumes, empties each one OUTSIDE podman (a `podman volume rm`
// of a big volume blocks every other `podman run` that mounts a volume, measured), then
// removes it. Whatever it never reaches — a launcher SIGKILLed before the spawn, a host
// that rebooted mid-delete — is dangling and past the age floor at the next launch, whose
// housekeeping slot starts the same remover for it, and `yolo prune --apply` removes it
// too (prune.PruneScratchVolumes).
//
// A DETACHED WRITER OUTLIVES ITS LAUNCH, and that has two consequences this file owns
// (CI run 36383731749: the integration suite's t.TempDir cleanup failed with "directory
// not empty" because a remover recreated `<workspace>/.yolo/housekeeping.log` mid-RemoveAll).
// First, the remover never CREATES the workspace or its `.yolo`: a user who deletes a
// workspace right after quitting must not find it resurrected by a log line
// (writeHousekeepingNote with paths.OpenExistingWorkspaceStateFile). Second, whoever needs
// the workspace quiescent — the suite's cleanup, today — can WAIT for every remover started
// for it: each one holds a shared flock on `<workspace>/.yolo/scratch-rm.lock` for its whole
// life, taken by the launcher BEFORE the spawn and inherited by the child, so there is no
// window in which the launcher has exited and the child has not yet locked
// (WaitForScratchRemovers).

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// ScratchRemoverVerb is the hidden `yolo internal` verb the remover runs as.
const ScratchRemoverVerb = "scratch-rm"

// scratchRemovalWait bounds how long the exit-time remover waits for podman to stop
// referencing the volumes. On the normal exit arm the container is already removed when
// the launcher gets here (the client removes it before it exits), so the first listing
// answers. On the signal arm the stop may still be running, and --rm's cleanup is then
// conmon's; a minute covers `stop -t 5` with margin. Past it the volumes are left for the
// reaper rather than waited on forever.
const scratchRemovalWait = time.Minute

// scratchPollInterval is the remover's re-listing cadence while it waits.
const scratchPollInterval = 500 * time.Millisecond

// ScratchRemoverLockName is the file directly under <workspace>/.yolo on which every
// in-flight remover for that workspace holds a SHARED flock, for its whole life. It is a
// marker, not a mutex: two removers share it, and only a waiter takes it exclusively.
const ScratchRemoverLockName = "scratch-rm.lock"

// scratchRemoverLockFD is the child's descriptor for the inherited lock: exec.Cmd's
// ExtraFiles[0] is always fd 3.
const scratchRemoverLockFD = 3

// scratchRemoverArgv is the remover's command line. "yolo" is substituted for the
// running binary (execx.SelfExecArgv) by the caller, as every self-exec here is. locked
// says the spawn hands the child the in-flight lock as fd 3 (spawnScratchRemover).
func scratchRemoverArgv(rt, workspace string, wait time.Duration, locked bool, names []string) []string {
	argv := []string{"yolo", "internal", ScratchRemoverVerb,
		"--runtime", rt, "--workspace", workspace, "--wait", wait.String()}
	if locked {
		argv = append(argv, "--lock-fd", fmt.Sprint(scratchRemoverLockFD))
	}
	argv = append(argv, "--")
	return append(argv, names...)
}

// scratchRemoverLock opens <workspace>/.yolo/scratch-rm.lock and takes a SHARED flock on
// it, for the spawn to hand to the child; nil when either fails.
//
// NON-BLOCKING, deliberately: `.yolo` is jail-writable, so a jail can hold the file
// exclusively, and a blocking lock here would let it hang the teardown that gives the user
// their terminal back. A remover spawned without the lock still removes the volumes; only
// a waiter's view of it is lost.
func (o *Options) scratchRemoverLock() *os.File {
	f, err := paths.OpenWorkspaceStateFile(o.Workspace, ScratchRemoverLockName, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil
	}
	return f
}

// spawnScratchRemover starts the remover over names, detached, holding the in-flight lock.
// The launcher's own descriptor is closed once the child has its copy, so the lock lives
// exactly as long as the child does.
func (o *Options) spawnScratchRemover(rt string, wait time.Duration, names []string) error {
	lock := o.scratchRemoverLock()
	if lock != nil {
		defer lock.Close()
	}
	argv := o.selfExecArgv(scratchRemoverArgv(rt, o.Workspace, wait, lock != nil, names))
	return o.StartDetached(argv, lock)
}

// selfExecArgv resolves a leading "yolo" to the binary to run. A launch uses the running binary's
// path (execx.SelfExecArgv). A KEEPER uses its own inode on Linux (keeperSelfExe): it can outlive
// its launch by days, across a `just install` that replaced the file at that path, and a scratch
// remover or a daemon it starts must run the code the jail was started with
// (docs/design/jail-lifetime-last-session-wins.md JL-D5).
func (o *Options) selfExecArgv(argv []string) []string {
	if o.keeperMode && len(argv) > 0 && argv[0] == "yolo" {
		if exe := keeperSelfExe(); exe != "" {
			out := append([]string{exe}, argv[1:]...)
			return out
		}
	}
	return execx.SelfExecArgv(argv)
}

// scratchRemoverWaitPoll is WaitForScratchRemovers' re-try cadence.
const scratchRemoverWaitPoll = 50 * time.Millisecond

// WaitForScratchRemovers blocks until no remover started for workspace is still running,
// or timeout passes. No lock file (or no `.yolo`) means none was ever started with one, and
// returns at once; it creates nothing either way.
//
// Its caller is the integration suite, which must not delete a workspace a remover is about
// to log into (see the top of this file). A remover is bounded by scratchRemovalWait plus
// its podman calls, so a timeout past that is the caller's to choose.
func WaitForScratchRemovers(workspace string, timeout time.Duration) error {
	r, err := paths.OpenStateDirRoot(paths.WorkspaceStateDir(workspace))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer r.Close()
	f, err := paths.OpenRegularFileBeneath(r, ScratchRemoverLockName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("a scratch-volume remover for %s is still running after %s", workspace, timeout)
		}
		time.Sleep(scratchRemoverWaitPoll)
	}
}

// startScratchRemoval hands this launch's scratch volumes to the detached remover.
//
// Called from BOTH teardown arms, after the proxy has returned — so after the child was
// reaped and the termios restored (`child.termios_restored`) on the normal arm — and it
// never waits on what it starts: the launcher's exit, which is what gives the shell its
// prompt back, must not depend on how many files the jail left in /tmp. The Once makes
// the two arms one spawn on the signal path, where both run.
//
// A spawn that fails leaves the volumes where the reaper finds them, and says so in the
// housekeeping log rather than on a terminal the user has just been handed back.
func (o *Options) startScratchRemoval(rt string) {
	if len(o.scratchVolumes) == 0 || o.scratchRemovalOnce == nil {
		return
	}
	o.scratchRemovalOnce.Do(func() {
		names := o.scratchVolumes
		if err := o.spawnScratchRemover(rt, scratchRemovalWait, names); err != nil {
			o.Perf.Mark("shutdown.scratch_volumes.rm_not_started")
			o.housekeepingNote("scratch: could not start the removal of %d volume(s) (%v); the next "+
				"launch's housekeeping slot, or `yolo prune --apply`, removes them", len(names), err)
			return
		}
		o.Perf.Mark("shutdown.scratch_volumes.rm_started")
	})
}

// reapScratchVolumes is the housekeeping slot's class for scratch volumes no remover
// reached: dangling (no container references them) and past prune.ScratchVolumeGrace.
// It starts the same detached remover over them with no wait, so a large leftover
// never holds the slot's lock for its deletion.
//
// Not debounced, unlike the walking classes: it is two `podman volume ls` calls, and a
// leak here can be the nested podman store of a whole session, which a day's debounce
// would keep on disk for a day. Tri-state: a listing that did not answer reaps nothing.
func (o *Options) reapScratchVolumes(rt string) {
	if rt != "podman" || o.Getenv(autoReapOptOutEnv) != "" { // parity: NotApplicable — only podman jails get scratch volumes; Apple Container's scratch is tmpfs
		return
	}
	vols, known := prune.ListScratchVolumes(rt, o.pruneRunFunc())
	if !known {
		return
	}
	reap := prune.ReapableScratchVolumes(vols, o.Now(), prune.ScratchVolumeGrace)
	if len(reap) == 0 {
		return
	}
	names := make([]string, 0, len(reap))
	for _, v := range reap {
		names = append(names, v.Name)
	}
	if err := o.spawnScratchRemover(rt, 0, names); err != nil {
		o.housekeepingNote("scratch: could not start the removal of %d leftover volume(s) (%v)", len(names), err)
		return
	}
	o.housekeepingNote("scratch: removing %d leftover volume(s) of gone jails in the background: %s",
		len(names), strings.Join(names, " "))
}

// errTestBinarySelfExec refuses the one spawn that must never happen: a unit test's
// binary re-executing itself as the remover, which would run the test binary with
// arguments it does not understand, from inside a test.
var errTestBinarySelfExec = errors.New("a test binary does not self-exec the scratch remover")

// startDetached is the real Options.StartDetached: start argv in its own session with
// stdio on /dev/null, and do not wait for it. A non-nil inherit is the child's fd 3.
//
// Its own SESSION, so the terminal the launcher is about to hand back is not its
// controlling terminal — closing the window does not SIGHUP it, and no job-control
// signal meant for the shell's foreground job reaches it. Stdio on /DEV/NULL (nil
// fields), because an inherited stdout would hold open whatever the launcher's was:
// `yolo -- make | tee log` would then wait for the remover, which is the linger again by
// another route. The Wait runs on a goroutine only so the child is reaped if it ends
// while the launcher is still alive (the housekeeping slot's spawn outlives it by hours
// otherwise as a zombie); the launcher's exit never waits on it.
func startDetached(argv []string, inherit *os.File) error {
	if len(argv) == 0 {
		return errors.New("empty argv")
	}
	if flag.Lookup("test.v") != nil && len(argv) > 1 && argv[1] == "internal" {
		return errTestBinarySelfExec
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if inherit != nil {
		cmd.ExtraFiles = []*os.File{inherit}
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

// scratchRemovalResult is what one remover run did, per volume name.
type scratchRemovalResult struct {
	removed []string // emptied and removed by this run
	gone    []string // not there when asked: removed already, or never created
	failed  []string // dangling, and the removal did not succeed
	pending []string // still referenced by a container, or unlistable, when the wait ran out
}

// removeScratchVolumes is the remover's loop: list, remove every named volume podman
// reports dangling, and wait for the rest until the deadline. Only names that parse as
// scratch volumes are ever touched — the argv is the whole authority, and this refuses to
// let it name any other volume.
func removeScratchVolumes(rt string, names []string, wait time.Duration, run prune.RunFunc,
	now func() time.Time, sleep func(time.Duration)) scratchRemovalResult {
	var r scratchRemovalResult
	want := map[string]bool{}
	for _, n := range names {
		if _, _, _, ok := prune.ParseScratchVolumeName(n); ok {
			want[n] = true
		} else {
			r.failed = append(r.failed, n)
		}
	}
	deadline := now().Add(wait)
	for {
		if vols, known := prune.ListScratchVolumes(rt, run); known {
			present := map[string]prune.ScratchVolume{}
			for _, v := range vols {
				if want[v.Name] {
					present[v.Name] = v
				}
			}
			for n := range want {
				v, ok := present[n]
				switch {
				case !ok:
					r.gone = append(r.gone, n)
					delete(want, n)
				case v.Dangling:
					if prune.RemoveScratchVolume(rt, v, run) {
						r.removed = append(r.removed, n)
					} else {
						r.failed = append(r.failed, n)
					}
					delete(want, n)
				}
			}
		}
		if len(want) == 0 || !now().Before(deadline) {
			break
		}
		sleep(scratchPollInterval)
	}
	for n := range want {
		r.pending = append(r.pending, n)
	}
	for _, s := range [][]string{r.removed, r.gone, r.failed, r.pending} {
		sort.Strings(s)
	}
	return r
}

// ScratchRemoverMain is `yolo internal scratch-rm --runtime <rt> [--workspace <ws>]
// [--wait <duration>] [--lock-fd <n>] -- <volume>...`: the detached remover. It never
// writes to a terminal (it has none); what it did goes to the workspace's housekeeping.log,
// beside the other things a launch does behind the user's back — but only into a `.yolo`
// that still exists. --lock-fd names the inherited in-flight lock, which it holds by
// simply keeping the descriptor open until it exits, and keeps out of the podman calls it
// makes.
func ScratchRemoverMain(args []string) int {
	rt, workspace := "", ""
	wait := time.Duration(0)
	var names []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			names = append(names, args[i+1:]...)
			i = len(args)
		case a == "--runtime" && i+1 < len(args):
			rt = args[i+1]
			i++
		case a == "--workspace" && i+1 < len(args):
			workspace = args[i+1]
			i++
		case a == "--lock-fd" && i+1 < len(args):
			var fd int
			if _, err := fmt.Sscan(args[i+1], &fd); err != nil || fd < 3 {
				fmt.Fprintf(os.Stderr, "yolo internal %s: bad --lock-fd %q\n", ScratchRemoverVerb, args[i+1])
				return 2
			}
			syscall.CloseOnExec(fd)
			i++
		case a == "--wait" && i+1 < len(args):
			d, err := time.ParseDuration(args[i+1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "yolo internal %s: bad --wait %q\n", ScratchRemoverVerb, args[i+1])
				return 2
			}
			wait = d
			i++
		default:
			fmt.Fprintf(os.Stderr, "yolo internal %s: unexpected argument %q\n", ScratchRemoverVerb, a)
			return 2
		}
	}
	if rt == "" || len(names) == 0 {
		fmt.Fprintf(os.Stderr, "usage: yolo internal %s --runtime <rt> [--workspace <ws>] [--wait <d>] [--lock-fd <n>] -- <volume>...\n",
			ScratchRemoverVerb)
		return 2
	}
	// Below the user's interactive work: the deletion is bulk metadata IO nobody is
	// waiting for any more. Best-effort.
	_ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, 10)

	run := func(argv []string, timeout time.Duration) prune.ProbeResult {
		res := realExec(argv, "", nil, timeout)
		return prune.ProbeResult{Stdout: res.Stdout, RC: res.RC, Ran: res.Ran && !res.Timeout}
	}
	start := time.Now()
	r := removeScratchVolumes(rt, names, wait, run, time.Now, time.Sleep)
	if workspace != "" {
		// Never paths.OpenWorkspaceStateFile, which creates `.yolo` and so the workspace
		// above it: this runs up to a minute after the jail exited, and the workspace may
		// be gone by then.
		writeHousekeepingNote(paths.OpenExistingWorkspaceStateFile, workspace, time.Now(),
			"scratch: "+r.summary(time.Since(start)))
	}
	if len(r.failed) > 0 || len(r.pending) > 0 {
		return 1
	}
	return 0
}

// summary is the one housekeeping line a remover run leaves.
func (r scratchRemovalResult) summary(took time.Duration) string {
	parts := []string{fmt.Sprintf("removed %d volume(s) in %.1fs", len(r.removed), took.Seconds())}
	if len(r.gone) > 0 {
		parts = append(parts, fmt.Sprintf("%d already gone", len(r.gone)))
	}
	if len(r.failed) > 0 {
		parts = append(parts, "could not remove "+strings.Join(r.failed, " "))
	}
	if len(r.pending) > 0 {
		parts = append(parts, "still in use or unlistable, left for the next launch's slot or "+
			"`yolo prune --apply`: "+strings.Join(r.pending, " "))
	}
	return strings.Join(parts, "; ")
}
