package run

// liveapplyresources.go is the resources slice of `yolo config apply`
// (docs/design/live-config-apply.md LA-S4c, LA-D23 to LA-D25): a changed `resources.cpus`,
// `resources.memory` or `resources.pids_limit` reaches a running podman jail through
// `podman update`, which rewrites the container's cgroup limits in place. Everything else under
// `resources` keeps the restart, each for its own reason.

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// liveResourceKeys are the `resources` children `podman update` can change, each with the flag
// the launch passes for it (appliedResourceLimits). `io` is not one: an attach reads the
// launched value on purpose (prepare.go), and the entrypoint applied it to the jail's processes
// when it started.
var liveResourceKeys = map[string]bool{"cpus": true, "memory": true, "pids_limit": true}

// resourcesLiveFor is whether this apply can change the running jail's limits, and why not when
// it cannot (LA-D24). Only podman has the verb. A jail started from inside another container is
// podman-in-podman, which this image runs with `--cgroups=disabled`
// (/etc/containers/containers.conf), so there is no cgroup of its own to change.
func (o *Options) resourcesLiveFor(rt string) (bool, string) {
	if rt != "podman" { // parity: Warned — Apple Container has no update verb; its line says so
		return false, ""
	}
	if o.inContainer() {
		return false, "a jail started from inside another container runs with cgroups disabled, so podman cannot change its limits"
	}
	return true, ""
}

// resourcesOutcome is the `resources` row for this jail.
func resourcesOutcome(rt string, live bool, reason string) (applyOutcome, string) {
	row, err := changetiming.Classify("resources", changetiming.Context{Backend: timingBackend(rt), LiveResources: live})
	if err != nil {
		return outcomeNextRestart, "no change-timing row is available for resources"
	}
	if reason != "" && row.Outcome == changetiming.AtNextRestart {
		row.Reason = reason
	}
	return applyOutcomeForTiming(row.Outcome), row.Reason
}

// resourceChanges is one change per changed child of the `resources` table at path (the
// readable `machine.resources`, or a legacy top-level `resources`), each folded alone, so a
// live `cpus` change is not held back by an `io` change still owed to a restart.
//
// The value `podman update` is handed is the composed config's (in.resourceLimits), the one a
// launch would pass now, not the workspace file's: a user-scope value composes with it.
func resourceChanges(in applyPlanInput, path string, boot, current any) []applyChange {
	bm, bok := boot.(*jsonx.OrderedMap)
	cm, cok := current.(*jsonx.OrderedMap)
	if (boot != nil && !bok) || (current != nil && !cok) {
		// Not a table on one side: there is no child to change in place.
		o, why := resourcesOutcome(in.rt, false, "")
		sign := "~"
		if boot == nil {
			sign = "+"
		} else if current == nil {
			sign = "-"
		}
		return []applyChange{{kind: changeKey, sign: sign, label: path, key: path, outcome: o, reason: why}}
	}
	mo, mwhy := resourcesOutcome(in.rt, in.resourcesLive, in.resourcesReason)
	var out []applyChange
	for _, sub := range changedKeys(bm, cm) {
		c := applyChange{kind: changeKey, sign: keySign(bm, cm, sub), label: path + "." + sub,
			key: path + "." + sub, outcome: mo, reason: mwhy}
		switch {
		case mo != outcomeAppliedNow:
		case !liveResourceKeys[sub]:
			c.outcome = outcomeNextRestart
			if sub == "io" {
				c.reason = "the disk I/O priority is set on the jail's processes when it starts"
			} else {
				c.reason = "podman update changes only cpus, memory and pids_limit"
			}
		default:
			flag, value, ok := composedResourceFlag(in.resourceLimits, sub)
			if !ok {
				// A dropped cpus or memory limit: the launch passes no flag for it, and the
				// unlimited value has no spelling this apply trusts across podman versions.
				c.outcome = outcomeNextRestart
				c.reason = "a removed limit is lifted when the jail starts"
				break
			}
			c.kind = changeResources
			c.resourceFlag, c.resourceValue = flag, value
		}
		out = append(out, c)
	}
	return out
}

// composedResourceFlag is the flag and value the launch would pass now for the config key sub.
func composedResourceFlag(limits []resourceLimit, sub string) (flag, value string, ok bool) {
	for _, l := range limits {
		if l.key == sub {
			return l.flag, l.value, true
		}
	}
	return "", "", false
}

