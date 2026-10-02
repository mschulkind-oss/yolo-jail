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
	// Caveat is the durable dir's lifetime when it is only as durable as something around
	// it: a nested jail whose workspace lives in the enclosing jail's /tmp. It REPLACES the
	// lead's "survives restarts and yolo never deletes it", which the enclosing yolo's
	// cleanup of that /tmp makes false, so the lifetime is still said once (DS-D32).
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
// jail exits (run.startScratchRemoval; nothing, when the map says they are in RAM, since a
// tmpfs goes with the jail), `yolo prune --apply`'s age-out of a few agents' log dirs in the
// workspace overlay (prune.agentLogWorkspaceSubdirs; on a whole-home bind, only yolo's own
// delivered files instead), and its age-out of a fixed list of tool caches under ~/.cache
// (prune.CachePurgeDefaultSubdirs, 30 days by default), with the launch's housekeeping
// trimming yolo's own image cache there and the boot's store step unlinking DANGLING mise
// symlinks in /mise (provision.StepPruneStore). All of the machine-tier deletions are of
// bytes that can be fetched or built again. yolo deletes nothing in the durable dir (OQ-DS2).
//
// THE MAP, NOT THE MECHANISM NAME, PICKS THE WORDING (DS-P1). Apple Container's section differs
// from podman's in exactly the two facts the map carries — the whole home is one per-workspace
// bind (HomeIsDurable) and the per-launch set is tmpfs (PerLaunchInRAM) — so those two facts
// choose its clauses, and podman under `ephemeral_storage: "tmpfs"` gets the RAM clauses too,
// because they are as true of its tmpfs.
//
// THE DURABLE DIR'S LIFETIME IS SAID ONCE, in the lead (DS-D31). Three fresh readers found it
// said three ways — "yolo never deletes anything there", "lost only if the workspace's `.yolo`
// is deleted" and, for the workspace around it, "survives everything" — and could not tell
// which applied. So the class bullets below state their class's lifetime and add no clause
// about the durable dir that the lead has not already said.
//
// workspace is the workspace at the jail's spelling and hostWorkspace at the host's; they
// differ on the container backends, which is what makes `--lock` necessary (durableLead).
func persistenceSection(m *PersistenceMap, d *DurableDir, workspace, hostWorkspace, home string) []string {
	if m == nil && d == nil {
		return nil
	}
	lines := []string{persistenceHeading, ""}
	lines = append(lines, durableLead(d, workspace, hostWorkspace)...)
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
		// THE BACKING DECIDES TWO CLAUSES, not one word. On disk the set is podman's named
		// scratch volumes, which the launcher's remover deletes after the jail exits
		// (run.startScratchRemoval). In RAM — Apple Container always, podman under
		// `ephemeral_storage: "tmpfs"` — they are tmpfs: nothing of yolo's deletes them, they
		// are gone when the jail stops, and every byte there is memory the jail's other
		// processes do not get (docs/design/durable-scratch-space.md §3.5). The podman
		// sentence said "yolo deletes these once the jail exits" of both (§9 step 6).
		backing := "on disk"
		survives := "Survives nothing: a restart is a new launch with new, empty ones, and yolo " +
			"deletes these once the jail exits."
		if m.PerLaunchInRAM {
			backing = "in RAM"
			survives = "Survives nothing: they are gone when the jail stops, and a restart is a " +
				"new launch with new, empty ones. Everything you put there uses this jail's memory."
		}
		lines = append(lines, "- **Per launch** ("+backing+"): "+joinTilde(perLaunch, home)+". "+
			"Shared by every terminal attached to this jail. "+survives+" A scratchpad a harness "+
			"hands you under `/tmp` is in this class. Throwaway files only, never a worktree.")
	}

	// What the per-workspace home dirs are FOR, because "survives restarts" alone read as an
	// invitation: they hold the agents' and tools' own state and installs, and a worktree or
	// draft put there sits among another program's files. Said as a prohibition, and the
	// durable dir named first and as the only place for work, because an agent corrected off
	// /tmp chose `~/.local` from this same list.
	const toolsOwn = " (the agents' and tools' own state and installs: never put your work there)"
	var where []string
	if hasDurable {
		where = append(where, "`$"+durable.EnvVar+"`, your scratch space")
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
	// What yolo deletes in this class. On a read-only home it is `yolo prune --apply`'s
	// age-out of a few agents' log dirs, which it finds at `<ws>/.yolo/home/copilot/logs`
	// and `gemini/tmp` (prune.agentLogWorkspaceSubdirs): the names podman binds with the
	// leading dot stripped. A WHOLE-HOME bind (Apple Container) keeps the dots, so that
	// age-out finds nothing there, and what yolo does delete is its own delivered files —
	// the pack tree and generated scripts it replaces at each launch — which the
	// "Rewritten at each launch" bullet below says.
	deletes := "yolo deletes nothing here but some agents' old log files (`yolo prune --apply`)."
	switch {
	case homeDurable:
		where = append(where, "all of `"+home+"` outside the other classes, writable and kept in "+
			"this workspace's `.yolo/home`"+toolsOwn)
		deletes = "yolo deletes nothing here but its own files (below)."
	case len(inHome) > 0:
		where = append(where, "in home, only "+joinTilde(inHome, home)+toolsOwn)
	}
	if len(outside) > 0 {
		where = append(where, joinTilde(outside, home)+" (this jail's own copies, not the host's)")
	}
	if len(where) > 0 {
		lines = append(lines, "- **Per workspace**: "+strings.Join(where, "; ")+". Survives "+
			"restarts and every new launch of this workspace; another workspace has its own. "+deletes)
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
		// "Outlives every jail", not "survives everything": the durable dir is inside it, and a
		// `git clean -fdx` deletes that, so "everything" contradicted the lead.
		lines = append(lines, "- **The workspace itself**: "+joinTilde(project, home)+", live on "+
			"the host: the user's project, not a scratch area"+mine+". It outlives every jail, "+
			"and yolo never cleans it up.")
	}

	if !homeDurable {
		// "The rest", not "everything else": the internal entries (a generated-script dir, a
		// log, the shell history) are writable, and are left unnamed rather than offered.
		lines = append(lines, "- **Read-only**: the rest of `"+home+"` (apart from a few files "+
			"yolo keeps there itself), and the briefing and skills files even inside the "+
			"directories above. A write there fails.")
	} else {
		// A WHOLE-HOME BIND HAS NO READ-ONLY REST, and saying nothing left the agent to infer
		// that yolo's own files there are as durable as the rest. They are not: Apple
		// Container gets each briefing as a writable COPY into the home (run.acMaterialize),
		// not a `:ro` bind; its skills are a `:ro` bind of a staging dir every invocation
		// rebuilds (refreshJailBriefings), and a release before 1.1.0 may ignore that `:ro`
		// (run.acROBindsFloor); and yolo replaces its generated scripts and pack tree there at
		// each launch. So a write may succeed or fail, and lasts only until the next launch
		// either way. Internal paths stay unnamed, as on the read-only home (DS-D15).
		lines = append(lines, "- **Rewritten at each launch**: the briefing and skills files, and a "+
			"few files yolo keeps in the home itself. A write to one may succeed here, and the "+
			"next launch replaces it.")
	}
	lines = append(lines,
		"- **Secrets**: nowhere you choose. Credentials reach this jail through yolo's own "+
			"channels, and a copy you write is one nothing rotates or revokes.",
		"",
	)
	return lines
}

