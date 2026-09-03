package packload

// agentnamecollisions_test.go pins OQ-BA6/BA7 (briefing-audiences.md §4.2): an agent NAME has
// exactly one owning pack, across every kind that claims one.
//
// The pass exists because the generic exclusive loop cannot see the claim — two of the four
// claiming kinds merge by design, and a cross-KIND clash is two different `(kind, target)`
// keys even for the two that do not. Both of those are pinned here as NEGATIVES
// (TestGenericCollisionsCannotSeeAnAgentNameClash), so a later "simplification" back into
// Collisions fails in this file rather than in a user's home.

import (
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// claimPack is a pack whose whole content is the contributions given — no tree, since every
// claim this pass reads is a manifest string.
func claimPack(t *testing.T, name string, contributes ...packdecl.Contribution) *Pack {
	t.Helper()
	return agentPack(t, name, contributes...)
}

// collidedNames is the sorted set of names AgentNameCollisions reported.
func collidedNames(cols []Collision) []string {
	var out []string
	for _, c := range cols {
		out = append(out, c.Target)
	}
	sort.Strings(out)
	return out
}

// THE HEADLINE RULE, in the shape §4.2's own example takes: `claude-official` installs the
// binary, `claude-matt-fork` declares where claude reads. Two different KINDS, one name, and
// the generic loop is blind to it.
func TestTwoPacksClaimingOneAgentNameAcrossKindsIsACollision(t *testing.T) {
	official := claimPack(t, "claude-official",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"})
	fork := claimPack(t, "claude-matt-fork",
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})

	cols := AgentNameCollisions([]*Pack{official, fork})
	if len(cols) != 1 {
		t.Fatalf("want exactly one collision on `claude`, got %d: %+v", len(cols), cols)
	}
	c := cols[0]
	if c.Target != "claude" || c.Kind != AgentNameClaimKind {
		t.Errorf("collision = {kind:%q target:%q}, want {kind:%q target:\"claude\"}",
			c.Kind, c.Target, AgentNameClaimKind)
	}
	if got := strings.Join(c.Packs, ","); got != "claude-matt-fork,claude-official" {
		t.Errorf("Packs = %q, want both claimants sorted", got)
	}
	for _, want := range []string{
		"claude-official (program)",         // which pack, through which field
		"claude-matt-fork (briefing.agent)", // ditto, and the other field
		"exactly ONE owning pack",           // the rule
		"-p claude=<profile>",               // one consumer of the name
		"agents: [\"claude\"]",              // the other
		"cannot both be the claude pack",    // the remedy
	} {
		if !strings.Contains(c.Reason, want) {
			t.Errorf("reason missing %q — an author hitting this has four kinds and two\n"+
				"manifests to search; got:\n%s", want, c.Reason)
		}
	}
}

// SAME KIND, both non-exclusive: two packs each declaring `agent: "claude"` on a `briefing`.
// `briefing` is CombineConcat, so the generic loop skips the group entirely.
func TestTwoPacksDeclaringOneAgentIdentityIsACollision(t *testing.T) {
	a := claimPack(t, "alpha",
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})
	b := claimPack(t, "beta",
		packdecl.Contribution{Kind: packdecl.KindSkills, Into: ".claude/skills", Agent: "claude"})
	if got := collidedNames(AgentNameCollisions([]*Pack{a, b})); len(got) != 1 || got[0] != "claude" {
		t.Fatalf("names = %v, want [claude]: a `briefing` identity and a `skills` identity are "+
			"claims on ONE name (P5 — one owner provides all of that agent's plumbing)", got)
	}
}

// ONE PACK claiming its own name in several kinds is one pack owning one name — §4.2 names
// packs/copilot, which declares `copilot` on both `program` and `launch`.
func TestOnePackClaimingItsOwnNameInFourKindsIsLegal(t *testing.T) {
	solo := claimPack(t, "copilot",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "copilot", Via: "npm", Package: "x"},
		packdecl.Contribution{Kind: packdecl.KindLaunch, Bin: "copilot", Flags: []string{"--yolo"}},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".copilot/x.md", Agent: "copilot"},
		packdecl.Contribution{Kind: packdecl.KindSkills, Into: ".copilot/skills", Agent: "copilot"},
	)
	if cols := AgentNameCollisions([]*Pack{solo}); len(cols) != 0 {
		t.Fatalf("one pack owning one name through four kinds must be legal; got %+v", cols)
	}
}