// updateResourcesSeam runs `podman update <flag> <value> <cname>`, ending by deadline.
var updateResourcesSeam = (*Options).updateResources

// updateResources changes one cgroup limit of the running jail. podman's own words are the
// error, since they name what is missing (a rootless host without cgroup v2 controller
// delegation answers here; M6).
func (o *Options) updateResources(rt, cname, flag, value string, deadline time.Time) error {
	timeout := time.Until(deadline)
	if timeout <= 0 {
		return errors.New("the step's deadline passed before podman update ran")
	}
	argv := []string{rt, "update", flag, value, cname}
	r := o.Exec(argv, "", nil, timeout)
	switch {
	case !r.Ran:
		return errors.New("could not run " + strings.Join(argv, " "))
	case r.Timeout:
		return errors.New(strings.Join(argv, " ") + " did not finish in time")
	case r.RC != 0:
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(r.Stdout)
		}
		if msg == "" {
			msg = "exit " + strconv.Itoa(r.RC)
		}
		return errors.New("podman update failed: " + msg)
	}
	// THE EXIT IS NOT THE WITNESS. In a nested jail `podman update --memory` was measured
	// failing to write memory.max while still moving the container's recorded limit
	// (docs/plans/setup-support-gaps.md), so the line says applied now only once the jail's own
	// cgroup file shows the value.
	return o.verifyCgroupLimit(rt, cname, flag, value, deadline)
}

// cgroupLimitFiles are the cgroup v2 files, as the jail sees its own cgroup, that show each
// flag's limit.
var cgroupLimitFiles = map[string]string{
	"--cpus": "/sys/fs/cgroup/cpu.max", "--memory": "/sys/fs/cgroup/memory.max", "--pids-limit": "/sys/fs/cgroup/pids.max",
}

// verifyCgroupLimit reads the limit's cgroup file inside the jail and compares it with value.
func (o *Options) verifyCgroupLimit(rt, cname, flag, value string, deadline time.Time) error {
	file := cgroupLimitFiles[flag]
	timeout := time.Until(deadline)
	if file == "" || timeout <= 0 {
		return errors.New("podman update ran, but there was no time left to check the jail's cgroup")
	}
	r := o.Exec(applyExecArgv(rt, false, cname, "cat", file), "", nil, timeout)
	if !r.Ran || r.Timeout || r.RC != 0 {
		return errors.New("podman update ran, but the jail's " + file + " could not be read to confirm it")
	}
	if !cgroupShowsLimit(flag, value, strings.TrimSpace(r.Stdout)) {
		return errors.New("podman update ran, but the jail's " + file + " reads " +
			strings.TrimSpace(r.Stdout) + ", not " + value)
	}
	return nil
}

// cgroupShowsLimit reports whether a cgroup v2 file's content is the limit value asks for:
// cpu.max is "<quota> <period>" whose ratio is the CPU count, memory.max and pids.max a number.
// Anything it cannot parse is not shown.
func cgroupShowsLimit(flag, value, content string) bool {
	switch flag {
	case "--cpus":
		want, err := strconv.ParseFloat(value, 64)
		f := strings.Fields(content)
		if err != nil || len(f) != 2 {
			return false
		}
		quota, err1 := strconv.ParseFloat(f[0], 64)
		period, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil || period <= 0 {
			return false
		}
		got := quota / period
		return got > want-0.001 && got < want+0.001
	case "--memory":
		want, ok := memoryBytes(value)
		got, err := strconv.ParseInt(content, 10, 64)
		// The kernel rounds memory.max down to its page size.
		return ok && err == nil && got <= want && want-got < 65536
	case "--pids-limit":
		return content == strings.TrimSpace(value)
	}
	return false
}

// memoryBytes is podman's memory spelling (a number with an optional b, k, m or g suffix,
// powers of 1024) in bytes.
func memoryBytes(v string) (int64, bool) {
	s := strings.ToLower(strings.TrimSpace(v))
	s = strings.TrimSuffix(s, "b")
	mult := int64(1)
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'k':
			mult = 1 << 10
		case 'm':
			mult = 1 << 20
		case 'g':
			mult = 1 << 30
		}
		if mult != 1 {
			s = s[:n-1]
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return int64(n * float64(mult)), true
}
