package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/changetiming"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// reachable is everything a jail's agent can read about its jail without asking anyone: the core
// in its briefing, and every topic file of the jail-mechanics skill it points at. A test that
// pinned a sentence of the pre-diet body pins it here, which still fails when the fact stops
// reaching the agent (docs/reference/agent-briefings.md#where-the-rest-went: every sentence has a destination). Each
// topic is headed `## <Title>`, as the inlined fallback heads it, so a test anchored on a pre-diet
// section heading measures the same section.
func reachable(in BriefingInput) string {
	var b strings.Builder
	b.WriteString(BriefingContent(in))
	for _, t := range BriefingTopics(in) {
		b.WriteString("\n## " + t.Title + "\n\n" + strings.Join(t.Body, "\n") + "\n")
	}
	return b.String()
}

// topicText is one topic file as the jail-mechanics skill holds it, or "" when the jail has none.
func topicText(in BriefingInput, file string) string {
	for _, t := range BriefingTopics(in) {
		if t.File == file {
			return t.Text()
		}
	}
	return ""
}

// The core points at the topics the skill holds, by the names the index lists, and never at one
// the jail has not got: a topic exists only when its subject does.
func TestTheCorePointsAtExactlyTheTopicsTheJailHas(t *testing.T) {
	bare := BriefingInput{Workspace: "/w"}
	files := map[string]bool{}
	for _, topic := range BriefingTopics(bare) {
		files[topic.File] = true
	}
	for _, want := range []string{"storage.md", "network.md", "host-requests.md", "limits.md"} {
		if !files[want] {
			t.Errorf("every jail has a %s topic: %v", want, files)
		}
	}
	for _, absent := range []string{"mounts.md", "loopholes.md", "blocked-tools.md"} {
		if files[absent] {
			t.Errorf("a jail with no %s subject got the topic", absent)
		}
	}
	core := BriefingContent(bare)
	if !strings.Contains(core, "Its topic files: storage, network, host requests, limits.") {
		t.Errorf("the pointer does not list exactly this jail's topics:\n%s", core)
	}
	full := referenceFactSheets()["podman-self"]
	if !strings.Contains(BriefingContent(full), "storage, network, mounts, host requests, loopholes, blocked tools, limits.") {
		t.Errorf("the pointer does not list every topic of a jail that has them all:\n%s", BriefingContent(full))
	}
	if HostBriefingBase(false) == "" || len(BriefingTopics(BriefingInput{Setup: setup.MustBuiltIn("host-self")})) != 0 {
		t.Error("the host notch has no topics")
	}
}

// The pointer names the destination agent's own SKILL.md (BD-D6), and no path for a destination
// with no skills directory of its identity.
func TestThePointerNamesTheDestinationsSkillPath(t *testing.T) {
	with := BriefingContent(BriefingInput{Workspace: "/w", MechanicsSkillPath: "~/.claude/skills/jail-mechanics/SKILL.md"})
	if !strings.Contains(with, "Read the **jail-mechanics** skill (`~/.claude/skills/jail-mechanics/SKILL.md`) before") {
		t.Errorf("the pointer does not name the skill's path:\n%s", with)
	}
	without := BriefingContent(BriefingInput{Workspace: "/w"})
	if !strings.Contains(without, "Read the **jail-mechanics** skill before") {
		t.Errorf("a destination with no skills dir must get the sentence without a path:\n%s", without)
	}
	prefixed := BriefingContent(BriefingInput{Workspace: "/w", ShippedSkillPrefix: "yolo"})
	if !strings.Contains(prefixed, "**yolo-jail-mechanics**") {
		t.Errorf("the pointer must name the skill as it is delivered under the prefix:\n%s", prefixed)
	}
}

