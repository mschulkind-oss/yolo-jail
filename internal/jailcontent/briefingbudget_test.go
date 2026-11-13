package jailcontent

// The briefing diet's budgets (docs/design/briefing-diet.md §8, BD-D4), as tests: a budget that is
// not tested is a wish (P5). A failure names the bytes over and the longest lines, and the fix it
// names is moving text to a topic file, not raising the number.

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent/builtinskills"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// coreBudget is the core's limit in bytes, for every setup's reference fact sheet.
const coreBudget = 1800

// skillDescriptionBudget is the limit on the `description` of a skill yolo ships.
const skillDescriptionBudget = 200

// referenceGuardrails are the guardrails pack's blocks and guards, as the run pipeline hands them
// to the briefing (run.blockedToolRecords).
func referenceGuardrails() []BlockedTool {
	return []BlockedTool{
		{Name: "grep", Message: "grep's recursive mode is blocked. Use ripgrep (rg) for recursive searches; pipe filters and single-file greps pass through.", Suggestion: "Try: rg <pattern> [path]", Replacement: "rg"},
		{Name: "find", Message: "find is blocked to prevent unintended recursive searches. Use fd for a faster, more intuitive alternative.", Suggestion: "Try: fd <pattern>", Replacement: "fd"},
		{Name: "mv", Guard: "workspace-state", Message: "refuses to move the workspace's .yolo (this workspace's agent homes) or anything in it; your own files under $YOLO_DURABLE_DIR move freely."},
		{Name: "rm", Guard: "workspace-state", Message: "refuses to delete the workspace's .yolo (this workspace's agent homes), anything in it, or a directory holding it; your own files under $YOLO_DURABLE_DIR delete freely."},
		{Name: "git", Guard: "workspace-state", Message: "refuses git clean scopes that can delete this workspace's .yolo state or durable directory; dry-runs and explicit ordinary paths (including your files under $YOLO_DURABLE_DIR) pass. Use git clean -n to inspect first."},
	}
}

// referenceFactSheets are one briefing input per built-in jail setup, carrying the facts that add
// lines to the core (§8): one forwarded port, one context mount, a durable dir, the guardrails
// blocks, and a loophole. A realistic workspace path, and the Claude destination's skill path.
func referenceFactSheets() map[string]BriefingInput {
	common := func(name string) BriefingInput {
		s := setup.MustBuiltIn(name)
		return BriefingInput{
			Workspace: "/home/someone/code/project", Setup: s, SetupDescription: setup.Describe(s),
			BlockedTools:       referenceGuardrails(),
			ForwardHostPorts:   []any{8919},
			Loopholes:          []Loophole{{Name: "git-broker", Desc: "Run the jail's git fetch and push through the host's own credentials."}},
			MechanicsSkillPath: "~/.claude/skills/jail-mechanics/SKILL.md",
			ConfigName:         "yolo-jail.toml",
		}
	}
	podman := common("podman-self")
	podman.Mechanism, podman.NetMode, podman.AppliedNetMode = "podman", "bridge", "bridge"
	podman.Persistence = podmanShapedMap()
	podman.Durable = &DurableDir{Path: "/workspace/.yolo/durable"}
	podman.ContextMounts = []ContextMount{{Path: "/ctx/host-yolo-logs", Host: "/home/someone/.local/share/yolo-jail/logs", Optional: true}}
	podman.HostNix = true

	ac := common("apple-container-self")
	ac.Mechanism, ac.IsMacOS, ac.AppliedNetMode = "container", true, "bridge"
	ac.Persistence = appleContainerShapedMap()
	ac.Durable = &DurableDir{Path: "/workspace/.yolo/durable"}
	ac.ContextMounts = []ContextMount{{Path: "/ctx/notes", Host: "/Users/someone/notes"}}

	native := func(name string) BriefingInput {
		in := common(name)
		in.Workspace = "/Users/someone/code/project"
		in.Mechanism, in.IsMacOS, in.AppliedNetMode = "macos-user", true, "host"
		in.Home = "/Users/someone/code/project/.yolo/setups/" + name + "/home"
		in.Durable = &DurableDir{Path: "/Users/someone/code/project/.yolo/durable"}
		in.ContextDir = in.Home + "/ctx"
		in.ContextMounts = []ContextMount{{Path: in.ContextDir + "/notes", Host: "/Users/someone/notes"}}
		in.ForwardHostPorts = nil
		return in
	}
	return map[string]BriefingInput{
		"podman-self":           podman,
		"apple-container-self":  ac,
		"seatbelt-self":         native("seatbelt-self"),
		"seatbelt-yolo-account": native("seatbelt-yolo-account"),
	}
}

