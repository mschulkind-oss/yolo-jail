package run

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// The production runtime spelling comes from setup.Backend(), not a backend's label.
func TestSourcedAppleContainerReadingRootUsesSetupBackend(t *testing.T) {
	rt := setup.Setup{Confinement: setup.ConfinementAppleContainer}.Backend()
	got := skillPayloadReadingRoot("cname", rt, "/host/tree")
	t.Logf("runtime=%q readingRoot=%q acPackRootInJail=%q", rt, got, acPackRootInJail)
	if got != acPackRootInJail {
		t.Fatalf("Apple Container (rt=%q) registration links target %q, but the jail's pack tree is %q", rt, got, acPackRootInJail)
	}
}
