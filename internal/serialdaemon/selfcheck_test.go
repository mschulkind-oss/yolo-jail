package serialdaemon

import (
	"testing"
)

func TestSelfCheck(t *testing.T) {
	// No settings path: no jail has launched this loophole yet, which is not a failure; the
	// check reports that nothing is granted (TestSelfCheckReportsTheStanding pins the text).
	rc := SelfCheck("")
	if rc != 0 {
		t.Errorf("SelfCheck(\"\") = %d, want 0", rc)
	}
}
