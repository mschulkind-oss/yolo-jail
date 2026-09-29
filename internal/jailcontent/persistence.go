package jailcontent

// The briefing's storage-classes section (docs/design/durable-scratch-space.md §4.1, reframed
// by the maintainer on 2026-09-28 as storage classes with their lifecycles: DS-D13). This file
// RENDERS a persistence map; it never decides one. What each path is comes from the run
// pipeline, which builds the map from the same definitions the launch's mount argv reads
// (run.persistenceMapFor), so the section is a view of the mounts rather than a hand-written
// summary of them (DS-P1). A hand-written line is exactly what this replaced: the Environment
// block's "Home: /home/agent (persistent across sessions)", said of a home that is mounted
// read-only on podman.

import (
	"slices"
	"sort"
	"strings"
)

// PathClass is one storage class of the persistence map. The order of the constants is the
// order the section renders them in.
type PathClass int

const (
	// PathPerLaunch is created for one fresh launch, shared by every terminal that attaches
	// to that jail, and deleted after the jail exits.
	PathPerLaunch PathClass = iota + 1
	// PathWorkspaceDurable survives the jail's exit and every later launch of this same
	// workspace, and is not shared with any other workspace.
	PathWorkspaceDurable
	// PathMachineDurable survives the jail's exit and is shared by every workspace on this
	// machine (a bind from the machine store).
	PathMachineDurable
	// PathProject is the workspace itself: the host's own directory, live. It survives
	// everything, and it is the user's project rather than a place for scratch.
	PathProject
	// PathInternal is writable and durable but holds yolo's own generated or bookkeeping
	// content (a generated-script dir, a lock, a log, a history file). It is in the map so
	// the map covers every writable mount of the launch, and the section never names it:
	// it is not a place for an agent's work.
	PathInternal
)

// String names the class, for test failures and debugging.
func (c PathClass) String() string {
	switch c {
	case PathPerLaunch:
		return "per launch"
	case PathWorkspaceDurable:
		return "per workspace"
	case PathMachineDurable:
		return "every workspace on this machine"
	case PathProject:
		return "the workspace itself"
	case PathInternal:
		return "internal"
	}
	return "unclassified"
}

// PersistentPath is one mounted path in the jail and its class. Path is absolute, at the
// jail's spelling (`/home/agent/.claude`, `/workspace`, `/tmp`).
type PersistentPath struct {
	Path  string
	Class PathClass
}

// PersistenceMap is the launch's persistence map (a term coined in
// docs/design/durable-scratch-space.md §1.2): the paths it makes writable, each with a storage
// class. A nil map renders no section, which is every caller that has not resolved a
// container backend (the host notch, macos-user, and unit tests that build a BriefingInput by
// hand).
type PersistenceMap struct {
	Paths []PersistentPath
	// PerLaunchInRAM is true when the per-launch paths are tmpfs rather than disk-backed
	// volumes: `ephemeral_storage: "tmpfs"` on podman, and always on Apple Container.
	PerLaunchInRAM bool
}

// Of returns the paths of one class, in the map's own order.
func (m *PersistenceMap) Of(c PathClass) []string {
	if m == nil {
		return nil
	}
	var out []string
	for _, p := range m.Paths {
		if p.Class == c {
			out = append(out, p.Path)
		}
	}
	return out
}

// HomeIsDurable reports whether the whole home is itself a per-workspace path (Apple
// Container binds `<ws>/.yolo/home` at the home, read-write, in one mount). When it is not,
// the home is a read-only base and only the paths the map names are writable in it.
func (m *PersistenceMap) HomeIsDurable(home string) bool {
	return slices.Contains(m.Of(PathWorkspaceDurable), home)
}

// persistenceHeading is the section's heading, which the Home line names.
const persistenceHeading = "## Storage classes: what survives a restart"

// homeLineNote is the Home line's suffix on a container backend. With a map it states the
// home's class and points at the section. Without one it states nothing: the old
// "(persistent across sessions)" was a claim made without the mounts in hand, and a nil map
// is exactly a caller that has not got them (the podman home is read-only, Apple
// Container's is not, and only the map knows which this is).
func homeLineNote(m *PersistenceMap, home string) string {
	switch {
	case m == nil:
		return ""
	case m.HomeIsDurable(home):
		return " (writable, and kept for this workspace; see **Storage classes** below)"
	default:
		return " (mostly read-only; see **Storage classes** below)"
	}
}

func tildePath(p, home string) string {
	if strings.HasPrefix(p, home+"/") {
		return "`~/" + strings.TrimPrefix(p, home+"/") + "`"
	}
	return "`" + p + "`"
}

// joinTilde renders paths with the home's as `~/…`: those first and sorted, then the rest in
// the map's order (the per-launch paths keep the argv's order, /tmp first).
func joinTilde(ps []string, home string) string {
	var inHome, rest []string
	for _, p := range ps {
		if strings.HasPrefix(p, home+"/") {
			inHome = append(inHome, tildePath(p, home))
		} else {
			rest = append(rest, tildePath(p, home))
		}
	}
	sort.Strings(inHome)
	return strings.Join(append(inHome, rest...), ", ")
}
