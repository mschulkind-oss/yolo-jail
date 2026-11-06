package packload

// agentaudience_test.go pins FS-D6 (docs/design/pack-files-for-agent-state.md#FS-D6, §4.2): an
// `agents` selector naming an agent no SELECTED pack provides is SKIPPED, never refused, and
// each skip is one line naming the pack, the kind and the agent as written, with `yolo pack
// add <pack>` as the next step when a pack yolo ships provides that agent.
//
// The call sites are pinned where they live (internal/cli/run/packagentaudience_test.go,
// internal/cli/applyhostaudience_test.go, internal/cli/check/packaudiences_test.go).

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// audiencePack is a content pack whose briefing addresses `agents`.
func audiencePack(t *testing.T, name string, agents ...string) *Pack {
	t.Helper()
	return agentPack(t, name, packdecl.Contribution{
		Kind: packdecl.KindBriefing, From: "prose.md", Agents: agents})
}

func claudeClaimPack(t *testing.T) *Pack {
	t.Helper()
	return claimPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})
}

// FS-D6's HEADLINE: a typo and a real agent nobody selected are skipped IDENTICALLY, because
// yolo cannot know every pack a user could select. Only the next step differs: a shipped pack
// that provides the name is named; any other name stands alone, as written.
func TestAnAgentNoSelectedPackProvidesIsSkipped(t *testing.T) {
	codex := claimPack(t, "codex",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "codex", Via: "npm", Package: "x"})
	shipped := []*Pack{claudeClaimPack(t), codex}

	for _, tc := range []struct{ name, addressed, wantNext string }{
		{"a real agent nobody selected", "codex", "yolo pack add codex"},
		{"a misspelled name", "cloude", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AgentAudienceSkips(
				[]*Pack{claudeClaimPack(t), audiencePack(t, "house", tc.addressed)}, shipped)
			if len(got) != 1 {
				t.Fatalf("want one skip, got %d: %+v", len(got), got)
			}
			s := got[0]
			if s.Pack != "house" || s.Kind != packdecl.KindBriefing || s.Agent != tc.addressed {
				t.Errorf("skip = %+v, want pack house, kind briefing, agent %q", s, tc.addressed)
			}
			want := "pack house: briefing for " + tc.addressed + " not delivered — no selected " +
				"pack provides " + tc.addressed
			if s.Line() != want {
				t.Errorf("Line() = %q\nwant      %q", s.Line(), want)
			}
			if tc.wantNext == "" {
				if s.NextStep() != "" {
					t.Errorf("a name no shipped pack provides must carry no next step (FS-D6: "+
						"yolo cannot guess the pack); got %q", s.NextStep())
				}
				return
			}
			if !strings.Contains(s.NextStep(), tc.wantNext) {
				t.Errorf("NextStep() = %q, want it to name %q", s.NextStep(), tc.wantNext)
			}
		})
	}
}

// The HAPPY PATH: a selector naming an agent the selected set provides is no skip.
func TestAnEnabledAgentIsNotSkipped(t *testing.T) {
	if got := AgentAudienceSkips(
		[]*Pack{claudeClaimPack(t), audiencePack(t, "house", "claude")}, nil); len(got) != 0 {
		t.Errorf("addressing an enabled agent must not be skipped; got %+v", got)
	}
}

// A contribution addressed to several agents is skipped only for the names nobody selected:
// the selected ones still receive it.
func TestOnlyTheUnselectedNamesOfAnAudienceAreSkipped(t *testing.T) {
	got := AgentAudienceSkips(
		[]*Pack{claudeClaimPack(t), audiencePack(t, "house", "claude", "agy")}, nil)
	if len(got) != 1 || got[0].Agent != "agy" {
		t.Errorf("want one skip for agy alone; got %+v", got)
	}
}

// NO SELECTOR IS NO SKIP. A pack that names no audience — every shipped pack, and the
// zero-ceremony pack that has no manifest to name one in — has nothing to skip.
func TestNoSelectorIsNoSkip(t *testing.T) {
	packs := []*Pack{
		agentPack(t, "house", packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".x/y.md"}),
		zeroCeremonyPack(t, "silent", true, true),
	}
	if got := AgentAudienceSkips(packs, nil); len(got) != 0 {
		t.Errorf("a pack naming no audience must never be skipped; got %+v", got)
	}
}

