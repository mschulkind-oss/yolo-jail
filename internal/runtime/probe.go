// Package runtime provides the container-runtime plumbing the ps command,
// prune, and storage lean on. The subprocess invocations stay thin wrappers
// around pure parsing functions; the liveness polarity (None-vs-empty) is
// unit-tested against canned podman / Apple-Container output.
package runtime

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// LiveSet is the tri-state result of enumerating live yolo-* containers. Known
// distinguishes "enumerated (maybe empty)" from "could not enumerate" — the
// SAME fail-safe polarity as prune.ReferencedSet and Python's
// _live_yolo_containers returning None (NOT an empty set) when the runtime
// can't be probed. Callers that decline to act on unknown MUST check Known,
// never collapse it to "nothing live".
type LiveSet struct {
	// Known is false when the runtime couldn't be enumerated (missing binary,
	// timeout, non-zero exit). True even for an empty Names.
	Known bool
	// Names holds the live yolo-* container names.
	Names map[string]struct{}
}

// ResolveRuntime resolves the effective container runtime for the tolerant
// LISTING commands (`yolo ps` / `yolo prune`) with run()'s precedence: an
// explicit YOLO_RUNTIME, then the config `runtime` key, then a platform-aware
// probe. env/config are honored only when they name a known runtime
// (paths.AllRuntimes); anything else falls through. macOS prefers Apple
// Container then podman; Linux is always podman. hasBinary reports PATH presence
// (nil => assume present).
//
// Unlike run's resolveRuntime this does NO connectivity probe and never exits:
// the ps/prune fail-safe is the tri-state enumeration guard (a probe that
// couldn't run declines to prune), not a hard runtime gate. This closes the
// audit §B/D11 bug — the old config-blind resolver picked "podman" on a macOS
// host running Apple Container, so `podman ps` came back empty and the
// stale-tracking prune deleted LIVE AC jails' files — and finding 5 (ps never
// consulted the config `runtime` key at all).
func ResolveRuntime(envRuntime, configRuntime string, isMacOS bool, hasBinary func(string) bool) string {
	if envRuntime != "" && inRuntimeSet(envRuntime) {
		return envRuntime
	}
	if configRuntime != "" && inRuntimeSet(configRuntime) {
		return configRuntime
	}
	if hasBinary == nil {
		hasBinary = func(string) bool { return true }
	}
	if isMacOS {
		for _, rt := range []string{"container", "podman"} {
			if hasBinary(rt) {
				return rt
			}
		}
	}
	return "podman"
}

func inRuntimeSet(rt string) bool {
	for _, x := range paths.AllRuntimes {
		if x == rt {
			return true
		}
	}
	return false
}

// livePodmanStates are the container states podman reports that count as a live
// jail for the sweep-guard purpose.
var livePodmanStates = map[string]struct{}{
	"running":    {},
	"paused":     {},
	"restarting": {},
}

// ParsePodmanLive parses `podman ps -a --format "{{.Names}} {{.State}}"` stdout
// into the set of live yolo-* names.
// _live_yolo_containers: split each line on whitespace, require >=2 fields,
// keep yolo-* whose state (lowercased) is running/paused/restarting.
func ParsePodmanLive(stdout string) map[string]struct{} {
	live := map[string]struct{}{}
	for _, line := range strings.Split(stdout, "\n") {
		parts := strings.Fields(strings.TrimSpace(line))
		if len(parts) < 2 {
			continue
		}
		name, state := parts[0], parts[1]
		if !strings.HasPrefix(name, "yolo-") {
			continue
		}
		if _, ok := livePodmanStates[strings.ToLower(state)]; ok {
			live[name] = struct{}{}
		}
	}
	return live
}

// ParseContainerLsLive parses Apple Container's `container ls` stdout (running
// only, fixed table) into the set of live yolo-* names.
// branch: skip the header row, take the first whitespace field, keep yolo-*.
// Because `container ls` lists only running containers, every yolo-* row is
// live.
func ParseContainerLsLive(stdout string) map[string]struct{} {
	live := map[string]struct{}{}
	for _, line := range tableRows(stdout) {
		parts := strings.Fields(line)
		if len(parts) > 0 && strings.HasPrefix(parts[0], "yolo-") {
			live[parts[0]] = struct{}{}
		}
	}
	return live
}

// ParseRunningJailNames parses `podman ps --filter name=^yolo- --format
// "{{.Names}}"` stdout: one name per non-blank line, trimmed.
// podman branch of list_running_jail_names.
func ParseRunningJailNames(stdout string) []string {
	var names []string
	for _, line := range strings.Split(stdout, "\n") {
		if n := strings.TrimSpace(line); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// ParseContainerLsNames parses `container ls` stdout for yolo-* names (Apple
// Container branch of list_running_jail_names): skip header, first field,
// yolo-* prefix.
func ParseContainerLsNames(stdout string) []string {
	var names []string
	for _, line := range tableRows(stdout) {
		parts := strings.Fields(line)
		if len(parts) > 0 && strings.HasPrefix(parts[0], "yolo-") {
			names = append(names, parts[0])
		}
	}
	return names
}

// PsRow is one parsed row of the `yolo ps` display: name, status, and how long
// it has been running (RunningFor; empty for Apple Container).
type PsRow struct {
	Name       string
	Status     string
	RunningFor string
}

// ParsePodmanPsRows parses `podman ps --filter name=^yolo- --format
// "{{.Names}}\t{{.Status}}\t{{.RunningFor}}"` stdout into rows, mirroring the
// podman branch of ps(): strip, split lines, then split each on tab and keep
// rows with >=3 fields. A blank stdout yields no rows.
func ParsePodmanPsRows(stdout string) []PsRow {
	var rows []PsRow
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return rows
	}
	for _, line := range strings.Split(trimmed, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) >= 3 {
			rows = append(rows, PsRow{Name: parts[0], Status: parts[1], RunningFor: parts[2]})
		}
	}
	return rows
}

// ParseContainerLsRows parses Apple Container `container ls` stdout into ps
// rows, mirroring the container branch of ps(): skip header, first field is the
// name, the remaining fields joined by a single space are the status, and
// RunningFor is always empty (the Python builds "{cname}\t{status}\t" then
// re-splits on tab, yielding an empty third field).
func ParseContainerLsRows(stdout string) []PsRow {
	var rows []PsRow
	for _, line := range tableRows(stdout) {
		parts := strings.Fields(line)
		if len(parts) == 0 || !strings.HasPrefix(parts[0], "yolo-") {
			continue
		}
		status := ""
		if len(parts) > 1 {
			status = strings.Join(parts[1:], " ")
		}
		rows = append(rows, PsRow{Name: parts[0], Status: status})
	}
	return rows
}

// tableRows returns the data rows of a fixed-table CLI output: strip the whole
// blob, split on newline, drop the header row. Mirrors
// `stdout.strip().splitlines()[1:]`. A blank blob yields no rows (Python's
// "".splitlines() is [], and [1:] of [] is []).
func tableRows(stdout string) []string {
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
