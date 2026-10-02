package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
)

// THE SECTION LEADS WITH THE ANSWER. An agent reading the first version — which named what
// vanishes, listed the home dirs as surviving, and closed with "a dedicated durable directory
// … is being designed" — put its clones and drafts in /tmp, and corrected, chose `~/.local`
// (docs/design/durable-scratch-space.md §9). So the first words under the heading are where
// work goes, the variable and its path; then how to make a worktree there; then the classes
// in the maintainer's order, each home dir said to be the agents' and tools' own; and no
// sentence may say the directory is still being designed.
func TestThePersistenceSectionLeadsWithTheDurableDir(t *testing.T) {
	out := BriefingContent(BriefingInput{Workspace: "/w", Mechanism: "podman", Persistence: podmanShapedMap(),
		Durable: &DurableDir{Path: durable.ContainerJailPath}})
	if !strings.Contains(out, "- **Home**: `/home/agent` (mostly read-only; see **Storage classes** below)") {
		t.Errorf("the Home line does not point at the section:\n%s", environmentBlockOf(t, out))
	}
	sec := persistenceSectionOf(t, out)
	lead := persistenceHeading + "\n\n" +
		"**`$YOLO_DURABLE_DIR`** (`/workspace/.yolo/durable`) is scratch space for what you do not want to lose in a restart: worktrees, clones, intermediate files. It is not part of the project, and the user never needs to look in it, so anything meant for the project or for the user goes where it otherwise would. Use any layout under it.\n" +
		"It survives restarts and yolo never deletes it. It sits in the workspace's `.yolo`, which git ignores, so a `git clean -fdx` (or `-fdX`) in the workspace deletes it: never run one there. The rest of `/workspace` is the user's project.\n"
	if !strings.HasPrefix(sec, lead) {
		t.Errorf("the section does not open with the durable dir:\n%s", sec)
	}
	order := []string{
		// The relayed /tmp confusion: an agent followed its harness's /tmp over the classes.
		// "Survive a restart", not "outlive this session": a new session in the same launch
		// still sees /tmp.
		"If a harness, workflow or tool tells you to put scratch work under `/tmp`, put anything that must survive a restart in `$YOLO_DURABLE_DIR` instead; the harness cannot see this jail's storage classes.",
		// `--lock` says why: the host has this workspace at "/w", not /workspace. The cause is
		// in the sentence (the recorded path is one the host lacks), and what a prune takes is
		// the registration, not the files.
		"Make a worktree with `git worktree add --lock \"$YOLO_DURABLE_DIR/worktrees/<task>\"`: git records it under this jail's `/workspace`, a path the host does not have, so without the lock a `git worktree prune` or `git gc` on the host would drop its registration. Drive it with `git -C <path>`, not `cd <path> && …`, so a missing tree fails the command instead of running it in the workspace.",
		"Remove one with `git worktree unlock <path> && git worktree remove <path>`, which refuses while it holds uncommitted changes; `git worktree remove -f -f <path>` discards them.",
		"- **Per launch** (on disk): `/tmp`, `/run`. Shared by every terminal attached to this jail. Survives nothing",
		"yolo deletes these once the jail exits",
		"Throwaway files only, never a worktree.",
		"- **Per workspace**: `$YOLO_DURABLE_DIR`, your scratch space; in home, only `~/.claude`, `~/.config`, `~/go` (the agents' and tools' own state and installs: never put your work there); `/workspace/.venv` (this jail's own copies, not the host's). Survives restarts and every new launch of this workspace; another workspace has its own. yolo deletes nothing here but some agents' old log files (`yolo prune --apply`).",
		"- **Every workspace on this machine**: `~/.cache`, `/mise`. Survives restarts and workspace switches, and every jail on this machine shares them",
		"Tool caches belong here (pip's `~/.cache/pip`, for one), never your work",
		"- **The workspace itself**: `/workspace`, live on the host: the user's project, not a scratch area; only `$YOLO_DURABLE_DIR` inside it is yours. It outlives every jail, and yolo never cleans it up.",
		"- **Read-only**: the rest of `/home/agent`",
		"- **Secrets**: nowhere you choose.",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(sec, want)
		if i < 0 {
			t.Errorf("the section does not say %q:\n%s", want, sec)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order:\n%s", want, sec)
		}
		last = i
	}
	// An internal entry is never offered as a place for work; core recommends no agent's
	// directory; and nothing is "being designed" any more.
	// Nor is the doubled `-f` offered as THE way to remove one: it overrides git's refusal
	// to delete uncommitted work as well as the lock.
	// The durable dir's lifetime is said ONCE (DS-D31): no second, differently-qualified
	// version of it, and no "survives everything" for the workspace that contains it. And
	// `--relative-paths` is never offered: its extension makes a git older than 2.48 refuse
	// the user's repository, on the host too.
	for _, gone := range []string{"~/.yolo/bin", ".claude/worktrees", "git check-ignore", "being designed",
		"remove yours with `git worktree remove -f -f", "the lock makes it take two",
		"lost only if", "Survives everything", "never deletes anything there", "this session",
		"next session", "worktrees go in", "relative-paths", "relativeWorktrees", "useRelativePaths",
		// "Hidden from git" is no git term, and read as "git cannot touch it" beside the clean
		// that deletes the dir BECAUSE git ignores it; "sees this tree at another path … as
		// missing" had the host's git knowing where the tree is and not finding it.
		"hidden from git", "sees this tree", "prune it as missing"} {
		if strings.Contains(sec, gone) {
			t.Errorf("the section says %q:\n%s", gone, sec)
		}
	}
	// Before every capability section, after Environment.
	if e, s, l := strings.Index(out, "## Environment"), strings.Index(out, persistenceHeading),
		strings.Index(out, "## Limitations"); !(e < s && s < l) {
		t.Errorf("the section is not between Environment and Limitations (%d, %d, %d)", e, s, l)
	}
}

