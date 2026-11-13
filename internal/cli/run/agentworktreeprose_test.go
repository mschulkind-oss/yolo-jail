package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// EACH AGENT'S OWN PACK SAYS WHERE ITS OWN WORKFLOW TOOL PUTS WORKTREES, AND ONLY TO THAT AGENT
// (docs/reference/jail-home.md#each-agents-own-pack; DS-D33). Core's storage-classes section
// names no agent's directory, so the claude pack tells Claude that its own worktrees stay in
// `.claude/worktrees/`, and the pi pack tells pi where pi-subagents' and pi-dynamic-workflows'
// worktrees go. Each is one agent's tool, so it must reach that agent and no other: Claude's
// directory in pi's instructions, or in codex's, is the "one agent's directory offered to
// everyone" that DS-P2 and row B″ reject.
//
// SINCE THE BRIEFING DIET (docs/design/briefing-diet.md BD-D2) each pack's always-loaded prose
// is one pointer of at most 250 bytes, which says how its harness starts the background
// `yolo wait` and names the pack's skill; the worktree text is in that skill (`yolo-claude`,
// `yolo-pi`). Both halves are addressed to the pack's own agent.
//
// Driven through refreshJailBriefings and the jail's skills staging with the REAL official packs,
// so deleting either pack's contribution, either file, or the `agents` line that narrows it fails
// here; and through the host notch's composers, which must say the same words to the same agent.
func TestEachAgentPackShipsItsOwnWorktreeProseToItsOwnAgent(t *testing.T) {
	const claudeProse = "## Claude Code's own worktrees\n\n" +
		"Claude Code puts the worktrees it makes in the repository's `.claude/worktrees/`: those of\n" +
		"`claude --worktree`, of `EnterWorktree`, and of subagents and workflows that run in a worktree.\n" +
		"Leave them there; they survive a restart. Never make `.claude` or `.claude/worktrees` a symbolic\n" +
		"link, because Claude Code then refuses to make a worktree.\n\n" +
		"A worktree you make yourself with `git worktree add` is not one of these, and Claude Code's\n" +
		"cleanup keeps it until you remove it. Put it where the user or the project says; failing that,\n" +
		"where your briefing's storage guidance says. In a yolo jail where `$YOLO_DURABLE_DIR` is set, that\n" +
		"is `$YOLO_DURABLE_DIR/worktrees/<task>`; where that guidance says there is no durable directory,\n" +
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
		"pi-subagents removes a run's worktree only when nothing in it would be lost; one holding work it\n" +
		"could not capture as a patch, and one whose run was cut short, stays. Where their directory lies\n" +
		"beneath `$YOLO_DURABLE_DIR` (yolo's, or a `PI_SUBAGENTS_WORKTREE_DIR` you set there), the same\n" +
		"extension bounds them: at most once an hour it removes, with `git worktree remove`, each\n" +
		"`pi-worktree-*` worktree there that is clean (with no edit the index hides from `git status`), on\n" +
		"a branch, in no rebase, merge or other git operation, without submodules, unlocked, used by no\n" +
		"process in this jail and idle for a day. Its branch stays, so a commit made there is not lost,\n" +
		"unless it is a `pi-subagents/*` branch whose commit another branch or a tag holds. Files the\n" +
		"worktree ignores go with it. Every other worktree is kept and listed, with the reason, in that\n" +
		"directory's `.yolo-reap.json`: remove it yourself once its work is saved.\n\n" +
		"pi-dynamic-workflows puts its worktrees in the repository's `.pi/worktrees/`, which it has no\n" +
		"setting to change. They survive a restart. Unless the repository ignores `.pi/worktrees/`,\n" +
		"`git status` lists it: never `git add` it.\n\n" +
		"Neither place is a pattern for a worktree you make yourself. Put that where the user or the\n" +
		"project says; failing that, where your briefing's storage guidance says. In a yolo jail where\n" +
		"`$YOLO_DURABLE_DIR` is set, that is `$YOLO_DURABLE_DIR/worktrees/<task>`; where that guidance\n" +
		"says there is no durable directory, ask the user.\n"
	const claudeDest, piDest, codexDest = ".claude/CLAUDE.md", ".pi/agent/AGENTS.md", ".codex/AGENTS.md"

	pointers := map[string]string{}
	for dest, file := range map[string]string{claudeDest: "claude/briefing/claude.md", piDest: "pi/briefing/pi.md"} {
		raw, err := officialpacks.FS.ReadFile(file)
		if err != nil {
			t.Fatalf("packs/%s: %v", file, err)
		}
		pointers[dest] = strings.TrimRight(string(raw), "\n")
	}

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
		for dest, want := range pointers {
			body := strings.TrimRight(files[dest], "\n")
			// Last, after core's section and every other section: the pack's prose follows
			// the base (composed here without one at the host, as briefingparity_test does).
			if !strings.HasSuffix(body, want) {
				t.Errorf("%s %s does not end with its own agent's pointer:\n%s", notch, dest, body)
			}
			if strings.Count(body, want) != 1 {
				t.Errorf("%s %s carries its pointer %d times, want 1", notch, dest, strings.Count(body, want))
			}
		}
		// Each agent's pointer says how its own harness starts the background `yolo wait`
		// core's host-run section asks for (core names no harness).
		for dest, want := range map[string]string{
			claudeDest: "with the Bash tool and `run_in_background: true`",
			piDest:     "with `background_task` and `completionPolicy: \"wake\"`",
		} {
			if strings.Count(files[dest], want) != 1 {
				t.Errorf("%s %s does not say once how its harness starts the waiter (%q):\n%s", notch, dest, want, files[dest])
			}
		}
		// Neither agent's prose reaches another agent.
		for dest, gone := range map[string][]string{
			claudeDest: {pointers[piDest], "yolo-pi"},
			piDest:     {pointers[claudeDest], "yolo-claude"},
			codexDest:  {pointers[claudeDest], pointers[piDest], "yolo-claude", "yolo-pi"},
		} {
			for _, g := range gone {
				if strings.Contains(files[dest], g) {
					t.Errorf("%s %s carries another agent's prose %q:\n%s", notch, dest, g, files[dest])
				}
			}
		}
	}

	// THE SKILLS, in the jail: each agent's skills directory holds its own pack's skill, and no
	// other agent's.
	_, targets, skillStaging := stageSkillTargets(t, "yolo-ws-abcd1235")
	for dest, want := range map[string]struct{ has, hasNot []string }{
		".claude/skills":   {[]string{"yolo-claude"}, []string{"yolo-pi"}},
		".pi/agent/skills": {[]string{"yolo-pi"}, []string{"yolo-claude"}},
		".codex/skills":    {nil, []string{"yolo-claude", "yolo-pi"}},
	} {
		names, ok := stagedFor(t, targets, skillStaging, dest)
		if !ok {
			t.Fatalf("no jail skills target for %s; targets = %v", dest, targets)
		}
		for _, n := range want.has {
			if !hasName(names, n) {
				t.Errorf("jail %s staged %v, without its own agent's %s skill", dest, names, n)
			}
		}
		for _, n := range want.hasNot {
			if hasName(names, n) {
				t.Errorf("jail %s staged %v, with another agent's %s skill", dest, names, n)
			}
		}
	}
	// And at the host: the pack's skills tree is composed into its own agent's directory only.
	for _, d := range hostskills.ComposeHostSkills(resolved, hostHome) {
		rel, _ := filepath.Rel(hostHome, d.Dir)
		for _, l := range d.Layers {
			for _, src := range l.Sources {
				owner := map[string]string{"claude": ".claude/skills", "pi": ".pi/agent/skills"}[l.Pack]
				if owner != "" && strings.HasSuffix(filepath.ToSlash(src), "/skills") && filepath.ToSlash(rel) != owner {
					t.Errorf("host composes the %s pack's skills into %s, want %s only", l.Pack, rel, owner)
				}
			}
		}
	}

	// The skills the packs ship hold the worktree prose pinned above, so the pin is of what
	// ships, and the prose never sends the agent under an unset $YOLO_DURABLE_DIR.
	for file, want := range map[string]string{"claude/skills/yolo-claude/SKILL.md": claudeProse, "pi/skills/yolo-pi/SKILL.md": piProse} {
		got, err := officialpacks.FS.ReadFile(file)
		if err != nil || !strings.Contains(string(got), want) {
			t.Errorf("packs/%s does not hold the pinned prose (err %v):\n%s", file, err, got)
		}
		for _, s := range unconditionedDurablePaths(want) {
			t.Errorf("packs/%s sends the agent under the unset variable with no condition: %q", file, s)
		}
		if !strings.Contains(strings.Join(strings.Fields(want), " "),
			"where that guidance says there is no durable directory, ask the user") {
			t.Errorf("packs/%s says nothing for the launch whose briefing says there is no durable dir", file)
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
