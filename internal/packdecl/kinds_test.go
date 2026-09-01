package packdecl

import (
	"strings"
	"testing"
)

// The kind set is closed and every kind has a footprint. If a kind is added to
// the const block but not to the footprints map (or vice versa), one of these
// fails — the drift guard the closed-set pattern exists to provide.
func TestKnownKindsCoverEveryConstant(t *testing.T) {
	// Every declared Kind constant must be registered.
	for _, k := range []Kind{
		KindProgram, KindRequires, KindSkills, KindBriefing, KindFiles, KindConfig,
		KindConfigOverlay, KindState, KindReadsHost, KindMount, KindEnv,
		KindLaunch, KindHook, KindAutonomy, KindProfile, KindProvider, KindLoophole,
	} {
		fp, ok := FootprintOf(k)
		if !ok {
			t.Errorf("kind %q has no footprint descriptor", k)
			continue
		}
		if fp.Kind != k {
			t.Errorf("footprint for %q has Kind=%q (self-mismatch)", k, fp.Kind)
		}
		if fp.Claims == "" {
			t.Errorf("kind %q has an empty Claims description", k)
		}
	}
	if got := len(KnownKinds()); got != 17 {
		t.Errorf("KnownKinds() has %d entries, want 17 — a kind was added/removed without updating the test", got)
	}
}

// KnownKinds is sorted and deterministic (error messages depend on it).
func TestKnownKindsSorted(t *testing.T) {
	ks := KnownKinds()
	for i := 1; i < len(ks); i++ {
		if ks[i-1] >= ks[i] {
			t.Fatalf("KnownKinds() not sorted: %q before %q", ks[i-1], ks[i])
		}
	}
}

// ValidateKind is silent on a known kind and produces the standard "unknown …
// (expected …)" diagnostic on an unknown one, listing the real set.
func TestValidateKind(t *testing.T) {
	if msg := ValidateKind(KindConfig); msg != "" {
		t.Errorf("ValidateKind(config) = %q, want empty (it is known)", msg)
	}
	msg := ValidateKind(Kind("mcp-server"))
	if msg == "" {
		t.Fatal("ValidateKind(mcp-server) was silent — an unknown kind must be a loud error")
	}
	if !strings.Contains(msg, `unknown kind "mcp-server"`) {
		t.Errorf("diagnostic missing the offending kind: %q", msg)
	}
	// It must enumerate the real set so an author can fix the typo.
	for _, want := range []string{"config", "config-overlay", "reads-host", "skills"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic %q does not mention known kind %q", msg, want)
		}
	}
}

// The one-writer rule (§3.6) as a data invariant: the exclusive-combine kinds
// are the sole-owned files, and the others are the neutral-owner-combined ones.
// This pins the mapping the §3.2 conflict table encodes so a wrong Combine on a
// kind is caught.
func TestCombineRulesMatchDesign(t *testing.T) {
	want := map[Kind]Combine{
		KindProgram:       CombineExclusive,
		KindFiles:         CombineExclusive,
		KindConfig:        CombineExclusive,
		KindLaunch:        CombineExclusive,
		KindSkills:        CombineMerge,
		KindBriefing:      CombineConcat,
		KindConfigOverlay: CombineOverlay,
		KindReadsHost:     CombineShared,
		KindMount:         CombineShared,
		// requires is SHARED, not exclusive: many packs may require one binary, and none
		// owns a path for it. That is the difference from program, which is exclusive
		// precisely because it owns a launcher filename.
		KindRequires: CombineShared,
		KindEnv:      CombineMerge,
		KindState:    CombineScoped,
		KindHook:     CombinePerHook,
		// loophole is EXCLUSIVE by loophole NAME (the module dir's basename), the same rule
		// program has per bin — so one pack shipping three loopholes is ordinary and two
		// packs shipping one name is not. Name-keyed exclusivity needs its own pass
		// (packload.loopholeNameCollisions), because the claim TARGETS carry a
		// discriminator and the generic loop compares those.
		KindLoophole: CombineExclusive,
		// provider is EXCLUSIVE by provider NAME — the target IS the name, with no
		// discriminator, so unlike loophole it needs no pass of its own: the generic
		// exclusive loop compares two packs' provider names directly. Written here even
		// though CombineExclusive is the zero value, because a row that is absent from
		// this map is a combine rule nobody pinned (the reason autonomy went unpinned for
		// so long).
		KindProvider: CombineExclusive,
		// profile is EXCLUSIVE by (pack, name) — §3.4's stated rule, and the one kind whose
		// claim target carries the pack on purpose: a profile name is NOT owned across
		// packs (two packs both answering to "bedrock" is the unrelated-coincidence case),
		// so the pack prefix is what keeps the generic exclusive loop from firing. Written
		// here although Exclusive is the zero value, because a row absent from this map is
		// a combine rule nobody pinned.
		KindProfile: CombineExclusive,
	}
	for k, c := range want {
		fp, _ := FootprintOf(k)
		if fp.Combine != c {
			t.Errorf("kind %q Combine = %d, want %d", k, fp.Combine, c)
		}
	}
}

// Only the kinds that can produce a review-worthy claim are marked so — the set
// --footprint looks at for machine-scope state, host reads, and installer programs.
func TestReviewWorthyKinds(t *testing.T) {
	worthy := map[Kind]bool{
		KindProgram: true, KindState: true, KindReadsHost: true, KindMount: true,
		// loophole is the only kind review-worthy in EVERY instance, not just some: its
		// claims are enumerated one per boundary CROSSING (loophole-packaging.md §3.3), so a
		// loophole claim that needed no review would be a contradiction. It is also the
		// first kind whose crossing is host code EXECUTION rather than a host read — a
		// distinction ReviewWorthy's single boolean cannot carry, so the claim's Detail
		// spells out "RUNS … on your machine".
		KindLoophole: true,
	}
	for _, k := range KnownKinds() {
		fp, _ := FootprintOf(k)
		if fp.MayBeReviewWorthy != worthy[k] {
			t.Errorf("kind %q MayBeReviewWorthy = %v, want %v", k, fp.MayBeReviewWorthy, worthy[k])
		}
	}
}