// The briefing names the variable by the one spelling the launcher exports.
func TestTheDurableLineNamesTheVariable(t *testing.T) {
	sec := strings.Join(persistenceSection(podmanShapedMap(), &DurableDir{Path: "/x"}, "/workspace", "/h", "/home/agent"), "\n")
	if !strings.Contains(sec, "`$"+durable.EnvVar+"`** (`/x`)") {
		t.Errorf("the lead does not name $%s:\n%s", durable.EnvVar, sec)
	}
}

// A launch with no durable dir says so and why, and names NO path to put work in: the
// variable is unset, and a briefing pointing at it would send the agent to nothing
// (docs/design/durable-scratch-space.md §5.6).
func TestADurableDirThatCouldNotBeMadeIsSaidAndNotOffered(t *testing.T) {
	out := BriefingContent(BriefingInput{Workspace: "/w", Mechanism: "podman", Persistence: podmanShapedMap(),
		Durable: &DurableDir{Unavailable: "`.yolo` is a symbolic link"}})
	sec := persistenceSectionOf(t, out)
	if !strings.Contains(sec, "**No durable directory this launch**: `.yolo` is a symbolic link. Tell the user. "+
		"Until it is fixed, put nothing that must survive a restart under `/tmp`") {
		t.Errorf("the section does not say the durable dir is unavailable, and why:\n%s", sec)
	}
	for _, gone := range []string{"$YOLO_DURABLE_DIR", "git worktree add", "/workspace/.yolo/durable"} {
		if strings.Contains(sec, gone) {
			t.Errorf("the section offers %q with no durable dir:\n%s", gone, sec)
		}
	}
}

// A nested jail launched from the enclosing jail's /tmp gets a durable dir that is only as
// durable as that /tmp, and the caveat TAKES THE PLACE OF the lifetime sentence. Said after
// it, as it was, "yolo never deletes it" stood one line above "lasts only as long as that
// jail", and the enclosing yolo deletes that /tmp: two lifetimes, one false.
func TestTheDurableCaveatReplacesTheLifetimeSentence(t *testing.T) {
	sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: "/tmp/n", Mechanism: "podman",
		Persistence: podmanShapedMap(), Durable: &DurableDir{Path: durable.ContainerJailPath, Caveat: "Only as long as X."}}))
	if !strings.Contains(sec, "Use any layout under it.\n⚠ Only as long as X. It sits in the workspace's `.yolo`, ") {
		t.Errorf("the caveat is not where the lifetime sentence was:\n%s", sec)
	}
	if strings.Contains(sec, "never deletes it") {
		t.Errorf("the caveated lead still says yolo never deletes it:\n%s", sec)
	}
}

