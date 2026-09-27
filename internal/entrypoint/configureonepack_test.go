package entrypoint

// configureonepack_test.go pins the autonomy bit `yolo check`'s dry-run probe hands to
// packoverlay.Collect (configureOnePack, behind ConfigurePackByName). The bit's one visible
// effect there is which POSTURE LIST renders (docs/design/notch-scoped-config-contributions.md
// §4.1), and no embedded pack declares one, so without a fixture the argument could be
// inverted with the whole suite green (NS-D13).

import (
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A pack that owns pi/settings and carries BOTH postures' lists on it: the probe renders a
// jail, whose posture is autonomous, so only the autonomous entry lands. Invert the bit at the
// probe's Collect call, or drop the call, and the file says so.
func TestTheCheckProbeRendersOnlyTheJailPosturesList(t *testing.T) {
	e, _ := overlayRenderEnv(t)
	owner := listOwnerPack(t, "", nil)
	list := func(entry string) []packdecl.PostureList {
		raw, err := json.Marshal([]any{entry})
		if err != nil {
			t.Fatal(err)
		}
		return []packdecl.PostureList{{Surface: "pi/settings", Path: "/packages", Add: raw}}
	}
	p := &packload.Pack{Name: owner.Name, Decl: &packdecl.Manifest{Contributes: append(
		owner.Decl.Contributes, packdecl.Contribution{Kind: packdecl.KindAutonomy,
			Autonomous: &packdecl.AutonomyPosture{Lists: list(postureJailEntry)},
			Guarded:    &packdecl.AutonomyPosture{Lists: list(postureHostEntry)}})}}

	if err := configureOnePack(e, p); err != nil {
		t.Fatalf("the probe's render failed: %v", err)
	}
	wantPackages(t, e.Home, "npm:owner-a", "npm:owner-b", postureJailEntry)
}