// `requires` IS NOT A CLAIM, and this is the reductio that decided it against §4.2's literal
// list of five kinds: docs/examples/claude-fzf-pack declares `requires fzf`, so counting it
// would refuse a launch for two packs that merely need the same tool — and refuse the most
// ordinary shape there is, a content pack asserting `claude` beside the pack that provides it.
// `requires` is CombineShared for exactly this reason.
func TestRequiresIsNotAnAgentNameClaim(t *testing.T) {
	fzfA := claimPack(t, "fzf-pack-a",
		packdecl.Contribution{Kind: packdecl.KindRequires, Bin: "fzf"})
	fzfB := claimPack(t, "fzf-pack-b",
		packdecl.Contribution{Kind: packdecl.KindRequires, Bin: "fzf"})
	if cols := AgentNameCollisions([]*Pack{fzfA, fzfB}); len(cols) != 0 {
		t.Errorf("two packs requiring one tool is what CombineShared MEANS; got %+v", cols)
	}

	needsClaude := claimPack(t, "house-rules",
		packdecl.Contribution{Kind: packdecl.KindRequires, Bin: "claude"})
	claude := claimPack(t, "claude",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"})
	if cols := AgentNameCollisions([]*Pack{needsClaude, claude}); len(cols) != 0 {
		t.Errorf("a content pack asserting the binary the agent pack installs is a dependency, "+
			"not a rival owner; got %+v", cols)
	}

	// And the case P5's "whether it `program`s the binary or `requires` it" is actually about
	// still collides — through the identity it declares, which is where ownership lives.
	fork := claimPack(t, "claude-fork",
		packdecl.Contribution{Kind: packdecl.KindRequires, Bin: "claude"},
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})
	if cols := AgentNameCollisions([]*Pack{fork, claude}); len(cols) != 1 {
		t.Fatalf("a pack that ASSERTS claude's binary and declares where claude reads is a "+
			"second owner and must be refused; got %+v", cols)
	}
}

// A destination that declares NO identity is never a claimant and never an error (R4) — the
// state every pack.json was in before the field existed.
func TestADestinationWithNoIdentityClaimsNothing(t *testing.T) {
	a := claimPack(t, "alpha",
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md"})
	b := claimPack(t, "beta",
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md"})
	if cols := AgentNameCollisions([]*Pack{a, b}); len(cols) != 0 {
		t.Fatalf("two packs concatenating prose at one path is `briefing`'s whole feature, and "+
			"neither declared an identity; got %+v", cols)
	}
}

// TestGenericCollisionsCannotSeeAnAgentNameClash is the measured reason this is its own pass,
// pinned as a negative. If Collisions ever grows the ability, this test says so out loud
// rather than leaving a redundant pass in place — but note the pass would still be needed,
// because Collisions is not consulted at launch.
func TestGenericCollisionsCannotSeeAnAgentNameClash(t *testing.T) {
	a := claimPack(t, "alpha",
		packdecl.Contribution{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "x"})
	b := claimPack(t, "beta",
		packdecl.Contribution{Kind: packdecl.KindBriefing, Into: ".claude/CLAUDE.md", Agent: "claude"})
	for _, c := range Collisions([]*Pack{a, b}) {
		if c.Target == "claude" && c.Kind != packdecl.KindProgram {
			t.Fatalf("Collisions now reports an agent-name clash (%+v). Welcome, but it does "+
				"NOT make this pass redundant: Collisions keys by (kind, target), skips "+
				"non-exclusive kinds, and is never called at launch.", c)
		}
	}
	if got := AgentNameCollisions([]*Pack{a, b}); len(got) != 1 {
		t.Errorf("the pass must catch what Collisions misses; got %+v", got)
	}
}

// EVERY SHIPPED PACK together must be collision-free, or the pre-flight refuses every launch
// of a stock config. This is the assertion that actually protects a user: the six agent packs
// declare `agent` beside `into` and also `program` their own bin, so each one claims its own
// name through two to four kinds at once.
func TestShippedPacksClaimNoAgentNameTwice(t *testing.T) {
	packs := Embedded()
	if len(packs) == 0 {
		t.Fatal("no embedded packs — this test is not testing anything")
	}
	if cols := AgentNameCollisions(packs); len(cols) != 0 {
		t.Fatalf("the packs yolo SHIPS collide on an agent name, so the seventh launch "+
			"pre-flight refuses a stock config: %+v", cols)
	}
}

// Deterministic output: the refusal is user-visible text, and map iteration order would make
// it flap between launches for one unchanged config.
func TestAgentNameCollisionOrderIsDeterministic(t *testing.T) {
	in := []*Pack{
		claimPack(t, "zeta", packdecl.Contribution{
			Kind: packdecl.KindProgram, Bin: "zulu", Via: "npm", Package: "x"}),
		claimPack(t, "alpha", packdecl.Contribution{
			Kind: packdecl.KindBriefing, Into: ".z/x.md", Agent: "zulu"}),
		claimPack(t, "mike", packdecl.Contribution{
			Kind: packdecl.KindProgram, Bin: "alfa", Via: "npm", Package: "x"}),
		claimPack(t, "bravo", packdecl.Contribution{
			Kind: packdecl.KindBriefing, Into: ".a/x.md", Agent: "alfa"}),
	}
	first := AgentNameCollisions(in)
	if len(first) != 2 {
		t.Fatalf("want 2 collisions, got %d: %+v", len(first), first)
	}
	if first[0].Target != "alfa" || first[1].Target != "zulu" {
		t.Errorf("collisions must be ordered by name; got %s then %s", first[0].Target, first[1].Target)
	}
	if strings.Index(first[1].Reason, "pack alpha") > strings.Index(first[1].Reason, "pack zeta") {
		t.Errorf("claimants within one collision must be sorted; got:\n%s", first[1].Reason)
	}
	for i := 0; i < 5; i++ {
		again := AgentNameCollisions(in)
		if len(again) != len(first) {
			t.Fatalf("unstable count across runs: %d vs %d", len(again), len(first))
		}
		for j := range again {
			if again[j].Target != first[j].Target || again[j].Reason != first[j].Reason {
				t.Fatalf("unstable output across runs:\n%+v\nvs\n%+v", first, again)
			}
		}
	}
}
