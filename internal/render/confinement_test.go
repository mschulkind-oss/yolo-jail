package render

import "testing"

// The confinement presets set the §4.2 AgentAutonomy policy along the wall: a contained
// notch (jail/guest) runs the agent without permission prompts; the uncontained host notch
// does not. This pins the defaults so a future edit to a preset cannot silently ship the
// jail-bypass posture to the host.
func TestProfileAgentAutonomyPresets(t *testing.T) {
	cases := []struct {
		name string
		prof Profile
		want bool
	}{
		{"jail-namespaces", JailProfile(false), true},
		{"jail-vm", JailProfile(true), true},
		{"guest-macos", GuestProfileMacOS(), true},
		{"guest-linux", GuestProfileLinux(), true},
		{"host", HostProfile(), false},
	}
	for _, c := range cases {
		if c.prof.AgentAutonomy != c.want {
			t.Errorf("%s: AgentAutonomy = %v, want %v", c.name, c.prof.AgentAutonomy, c.want)
		}
	}
}

// ProfileFor maps every Kind — including the zero value — to a preset, and the direction of
// each answer is the security property, not a detail. This is the ONE table the render paths
// read their §4.2 policy from now that the four literals are gone, so an inversion here is
// either a jail losing YOLO mode or, far worse, a real host GAINING an agent's permission
// bypass. It is asserted per kind rather than as a set so the failure message names which.
func TestProfileForEveryKind(t *testing.T) {
	for _, c := range []struct {
		kind     Kind
		name     string
		autonomy bool
	}{
		{KindJail, "jail", true},
		{KindGuest, "guest", true},
		{KindHost, "host", false},
		{KindPreview, "preview", true}, // previews the JAIL render, so it carries jail policy
		// The zero value is not a notch, and it must resolve to the RESTRICTED answer. If
		// KindUnset ever yielded autonomy, a Target nobody constructed would render an
		// agent's jail-bypass keys — the leak this whole batch exists to close, reachable
		// from a struct literal.
		{KindUnset, "unset", false},
	} {
		if got := ProfileFor(c.kind).AgentAutonomy; got != c.autonomy {
			t.Errorf("ProfileFor(%s).AgentAutonomy = %v, want %v", c.name, got, c.autonomy)
		}
	}
}

// Every primitive the model defines is both ORDERED and DESCRIBED. Without this, adding a
// Primitive silently prints a blank (or omits itself entirely) in the two outputs whose whole
// job is naming what enforces the confinement: `yolo describe`'s vector and the per-notch
// briefing header (C2).
//
// It lives here rather than in either caller because the table does: two independently worded
// descriptions of one primitive drift, and a reader who hits the disagreement cannot tell which
// is current. PrimitiveDoes never returns "" (it falls back to the number), so the emptiness
// check is against the FALLBACK rather than against a blank — a primitive nobody described
// shows up as "Primitive(6)", which is visible but not acceptable output.
func TestEveryPrimitiveIsOrderedAndDescribed(t *testing.T) {
	order := PrimitiveOrder()
	if len(order) != len(primitiveDoes) {
		t.Errorf("PrimitiveOrder (%d) and primitiveDoes (%d) disagree — a primitive is unordered "+
			"or undescribed", len(order), len(primitiveDoes))
	}
	seen := map[Primitive]bool{}
	for _, prim := range order {
		if seen[prim] {
			t.Errorf("primitive %d is listed twice in PrimitiveOrder", prim)
		}
		seen[prim] = true
		if _, ok := primitiveDoes[prim]; !ok {
			t.Errorf("primitive %d has no human description (PrimitiveDoes falls back to %q)",
				prim, PrimitiveDoes(prim))
		}
	}
	// And the reverse direction: a described primitive nobody ordered never prints at all,
	// which is the silent half of the same drift.
	for prim := range primitiveDoes {
		if !seen[prim] {
			t.Errorf("primitive %d is described but absent from PrimitiveOrder, so it never prints",
				prim)
		}
	}
	// PrimitiveOrder must hand out a COPY: a caller that sorted or truncated the canonical
	// slice in place would change every other consumer's output.
	order[0] = PrimBakedImage
	if PrimitiveOrder()[0] != PrimNamespaces {
		t.Error("PrimitiveOrder returns the canonical slice by reference — a caller can reorder it")
	}
}

// A Target's Profile follows its stated Kind, so a caller holding a Target holds the policy
// and never chooses a boolean of its own. The host row is the one that was a literal `false`
// in three separate files before Q2.
func TestTargetProfileFollowsKind(t *testing.T) {
	if !Jail("/home/agent", "/workspace", nil).Profile().AgentAutonomy {
		t.Error("a jail target must render the AUTONOMOUS posture — a jail without it loses YOLO mode")
	}
	if Host("/home/me", nil).Profile().AgentAutonomy {
		t.Error("a host target must render the GUARDED posture — autonomy here leaks an " +
			"agent's permission bypass onto a real machine (§4.2)")
	}
	// An unconstructed Target cannot claim a notch, so it cannot claim autonomy either.
	if (Target{Home: "/home/me", Workspace: "/workspace"}).Profile().AgentAutonomy {
		t.Error("a Target with no constructor behind it must not resolve to an autonomous notch")
	}
}
