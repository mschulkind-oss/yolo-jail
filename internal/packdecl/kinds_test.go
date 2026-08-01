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
		KindProgram, KindSkills, KindBriefing, KindFiles, KindConfig,
		KindConfigOverlay, KindState, KindReadsHost, KindMount, KindEnv,
		KindLaunch, KindHook, KindAutonomy,
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
	if got := len(KnownKinds()); got != 13 {
		t.Errorf("KnownKinds() has %d entries, want 13 — a kind was added/removed without updating the test", got)
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
		KindEnv:           CombineMerge,
		KindState:         CombineScoped,
		KindHook:          CombinePerHook,
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
	worthy := map[Kind]bool{KindProgram: true, KindState: true, KindReadsHost: true, KindMount: true}
	for _, k := range KnownKinds() {
		fp, _ := FootprintOf(k)
		if fp.MayBeReviewWorthy != worthy[k] {
			t.Errorf("kind %q MayBeReviewWorthy = %v, want %v", k, fp.MayBeReviewWorthy, worthy[k])
		}
	}
}
