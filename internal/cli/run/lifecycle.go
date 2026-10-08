package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Teardown timing.
const (
	teardownStopTimeoutSeconds     = 5
	lockReleasePollAttempts        = 20
	lockReleasePollIntervalSeconds = 0.25
)

func ownerPIDDir() string { return filepath.Join(paths.GlobalStorage(), "owners") }

func ownerPIDFile(cname string) string { return filepath.Join(ownerPIDDir(), cname) }

// writeOwnerPID records that THIS process started the jail. Best-effort.
func (o *Options) writeOwnerPID(cname string) {
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(ownerPIDFile(cname), []byte(strconv.Itoa(o.Getpid())+"\n"), 0o644)
}

// clearOwnerPID removes the owner-PID file (missing_ok).
func clearOwnerPID(cname string) {
	_ = os.Remove(ownerPIDFile(cname))
}

// pidAlive is true if a process with pid exists (owned by anyone). Process not
// found → dead; permission denied → alive; any other error → assume alive
// (never reap a jail we can't prove is orphaned). execx's tri-state maps
// LivenessUnknown → alive here (the conservative polarity).
func pidAlive(pid int) bool {
	switch execx.ProcessLiveness(pid) {
	case execx.LivenessDead:
		return false
	default:
		return true // Alive or Unknown → alive (conservative)
	}
}

// findRunningContainer returns the container ID/name if a container with this
// name is running, else "". Uses the Exec seam.
func (o *Options) findRunningContainer(cname, rt string) string {
	if rt == "container" {
		res := o.Exec([]string{"container", "ls"}, "", nil, 0)
		if !res.Ran {
			return ""
		}
		for _, line := range tableBody(res.Stdout) {
			parts := strings.Fields(line)
			if len(parts) > 0 && parts[0] == cname {
				return cname
			}
		}
		return ""
	}
	res := o.Exec([]string{rt, "ps", "-q", "--filter", "name=^/" + cname + "$"}, "", nil, 0)
	if !res.Ran {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// attachProbeTimeout bounds the attach decision's `ps` (probeRunningContainer). It runs after
// the readiness gate has just heard podman answer, so it is not waiting out a post-boot
// refresh; what it can still wait behind is another launch's container create, which takes
// seconds, not half a minute.
const attachProbeTimeout = 30 * time.Second

// probeRunningContainer is findRunningContainer with the TRI-STATE kept, in
// probeExistingContainer's shape (PR-D8 of docs/design/podman-reboot-readiness.md): known is
// true only when the runtime answered — it ran, within timeout, and exited 0. So ("", true) is
// "no container of this name is running" and ("", false) is "could not ask".
//
// Its one caller is the attach decision, which REFUSES on "could not ask". findRunningContainer
// read a failed `ps` as "not running", so a runtime that could not answer sent the launch down
// the fresh path, beside a jail that might be up.
func (o *Options) probeRunningContainer(cname, rt string, timeout time.Duration) (string, bool) {
	if rt == "container" { // parity: HonoredBy — `container ls` lists running containers only, the question `ps -q` answers on podman, with the same tri-state
		res := o.Exec([]string{"container", "ls"}, "", nil, timeout)
		if !res.Ran {
			return "", false
		}
		for _, line := range tableBody(res.Stdout) {
			parts := strings.Fields(line)
			if len(parts) > 0 && parts[0] == cname {
				return cname, true
			}
		}
		return "", !res.Timeout && res.RC == 0
	}
	res := o.Exec([]string{rt, "ps", "-q", "--filter", "name=^/" + cname + "$"}, "", nil, timeout)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return "", false
	}
	return strings.TrimSpace(res.Stdout), true
}

// runningListCommand is the command that lists rt's running containers, the question the
// attach decision asks: the one a user runs to see why it could not be asked.
func runningListCommand(rt string) string {
	if rt == "container" { // parity: HonoredBy — Apple's CLI lists running containers with `container ls`, the question `ps` answers on podman; the hint names the command probeRunningContainer ran
		return "container ls"
	}
	return rt + " ps"
}

// findExistingContainer returns the container ID/name if it exists, running OR
// stopped.
func (o *Options) findExistingContainer(cname, rt string) string {
	id, _ := o.probeExistingContainer(cname, rt, 0)
	return id
}

// probeExistingContainer is findExistingContainer with the TRI-STATE kept rather than
// collapsed: known is true only when the runtime answered (it ran, within timeout, and
// exited 0). So ("", true) is "no container of this name exists" and ("", false) is "could
// not ask", which findExistingContainer reads the same way and forgetGoneContainer must not.
// The id is what findExistingContainer has always returned, whatever the exit code.
func (o *Options) probeExistingContainer(cname, rt string, timeout time.Duration) (string, bool) {
	if rt == "container" {
		res := o.Exec([]string{"container", "ls", "--all"}, "", nil, timeout)
		if !res.Ran {
			return "", false
		}
		for _, line := range tableBody(res.Stdout) {
			parts := strings.Fields(line)
			if len(parts) > 0 && parts[0] == cname {
				return cname, true
			}
		}
		return "", !res.Timeout && res.RC == 0
	}
	res := o.Exec([]string{rt, "ps", "-a", "-q", "--filter", "name=^/" + cname + "$"}, "", nil, timeout)
	if !res.Ran {
		return "", false
	}
	id := strings.TrimSpace(res.Stdout)
	return id, id != "" || (!res.Timeout && res.RC == 0)
}

// ProbeExistingContainer asks the selected runtime whether cname exists, including stopped
// containers. It keeps the tri-state result: false/true means the runtime answered and the
// container is absent; true/true means it exists; either state with known=false means the caller
// must not reuse resources that a still-live container may hold.
func ProbeExistingContainer(cname, rt string, timeout time.Duration) (exists, known bool) {
	o := NewDefaultOptions()
	o.Exec = realExec
	id, known := o.probeExistingContainer(cname, rt, timeout)
	return id != "", known
}

// removeStaleContainer force-removes a container and clears its tracking.
func (o *Options) removeStaleContainer(cname, rt string) bool {
	var res ExecResult
	if rt == "container" {
		res = o.Exec([]string{"container", "rm", "--force", cname}, "", nil, 0)
	} else {
		res = o.Exec([]string{rt, "rm", cname}, "", nil, 0)
	}
	if res.Ran && res.RC == 0 {
		runtime.CleanupContainerTracking(cname)
		return true
	}
	return false
}

// forceRemoveStoppedContainer removes cname, which the caller has just seen stopped, by force, and
// reports whether the existence probe then answers that no container of the name exists. The
// removal's own status is not the answer: podman's forced rm of a container whose exec session it
// cannot take a handle on removes the container and still exits 125 (JL-D82). Never for a container
// that may run: removeStaleContainer is the removal that refuses a live one.
func (o *Options) forceRemoveStoppedContainer(cname, rt string) bool {
	o.Exec([]string{rt, "rm", "--force", cname}, "", nil, trackingProbeTimeout)
	if id, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); known && id == "" {
		runtime.CleanupContainerTracking(cname)
		return true
	}
	return false
}

