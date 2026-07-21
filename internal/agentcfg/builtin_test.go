package agentcfg

import "testing"

// TestBuiltinManifestValid asserts the yolo-shipped manifest passes the
// manifest validator (catches a malformed builtin at test time, not runtime).
func TestBuiltinManifestValid(t *testing.T) {
	m := BuiltinManifest()
	if m.Len() == 0 {
		t.Fatal("builtin manifest is empty")
	}
	s, ok := m.Lookup("pi", "settings")
	if !ok {
		t.Fatal("builtin manifest missing pi/settings")
	}
	if s.Codec != "json" {
		t.Errorf("pi/settings codec = %q, want json", s.Codec)
	}
	if s.Managed["defaultProjectTrust"] != "always" {
		t.Errorf("pi/settings should enforce defaultProjectTrust=always, got %v", s.Managed["defaultProjectTrust"])
	}
}
