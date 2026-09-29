package jailcontent

// The briefing's "Durable vs ephemeral paths" section (docs/design/durable-scratch-space.md
// §4.1). This file RENDERS a persistence map; it never decides one. What each path is comes
// from the run pipeline, which builds the map from the same definitions the launch's mount
// argv reads (run.persistenceMapFor), so the section is a view of the mounts rather than a
// hand-written summary of them (DS-P1). A hand-written line is exactly what this replaced:
// the Environment block's "Home: /home/agent (persistent across sessions)", said of a home
// that is mounted read-only on podman.

import (
	"slices"
	"sort"
	"strings"
)

// PathClass is one durability class of the persistence map (the design's §4.1 table). The
// order of the constants is the order the section renders them in.
type PathClass int

const (
	// PathWorkspaceDurable survives the jail's exit and the next launch of this same
	// workspace, and is not shared with any other workspace.
	PathWorkspaceDurable PathClass = iota + 1
	// PathMachineDurable survives the jail's exit and is shared by every workspace on this
	// machine (a bind from the machine store).
	PathMachineDurable
	// PathPerLaunch is created for one fresh launch, shared by every terminal that attaches
	// to that jail, and deleted after the jail exits.
	PathPerLaunch
	// PathInternal is writable and durable but holds yolo's own generated or bookkeeping
	// content (a generated-script dir, a lock, a log, a history file). It is in the map so
	// the map covers every writable mount of the launch, and the section never names it:
	// it is not a place for an agent's work.
	PathInternal
)

// String names the class, for test failures and debugging.
func (c PathClass) String() string {
	switch c {
	case PathWorkspaceDurable:
		return "durable, this workspace"
	case PathMachineDurable:
		return "durable, every workspace"
	case PathPerLaunch:
		return "per-launch"
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

// PersistenceMap is the launch's persistence map: the paths it makes writable, each with a
// durability class and a scope. A nil map renders no section, which is every caller that has
// not resolved a container backend (the host notch, macos-user, and unit tests that build a
// BriefingInput by hand).
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

// HomeIsDurable reports whether the whole home is itself a per-workspace durable path (Apple
// Container binds `<ws>/.yolo/home` at the home, read-write, in one mount). When it is not,
// the home is a read-only base and only the paths the map names are writable in it.
func (m *PersistenceMap) HomeIsDurable(home string) bool {
	return slices.Contains(m.Of(PathWorkspaceDurable), home)
}

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
		return " (writable, and kept for this workspace; see **Durable vs ephemeral paths** below)"
	default:
		return " (mostly read-only; see **Durable vs ephemeral paths** below)"
	}
}

// persistenceSection renders the section, or nothing for a nil map. A class with no members
// renders no bullet (the design's degenerate-inputs rule).
func persistenceSection(m *PersistenceMap, home string) []string {
	if m == nil {
		return nil
	}
	homeDurable := m.HomeIsDurable(home)
	lines := []string{"## Durable vs ephemeral paths", ""}

	// This workspace only: the paths outside the home first (the workspace itself), then
	// the home's own writable directories.
	var clauses, inHome []string
	for _, p := range m.Of(PathWorkspaceDurable) {
		switch {
		case p == home:
			// Rendered as its own clause below.
		case strings.HasPrefix(p, home+"/"):
			inHome = append(inHome, p)
		case p == "/workspace":
			clauses = append(clauses, "`/workspace` (the host's own files)")
		default:
			clauses = append(clauses, "`"+p+"`")
		}
	}
	switch {
	case homeDurable:
		clauses = append(clauses, "all of `"+home+"` except the paths below")
	case len(inHome) > 0:
		clauses = append(clauses, "in home, only "+joinTilde(inHome, home))
	}
	if len(clauses) > 0 {
		lines = append(lines, "- **Survives a restart, this workspace only**: "+strings.Join(clauses, "; ")+".")
	}

	if machine := m.Of(PathMachineDurable); len(machine) > 0 {
		lines = append(lines, "- **Survives, shared by every workspace on this machine**: "+
			joinTilde(machine, home)+".")
	}

	if !homeDurable {
		// "The rest", not "everything else": the internal entries (a generated-script dir, a
		// log, the shell history) are writable, and are left unnamed rather than offered.
		lines = append(lines, "- **Read-only**: the rest of `"+home+"` (apart from a few files "+
			"yolo keeps there itself), and the briefing and skills files even inside the "+
			"directories above. A write there fails.")
	}

	if perLaunch := m.Of(PathPerLaunch); len(perLaunch) > 0 {
		ram := ""
		if m.PerLaunchInRAM {
			ram = ", in RAM"
		}
		lines = append(lines, "- **Deleted once this jail exits** (per launch"+ram+"; every "+
			"attached terminal shares them): "+joinTilde(perLaunch, home)+". A scratchpad a "+
			"harness hands you under `/tmp` is in this set.")
	}

	// THE WORKTREE BULLET, interim until OQ-DS1 names the durable dir. It names no agent's
	// directory: core does not know what an agent is, and endorsing one agent's worktree dir
	// for every agent is the design's rejected alternative B″ (DS-D13).
	lines = append(lines,
		"- **Worktrees and anything you need next session**: inside `/workspace`, in a "+
			"directory git ignores (`git check-ignore -q <path>` says whether it does), never "+
			"`/tmp`. Drive them with `git -C <path>`, not `cd <path> && …`, so a missing "+
			"directory fails the git command itself instead of leaving later commands running "+
			"in the wrong tree.",
		"",
	)
	return lines
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