// liveYoloContainers returns the names of yolo-* containers
// running/paused/restarting, or (nil, false) when the runtime can't be
// enumerated ("liveness unknown" — never read as "nothing live").
func (o *Options) liveYoloContainers(rt string) (map[string]struct{}, bool) {
	if rt == "container" {
		res := o.Exec([]string{"container", "ls"}, "", nil, 10*time.Second)
		if !res.Ran || res.Timeout || res.RC != 0 {
			return nil, false
		}
		return runtime.ParseContainerLsLive(res.Stdout), true
	}
	res := o.Exec([]string{rt, "ps", "-a", "--format", "{{.Names}} {{.State}}"}, "", nil, 10*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return nil, false
	}
	return runtime.ParsePodmanLive(res.Stdout), true
}

// stopJail does a best-effort stop (--rm removes it). Bounded timeout so teardown can't hang.
//
// reason says why, and is recorded BEFORE the stop (stopreason.go): every session the stop ends
// prints it, and a record written after would race the sessions reading it. Every caller has to
// give one, so no stop is silent to the sessions it ends.
//
// THE OWNER-PID FILE IS NOT ITS TO REMOVE any more. It names the jail's keeper, and a stop may come
// from anyone: the keeper's own teardown removes it while it still names the keeper, the reaper
// while it still names the dead owner it read (clearOwnerPIDIf, JL-D28 (4)). An unconditional
// removal here took the file of the next jail's keeper away whenever a stop ran late.
func (o *Options) stopJail(cname, rt, reason string) {
	o.recordJailStop(cname, reason)
	if rt == "container" {
		o.Exec([]string{"container", "stop", cname}, "", nil, 30*time.Second)
	} else {
		o.Exec([]string{rt, "stop", "-t", strconv.Itoa(teardownStopTimeoutSeconds), cname}, "", nil,
			time.Duration(teardownStopTimeoutSeconds+5)*time.Second)
	}
}

