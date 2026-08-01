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
