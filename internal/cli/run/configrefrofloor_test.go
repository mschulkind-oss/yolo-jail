package run

import (
	"os"
	"strings"
	"testing"
)

// TestConfigRefStatesTheAppleContainerReadOnlyFloor ties the `yolo config-ref` text for
// workspace_readonly to the constant that decides it.
//
// config_ref.txt said "Apple Container IGNORES :ro (a loud warning is printed and the paths
// stay writable)" after roBindsUnsupported learned, on 2026-09-14, that Apple Container
// honors `:ro` from acROBindsFloor. So the CLI's own reference told an Apple Container user
// that a protection they actually have is missing, and the natural response to that is to
// stop relying on it.
//
// The test lives in package run rather than beside config_ref.txt because the floor is
// unexported here, and a copy of the version string in a test would drift exactly the way
// the prose did. Both directions fail: an unconditional "IGNORES :ro" claim, and a passage
// that does not name the floor the code uses.
func TestConfigRefStatesTheAppleContainerReadOnlyFloor(t *testing.T) {
	b, err := os.ReadFile("../config_ref.txt")
	if err != nil {
		t.Fatalf("read config_ref.txt: %v", err)
	}
	ref := string(b)

	const heading = "[bold]workspace_readonly[/bold]"
	i := strings.Index(ref, heading)
	if i < 0 {
		t.Fatalf("no %q entry in config_ref.txt — this test has lost its subject and is "+
			"now vacuous; repoint it at wherever workspace_readonly is explained", heading)
	}
	passage := ref[i+len(heading):]
	if j := strings.Index(passage, "[bold]"); j >= 0 {
		passage = passage[:j]
	}
	// The passage is wrapped at a fixed width, so compare with the line breaks and the
	// indentation folded to single spaces.
	flat := strings.Join(strings.Fields(passage), " ")

	if strings.Contains(flat, "Apple Container IGNORES :ro") {
		t.Error("config_ref.txt says Apple Container ignores workspace_readonly's :ro, " +
			"but roBindsUnsupported honors it from " + acROBindsFloor)
	}
	if !strings.Contains(flat, "Apple Container") || !strings.Contains(flat, acROBindsFloor) {
		t.Errorf("config_ref.txt's workspace_readonly passage does not name the Apple "+
			"Container version (%s) from which the read-only overlay is enforced; a "+
			"reader cannot tell whether their version is covered.\npassage: %s",
			acROBindsFloor, flat)
	}
}
