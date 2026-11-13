// Package runtime provides the container-runtime plumbing the ps command,
// prune, and storage lean on. The subprocess invocations stay thin wrappers
// around pure parsing functions; the liveness polarity (None-vs-empty) is
// unit-tested against canned podman / Apple-Container output.
package runtime

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// LiveSet is the tri-state result of enumerating live yolo-* containers. Known
// distinguishes "enumerated (maybe empty)" from "could not enumerate" — the
// SAME fail-safe polarity as prune.ReferencedSet: unknown (NOT an empty set)
// when the runtime can't be probed. Callers that decline to act on unknown
// MUST check Known, never collapse it to "nothing live".
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
// into the set of live yolo-* names: split each line on whitespace, require >=2
// fields, keep yolo-* whose state (lowercased) is running/paused/restarting.
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
// only, fixed table) into the set of live yolo-* names: skip the header row,
// take the first whitespace field, keep yolo-*. Because `container ls` lists
// only running containers, every yolo-* row is live.
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

// ParseContainerLsImages parses Apple Container's `container ls` stdout
// (running only, fixed table, IMAGE the second column) into the image
// reference of every running container, yolo's or not — the same set `podman
// ps --format {{.Image}}` prints. A row with no second field contributes
// nothing.
func ParseContainerLsImages(stdout string) []string {
	var refs []string
	for _, line := range tableRows(stdout) {
		if parts := strings.Fields(line); len(parts) >= 2 {
			refs = append(refs, parts[1])
		}
	}
	return refs
}

// ParseRunningJailNames parses `podman ps --filter name=^yolo- --format
// "{{.Names}}"` stdout: one name per non-blank line, trimmed.
func ParseRunningJailNames(stdout string) []string {
	var names []string
	for _, line := range strings.Split(stdout, "\n") {
		if n := strings.TrimSpace(line); n != "" {
			names = append(names, n)
		}
	}
	return names
}

// ParseContainerLsNames parses `container ls` stdout for yolo-* names: skip
// header, first field, yolo-* prefix.
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

// PsRow is one parsed row of a runtime's listing: the jail's name, the runtime's status words, and
// when it started (the zero time when the runtime did not say).
type PsRow struct {
	Name    string
	Status  string
	Started time.Time
}

// PodmanPsArgv is the Podman listing ParsePodmanPsRows reads (named-setups-plan.md SUP-D25): every
// running yolo-* container, its status words and its start time in unix seconds, the field
// prune.RunningJailStarts reads too.
var PodmanPsArgv = []string{"podman", "ps", "--filter", "name=^yolo-", "--format", "{{.Names}}\t{{.Status}}\t{{.StartedAt}}"}

// ParsePodmanPsRows parses PodmanPsArgv's stdout into rows: strip, split lines, then split each on
// tab and keep rows with >=3 fields. A start time that is missing, not a number or not positive is
// unknown (the zero time), never an error. A blank stdout yields no rows.
func ParsePodmanPsRows(stdout string) []PsRow {
	var rows []PsRow
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return rows
	}
	for _, line := range strings.Split(trimmed, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) >= 3 {
			row := PsRow{Name: parts[0], Status: parts[1]}
			if sec, err := strconv.ParseInt(strings.TrimSpace(parts[2]), 10, 64); err == nil && sec > 0 {
				row.Started = time.Unix(sec, 0).UTC()
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// ContainerLsJSONArgv is the Apple Container listing ParseContainerLsRows reads: running
// containers only, as `podman ps` lists, in the JSON form rather than the table, whose columns
// (`ID IMAGE OS ARCH STATE IP CPUS MEMORY STARTED`, MEMORY two tokens) changed under the old
// whitespace parser.
var ContainerLsJSONArgv = []string{"container", "ls", "--format", "json"}

// containerLsEntry is the part of one `container ls --format json` element ps reads. The
// shape is MEASURED (docs/lab-reports/2026-11-06-mac-ci-triage-witnesses-run.md §3): top-level
// `configuration`, `id` and `status`, where `status` is an object {networks, startedDate,
// state} and startedDate an RFC 3339 UTC string.
type containerLsEntry struct {
	ID            string `json:"id"`
	Configuration struct {
		ID string `json:"id"`
	} `json:"configuration"`
	Status struct {
		State       string `json:"state"`
		StartedDate string `json:"startedDate"`
	} `json:"status"`
}

// ParseContainerLsRows parses `container ls --format json` stdout (ContainerLsJSONArgv) into
// ps rows: every yolo-* container, named by its `id` (else `configuration.id`), with Status
// "<state> since <startedDate>" (the bare state when no start time is given), and Started that
// RFC 3339 time (W-SU11 measured its encoding; named-setups-plan.md SUP-D25). ok=false means the output is not a JSON list: the caller must treat that as "could
// not enumerate", never as "no jails" (D11), since an empty answer authorizes the tracking
// prune.
func ParseContainerLsRows(stdout string) ([]PsRow, bool) {
	var entries []containerLsEntry
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		return nil, false
	}
	rows := []PsRow{}
	for _, e := range entries {
		name := e.ID
		if name == "" {
			name = e.Configuration.ID
		}
		if !strings.HasPrefix(name, "yolo-") {
			continue
		}
		status := e.Status.State
		if e.Status.StartedDate != "" {
			status += " since " + e.Status.StartedDate
		}
		row := PsRow{Name: name, Status: strings.TrimSpace(status)}
		if t, err := time.Parse(time.RFC3339, e.Status.StartedDate); err == nil {
			row.Started = t.UTC()
		}
		rows = append(rows, row)
	}
	return rows, true
}

// tableRows returns the data rows of a fixed-table CLI output: strip the whole
// blob, split on newline, drop the header row. A blank blob yields no rows.
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
