package integration

import "testing"

// TestDefaultJailTimeoutHasColdStartHeadroom is the only non-gated (fast) test
// in this package: it locks the cold-start floor for the per-command deadline so
// a well-meaning revert cannot silently drop it back under the value that made
// integration CI flake. 240s is the empirical floor; 300s is what we ship.
func TestDefaultJailTimeoutHasColdStartHeadroom(t *testing.T) {
	if defaultJailTimeoutSeconds < 240 {
		t.Fatalf("defaultJailTimeoutSeconds=%ds is under the 240s cold-start floor — integration CI will flake",
			defaultJailTimeoutSeconds)
	}
}