// reapOrphanedJails stops running jails whose owner is gone. Conservative — only reaps what it can
// prove orphaned: a live jail whose owner-PID file names a dead process, whose keeper's liveness
// lock is free, and whose session lock no session holds.
//
// THE LIVENESS LOCK IS THE EVIDENCE for a jail a keeper owns (JL-D7, JL-D18): the reaper takes it
// exclusively, then the session lock, and holds both across the stop and the host-services cleanup,
// so an arrival meanwhile waits for the reap as it waits for a draining keeper (JL-D28). A lock it
// cannot take is a keeper alive, or another reaper at work: it leaves the jail. A jail started before
// keepers has no keeper to hold the lock, so its lock is free and its old owner-PID rule decides.
//
// APPLE CONTAINER'S KEEPER-ERA JAILS ARE REAPED TOO: its jails used to have no owner-PID lifecycle,
// so the reaper returned at once there; a jail a keeper started there has a start record
// (keeperstate.go), which is what makes its dead owner evidence. One started before keepers still
// has none, and is left as it always was.
//
// The orphan's host-services dir goes with it. Its owner died without its teardown, so
// nothing else removes the dir, and the endpoint files in it name fronts that died with
// the owner. It goes through stopLoopholes' own guard stack (the orphan's relaunch lock,
// then the tri-state existence probe) rather than a bare rmtree, because the orphan's
// workspace may be relaunching while this reap runs: a relaunch that holds the lock, or
// whose container exists but is not yet running, keeps the dir
// (TestReapingAnOrphanKeepsTheDirOfARelaunch).
//
// A DEAD OWNER IS NOT ENOUGH: the jail must also have no session in it. A first launcher
// SIGKILLed while another terminal was attached leaves that terminal's session running in the
// jail, and stopping the jail would end it (docs/design/jail-lifetime-last-session-wins.md
// §2.3, item 4). So the reaper takes the jail's session lock exclusively (sessionlock.go),
// which succeeds only while no session holds it, and HOLDS it across the stop and the
// host-services cleanup (JL-D7): an arrival meanwhile cannot count itself into a jail being
// stopped, and waits instead (takeSessionLock). A lock it cannot take, or cannot open, is a
// jail it leaves alone and says nothing about: its sessions are the evidence it is not
// orphaned, and "could not count" is never zero.
func (o *Options) reapOrphanedJails(rt string) {
	live, ok := o.liveYoloContainers(rt)
	if !ok || len(live) == 0 {
		return
	}
	out := o.pr(o.Stdout)
	for name := range live {
		if rt == "container" { // parity: HonoredBy — only a keeper-era Apple Container jail has an owner-PID lifecycle, whose start record is the evidence
			if _, keeperEra := readKeeperRecord(name); !keeperEra {
				continue
			}
		}
		pid, ok := readOwnerPID(name)
		if !ok {
			continue // no owner recorded — can't prove orphaned
		}
		liveness, err := holdLivenessLock(name)
		if err != nil {
			continue // its keeper is alive, or another reaper is at work
		}
		if o.PIDAlive(pid) {
			releaseLock(liveness)
			continue
		}
		sessions, ok := tryExclusiveSessionLock(name)
		if !ok {
			releaseLock(liveness)
			continue // a session is still in it, or its count cannot be read
		}
		out.printf("[dim]Reaping orphaned jail %s (owner pid %d is gone)...[/dim]", name, pid)
		o.stopJail(name, rt, orphanReapReason(o.Getpid(), pid))
		o.stopLoopholes(nil, hostServiceSocketsDir(name, o.IsMacOS), name, rt)
		clearOwnerPIDIf(name, pid)
		removeKeeperRecord(name, pid)
		sessions.release()
		releaseLock(liveness)
	}
}

// refuseUnkeptJail is an arrival at a running jail whose keeper is dead (an UNKEPT jail): it is
// refused, as OQ-JL7 ruled (JL-D13), naming what is left in it and the one remedy. The sessions run
// on without host services, and nothing restarts the keeper ("a jail whose launcher is gone is
// relaunched, not attached-and-repaired", attachExisting): entering would put one more session in a
// jail with no credential services, port forwards or cgroup delegate. `yolo stop` then tears it down
// itself, and the next launch is fresh (JL-D30).
//
// A jail whose liveness lock is free and which has no start record and a live owner is not unkept:
// an older yolo's launcher owns it, and the arrival attaches as it always did.
func (o *Options) refuseUnkeptJail(cname, rt string) bool {
	if probeKeeper(cname) != keeperGone {
		return false
	}
	rec, era := o.keeperEra(cname)
	if !era {
		return false
	}
	who := "its keeper"
	if rec.PID > 0 {
		who = fmt.Sprintf("its keeper (pid %d)", rec.PID)
	}
	o.pr(o.Stderr).printf("[bold red]Refusing to enter %s: %s is gone, so the jail's host services "+
		"(yolo's credential services, port forwards and the cgroup delegate) are down, and %s "+
		"without them.[/bold red]", cname, who, o.jailSessionsLeft(rt, cname))
	o.pr(o.Stderr).printf("[dim]Run %s: it ends them and the jail, and a launch after it starts the "+
		"jail fresh.[/dim]", stopRemedy(rt, cname))
	return true
}