// THE SPLIT THAT KEEPS FS-D6 AND R1 APART. A name whose pack IS selected but which declares no
// destination of THIS kind is not a skip — the remedy is an `agent` on the owning pack's
// contribution of that kind, reported through the resolution outcome (R1).
func TestAnEnabledNameWithNoDestinationOfThatKindIsNotSkipped(t *testing.T) {
	house := agentPack(t, "house", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Agents: []string{"claude"}})
	if got := AgentAudienceSkips([]*Pack{claudeClaimPack(t), house}, nil); len(got) != 0 {
		t.Fatalf("the NAME is selected, so this is R1's report, not a skip; got %+v", got)
	}
}

// AgentNames IS the selected vocabulary, drawn from the SELECTED packs and nothing wider.
func TestAgentNamesIsTheSelectedSetsClaims(t *testing.T) {
	packs := []*Pack{
		claimPack(t, "a", packdecl.Contribution{
			Kind: packdecl.KindProgram, Bin: "acli", Via: "npm", Package: "x"}),
		claimPack(t, "b", packdecl.Contribution{
			Kind: packdecl.KindBriefing, Into: ".b/x.md", Agent: "bcli"}),
		// Declares no identity — unaddressable, and not a name (R4).
		agentPack(t, "c", packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".c/x.md"}),
	}
	if got := strings.Join(AgentNames(packs), ","); got != "acli,bcli" {
		t.Errorf("AgentNames = %q, want \"acli,bcli\": a claim through `program` and one through "+
			"a briefing's `agent` are both names, and a destination declaring none is not", got)
	}
	if got := AgentNames(nil); len(got) != 0 {
		t.Errorf("no packs must mean no names, not a fallback to what yolo ships; got %v", got)
	}
}

// One skip per (pack, kind, agent), deterministically ordered: the lines are user-visible.
func TestAudienceSkipsAreDeterministicAndDeduplicated(t *testing.T) {
	house := agentPack(t, "house",
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "a.md", Agents: []string{"zulu"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "b.md", Agents: []string{"zulu"}},
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "s", Agents: []string{"alfa"}})
	first := AgentAudienceSkips([]*Pack{house}, nil)
	if len(first) != 2 {
		t.Fatalf("want 2 skips (one per kind+name, deduplicated), got %d: %+v", len(first), first)
	}
	if first[0].Kind != packdecl.KindBriefing || first[1].Kind != packdecl.KindSkills {
		t.Errorf("skips must be ordered by pack then kind then name; got %+v", first)
	}
	for i := 0; i < 5; i++ {
		again := AgentAudienceSkips([]*Pack{house}, nil)
		if len(again) != len(first) || again[0] != first[0] || again[1] != first[1] {
			t.Fatalf("unstable output across runs:\n%+v\nvs\n%+v", first, again)
		}
	}
}

// The shipped set is read for ADVICE only: a shipped agent pack is named as the next step, and
// that is the shipped pack's own name, as `yolo pack add` takes it.
func TestShippedProviderIsNamedFromTheShippedSet(t *testing.T) {
	shipped := Embedded()
	if len(shipped) == 0 {
		t.Fatal("no embedded packs — this test is not testing anything")
	}
	got := AgentAudienceSkips([]*Pack{audiencePack(t, "house", "claude")}, shipped)
	if len(got) != 1 {
		t.Fatalf("want one skip, got %+v", got)
	}
	if got[0].Provider != "claude" || got[0].NextStep() != "If you want it here:  yolo pack add claude" {
		t.Errorf("the shipped claude pack must be named as the next step; got %+v / %q",
			got[0], got[0].NextStep())
	}
}

// EVERY SHIPPED PACK together addresses no agent outside the shipped set, so a stock config
// that selects them all skips nothing.
func TestShippedPacksSkipNothingTogether(t *testing.T) {
	packs := Embedded()
	if len(packs) == 0 {
		t.Fatal("no embedded packs — this test is not testing anything")
	}
	if got := AgentAudienceSkips(packs, packs); len(got) != 0 {
		t.Fatalf("the packs yolo SHIPS address an agent none of them provides: %+v", got)
	}
}
