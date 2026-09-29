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
		"**Your work goes in `$YOLO_DURABLE_DIR`** (`/workspace/.yolo/durable`): worktrees, clones, drafts, measurements, anything that must survive a restart. Use any layout under it; worktrees go in `worktrees/<task>`. yolo never deletes anything there.\n" +
		"It is inside the workspace, git-ignored by yolo's own `.yolo/.gitignore`, and the user sees the same files on the host; the rest of `/workspace` is the user's project.\n"
	if !strings.HasPrefix(sec, lead) {
		t.Errorf("the section does not open with the durable dir:\n%s", sec)
	}
	order := []string{
		// The relayed /tmp confusion: an agent followed its harness's /tmp over the classes.
		"If a harness, workflow or tool tells you to put work under `/tmp`, put anything that must outlive this session in `$YOLO_DURABLE_DIR` instead; the harness cannot see this jail's storage classes.",
		"Make a worktree with `git worktree add --lock \"$YOLO_DURABLE_DIR/worktrees/<task>\"` and drive it with `git -C <path>`, not `cd <path> && …`, so a missing tree fails the command instead of running it in the workspace.",
		"Remove one with `git worktree unlock <path> && git worktree remove <path>`, which refuses while it holds uncommitted changes; `git worktree remove -f -f <path>` discards them.",
		"- **Per launch** (on disk): `/tmp`, `/run`. Shared by every terminal attached to this jail. Survives nothing",
		"yolo deletes these once the jail exits",
		"Throwaway files only, never a worktree.",
		"- **Per workspace**: `$YOLO_DURABLE_DIR`, the one place for your work; in home, only `~/.claude`, `~/.config`, `~/go` (the agents' and tools' own state and installs: never put your work there); `/workspace/.venv` (this jail's own copies, not the host's). Survives restarts and every new launch of this workspace",
		"lost only if the workspace's `.yolo` is deleted",
		"- **Every workspace on this machine**: `~/.cache`, `/mise`. Survives restarts and workspace switches, and every jail on this machine shares them",
		"Tool caches belong here (pip's `~/.cache/pip`, for one), never your work",
		"- **The workspace itself**: `/workspace`, live on the host: the user's project, not a scratch area; only `$YOLO_DURABLE_DIR` inside it is yours.",
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
	for _, gone := range []string{"~/.yolo/bin", ".claude/worktrees", "git check-ignore", "being designed",
		"remove yours with `git worktree remove -f -f", "the lock makes it take two"} {
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
	sec := strings.Join(persistenceSection(podmanShapedMap(), &DurableDir{Path: "/x"}, "/workspace", "/home/agent"), "\n")
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
	if !strings.Contains(sec, "**No durable directory this launch**: `.yolo` is a symbolic link. Tell the user.") {
		t.Errorf("the section does not say the durable dir is unavailable, and why:\n%s", sec)
	}
	for _, gone := range []string{"$YOLO_DURABLE_DIR", "git worktree add", "/workspace/.yolo/durable"} {
		if strings.Contains(sec, gone) {
			t.Errorf("the section offers %q with no durable dir:\n%s", gone, sec)
		}
	}
}

// A nested jail launched from the enclosing jail's /tmp gets a durable dir that is only as
// durable as that /tmp, and the section says so beside the answer.
func TestTheDurableCaveatIsSaidBesideTheAnswer(t *testing.T) {
	sec := persistenceSectionOf(t, BriefingContent(BriefingInput{Workspace: "/tmp/n", Mechanism: "podman",
		Persistence: podmanShapedMap(), Durable: &DurableDir{Path: durable.ContainerJailPath, Caveat: "Only as long as X."}}))
	if !strings.Contains(sec, "the user's project.\n⚠ Only as long as X.\n") {
		t.Errorf("the caveat is not beside the answer:\n%s", sec)
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
		"**Your work goes in `$YOLO_DURABLE_DIR`** (`" + ws + "/.yolo/durable`)",
		"the rest of `" + ws + "` is the user's project.",
		"- **`/tmp`** is this Mac's own: it survives this launch, is shared with every workspace and the host user, and is cleared at reboot.",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("the macos-user section does not say %q:\n%s", want, sec)
		}
	}
	if strings.Contains(sec, "**Per launch**") || strings.Contains(sec, "/workspace") {
		t.Errorf("macos-user got a container's classes:\n%s", sec)
	}
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
