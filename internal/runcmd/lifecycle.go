package runcmd

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

// Teardown timing (mirror run_cmd.py module constants).
const (
	teardownStopTimeoutSeconds     = 5
	lockReleasePollAttempts        = 20
	lockReleasePollIntervalSeconds = 0.25
)

// ownerPIDDir mirrors OWNER_PID_DIR = GLOBAL_STORAGE / "owners".
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

// pidAlive ports _pid_alive: True if a process with pid exists (owned by
// anyone). ProcessLookupError → dead; PermissionError → alive; any other error →
// assume alive (never reap a jail we can't prove is orphaned). execx's tri-state
// maps LivenessUnknown → alive here (the conservative _pid_alive polarity).
func pidAlive(pid int) bool {
	switch execx.ProcessLiveness(pid) {
	case execx.LivenessDead:
		return false
	default:
		return true // Alive or Unknown → alive (conservative)
	}
}

// findRunningContainer ports find_running_container: the container ID/name if a
// container with this name is running, else "". Uses the Exec seam.
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

// findExistingContainer ports find_existing_container: running OR stopped.
func (o *Options) findExistingContainer(cname, rt string) string {
	if rt == "container" {
		res := o.Exec([]string{"container", "ls", "--all"}, "", nil, 0)
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
	res := o.Exec([]string{rt, "ps", "-a", "-q", "--filter", "name=^/" + cname + "$"}, "", nil, 0)
	if !res.Ran {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// removeStaleContainer ports _remove_stale_container.
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

// liveYoloContainers ports _live_yolo_containers: names of yolo-* containers
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

// stopJail ports _stop_jail: best-effort stop (--rm removes it), then drop the
// owner-PID file. Bounded timeout so teardown can't hang.
func (o *Options) stopJail(cname, rt string) {
	if rt == "container" {
		o.Exec([]string{"container", "stop", cname}, "", nil, 30*time.Second)
	} else {
		o.Exec([]string{rt, "stop", "-t", strconv.Itoa(teardownStopTimeoutSeconds), cname}, "", nil,
			time.Duration(teardownStopTimeoutSeconds+5)*time.Second)
	}
	clearOwnerPID(cname)
}

// reapOrphanedJails ports _reap_orphaned_jails: stop running jails whose owning
// yolo-run process is gone. Conservative — only reaps what it can prove orphaned
// (a live jail with a dead recorded owner PID). Apple Container has no owner-PID
// lifecycle yet, so it's a no-op there.
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
		if !pidAlive(pid) {
			out.printf("[dim]Reaping orphaned jail %s (owner pid %d is gone)...[/dim]", name, pid)
			o.stopJail(name, rt)
		}
	}
}

// maybeWarnAboutOOMKiller ports _maybe_warn_about_oom_killer: on macOS+podman
// exit 137 with a Podman Machine under the recommended memory floor, print the
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

// podmanMachineMemory ports _podman_machine_memory via the Exec seam + the
// runtime parser. Returns (name, memMB, ok).
func (o *Options) podmanMachineMemory() (string, int, bool) {
	res := o.Exec([]string{"podman", "machine", "inspect"}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 || strings.TrimSpace(res.Stdout) == "" {
		return "", 0, false
	}
	return parsePodmanMachineMemory(res.Stdout)
}

// parsePodmanMachineMemory runs the JSON parse of _podman_machine_memory:
// prefer a running machine, else the first; read Resources.Memory (MB).
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
// line), matching splitlines()[1:] with the empty-input guard.
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
