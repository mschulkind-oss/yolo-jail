package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
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
		"where the storage guidance above says. In a yolo jail where `$YOLO_DURABLE_DIR` is set, that is\n" +
		"`$YOLO_DURABLE_DIR/worktrees/<task>`; where that guidance says there is no durable directory,\n" +
		"ask the user.\n\n" +
		"If `git status` lists `.claude/worktrees/`, this repository does not ignore it: never `git add`\n" +
		"it, and tell the user, since the fix is a line in their `.gitignore`.\n"
	const piProse = "## Where pi's workflow tools put worktrees\n\n" +
		"pi-subagents makes a `worktree: true` run's worktrees in the system temp dir unless told\n" +
		"otherwise: `/tmp`, which a container jail deletes when it exits. So where `$YOLO_DURABLE_DIR` is\n" +
		"set, yolo's extension `~/.pi/agent/extensions/yolo-durable-worktrees.js` sets\n" +
		"`worktreeBaseDir` in `~/.pi/agent/extensions/subagent/config.json` to\n" +
		"`$YOLO_DURABLE_DIR/worktrees/pi-subagents`, and they go there. A `PI_SUBAGENTS_WORKTREE_DIR`\n" +
		"already set, or a user-configured `worktreeBaseDir`, wins.\n" +
		"pi-subagents removes a run's worktrees when the run ends; one a restart cut short stays until you\n" +
		"`git worktree remove` it.\n\n" +
		"pi-dynamic-workflows puts its worktrees in the repository's `.pi/worktrees/`, which it has no\n" +
		"setting to change. They survive a restart. Unless the repository ignores `.pi/worktrees/`,\n" +
		"`git status` lists it: never `git add` it.\n\n" +
		"Neither place is a pattern for a worktree you make yourself. Put that where the user or the\n" +
		"project says; failing that, where the storage guidance above says. In a yolo jail where\n" +
		"`$YOLO_DURABLE_DIR` is set, that is `$YOLO_DURABLE_DIR/worktrees/<task>`; where that guidance\n" +
		"says there is no durable directory, ask the user.\n"
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

	// A LAUNCH WITH NO DURABLE DIR (docs/design/durable-scratch-space.md §5.1's degenerate
	// inputs, §5.6): `.yolo` a link, so ensureDurableDir exports nothing and core's section
	// says there is none. The pack prose is static and follows that section, so it must not
	// send the agent to `$YOLO_DURABLE_DIR/…` unconditionally: with the variable unset that
	// is `/worktrees/<task>`, on a read-only root. Each sentence naming a path under the
	// variable says it holds where the variable is set, and the prose says what to do where
	// the section above says there is none, in that section's own words. Both container
	// backends, whose sections differ.
	for _, rt := range []string{"podman", "container"} {
		wsNone := t.TempDir()
		if err := os.Symlink(t.TempDir(), filepath.Join(wsNone, ".yolo")); err != nil {
			t.Fatal(err)
		}
		oNone := goldenOptions(wsNone, home)
		oNone.Stdout, oNone.Stderr = discardBuf(), discardBuf()
		if d := oNone.ensureDurableDir(rt, cfg); d.Path != "" || d.Unavailable == "" {
			t.Fatalf("%s: a linked .yolo still got a durable dir: %+v", rt, d)
		}
		stagingNone, err := oNone.refreshJailBriefings("yolo-ws-abcd1234", cfg, rt,
			stagedPacks{packs: packs, briefings: proses}, ioprio.Normal)
		if err != nil {
			t.Fatalf("%s refreshJailBriefings: %v", rt, err)
		}
		for dest, want := range map[string]string{claudeDest: claudeProse, piDest: piProse} {
			raw, err := os.ReadFile(filepath.Join(stagingNone, briefingStagingName(dest)))
			if err != nil {
				t.Fatalf("%s: no %s briefing written: %v", rt, dest, err)
			}
			body := string(raw)
			if !strings.Contains(body, "**No durable directory this launch**") {
				t.Errorf("%s %s: core's section does not say there is no durable dir:\n%s", rt, dest, body)
			}
			if !strings.HasSuffix(body, want) {
				t.Errorf("%s %s does not end with its own agent's worktree prose:\n%s", rt, dest, body)
			}
			for _, s := range unconditionedDurablePaths(want) {
				t.Errorf("%s %s sends the agent under the unset variable with no condition: %q", rt, dest, s)
			}
			if !strings.Contains(strings.Join(strings.Fields(want), " "),
				"where that guidance says there is no durable directory, ask the user") {
				t.Errorf("%s %s: the prose says nothing for the launch whose section says there is no durable dir:\n%s", rt, dest, want)
			}
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

// unconditionedDurablePaths is each sentence of prose that names a path under
// $YOLO_DURABLE_DIR without saying, in that sentence, that it holds where the variable is set.
func unconditionedDurablePaths(prose string) []string {
	under, cond := "`$"+durable.EnvVar+"/", "`$"+durable.EnvVar+"` is set"
	var bad []string
	for _, s := range strings.SplitAfter(strings.Join(strings.Fields(prose), " "), ". ") {
		if strings.Contains(s, under) && !strings.Contains(s, cond) {
			bad = append(bad, s)
		}
	}
	return bad
}
