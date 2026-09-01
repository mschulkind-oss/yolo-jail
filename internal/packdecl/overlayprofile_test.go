package packdecl

// overlayprofile_test.go pins the `profile` MODIFIER on the config-overlay kind
// (profiles-as-pack-variants.md §7, build-order step 6): one optional field gating a
// cross-pack config contribution on a profile being active for the surface's owning
// agent. The schema's two halves are tested here — the field decodes and travels with
// the contribution, and it is REFUSED on every other kind so it cannot be written where
// it silently does nothing. The gate itself is packoverlay's (see that package's tests).

import (
	"strings"
	"testing"
)

// The field decodes on the strict path and reaches the projection callers gate on —
// a field that decodes but never leaves the Contribution would be a gate that cannot
// fire.
func TestConfigOverlayProfileDecodesAndTravels(t *testing.T) {
	m, problems := Decode([]byte(`{
		"name": "zai",
		"contributes": [
			{"kind": "config-overlay", "profile": "zai", "surface": "claude/settings",
			 "config": {"managed": {"env": {"ANTHROPIC_BASE_URL": "https://api.z.ai/api/anthropic"}}}}
		]
	}`))
	if len(problems) != 0 {
		t.Fatalf("a profile-gated config-overlay should decode cleanly, got: %v", problems)
	}
	ovs := m.ConfigOverlayContributions()
	if len(ovs) != 1 {
		t.Fatalf("want 1 config-overlay, got %d", len(ovs))
	}
	if ovs[0].Profile != "zai" {
		t.Errorf("Profile = %q, want zai — the gate cannot fire on a field the projection drops", ovs[0].Profile)
	}
	if ovs[0].Surface != "claude/settings" {
		t.Errorf("Surface = %q, want claude/settings", ovs[0].Surface)
	}
}

// The field is config-overlay-ONLY: on any other kind it names a gate nothing honors,
// which is the accepted-and-ignored defect this schema refuses everywhere (the
// `requires does not take "via"` rule). The refusal must name the field and the one kind
// that takes it, or an author cannot act on it.
func TestProfileFieldRefusedOffConfigOverlay(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"env", `{"kind": "env", "profile": "zai", "vars": {"A": "b"}}`},
		{"launch", `{"kind": "launch", "profile": "zai", "bin": "claude"}`},
		{"profile", `{"kind": "profile", "profile": "zai", "name": "zai"}`},
		{"provider", `{"kind": "provider", "profile": "zai", "name": "zai"}`},
		{"program", `{"kind": "program", "profile": "zai", "bin": "fzf", "via": "npm", "package": "fzf"}`},
		{"config", `{"kind": "config", "profile": "zai", "config": [{"agent": "a", "name": "n"}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, problems := Decode([]byte(`{"name": "p", "contributes": [` + tc.raw + `]}`))
			if len(problems) == 0 {
				t.Fatalf("a \"profile\" on kind %s must be refused — off the one kind that gates "+
					"on it, the field silently does nothing", tc.name)
			}
			joined := strings.Join(problems, "\n")
			for _, want := range []string{"does not take \"profile\"", "config-overlay modifier"} {
				if !strings.Contains(joined, want) {
					t.Errorf("refusal %q missing %q — it must name the field and the kind that takes it", joined, want)
				}
			}
			_ = m
		})
	}
}

// The mirror half: ON config-overlay the field validates cleanly, and absent it is
// unconditional — the back-compat shape every existing overlay keeps.
func TestProfileFieldCleanOnConfigOverlay(t *testing.T) {
	for _, raw := range []string{
		`{"kind": "config-overlay", "profile": "zai", "surface": "claude/settings",
		  "config": {"managed": {"k": 1}}}`,
		`{"kind": "config-overlay", "surface": "claude/settings",
		  "config": {"managed": {"k": 1}}}`,
	} {
		if _, problems := Decode([]byte(`{"name": "p", "contributes": [` + raw + `]}`)); len(problems) != 0 {
			t.Errorf("config-overlay should validate with and without the field, got: %v", problems)
		}
	}
}

// The refusal survives the TOLERANT path too: DecodeTolerant validates each kept entry
// through the same validateContributionAt, so a manifest staged by a newer host and read
// by this build still hears that its `profile` sits on a kind that ignores it — the
// one-class-of-malformed both builds understand.
func TestProfileFieldRefusedOnTolerantPath(t *testing.T) {
	_, problems, _ := DecodeTolerant([]byte(`{"name": "p", "contributes": [
		{"kind": "env", "profile": "zai", "vars": {"A": "b"}}]}`))
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "does not take \"profile\"") {
		t.Errorf("the tolerant path must refuse the field off config-overlay too, got: %q", joined)
	}
}
