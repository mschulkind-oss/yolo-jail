package setup

import (
	"strings"
	"testing"
)

// TestChooseSpellsTheFlagOutsideASessionAndTheVariableInOne: a next step naming a setup is the
// --setup flag on the command the reader types, and inside a launched session the YOLO_SETUP
// variable; ContainerStep names both container built-ins that way.
func TestChooseSpellsTheFlagOutsideASessionAndTheVariableInOne(t *testing.T) {
	if got := Choose("podman-self", false); got != "`--setup podman-self`" {
		t.Errorf("Choose outside a session = %q", got)
	}
	if got := Choose("podman-self", true); got != "`YOLO_SETUP=podman-self`" {
		t.Errorf("Choose inside a session = %q", got)
	}
	for _, inSession := range []bool{false, true} {
		got := ContainerStep(inSession)
		for _, name := range []string{"podman-self", "apple-container-self"} {
			if !strings.Contains(got, Choose(name, inSession)) {
				t.Errorf("ContainerStep(%v) = %q, does not name %s", inSession, got, name)
			}
			if !IsBuiltIn(name) || MustBuiltIn(name).Kind() != KindJail {
				t.Errorf("%s is not a built-in container setup", name)
			}
		}
	}
}
