package packload

import (
	"slices"
	"strings"
	"testing"
)

// Agents that share one reason for being unreached share ONE sentence of it (2026-11-24: a bare
// profile over nine programs printed the same four-hundred-character reason nine times): the
// line names them all, explains once, and gives ONE next step for the group.
func TestUnreachedAgentsSharingAReasonSayItOnce(t *testing.T) {
	names := []string{"claude", "copilot", "agy", "bedrock", "aws-auth"}
	table := map[string]string{"copilot": "bedrock", "agy": "bedrock"}
	packs := embeddedNamed(t, names...)
	providers, resolved, _ := launchSelection(t, packs, nil, nil, table)
	var asked [][]string
	got := ProfileDisclosures(ProfileDisclosureInput{Table: table, Packs: packs, Resolved: resolved, Providers: providers,
		Deselect: func(agent, _ string) string { return "single-" + agent },
		DeselectAll: func(agents []string, _ string) string {
			asked = append(asked, slices.Clone(agents))
			return "the selection is one place, so write all of " + strings.Join(agents, ", ") + " there"
		}})
	if len(got) != 1 {
		t.Fatalf("disclosures = %+v", got)
	}
	warnings := got[0].Warnings()
	if len(warnings) != 1 {
		t.Fatalf("want one warning line, got %q", warnings)
	}
	line := warnings[0]
	if n := strings.Count(line, "names no endpoint, only platform"); n != 1 {
		t.Errorf("the shared reason appears %d times, want once:\n%s", n, line)
	}
	for _, want := range []string{`Warning: profile "bedrock" reaches nothing for agy and copilot: `,
		"-p <program>=<name>", "write all of agy, copilot there"} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q:\n%s", want, line)
		}
	}
	if strings.Contains(line, "single-") {
		t.Errorf("the group used the per-agent clause:\n%s", line)
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []string{"agy", "copilot"}) {
		t.Errorf("DeselectAll asked %v, want once for agy and copilot", asked)
	}
}

// With no batch clause the group still names every deselecting pair, once.
func TestAGroupWithNoBatchClauseListsTheDeselectingPairs(t *testing.T) {
	names := []string{"claude", "copilot", "agy", "bedrock", "aws-auth"}
	table := map[string]string{"copilot": "bedrock", "agy": "bedrock"}
	packs := embeddedNamed(t, names...)
	providers, resolved, _ := launchSelection(t, packs, nil, nil, table)
	got := ProfileDisclosures(ProfileDisclosureInput{Table: table, Packs: packs, Resolved: resolved, Providers: providers})
	line := got[0].Warnings()[0]
	if !strings.Contains(line, "`-p agy= -p copilot=`") {
		t.Errorf("line lacks the deselecting pairs:\n%s", line)
	}
}

// One unreached agent keeps its own single-agent sentence, whole.
func TestOneUnreachedAgentKeepsItsOwnSentence(t *testing.T) {
	names := []string{"claude", "copilot", "bedrock", "aws-auth"}
	table := map[string]string{"copilot": "bedrock"}
	packs := embeddedNamed(t, names...)
	providers, resolved, _ := launchSelection(t, packs, nil, nil, table)
	got := ProfileDisclosures(ProfileDisclosureInput{Table: table, Packs: packs, Resolved: resolved, Providers: providers,
		Deselect: func(agent, _ string) string { return "single-" + agent }})
	line := got[0].Warnings()[0]
	for _, want := range []string{`reaches nothing for copilot: provider "bedrock" names no endpoint`, "copilot's own client",
		"(`-p copilot=<name>`), or none for copilot: single-copilot"} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q:\n%s", want, line)
		}
	}
}
