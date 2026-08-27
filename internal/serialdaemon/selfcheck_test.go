package serialdaemon

import (
	"testing"
)

func TestSelfCheck(t *testing.T) {
	// Empty settings path runs on defaults
	rc := SelfCheck("")
	if rc != 0 {
		t.Errorf("SelfCheck(\"\") = %d, want 0", rc)
	}
}
