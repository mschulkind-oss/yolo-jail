package packload

// agentaudience_test.go pins P3/OQ-BA3 (briefing-audiences.md §4.3): an `agents` selector may
// name only an agent this pack set HAS, and anything else is a problem with one message.
//
// The severity is the caller's — every one makes it fatal — so these tests are about the
// question and the diagnostic. The call sites are pinned where they live
// (internal/cli/run/packagentname_test.go, internal/cli/applyhostagentname_test.go).

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

// P3's HEADLINE: a typo and a real name belonging to an unselected pack fail IDENTICALLY,
// because from the jail's point of view they are the same mistake with the same two remedies.
func TestAnAgentThisJailDoesNotHaveIsAProblem(t *testing.T) {
	claude := claimPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})

	for _, tc := range []struct{ name, addressed, wantGuess string }{
		{"a typo", "cloude", "claude"},
		{"a real agent nobody selected", "codex", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AgentAudienceProblems([]*Pack{claude, audiencePack(t, "house", tc.addressed)})
			if len(got) != 1 {
				t.Fatalf("want one problem, got %d: %v", len(got), got)
			}
			for _, want := range []string{
				tc.addressed,                          // the offending string (R3)
				"pack house",                          // the declaring pack (R3)
				"Agents your `packs` provide: claude", // the candidate list (R3)
				"Fix the name, or add the pack",
			} {
				if !strings.Contains(got[0], want) {
					t.Errorf("message missing %q; got:\n%s", want, got[0])
				}
			}
			if tc.wantGuess != "" && !strings.Contains(got[0], "did you mean \""+tc.wantGuess+"\"") {
				t.Errorf("a near miss must carry a did-you-mean (R3); got:\n%s", got[0])
			}
		})
	}
}

// The HAPPY PATH, and the one that decides whether the gate is usable at all: a selector
// naming an agent the set provides is silent.
func TestAnEnabledAgentIsNoProblem(t *testing.T) {
	claude := claimPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})
	if got := AgentAudienceProblems([]*Pack{claude, audiencePack(t, "house", "claude")}); len(got) != 0 {
		t.Errorf("addressing an enabled agent must be silent; got %v", got)
	}
}

// P2: NO SELECTOR IS NO PROBLEM. A pack that names no audience — every shipped pack, and the
// zero-ceremony pack that has no manifest to name one in — cannot fail this gate.
func TestNoSelectorIsNoProblem(t *testing.T) {
	packs := []*Pack{
		agentPack(t, "house", packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".x/y.md"}),
		zeroCeremonyPack(t, "silent", true, true),
	}
	if got := AgentAudienceProblems(packs); len(got) != 0 {
		t.Errorf("a pack naming no audience must never fail the audience gate; got %v", got)
	}
}

// THE SPLIT THAT KEEPS P3 AND R1 ONE RULE. A name whose pack IS here but which declares no
// destination of THIS kind is NOT a problem for this gate — the remedy is an `agent` on the
// owning pack's contribution of that kind, so refusing the launch would punish the wrong
// author (R4). It is reported instead, through the resolution outcome.
func TestAnEnabledNameWithNoDestinationOfThatKindIsNotRefusedHere(t *testing.T) {
	// claude is here and owns the name, but declares an identity only on its BRIEFING.
	claude := claimPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})
	house := agentPack(t, "house", packdecl.Contribution{
		Kind: packdecl.KindSkills, From: "skills", Agents: []string{"claude"}})

	if got := AgentAudienceProblems([]*Pack{claude, house}); len(got) != 0 {
		t.Fatalf("the NAME is enabled, so this gate must stay silent and let the resolution "+
			"report the missing destination (R1); got %v", got)
	}
}

// A pack set with NO agents at all gets its own sentence, because "Agents your `packs`
// provide: " followed by nothing sends the reader looking for a list that is not there.
func TestAPackSetWithNoAgentsSaysSo(t *testing.T) {
	got := AgentAudienceProblems([]*Pack{audiencePack(t, "house", "claude")})
	if len(got) != 1 {
		t.Fatalf("want one problem, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "NO agents at all") {
		t.Errorf("an agentless pack set needs its own wording; got:\n%s", got[0])
	}
	if !strings.Contains(got[0], "drop the `agents` selector") {
		t.Errorf("the message must offer the remedy that does not require another pack; got:\n%s",
			got[0])
	}
}

// AgentNames IS the candidate list, drawn from the SELECTED packs and nothing wider — which is
// the whole content of P3, and the reason config.UseProfileCLINames is the wrong source (it
// unions packload.Embedded(), which is deliberately not selection-gated).
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

// One message per mistake, deterministically ordered: the refusal is user-visible text.
func TestAudienceProblemsAreDeterministicAndDeduplicated(t *testing.T) {
	house := agentPack(t, "house",
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "a.md", Agents: []string{"zulu"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, From: "b.md", Agents: []string{"zulu"}},
		packdecl.Contribution{Kind: packdecl.KindSkills, From: "s", Agents: []string{"alfa"}})
	first := AgentAudienceProblems([]*Pack{house})
	if len(first) != 2 {
		t.Fatalf("want 2 problems (one per kind+name, deduplicated), got %d: %v", len(first), first)
	}
	if !strings.Contains(first[0], "briefing") || !strings.Contains(first[1], "skills") {
		t.Errorf("problems must be ordered by pack then kind then name; got:\n%v", first)
	}
	for i := 0; i < 5; i++ {
		if again := AgentAudienceProblems([]*Pack{house}); strings.Join(again, "\n") !=
			strings.Join(first, "\n") {
			t.Fatalf("unstable output across runs:\n%v\nvs\n%v", first, again)
		}
	}
}

// EVERY SHIPPED PACK together must pass, or the pre-flight refuses every launch of a stock
// config. None of them addresses an audience today, which is exactly what makes the field safe
// to have landed ahead of adoption (P2) — so this is a real assertion about the shipped set,
// not a tautology about an empty list.
func TestShippedPacksNameNoUnknownAudience(t *testing.T) {
	packs := Embedded()
	if len(packs) == 0 {
		t.Fatal("no embedded packs — this test is not testing anything")
	}
	if got := AgentAudienceProblems(packs); len(got) != 0 {
		t.Fatalf("the packs yolo SHIPS fail the audience gate, so the eighth launch pre-flight "+
			"refuses a stock config: %v", got)
	}
}
