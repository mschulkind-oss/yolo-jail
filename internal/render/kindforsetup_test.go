package render

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// Every built-in setup maps to the render Kind its confinement implies, and never to
// KindUnset or KindPreview: a setup always names a point on the dial.
func TestKindForSetupMapsEveryBuiltIn(t *testing.T) {
	want := map[setup.Confinement]Kind{
		setup.ConfinementPodman:         KindJail,
		setup.ConfinementAppleContainer: KindJail,
		setup.ConfinementSeatbelt:       KindGuest,
		setup.ConfinementLandlock:       KindGuest,
		setup.ConfinementHost:           KindHost,
	}
	for _, s := range setup.BuiltIns() {
		got := KindForSetup(s.Kind())
		if got != want[s.Confinement] {
			t.Errorf("%s: KindForSetup = %v, want %v", s.Name, got, want[s.Confinement])
		}
	}
	if got := KindForSetup(setup.KindUnset); got != KindUnset {
		t.Errorf("the zero setup.Kind maps to %v, want KindUnset", got)
	}
}
