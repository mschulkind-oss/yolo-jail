package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// EACH AGENT'S OWN PACK SAYS WHERE ITS OWN WORKFLOW TOOL PUTS WORKTREES, AND ONLY TO THAT AGENT
// (docs/design/durable-scratch-space.md §5.5, §9 step 5; DS-D33). Core's storage-classes section
// names no agent's directory, so the claude pack tells Claude that its own worktrees stay in
// `.claude/worktrees/`, and the pi pack tells pi where pi-subagents' and pi-dynamic-workflows'
// worktrees go. Each is one agent's tool, so it must reach that agent's briefing and no other:
// Claude's directory in pi's instructions, or in codex's, is the "one agent's directory offered
// to everyone" that DS-P2 and row B″ reject.
//
// Driven through refreshJailBriefings with the REAL official packs, so deleting either pack's
// contribution, either file, or the `agents` line that narrows it fails here; and through the
// host notch's composer, which must say the same words to the same agent.
func TestEachAgentPackShipsItsOwnWorktreeProseToItsOwnAgent(t *testing.T) {
	const claudeProse = "## Claude Code's own worktrees\n\n" +
		"Claude Code puts the worktrees it makes in the repository's `.claude/worktrees/`: those of\n" +
		"`claude --worktree`, of `EnterWorktree`, and of subagents and workflows that run in a worktree.\n" +
		"Leave them there; they survive a restart. Never make `.claude` or `.claude/worktrees` a symbolic\n" +
		"link, because Claude Code then refuses to make a worktree.\n\n" +
		"A worktree you make yourself with `git worktree add` is not one of these, and Claude Code's\n" +
		"cleanup keeps it until you remove it. Put it where the user or the project says; failing that,\n" +
		"where the storage guidance above says, which in a yolo jail is\n" +
		"`$YOLO_DURABLE_DIR/worktrees/<task>`.\n\n" +
		"If `git status` lists `.claude/worktrees/`, this repository does not ignore it: never `git add`\n" +
		"it, and tell the user, since the fix is a line in their `.gitignore`.\n"
	const piProse = "## Where pi's workflow tools put worktrees\n\n" +
		"pi-subagents makes a `worktree: true` run's worktrees in the system temp dir unless told\n" +
		"otherwise: `/tmp`, which a container jail deletes when it exits. So where `$YOLO_DURABLE_DIR` is\n" +
		"set, yolo's extension `~/.pi/agent/extensions/yolo-durable-worktrees.js` sets\n" +
		"`PI_SUBAGENTS_WORKTREE_DIR` to `$YOLO_DURABLE_DIR/worktrees/pi-subagents`, and they go there. A\n" +
		"`PI_SUBAGENTS_WORKTREE_DIR` already set, or pi-subagents' own `worktreeBaseDir` setting, wins.\n" +
		"pi-subagents removes a run's worktrees when the run ends; one a restart cut short stays until you\n" +
		"`git worktree remove` it.\n\n" +
		"pi-dynamic-workflows puts its worktrees in the repository's `.pi/worktrees/`, which it has no\n" +
		"setting to change. They survive a restart. Unless the repository ignores `.pi/worktrees/`,\n" +
		"`git status` lists it: never `git add` it.\n\n" +
		"Neither place is a pattern for a worktree you make yourself. Put that where the user or the\n" +
		"project says; failing that, where the storage guidance above says, which in a yolo jail is\n" +
		"`$YOLO_DURABLE_DIR/worktrees/<task>`.\n"
	const claudeDest, piDest, codexDest = ".claude/CLAUDE.md", ".pi/agent/AGENTS.md", ".codex/AGENTS.md"

	// THE JAIL: the production staging (stagePacks reads each pack's prose, packBriefingProses)
	// and the file refreshJailBriefings writes for each destination, from the user's `packs`.
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "pi", "codex"]`)
	pinLauncherInJail(t, false)
	emptyLoopholeDirs(t)
	ws := t.TempDir()
	o := goldenOptions(ws, home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	_, packs, proses, err := o.stagePacks("yolo-ws-abcd1234")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	cfg, _ := persistenceFixture(t, "")
	o.ensureDurableDir("podman", cfg)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, "podman",
		stagedPacks{packs: packs, briefings: proses}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	jail := map[string]string{}
	for _, d := range []string{claudeDest, piDest, codexDest} {
		raw, err := os.ReadFile(filepath.Join(staging, briefingStagingName(d)))
		if err != nil {
			t.Fatalf("no %s briefing written: %v", d, err)
		}
		jail[d] = string(raw)
	}

	// THE HOST NOTCH: the same packs through `yolo host apply`'s composer.
	hostHome := t.TempDir()
	resolved, _ := packload.ResolveDestinations(packs)
	host := map[string]string{}
	for _, d := range entrypoint.ComposeHostBriefings(resolved, hostHome, "", false) {
		rel, err := filepath.Rel(hostHome, d.Path)
		if err != nil {
			t.Fatal(err)
		}
		host[filepath.ToSlash(rel)] = d.Content
	}

	for notch, files := range map[string]map[string]string{"jail": jail, "host": host} {
		for dest, want := range map[string]string{claudeDest: claudeProse, piDest: piProse} {
			body := files[dest]
			// Last, after core's section and every other section: the pack's prose follows
			// the base (composed here without one at the host, as briefingparity_test does).
			if !strings.HasSuffix(body, want) {
				t.Errorf("%s %s does not end with its own agent's worktree prose:\n%s", notch, dest, body)
			}
			if strings.Count(body, want) != 1 {
				t.Errorf("%s %s carries its worktree prose %d times, want 1", notch, dest, strings.Count(body, want))
			}
		}
		// Neither agent's prose reaches another agent.
		for dest, gone := range map[string][]string{
			claudeDest: {"## Where pi's workflow tools put worktrees"},
			piDest:     {"## Claude Code's own worktrees"},
			codexDest:  {"## Claude Code's own worktrees", "## Where pi's workflow tools put worktrees"},
		} {
			for _, g := range gone {
				if strings.Contains(files[dest], g) {
					t.Errorf("%s %s carries another agent's prose %q:\n%s", notch, dest, g, files[dest])
				}
			}
		}
	}

	// In the jail the prose follows core's storage-classes section, which is the "storage
	// guidance above" both files point at.
	for _, dest := range []string{claudeDest, piDest} {
		s, p := strings.Index(jail[dest], "## Storage classes: what survives a restart"),
			strings.LastIndex(jail[dest], "\n## ")
		if s < 0 || p < s {
			t.Errorf("jail %s: the pack's prose is not below the storage-classes section:\n%s", dest, jail[dest])
		}
	}

	// The files the packs ship are the text pinned above, so the pin is of what ships.
	for file, want := range map[string]string{"claude/briefing/worktrees.md": claudeProse, "pi/briefing/worktrees.md": piProse} {
		got, err := officialpacks.FS.ReadFile(file)
		if err != nil || string(got) != want {
			t.Errorf("packs/%s is not the pinned prose (err %v):\n%s", file, err, got)
		}
	}
}
