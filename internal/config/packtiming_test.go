package config

import "testing"

// THE TIMING PRECEDENCE (docs/design/pi-extension-store-builds.md XB-D18, and the implementation
// decision that extends OQ-PD31 to a tree's two governing packs): the owning agent pack's own entry,
// then the contributing pack's, then "*", then a top-level value, then at the launch; a false is at
// the launch, and an entry that is no setting is absent.
func TestPackTimingDecisionPrecedence(t *testing.T) {
	const next, launch = AgentUpdatesNextLaunch, AgentUpdatesAtLaunch
	for _, c := range []struct {
		wire string
		want string
	}{
		{``, launch},
		{`true`, launch},
		{`false`, launch},
		{`"next-launch"`, next},
		{`{"*":"next-launch"}`, next},
		{`{"*":"next-launch","pi":true}`, launch},
		{`{"*":true,"pi":"next-launch"}`, next},
		{`{"matt":"next-launch"}`, next},
		{`{"pi":"launch","matt":"next-launch"}`, launch},
		{`{"*":"next-launch","matt":true}`, launch},
		{`{"pi":"later","*":"next-launch"}`, next},
		{`{"pi":false}`, launch},
		{`{"pi":false,"*":"next-launch"}`, launch},
		{`{"other":"next-launch"}`, launch},
		{`[1]`, launch},
		{`not json`, launch},
	} {
		if got := PackTimingDecision(c.wire, "pi", "matt"); got != c.want {
			t.Errorf("PackTimingDecision(%s, pi, matt) = %q, want %q", c.wire, got, c.want)
		}
	}
	// An owner-less tree reads its contributing pack alone.
	if got := PackTimingDecision(`{"*":true,"matt":"next-launch"}`, "matt"); got != next {
		t.Errorf("an owner-less tree's own entry = %q, want next-launch", got)
	}
	if got := PackTimingDecision(`{"pi":"next-launch"}`, "matt"); got != launch {
		t.Errorf("an owner-less tree read another pack's entry: %q", got)
	}
}