// durableLead is the section's first paragraph: the answer to "where does work that must
// survive a restart go?", its lifetime said once (DS-D31), then how to make a worktree there.
//
// `--lock` carries its reason where the reason is true: when the jail's spelling of the
// workspace differs from the host's (both container backends), a worktree made here records
// /workspace/… in git's admin files, which git on the host cannot resolve, so a host-side
// `git worktree prune` or `git gc` drops the registration (DS-D10, MEASURED §2.6). The
// sentence names that cause, the recorded path the host lacks, and what a prune takes, the
// registration: "git on the host sees this tree at another path … would prune it as missing"
// read as the host knowing where the tree is, and "prune it" as deleting its files (DS-D32).
// On macos-user the two spellings are one path and the lock is asked for without the reason.
// `git -C` carries its own, so a missing tree fails instead of running in the workspace (the
// §1.1 incident). NOT `--relative-paths`: it writes a repository extension into the user's own
// `.git/config`, which a git older than 2.48 then refuses until someone removes it by hand
// (DS-D31, MEASURED).
//
// "Which git ignores", not "hidden from git": a `git clean -fdx` deletes the dir BECAUSE git
// ignores it, and "hidden from git" read as "git cannot touch it" one clause before the clean
// that does (DS-D32).
//
// With no durable dir it says so and why and names no path, so no agent is sent to a
// directory that does not exist (§5.6).
func durableLead(d *DurableDir, workspace, hostWorkspace string) []string {
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
				"fixed, put nothing that must survive a restart under `/tmp`, and nothing in `" +
				workspace + "` without asking: it is the user's project.",
			"",
		}
	}
	v := "`$" + durable.EnvVar + "`"
	lifetime := "It survives restarts and yolo never deletes it."
	if d.Caveat != "" {
		lifetime = "⚠ " + d.Caveat
	}
	lead := []string{
		"**" + v + "** (`" + d.Path + "`) is scratch space for what you do not want to lose " +
			"in a restart: worktrees, clones, intermediate files. It is not part of the project, and the user " +
			"never needs to look in it, so anything meant for the project or for the user goes " +
			"where it otherwise would. Use any layout under it.",
		lifetime + " It sits in the workspace's `.yolo`, which git ignores, so a " +
			durable.CleanCommand + " in the workspace deletes it: never run one there. The rest of `" +
			workspace + "` is the user's project.",
	}
	worktree := "Make a worktree with `git worktree add --lock \"$" + durable.EnvVar + "/worktrees/<task>\"`"
	if workspace != hostWorkspace {
		worktree += ": git records it under this jail's `" + workspace + "`, a path the host " +
			"does not have, so without the lock a `git worktree prune` or `git gc` on the host " +
			"would drop its registration"
	}
	return append(lead,
		"",
		// The harness sentence: an agent told by its harness or workflow to use /tmp followed
		// that over the classes below, because the instruction was the more specific one.
		// "Survive a restart", not "outlive this session": a new session in the same launch
		// still sees /tmp, and the classes are defined by launches.
		"If a harness, workflow or tool tells you to put scratch work under `/tmp`, put anything "+
			"that must survive a restart in "+v+" instead; the harness cannot see this jail's "+
			"storage classes.",
		"",
		worktree+". Drive it with `git -C <path>`, not `cd <path> && …`, so a missing tree "+
			"fails the command instead of running it in the workspace. "+durable.RemoveAdvice,
		"",
	)
}