// macos-user mounts nothing, so its section is the durable answer at the REAL path and the
// one /tmp fact that differs from a container's (§5.1's per-backend wording): the Mac's own
// /tmp survives the launch and is shared, so a name there must be unique.
func TestTheMacosUserSectionIsTheDurableAnswerAtTheRealPath(t *testing.T) {
	ws := "/Users/Shared/yolo/p"
	sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: ws, Mechanism: "macos-user",
		Home: "/Users/_yolojail", Durable: &DurableDir{Path: ws + "/.yolo/durable"}}))
	for _, want := range []string{
		"**`$YOLO_DURABLE_DIR`** (`" + ws + "/.yolo/durable`) is scratch space for",
		"The rest of `" + ws + "` is the user's project.",
		"- **`/tmp`** is this Mac's own: it survives this launch, is shared with every workspace and the host user, and is cleared at reboot.",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("the macos-user section does not say %q:\n%s", want, sec)
		}
	}
	if strings.Contains(sec, "**Per launch**") || strings.Contains(sec, "/workspace") {
		t.Errorf("macos-user got a container's classes:\n%s", sec)
	}
	// The host sees this workspace at the same path, so `--lock` is asked for without the
	// container backends' reason, which would be false here.
	if !strings.Contains(sec, "/worktrees/<task>\"`. Drive it with `git -C <path>`") ||
		strings.Contains(sec, "the host does not have") {
		t.Errorf("macos-user's worktree line is not `--lock` without the container reason:\n%s", sec)
	}
}

// `--LOCK` SAYS WHY ONLY WHERE THE WHY IS TRUE: git on the host drops the registration of a
// worktree whose recorded path it cannot resolve, which happens only when the jail's
// spelling of the workspace differs from the host's (DS-D10,
// docs/design/durable-scratch-space.md §2.6). Through BriefingContent, so a call site that
// passed the jail's spelling twice, or none, fails here too.
func TestTheLockSaysWhyOnlyWhereTheHostSeesAnotherPath(t *testing.T) {
	const why = ": git records it under this jail's `/workspace`, a path the host does not have, " +
		"so without the lock a `git worktree prune` or `git gc` on the host would drop its registration."
	for _, c := range []struct {
		name, workspace, mechanism string
		want                       bool
	}{
		{"podman, host elsewhere", "/home/u/p", "podman", true},
		{"Apple Container, host elsewhere", "/Users/u/p", "container", true},
		{"podman, host at /workspace itself", "/workspace", "podman", false},
		{"macos-user", "/Users/Shared/yolo/p", "macos-user", false},
	} {
		in := BriefingInput{Workspace: c.workspace, Mechanism: c.mechanism,
			Durable: &DurableDir{Path: c.workspace + "/.yolo/durable"}}
		if c.mechanism != "macos-user" {
			in.Persistence, in.Durable.Path = podmanShapedMap(), durable.ContainerJailPath
		}
		sec := persistenceSectionOf(t, BriefingContent(in))
		if !strings.Contains(sec, "`git worktree add --lock") {
			t.Errorf("%s: no `--lock` worktree line:\n%s", c.name, sec)
		}
		if got := strings.Contains(sec, why); got != c.want {
			t.Errorf("%s: the lock's reason rendered = %v, want %v:\n%s", c.name, got, c.want, sec)
		}
	}
}

