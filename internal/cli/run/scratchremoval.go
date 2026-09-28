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

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
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

// scratchRemoverArgv is the remover's command line. "yolo" is substituted for the
// running binary (execx.SelfExecArgv) by the caller, as every self-exec here is.
func scratchRemoverArgv(rt, workspace string, wait time.Duration, names []string) []string {
	argv := []string{"yolo", "internal", ScratchRemoverVerb,
		"--runtime", rt, "--workspace", workspace, "--wait", wait.String(), "--"}
	return append(argv, names...)
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
		argv := execx.SelfExecArgv(scratchRemoverArgv(rt, o.Workspace, scratchRemovalWait, names))
		if err := o.StartDetached(argv); err != nil {
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
	argv := execx.SelfExecArgv(scratchRemoverArgv(rt, o.Workspace, 0, names))
	if err := o.StartDetached(argv); err != nil {
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
// stdio on /dev/null, and do not wait for it.
//
// Its own SESSION, so the terminal the launcher is about to hand back is not its
// controlling terminal — closing the window does not SIGHUP it, and no job-control
// signal meant for the shell's foreground job reaches it. Stdio on /DEV/NULL (nil
// fields), because an inherited stdout would hold open whatever the launcher's was:
// `yolo -- make | tee log` would then wait for the remover, which is the linger again by
// another route. The Wait runs on a goroutine only so the child is reaped if it ends
// while the launcher is still alive (the housekeeping slot's spawn outlives it by hours
// otherwise as a zombie); the launcher's exit never waits on it.
func startDetached(argv []string) error {
	if len(argv) == 0 {
		return errors.New("empty argv")
	}
	if flag.Lookup("test.v") != nil && len(argv) > 1 && argv[1] == "internal" {
		return errTestBinarySelfExec
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
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
// [--wait <duration>] -- <volume>...`: the detached remover. It never writes to a
// terminal (it has none); what it did goes to the workspace's housekeeping.log, beside
// the other things a launch does behind the user's back.
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
		fmt.Fprintf(os.Stderr, "usage: yolo internal %s --runtime <rt> [--workspace <ws>] [--wait <d>] -- <volume>...\n",
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
		o := &Options{Workspace: workspace, Now: time.Now}
		o.housekeepingNote("scratch: %s", r.summary(time.Since(start)))
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
