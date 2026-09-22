package packdecl

// nodefloor_test.go covers the `node_floor` field's semantics
// (docs/design/agent-program-runtimes.md, OQ-AR1 and OQ-AR4).

import (
	"strings"
	"testing"
)

// THE CASE THE WHOLE FUNCTION EXISTS FOR. Lexically "20.20.2" > "22.19", because '0' < '2' at the
// second character — so a strings.Compare would accept Node 20 against a floor of 22.19, which is
// precisely the failure this design was written to prevent, reached by the cheapest implementation.
func TestFloorRejectsNode20ForA2219Floor(t *testing.T) {
	if SatisfiesNodeFloor("20.20.2", "22.19") {
		t.Fatal("20.20.2 satisfied a floor of 22.19 — this is the lexical-compare bug, and it is " +
			"the one thing this comparison must never do")
	}
	// The measured trio from the design's §1 table.
	for _, ok := range []string{"22.23.2", "24.19.0"} {
		if !SatisfiesNodeFloor(ok, "22.19") {
			t.Errorf("%s must satisfy 22.19", ok)
		}
	}
}

// A NEWER Node satisfies an older floor, which is what lets the image move forward without a
// manifest edit — the stated reason the value is a floor rather than a selector or a pin.
func TestANewerNodeSatisfiesAnOlderFloor(t *testing.T) {
	for _, tc := range []struct{ cand, floor string }{
		{"24.19.0", "22"}, {"22.19.0", "22.19"}, {"22.19.1", "22.19"}, {"v24.19.0", "22.19"},
	} {
		if !SatisfiesNodeFloor(tc.cand, tc.floor) {
			t.Errorf("%s should satisfy %s", tc.cand, tc.floor)
		}
	}
	for _, tc := range []struct{ cand, floor string }{
		{"22.18.9", "22.19"}, {"21.0.0", "22"}, {"22.19.0", "22.19.1"},
	} {
		if SatisfiesNodeFloor(tc.cand, tc.floor) {
			t.Errorf("%s must NOT satisfy %s", tc.cand, tc.floor)
		}
	}
}

// A short floor is not padded at declaration; a missing part counts as zero at comparison, which is
// what makes "22.19" mean "22.19.0 or newer" without the schema inventing digits.
func TestAMissingPartIsZero(t *testing.T) {
	if CompareVersions("22", "22.0.0") != 0 {
		t.Error(`"22" and "22.0.0" must compare equal`)
	}
	if CompareVersions("22.19", "22.19.1") != -1 {
		t.Error(`"22.19" must be less than "22.19.1"`)
	}
}

// An empty floor is satisfied by anything: a program that declares nothing keeps today's behaviour
// byte-for-byte, and opencode-ai's native ELF depends on that.
func TestNoFloorIsAlwaysSatisfied(t *testing.T) {
	if !SatisfiesNodeFloor("", "") || !SatisfiesNodeFloor("20.0.0", "") {
		t.Error("an undeclared floor must never constrain anything")
	}
}

// A floor this package cannot compare is REFUSED at declaration rather than tolerated: one that
// silently compares wrong is worse than one that will not load.
func TestUnusableFloorsAreRefused(t *testing.T) {
	for _, good := range []string{"22", "22.19", "22.19.0", "v22.19.0"} {
		if !ValidNodeFloor(good) {
			t.Errorf("%q should be a usable floor", good)
		}
	}
	for _, bad := range []string{"", "22.19.0-rc.1", ">=22.19", "22.x", "22.19.0.1", "latest", "22..0"} {
		if ValidNodeFloor(bad) {
			t.Errorf("%q must be refused — an uncomparable floor accepts the wrong interpreter "+
				"silently", bad)
		}
	}
}

// The field is program's alone, and a bad value is caught, both through the real validator.
func TestNodeFloorIsRefusedOnEveryOtherKind(t *testing.T) {
	probs := validateContribution("c[0]", Contribution{
		Kind: KindSkills, From: "skills", Into: ".x/skills", NodeFloor: "22.19"})
	if !containsSubstr(probs, "does not take \"node_floor\"") {
		t.Errorf("a floor on a non-program kind must be refused; got %v", probs)
	}
	probs = validateContribution("c[0]", Contribution{
		Kind: KindProgram, Bin: "x", Via: "npm", Package: "p", NodeFloor: ">=22.19"})
	if !containsSubstr(probs, "not a usable node floor") {
		t.Errorf("an uncomparable floor must be refused on program too; got %v", probs)
	}
}

func containsSubstr(list []string, want string) bool {
	for _, s := range list {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}
