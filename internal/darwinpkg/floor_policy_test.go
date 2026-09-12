package darwinpkg

// floor_policy_test.go is THE ASSERTION OQ-P2 NEEDS AND NIX CANNOT PROVIDE.
//
// The two rulings behind the floor compose with a gap, and the gap is the whole
// reason this file exists (docs/design/macos-user-provisioning.md, the note under
// the Decision Ledger):
//
//	| kind of exclusion   | forgotten entry does what                          |
//	|---------------------|----------------------------------------------------|
//	| unbuildable on macOS| the nix eval DIES, naming the package. Fine.       |
//	| GNU userland        | it BUILDS FINE AND SHIPS SILENTLY, and the agent   |
//	|                     | gets GNU `sed` on a Mac.                           |
//
// So the fatal covers necessity and cannot cover policy. A hand-maintained list
// is not the fix either — the failure is precisely someone not editing it. What
// closes the gap is an assertion over the DERIVED floor, which examines every
// name the image core contains today, including one added after this file was
// written.

import (
	"strings"
	"testing"
)

// TestTheDarwinFloorCarriesNoGNUUserland is the gate. It reads the floor rather
// than the exclusion list, which is the difference that matters: add `gnumake` to
// the image core and forget the exclusions, and this fails on the name that
// arrived, not on a list nobody touched.
func TestTheDarwinFloorCarriesNoGNUUserland(t *testing.T) {
	var offenders []string
	for _, n := range FloorNames() {
		if IsGNUUserland(n) {
			offenders = append(offenders, n)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("the non-container floor carries GNU userland: %s\n\n"+
			"OQ-P2 ruled NO GNU USERLAND for this backend: its proposition is \"your Mac, "+
			"confined\", and an agent whose `sed -i` behaves unlike the human's is a surprise "+
			"in the direction that costs more. Every name above builds perfectly well for "+
			"darwin, which is why nix cannot catch this and this test has to.\n\n"+
			"Fix: add each to FloorExcludedPolicy in floor.go AND to noncontainerFloorPolicy "+
			"in flake.nix (the drift gate requires both). If one of them genuinely is not a "+
			"BSD-vs-GNU surprise, the honest fix is to narrow IsGNUUserland with the reason "+
			"written down — not to leave the floor as it is.",
			strings.Join(offenders, ", "))
	}
}

// TestTheGNUPolicyGateActuallyFires is the mutation half, and without it the test
// above is unfalsifiable: a predicate that returns false for everything passes it
// forever.
//
// For each policy exclusion, it re-derives the floor with THAT ONE entry put back
// and asserts the gate would have caught it. So the gate is proven to fire on
// every name it is currently protecting, rather than merely on none.
func TestTheGNUPolicyGateActuallyFires(t *testing.T) {
	if len(FloorExcludedPolicy) == 0 {
		t.Fatal("FloorExcludedPolicy is empty — either OQ-P2 was reversed (in which case " +
			"delete this file and say so in the design doc) or the list was lost, which is " +
			"the silent shipment it exists to prevent")
	}
	for _, restored := range FloorExcludedPolicy {
		if !IsGNUUserland(restored) {
			t.Errorf("FloorExcludedPolicy names %q, but IsGNUUserland does not flag it. "+
				"The two halves have drifted: the exclusion would be silently safe to "+
				"delete, because the gate that is supposed to catch its return does not "+
				"recognise it", restored)
			continue
		}
		// The floor with this one exclusion lifted — what shipping the mistake
		// would produce.
		var caught bool
		for _, n := range append(FloorNames(), restored) {
			if IsGNUUserland(n) {
				caught = true
			}
		}
		if !caught {
			t.Errorf("putting %q back on the floor does not trip the policy gate — the "+
				"gate is decorative for that name", restored)
		}
	}
}

// TestTheFloorStillHasTheToolsTheStageNeeds is the OTHER direction, and it is not
// symmetry for its own sake.
//
// Every name on this list can be removed from the floor by a one-word edit to an
// exclusion list, and the result is a jail that launches perfectly and then fails
// on the user's first real command — which is the failure class the whole design
// is about (§2 of macos-user-provisioning.md: four config keys that render and
// install nothing). `mise` and `nodejs` in particular are what half two's
// provisioning stage EXECUTES; without them the stage fails on its first line.
func TestTheFloorStillHasTheToolsTheStageNeeds(t *testing.T) {
	on := map[string]struct{}{}
	for _, n := range FloorNames() {
		on[n] = struct{}{}
	}
	for _, req := range []struct{ name, why string }{
		{"mise", "half two's stage runs `mise install`; without the binary it fails on line one"},
		{"nodejs_24", "the generated bootstrap script npm-installs LSP servers and MCP presets, " +
			"and the `via: npm` agent launchers exec `npm install -g` (measured 2026-09-11: " +
			"they fail with `npm: command not found` on the user's first command)"},
		{"git", "the agent commits; a jail without git is not a development environment"},
		{"ripgrep", "the guardrails pack blocks `grep` and names `rg` as the replacement — a " +
			"blocker is only generated when its replacement is on the agent's PATH, so " +
			"losing rg silently un-blocks grep instead of failing"},
		{"fd", "same, for `find`"},
		{"curl", "the `via: installer` agent launchers pipe a vendor installer through it"},
		{"cacert", "without it every https fetch the two rows above make fails certificate " +
			"verification"},
	} {
		if _, ok := on[req.name]; !ok {
			t.Errorf("%q is not on the non-container floor — %s", req.name, req.why)
		}
	}
}

// TestFloorIsTheCoreMinusExactlyTheExclusions pins the arithmetic, so a bug in
// FloorNames itself (an early return, a filter inverted) cannot leave every other
// test in this file passing about a list that is not the floor.
func TestFloorIsTheCoreMinusExactlyTheExclusions(t *testing.T) {
	want := len(ImageCoreNames) - len(FloorExcludedUnbuildable) - len(FloorExcludedPolicy)
	if got := len(FloorNames()); got != want {
		t.Fatalf("FloorNames() has %d entries, want %d (%d core − %d unbuildable − %d policy)",
			got, want, len(ImageCoreNames), len(FloorExcludedUnbuildable), len(FloorExcludedPolicy))
	}
	excluded := map[string]struct{}{}
	for _, n := range append(append([]string{}, FloorExcludedUnbuildable...), FloorExcludedPolicy...) {
		excluded[n] = struct{}{}
	}
	for _, n := range FloorNames() {
		if _, bad := excluded[n]; bad {
			t.Errorf("FloorNames() returned %q, which is on an exclusion list", n)
		}
	}
	// Order is flake order, so a diff between floor.go and flake.nix reads as a diff.
	i := 0
	for _, n := range ImageCoreNames {
		if _, skip := excluded[n]; skip {
			continue
		}
		if got := FloorNames()[i]; got != n {
			t.Fatalf("FloorNames()[%d] = %q, want %q — the floor is no longer in flake order", i, got, n)
		}
		i++
	}
}
