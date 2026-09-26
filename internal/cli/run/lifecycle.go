package run

import (
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

// awaitRunningContainer polls findRunningContainer until the container is
// visible (or the bounded attempts run out) and returns what it printed — the
// container's short id for podman, "" when it never appeared. onStarted's wait:
// the lock is released after it, and the Window A probe is armed with its id.
func (o *Options) awaitRunningContainer(cname, rt string) string {
	for i := 0; i < lockReleasePollAttempts; i++ {
		if id := o.findRunningContainer(cname, rt); id != "" {
			return id
		}
		time.Sleep(time.Duration(lockReleasePollIntervalSeconds * float64(time.Second)))
	}
	return ""
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

// stopJail does a best-effort stop (--rm removes it), then drops the owner-PID
// file. Bounded timeout so teardown can't hang.
func (o *Options) stopJail(cname, rt string) {
	if rt == "container" {
		o.Exec([]string{"container", "stop", cname}, "", nil, 30*time.Second)
	} else {
		o.Exec([]string{rt, "stop", "-t", strconv.Itoa(teardownStopTimeoutSeconds), cname}, "", nil,
			time.Duration(teardownStopTimeoutSeconds+5)*time.Second)
	}
	clearOwnerPID(cname)
}

// reapOrphanedJails stops running jails whose owning yolo-run process is gone.
// Conservative — only reaps what it can prove orphaned
// (a live jail with a dead recorded owner PID). Apple Container has no owner-PID
// lifecycle yet, so it's a no-op there.
//
// The orphan's host-services dir goes with it. Its owner died without its teardown, so
// nothing else removes the dir, and the endpoint files in it name fronts that died with
// the owner. It goes through stopLoopholes' own guard stack (the orphan's relaunch lock,
// then the tri-state existence probe) rather than a bare rmtree, because the orphan's
// workspace may be relaunching while this reap runs: a relaunch that holds the lock, or
// whose container exists but is not yet running, keeps the dir
// (TestReapingAnOrphanKeepsTheDirOfARelaunch).
func (o *Options) reapOrphanedJails(rt string) {
	if rt == "container" {
		return
	}
	live, ok := o.liveYoloContainers(rt)
	if !ok || len(live) == 0 {
		return
	}
	out := o.pr(o.Stdout)
	for name := range live {
		raw, err := os.ReadFile(ownerPIDFile(name))
		if err != nil {
			continue // no owner recorded — can't prove orphaned
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		if !o.PIDAlive(pid) {
			out.printf("[dim]Reaping orphaned jail %s (owner pid %d is gone)...[/dim]", name, pid)
			o.stopJail(name, rt)
			o.stopLoopholes(nil, hostServiceSocketsDir(name, o.IsMacOS), name, rt)
		}
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