// APPLE CONTAINER'S SECTION SAYS WHAT IS TRUE THERE (docs/design/durable-scratch-space.md §9
// step 6). It used to be podman's text with two words changed, which told an Apple Container
// agent that yolo deletes a tmpfs after the jail exits, that `yolo prune` ages out log files
// it cannot find there, and nothing at all about yolo's own files in a home that has no
// read-only rest. Each class bullet that differs is pinned whole, in order.
func TestTheAppleContainerSectionSaysWhatIsTrueThere(t *testing.T) {
	out := BriefingContent(BriefingInput{Workspace: "/Users/u/p", Mechanism: "container",
		Persistence: appleContainerShapedMap(), Durable: &DurableDir{Path: durable.ContainerJailPath}})
	sec := persistenceSectionOf(t, out)
	order := []string{
		"- **Per launch** (in RAM): `/tmp`, `/var/tmp`, `/run`. Shared by every terminal attached to this jail. " +
			"Survives nothing: they are gone when the jail stops, and a restart is a new launch with new, empty ones. " +
			"Everything you put there uses this jail's memory. A scratchpad a harness hands you under `/tmp` is in " +
			"this class. Throwaway files only, never a worktree.",
		"- **Per workspace**: `$YOLO_DURABLE_DIR`, your scratch space; all of `/home/agent` outside the " +
			"other classes, writable and kept in this workspace's `.yolo/home` (the agents' and tools' own state and " +
			"installs: never put your work there); `/workspace/.venv` (this jail's own copies, not the host's). " +
			"Survives restarts and every new launch of this workspace; another workspace has its own. yolo deletes " +
			"nothing here but its own files (below).",
		"- **Every workspace on this machine**: `~/.cache`, `/mise`.",
		"- **The workspace itself**: `/workspace`, live on the host",
		"- **Rewritten at each launch**: the briefing and skills files, and a few files yolo keeps in the home " +
			"itself. A write to one may succeed here, and the next launch replaces it.",
		"- **Secrets**: nowhere you choose.",
	}
	last := -1
	for _, want := range order {
		i := strings.Index(sec, want)
		if i < 0 {
			t.Errorf("the Apple Container section does not say %q:\n%s", want, sec)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order:\n%s", want, sec)
		}
		last = i
	}
	// Podman's clauses, each false here: no remover deletes a tmpfs, the log age-out looks
	// for podman's dot-stripped overlay names, and this home has no read-only rest.
	for _, gone := range []string{"(on disk)", "yolo deletes these once the jail exits",
		"some agents' old log files", "**Read-only**", "A write there fails"} {
		if strings.Contains(sec, gone) {
			t.Errorf("the Apple Container section says podman's %q:\n%s", gone, sec)
		}
	}
	// The workspace is at /workspace here and elsewhere on the host, so `--lock` keeps its
	// reason, as on podman.
	if !strings.Contains(sec, "git records it under this jail's `/workspace`, a path the host does not have") {
		t.Errorf("the Apple Container section lost the `--lock` reason:\n%s", sec)
	}
}

// The RAM clauses follow the BACKING, not the backend: podman under `ephemeral_storage:
// "tmpfs"` has a tmpfs /tmp that no remover deletes and that costs the jail memory, and it
// keeps its read-only home and its own deletion clause.
func TestAPodmanTmpfsSectionGetsTheRAMClausesAndKeepsItsReadOnlyHome(t *testing.T) {
	m := podmanShapedMap()
	m.PerLaunchInRAM = true
	sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: "/w", Mechanism: "podman",
		Persistence: m, Durable: &DurableDir{Path: durable.ContainerJailPath}}))
	for _, want := range []string{
		"- **Per launch** (in RAM): `/tmp`, `/run`. Shared by every terminal attached to this jail. Survives " +
			"nothing: they are gone when the jail stops, and a restart is a new launch with new, empty ones. " +
			"Everything you put there uses this jail's memory.",
		"yolo deletes nothing here but some agents' old log files (`yolo prune --apply`).",
		"- **Read-only**: the rest of `/home/agent`",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("the podman tmpfs section does not say %q:\n%s", want, sec)
		}
	}
	for _, gone := range []string{"yolo deletes these once the jail exits", "**Rewritten at each launch**"} {
		if strings.Contains(sec, gone) {
			t.Errorf("the podman tmpfs section says %q:\n%s", gone, sec)
		}
	}
}

func appleContainerShapedMap() *PersistenceMap {
	return &PersistenceMap{PerLaunchInRAM: true, Paths: []PersistentPath{
		{"/workspace", PathProject},
		{"/workspace/.venv", PathWorkspaceDurable},
		{"/home/agent", PathWorkspaceDurable},
		{"/home/agent/.cache", PathMachineDurable},
		{"/mise", PathMachineDurable},
		{"/tmp", PathPerLaunch},
		{"/var/tmp", PathPerLaunch},
		{"/run", PathPerLaunch},
	}}
}

// OQ-DS3: the host notch gets ONE static sentence and no variable.
func TestTheHostBriefingSaysWhereTmpLivesAndNamesNoVariable(t *testing.T) {
	out := HostBriefingBase("", false)
	if !strings.Contains(out, "`/tmp` is this machine's own: it survives an agent restart but may not survive a reboot; put worktrees you need later inside the repository or beside it.") {
		t.Errorf("the host header lacks OQ-DS3's sentence:\n%s", out)
	}
	if strings.Contains(out, durable.EnvVar) || strings.Contains(out, persistenceHeading) {
		t.Errorf("the host briefing names the jail's durable dir:\n%s", out)
	}
}
