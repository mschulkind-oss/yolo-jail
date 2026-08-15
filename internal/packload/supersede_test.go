package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// bedrockDecl is the design's own example: a pack that supersedes the capability the
// bundled broker serves, and ships nothing else.
func bedrockDecl() *packdecl.Manifest {
	return &packdecl.Manifest{Supersedes: []packdecl.Supersession{{
		Capability: "claude-oauth-refresh",
		Because:    "Bedrock overrides the OAuth path; no token is ever refreshed",
	}}}
}

// TestSupersedesAppearsInTheFootprint is R6: a supersession is a claim about the
// ENVIRONMENT ("this job will not be done here"), which is exactly what the footprint
// enumerates — and it is the only way a reader learns, BEFORE selecting a pack, that
// it will retire a loophole they rely on.
//
// The `because` rides in the Detail so the justification travels with the consequence
// on this surface too, exactly as it does in `yolo loopholes list`.
func TestSupersedesAppearsInTheFootprint(t *testing.T) {
	cs := claimSet(FootprintOf(pk("claude-bedrock", true, bedrockDecl())))
	c, ok := cs["supersedes claude-oauth-refresh"]
	if !ok {
		t.Fatalf("no supersedes claim in the footprint: %v", cs)
	}
	if !strings.Contains(c.Detail, "no token is ever refreshed") {
		t.Errorf("Detail = %q, want the pack author's own `because`", c.Detail)
	}
	if !strings.Contains(c.Detail, "retires the loophole") {
		t.Errorf("Detail = %q does not say what the claim DOES", c.Detail)
	}
}

// TestSupersedesIsNotReviewWorthy: both markers mean a claim WIDENS what the pack may
// do to your machine (a host read, an argv it will run). Supersession narrows —
// nothing is granted, so there is nothing to flag. The line still prints
// unconditionally, because every claim does.
func TestSupersedesIsNotReviewWorthy(t *testing.T) {
	c := claimSet(FootprintOf(pk("claude-bedrock", true, bedrockDecl())))["supersedes claude-oauth-refresh"]
	if c.ReviewWorthy || c.RunsHostCode {
		t.Errorf("supersedes claim flagged ReviewWorthy=%v RunsHostCode=%v; it grants nothing",
			c.ReviewWorthy, c.RunsHostCode)
	}
	if len(ReviewWorthy([]*Pack{pk("claude-bedrock", true, bedrockDecl())})) != 0 {
		t.Error("a pack whose only declaration is a supersession has a review summary")
	}
}

// TestSupersedesIsNotAHostAccessClaim is the R6 decision, pinned.
//
// HostAccessClaims is the APPROVAL/LOCKFILE key set: every string in it grants the
// pack something it may not otherwise have, and it is compared for exact equality at
// `yolo pack install` and again at launch. Supersession belongs to neither end of
// that — see packload/supersede.go for the four reasons. The consequence worth
// pinning: a pack whose ONLY declaration is a supersession asks nothing of the host,
// so it needs no approval and cannot be refused for lacking one.
func TestSupersedesIsNotAHostAccessClaim(t *testing.T) {
	p := pk("claude-bedrock", false, bedrockDecl())
	if claims := p.HostAccessClaims(); len(claims) != 0 {
		t.Errorf("HostAccessClaims() = %v; a supersession is not something to approve — "+
			"it relinquishes rather than grants, and keying an approval on it would be "+
			"either content-blind (capability alone) or endlessly re-prompting (with the "+
			"`because`, which an author may reword)", claims)
	}
	if probs := p.Decl.NeedsHostAccess(); len(probs) != 0 {
		t.Errorf("NeedsHostAccess() = %v for a pack that only supersedes", probs)
	}
}

// TestTwoPacksSupersedingOneCapabilityIsNotACollision: design §5 says any supersession
// wins and there is deliberately no `needs`, so two packs retiring one job is the
// mechanism working. Achieved by the SHAPE — SupersedesClaimKind is not in the closed
// kind registry, so the exclusive-target pass skips it — rather than by a special case
// in Collisions.
func TestTwoPacksSupersedingOneCapabilityIsNotACollision(t *testing.T) {
	a := pk("pack-a", true, bedrockDecl())
	b := pk("pack-b", true, bedrockDecl())
	if cols := Collisions([]*Pack{a, b}); len(cols) != 0 {
		t.Errorf("Collisions = %+v; two packs superseding one capability is the design's "+
			"stated behaviour, not a conflict", cols)
	}
}

// TestSupersedesClaimKindIsNotAContributionKind pins the shape the two properties
// above depend on. If somebody registers "supersedes" in packdecl's kind set, both the
// no-collision result and the four per-kind exhaustiveness tests change meaning
// silently — so it fails here first.
func TestSupersedesClaimKindIsNotAContributionKind(t *testing.T) {
	if packdecl.KnownKind(SupersedesClaimKind) {
		t.Fatal("SupersedesClaimKind is now a registered contribution kind — it is a " +
			"display label; registering it makes Collisions treat two superseders as a " +
			"conflict and pulls it into four per-kind exhaustiveness tests")
	}
}

// TestCapabilityNameRulesAgree pins the DUPLICATED rule.
//
// `serves` is validated by internal/loopholedecl and `supersedes` by internal/packdecl,
// and packdecl may not import anything (zero internal imports by design), so the rule
// is written twice. Both ends of a rendezvous have to agree about what a name IS: a
// name one side accepts and the other refuses is a capability that can be served and
// never superseded, or claimed and never matched. This package imports both, so the
// duplication drifting is a test failure rather than a silent mismatch.
func TestCapabilityNameRulesAgree(t *testing.T) {
	for _, name := range []string{
		"", "claude-oauth-refresh", "audio.playback", "a_b/c-2", "x",
		"has space", "has\ttab", "trailing ", "\nlead", "line\nbreak", "esc\x1b[2K",
		"\u00a0nbsp", "\u009b-c1", "unicode-\u00e9",
	} {
		lp := loopholedecl.CapabilityNameProblem(name)
		pd := packdecl.CapabilityNameProblem(name)
		if (lp == "") != (pd == "") {
			t.Errorf("%q: loopholedecl says %q, packdecl says %q — the two ends of the "+
				"rendezvous disagree about whether this is a legal capability name",
				name, lp, pd)
		}
		if lp != pd {
			t.Errorf("%q: the two rules give different explanations (%q vs %q); they are "+
				"deliberate mirrors, so the messages must match too", name, lp, pd)
		}
	}
}

// TestPackSupersessionsAccessorIsNilSafe: the run pipeline calls this on every loaded
// pack, and a pack with no manifest at all is the zero-ceremony case.
func TestPackSupersessionsAccessorIsNilSafe(t *testing.T) {
	if got := (&Pack{Name: "p"}).Supersessions(); got != nil {
		t.Errorf("Supersessions() = %v for a pack with no manifest", got)
	}
	if got := (*Pack)(nil).Supersessions(); got != nil {
		t.Errorf("Supersessions() = %v for a nil pack", got)
	}
	if got := pk("p", true, &packdecl.Manifest{}).Supersessions(); len(got) != 0 {
		t.Errorf("Supersessions() = %v for a manifest that declares none", got)
	}
}
