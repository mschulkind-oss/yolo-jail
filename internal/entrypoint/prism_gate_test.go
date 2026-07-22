package entrypoint

import "testing"

// TestPrismEnabledFor covers the YOLO_PRISM_SURFACES gate that selects, per
// surface, whether boot renders via the prism engine (ConfigurePiPrism etc.) or
// the bespoke Configure* writer. The gate is the surface-by-surface cutover
// control: empty => all bespoke (the safe default); a comma-separated list of
// "agent" (all that agent's surfaces) or "agent/name" (one surface) opts in.
func TestPrismEnabledFor(t *testing.T) {
	cases := []struct {
		name  string
		flag  string
		agent string
		want  bool
	}{
		{"empty means bespoke", "", "pi", false},
		{"agent match", "pi", "pi", true},
		{"agent no-match", "pi", "claude", false},
		{"whitespace + list", " claude , pi ", "pi", true},
		{"agent/name match", "pi/settings", "pi", true},
		{"agent/name other agent", "pi/settings", "claude", false},
		{"the all sentinel", "all", "pi", true},
		{"the all sentinel any agent", "all", "codex", true},
		{"trailing comma tolerated", "pi,", "pi", true},
		{"case sensitive agent", "PI", "pi", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Env{Vars: map[string]string{"YOLO_PRISM_SURFACES": tc.flag}}
			if got := prismEnabledFor(e, tc.agent); got != tc.want {
				t.Errorf("prismEnabledFor(%q, %q) = %v, want %v", tc.flag, tc.agent, got, tc.want)
			}
		})
	}
}
