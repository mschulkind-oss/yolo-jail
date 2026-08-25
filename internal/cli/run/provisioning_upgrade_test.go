package run

import "testing"

// TestFinalInternalCmdNeverUpgrades pins design ruling OQ-PD3
// (docs/design/program-delivery.md): a launch resolves tools on install only —
// no `mise upgrade` runs per launch, in either branch of buildFinalInternalCmd.
// The golden in TestBuildFinalInternalCmdBashGolden covers the non-profile
// branch only; the profile branch composes the same provisionScript and is
// otherwise unpinned, so a reintroduced upgrade would ship green there.
func TestFinalInternalCmdNeverUpgrades(t *testing.T) {
	for _, profile := range []bool{false, true} {
		got := buildFinalInternalCmd("bash", profile)
		if contains(got, "mise upgrade") {
			t.Errorf("profile=%v: launch command runs `mise upgrade`: %q", profile, got)
		}
		if contains(got, "yolo-mise-upgrade") {
			t.Errorf("profile=%v: launch command still writes the upgrade log: %q", profile, got)
		}
		// The install half is what remains load-bearing: it is where a workspace
		// mise.lock governs resolution.
		if !contains(got, "mise install --quiet") {
			t.Errorf("profile=%v: launch command no longer installs declared tools: %q", profile, got)
		}
	}
}