// overBudget is the failure message: the bytes over, the three longest lines, and the fix.
func overBudget(what string, text string, budget int) string {
	lines := strings.Split(text, "\n")
	sort.SliceStable(lines, func(i, j int) bool { return len(lines[i]) > len(lines[j]) })
	var longest []string
	for _, l := range lines[:min(3, len(lines))] {
		longest = append(longest, fmt.Sprintf("  %4d bytes: %s", len(l), l))
	}
	return fmt.Sprintf("%s is %d bytes, %d over its %d-byte budget (docs/design/briefing-diet.md §8). "+
		"Move text to a topic file of the jail-mechanics skill (briefingtopics.go), and leave the core "+
		"the one line an agent needs before it would look; do not raise the number. Longest lines:\n%s\n\n%s",
		what, len(text), len(text)-budget, budget, strings.Join(longest, "\n"), text)
}

// TestTheCoreFitsItsBudget: the core of every built-in jail setup's reference fact sheet is at most
// coreBudget bytes, through BriefingContent itself, with no alert (alerts are outside the budget,
// BD-D5).
func TestTheCoreFitsItsBudget(t *testing.T) {
	for name, in := range referenceFactSheets() {
		t.Run(name, func(t *testing.T) {
			core := BriefingContent(in)
			if len(core) > coreBudget {
				t.Error(overBudget("The "+name+" core", core, coreBudget))
			}
			t.Logf("%s core: %d bytes\n%s", name, len(core), core)
		})
	}
}

// The budget binds an unbounded fact at a fixed cost: twenty forwarded ports do not grow the core
// by twenty entries (§8).
func TestTheCoreDoesNotGrowWithEveryPort(t *testing.T) {
	in := referenceFactSheets()["podman-self"]
	var many []any
	for p := 9000; p < 9020; p++ {
		many = append(many, p)
	}
	in.ForwardHostPorts, in.PublishPorts = many, many
	core := BriefingContent(in)
	if len(core) > coreBudget {
		t.Error(overBudget("The podman-self core with twenty ports each way", core, coreBudget))
	}
	if !strings.Contains(core, "20 host ports are forwarded") || strings.Contains(core, "9019") {
		t.Errorf("past %d ports the core must give the count and name the topic:\n%s", corePortListMax, core)
	}
}

// TestShippedSkillDescriptionsFitTheirBudget: every built-in skill's `description`, and the
// generated jail-mechanics index's, is at most skillDescriptionBudget bytes (BD-D4). A shipped
// pack's own skills are held by the pack's budget test (Task 3 of the plan).
func TestShippedSkillDescriptionsFitTheirBudget(t *testing.T) {
	descs := map[string]string{}
	if err := fs.WalkDir(builtinskills.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "SKILL.md") {
			return err
		}
		data, err := builtinskills.FS.ReadFile(p)
		descs[p] = skillDescription(string(data))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, facts := range []FactSheet{{Host: true}, {WorkspacePath: "/workspace", Briefing: BriefingInput{Workspace: "/w"}}} {
		files, err := RenderBuiltinSkills(facts)
		if err != nil {
			t.Fatal(err)
		}
		for p, data := range files {
			if strings.HasSuffix(p, "SKILL.md") {
				descs[fmt.Sprintf("%s (host=%v)", p, facts.Host)] = skillDescription(string(data))
			}
		}
	}
	if len(descs) < 3 {
		t.Fatalf("found too few shipped skills to check: %v", descs)
	}
	for p, d := range descs {
		if d == "" {
			t.Errorf("%s has no description line", p)
		}
		if len(d) > skillDescriptionBudget {
			t.Errorf("%s: description is %d bytes, %d over its %d-byte budget (docs/design/briefing-diet.md §8). "+
				"Name the subjects an agent would ask about and move the rest into the skill's body:\n%s",
				p, len(d), len(d)-skillDescriptionBudget, skillDescriptionBudget, d)
		}
	}
}

// skillDescription is a SKILL.md front matter's `description` value, unquoted.
func skillDescription(skill string) string {
	for _, line := range strings.Split(skill, "\n") {
		if v, ok := strings.CutPrefix(line, "description: "); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}
