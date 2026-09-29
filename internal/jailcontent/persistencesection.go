package jailcontent

import (
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// DurableDir is what the briefing says about the launch's durable dir (a term coined in
// docs/design/durable-scratch-space.md §1.2: `<workspace>/.yolo/durable`, exported as
// $YOLO_DURABLE_DIR). The run pipeline fills it from the one place that made the directory
// on a fresh launch, or, on an attach, from the variable the running jail was started with.
type DurableDir struct {
	// Path is the directory at the jail's spelling, the variable's value. Empty means this
	// launch has none, and Unavailable says why.
	Path        string
	Unavailable string
	// Caveat is one more sentence when the durable dir is only as durable as something
	// around it: a nested jail whose workspace lives in the enclosing jail's /tmp.
	Caveat string
}

// persistenceSection renders the section, or nothing when there is neither a map nor a
// durable dir to describe. It LEADS WITH THE ANSWER — where work that must survive a restart
// goes — and then the classes. The answer used to come last, as "a dedicated durable
// directory … is being designed", and an agent reading that put its clones and drafts in
// /tmp and then in `~/.local`: the section named what vanishes and never where to go
// instead, and listed the home dirs as surviving without saying they are the agents' and
// tools' own (docs/design/durable-scratch-space.md §9).
//
// Each class says where it is, what it survives, who shares it and what yolo cleans up. A
// class with no members renders no bullet (the design's degenerate-inputs rule).
//
// THE CLEANUP CLAUSES are facts about internal/prune and the scratch remover, not about the
// map; each is the whole of what yolo deletes in that class: the scratch volumes after the
// jail exits (run.startScratchRemoval), `yolo prune --apply`'s age-out of a few agents' log
// dirs in the workspace overlay (prune.agentLogWorkspaceSubdirs), and its age-out of a fixed
// list of tool caches under ~/.cache (prune.CachePurgeDefaultSubdirs, 30 days by default),
// with the launch's housekeeping trimming yolo's own image cache there and the boot's store
// step unlinking DANGLING mise symlinks in /mise (provision.StepPruneStore). All of the
// machine-tier deletions are of bytes that can be fetched or built again. Nothing in the
// durable dir is ever deleted (OQ-DS2).
func persistenceSection(m *PersistenceMap, d *DurableDir, workspace, home string) []string {
	if m == nil && d == nil {
		return nil
	}
	lines := []string{persistenceHeading, ""}
	lines = append(lines, durableLead(d, workspace)...)
	if m == nil {
		// macos-user: it mounts nothing, so there are no classes to derive from mounts; the
		// durable answer, and the one fact about /tmp that differs most from a container's
		// (docs/design/durable-scratch-space.md §2.2).
		if workspace != "/workspace" {
			lines = append(lines, "- **`/tmp`** is this Mac's own: it survives this launch, is shared "+
				"with every workspace and the host user, and is cleared at reboot. Give anything "+
				"there a unique name (`mktemp`), and keep nothing there you need later.", "")
		}
		return lines
	}
	hasDurable := d != nil && d.Path != ""
	homeDurable := m.HomeIsDurable(home)

	if perLaunch := m.Of(PathPerLaunch); len(perLaunch) > 0 {
		backing := "on disk"
		if m.PerLaunchInRAM {
			backing = "in RAM"
		}
		lines = append(lines, "- **Per launch** ("+backing+"): "+joinTilde(perLaunch, home)+". "+
			"Shared by every terminal attached to this jail. Survives nothing: a restart is a new "+
			"launch with new, empty ones, and yolo deletes these once the jail exits. A scratchpad "+
			"a harness hands you under `/tmp` is in this class. Throwaway files only, never a worktree.")
	}

	// What the per-workspace home dirs are FOR, because "survives restarts" alone read as an
	// invitation: they hold the agents' and tools' own state and installs, and a worktree or
	// draft put there sits among another program's files. Said as a prohibition, and the
	// durable dir named first and as the only place for work, because an agent corrected off
	// /tmp chose `~/.local` from this same list.
	const toolsOwn = " (the agents' and tools' own state and installs: never put your work there)"
	var where []string
	if hasDurable {
		where = append(where, "`$"+durable.EnvVar+"`, the one place for your work")
	}
	var inHome, outside []string
	for _, p := range m.Of(PathWorkspaceDurable) {
		switch {
		case p == home:
			// Rendered as its own clause below.
		case strings.HasPrefix(p, home+"/"):
			inHome = append(inHome, p)
		default:
			outside = append(outside, p)
		}
	}
	switch {
	case homeDurable:
		where = append(where, "all of `"+home+"` outside the other classes"+toolsOwn)
	case len(inHome) > 0:
		where = append(where, "in home, only "+joinTilde(inHome, home)+toolsOwn)
	}
	if len(outside) > 0 {
		where = append(where, joinTilde(outside, home)+" (this jail's own copies, not the host's)")
	}
	if len(where) > 0 {
		lines = append(lines, "- **Per workspace**: "+strings.Join(where, "; ")+". Survives "+
			"restarts and every new launch of this workspace, and is lost only if the workspace's "+
			"`.yolo` is deleted (a `git clean -x` does that). Another workspace has its own. yolo "+
			"deletes nothing here but some agents' old log files (`yolo prune --apply`).")
	}

	if machine := m.Of(PathMachineDurable); len(machine) > 0 {
		// Where a tool cache goes, said outright: agents asked had to infer it.
		caches := "Caches only, never your work"
		if slices.Contains(machine, home+"/.cache") {
			caches = "Tool caches belong here (pip's `~/.cache/pip`, for one), never your work"
		}
		lines = append(lines, "- **Every workspace on this machine**: "+joinTilde(machine, home)+
			". Survives restarts and workspace switches, and every jail on this machine shares "+
			"them at the same time. "+caches+": yolo deletes what can be fetched or built again "+
			"here, such as old files in some tool caches under `~/.cache`.")
	}

	if project := m.Of(PathProject); len(project) > 0 {
		mine := ""
		if hasDurable {
			mine = "; only `$" + durable.EnvVar + "` inside it is yours"
		}
		lines = append(lines, "- **The workspace itself**: "+joinTilde(project, home)+", live on "+
			"the host: the user's project, not a scratch area"+mine+". Survives everything, and "+
			"yolo never cleans it up.")
	}

	if !homeDurable {
		// "The rest", not "everything else": the internal entries (a generated-script dir, a
		// log, the shell history) are writable, and are left unnamed rather than offered.
		lines = append(lines, "- **Read-only**: the rest of `"+home+"` (apart from a few files "+
			"yolo keeps there itself), and the briefing and skills files even inside the "+
			"directories above. A write there fails.")
	}
	lines = append(lines,
		"- **Secrets**: nowhere you choose. Credentials reach this jail through yolo's own "+
			"channels, and a copy you write is one nothing rotates or revokes.",
		"",
	)
	return lines
}

// durableLead is the section's first paragraph: the answer to "where does work that must
// survive a restart go?", then how to make a worktree there — `--lock`, so a host-side
// `git worktree prune` or `git gc` cannot drop a registration whose /workspace path the host
// cannot resolve (DS-D10), and `git -C`, so a missing tree fails instead of running in the
// workspace (the §1.1 incident). With no durable dir it says so and why and names no path,
// so no agent is sent to a directory that does not exist (§5.6).
func durableLead(d *DurableDir, workspace string) []string {
	if d == nil {
		return []string{"Put nothing that must survive a restart under `/tmp`.", ""}
	}
	if d.Path == "" {
		reason := d.Unavailable
		if reason == "" {
			reason = "yolo could not make it"
		}
		return []string{
			"**No durable directory this launch**: " + reason + ". Tell the user. Until it is " +
				"fixed, put nothing you need next session under `/tmp`, and nothing in `" + workspace +
				"` without asking: it is the user's project.",
			"",
		}
	}
	v := "`$" + durable.EnvVar + "`"
	lead := []string{
		"**Your work goes in " + v + "** (`" + d.Path + "`): worktrees, clones, drafts, " +
			"measurements, anything that must survive a restart. Use any layout under it; " +
			"worktrees go in `worktrees/<task>`. yolo never deletes anything there.",
		"It is inside the workspace, git-ignored by yolo's own `.yolo/.gitignore`, and the user " +
			"sees the same files on the host; the rest of `" + workspace + "` is the user's project.",
	}
	if d.Caveat != "" {
		lead = append(lead, "⚠ "+d.Caveat)
	}
	return append(lead,
		"",
		// The harness sentence: an agent told by its harness or workflow to use /tmp followed
		// that over the classes below, because the instruction was the more specific one.
		"If a harness, workflow or tool tells you to put work under `/tmp`, put anything that "+
			"must outlive this session in "+v+" instead; the harness cannot see this jail's "+
			"storage classes.",
		"",
		"Make a worktree with `git worktree add --lock \"$"+durable.EnvVar+"/worktrees/<task>\"` "+
			"and drive it with `git -C <path>`, not `cd <path> && …`, so a missing tree fails "+
			"the command instead of running it in the workspace. "+durable.RemoveAdvice,
		"",
	)
}