// jailSessionsLeft words the sessions that still run in a jail, for display (jailSessionCount).
func (o *Options) jailSessionsLeft(rt, cname string) string {
	n, ok := o.jailSessionCount(rt, cname)
	switch {
	case !ok:
		return "its sessions still run in it"
	case n == 1:
		return "1 session still runs in it"
	default:
		return fmt.Sprintf("%d sessions still run in it", n)
	}
}

// maybeWarnAboutOOMKiller: on macOS+podman exit 137 with a Podman Machine under
// the recommended memory floor, print the
// OOM hint. A single `podman machine inspect` probe.
func (o *Options) maybeWarnAboutOOMKiller(exitCode int, rt string) {
	if !(o.IsMacOS && rt == "podman" && exitCode == 137) {
		return
	}
	name, memMB, ok := o.podmanMachineMemory()
	msg, show := runtime.OOMKillerWarning(exitCode, rt, o.IsMacOS, name, memMB, ok)
	if show {
		o.pr(o.Stdout).print("[dim]" + msg + "[/dim]")
	}
}

// podmanMachineMemory probes the Podman Machine via the Exec seam + the
// runtime parser. Returns (name, memMB, ok).
func (o *Options) podmanMachineMemory() (string, int, bool) {
	res := o.Exec([]string{"podman", "machine", "inspect"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 || strings.TrimSpace(res.Stdout) == "" {
		return "", 0, false
	}
	return parsePodmanMachineMemory(res.Stdout)
}

// parsePodmanMachineMemory parses `podman machine inspect` JSON: prefer a
// running machine, else the first; read Resources.Memory (MB).
func parsePodmanMachineMemory(stdout string) (string, int, bool) {
	decoded, err := jsonx.Decode([]byte(stdout))
	if err != nil {
		return "", 0, false
	}
	machines, ok := decoded.([]any)
	if !ok || len(machines) == 0 {
		return "", 0, false
	}
	var machine *jsonx.OrderedMap
	for _, m := range machines {
		mm, ok := m.(*jsonx.OrderedMap)
		if !ok {
			continue
		}
		if st, _ := mm.Get("State"); st == "running" {
			machine = mm
			break
		}
	}
	if machine == nil {
		if mm, ok := machines[0].(*jsonx.OrderedMap); ok {
			machine = mm
		}
	}
	if machine == nil {
		return "", 0, false
	}
	resV, _ := machine.Get("Resources")
	resources, ok := resV.(*jsonx.OrderedMap)
	if !ok {
		return "", 0, false
	}
	memV, _ := resources.Get("Memory")
	memMB, ok := jsonx.AsInt(memV)
	if !ok || memMB <= 0 {
		return "", 0, false
	}
	name := ""
	if nv, _ := machine.Get("Name"); nv != nil {
		if s, ok := nv.(string); ok {
			name = s
		}
	}
	if name == "" {
		name = "podman-machine-default"
	}
	return name, int(memMB), true
}

// tableBody returns the non-header lines of a runtime `ls` table (skip the first
// line), with an empty-input guard.
func tableBody(stdout string) []string {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) <= 1 {
		return nil
	}
	return lines[1:]
}

// waitForRunningContainer polls for cname to become RUNNING, returning its id or "".
//
// It exists for one caller: the launch that found a container it could not remove. That
// container is alive by definition — `rm` refused it — so it is on its way to running, and
// the only question is whether this launch is willing to wait a moment rather than create
// into a name collision (run.go, the stale-removal block).
//
// BOUNDED AND SILENT ON TIMEOUT. Giving up returns "" and lets the caller carry on to the
// create it would have attempted anyway, so this can only ever turn a certain failure into a
// possible success. The window it covers is the gap between `created` and `running`, which is
// milliseconds on an idle machine and seconds on a loaded CI runner; five seconds is chosen
// to cover the latter without making a genuinely wedged container look like a slow one.
func (o *Options) waitForRunningContainer(cname, rt string) string {
	deadline := o.Now().Add(5 * time.Second)
	for {
		if cid := o.findRunningContainer(cname, rt); cid != "" {
			return cid
		}
		if !o.Now().Before(deadline) {
			return ""
		}
		time.Sleep(100 * time.Millisecond)
	}
}