// When this entry did not write the topic files, they are in the briefing in full and the core
// names no file (the trap the plan records: the core never points at a topic file that is not
// there).
func TestInlinedTopicsCarryEveryFactAndNameNoFile(t *testing.T) {
	in := referenceFactSheets()["podman-self"]
	in.InlineTopics = true
	got := BriefingContent(in)
	if strings.Contains(got, "SKILL.md") || strings.Contains(got, "Its topic files") {
		t.Errorf("an inlined briefing points at files this entry did not write:\n%s", got)
	}
	for _, topic := range BriefingTopics(in) {
		section := "## " + topic.Title + "\n\n" + strings.Join(topic.Body, "\n")
		if !strings.Contains(got, section) {
			t.Errorf("the inlined briefing lacks the %s topic:\n%s", topic.File, section)
		}
	}
	if !strings.Contains(got, "## Skills") || !strings.Contains(got, toolingLine) {
		t.Errorf("the inlined briefing lacks the skills section and the tooling line the index carries:\n%s", got)
	}
}

// The generated skill holds every topic the core names, and its index lists them above the
// recipes, with the tooling line and the other built-in skills the briefing used to name.
func TestTheMechanicsSkillHoldsTheTopicsItsIndexLists(t *testing.T) {
	in := referenceFactSheets()["podman-self"]
	files, err := RenderBuiltinSkills(FactSheet{Briefing: in, WorkspacePath: "/workspace", HostWorkspace: in.Workspace,
		ConfigPath: "/workspace/yolo-jail.toml", UserConfigPath: "/u/config.toml", RestartCommand: "yolo stop",
		Timing: []changetiming.Row{{Key: "mounts", Outcome: changetiming.AppliedNow, Reason: "now"}}})
	if err != nil {
		t.Fatal(err)
	}
	index := string(files["jail-mechanics/SKILL.md"])
	topics, recipes := strings.Index(index, "## Topics"), strings.Index(index, "## Recipes")
	if topics < 0 || recipes < 0 || topics > recipes {
		t.Fatalf("the index has no Topics list above Recipes:\n%s", index)
	}
	for _, topic := range BriefingTopics(in) {
		body, ok := files["jail-mechanics/topics/"+topic.File]
		if !ok || string(body) != topic.Text() {
			t.Errorf("topics/%s is missing or not the briefing's topic text", topic.File)
		}
		if !strings.Contains(index, "→ `topics/"+topic.File+"`") {
			t.Errorf("the index does not list topics/%s:\n%s", topic.File, index)
		}
	}
	for _, want := range []string{toolingLine, "**configuring-the-jail** before editing `yolo-jail.toml`", "**diagnosing-the-jail**",
		"workspace-level ones (`/workspace/.<agent>/skills/`) are writable"} {
		if !strings.Contains(index, want) {
			t.Errorf("the index lacks %q:\n%s", want, index)
		}
	}
	host, err := RenderBuiltinSkills(FactSheet{Host: true, HostWorkspace: "/h"})
	if err != nil {
		t.Fatal(err)
	}
	for name := range host {
		if strings.Contains(name, "/topics/") {
			t.Errorf("the host notch's skill got a topic file %s", name)
		}
	}
}

// The `rg -r` trap is said only where a block names rg (BD-D7), in the core and in the topic.
func TestTheRgWarningNeedsAnRgBlock(t *testing.T) {
	with := BriefingInput{Workspace: "/w", BlockedTools: referenceGuardrails()}
	if !strings.Contains(BriefingContent(with), "where `-r` means replace") ||
		!strings.Contains(topicText(with, "blocked-tools.md"), "in rg, `-r` means `--replace`") {
		t.Errorf("a ripgrep block must carry the warning:\n%s", reachable(with))
	}
	without := BriefingInput{Workspace: "/w", BlockedTools: []BlockedTool{{Name: "curl", Message: "use the broker."}}}
	if strings.Contains(reachable(without), "`-r`") {
		t.Errorf("a jail with no ripgrep block is warned about rg's flags:\n%s", reachable(without))
	}
	if strings.Contains(reachable(BriefingInput{Workspace: "/w"}), "`-r`") {
		t.Error("a jail with no block is warned about rg's flags")
	}
}

// coreAnchor opens the core's bullets: what follows the header and any alert. It replaces the
// "## Environment" heading the pre-diet body opened with, as the boundary tests measure from.
const coreAnchor = "- **Workspace**"
