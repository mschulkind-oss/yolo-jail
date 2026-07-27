package cli

import "testing"

// A6: config-ref promised "`yolo config reset` re-seeds" a `once` host_files
// entry. That cannot happen: reset discards CAPTURED edits by deleting the §5
// sidecars (capturedSurfaces gates on mode == "capture"), and a `once` surface
// writes no sidecar at all. The doc now says to delete the file instead, which is
// what actually re-seeds — `once` keys off the destination's absence.
//
// This test pins the CODE side of that claim so the doc cannot drift back: reset
// must not consider a non-capture surface.
func TestResetOnlyCoversCaptureSurfaces(t *testing.T) {
	for _, s := range capturedSurfaces("claude", "") {
		if prismSurfaceMode[s.Agent+"/"+s.Name] != "capture" {
			t.Errorf("reset considered non-capture surface %s/%s", s.Agent, s.Name)
		}
	}
	// claude/config is unrendered, so reset must never pick it up.
	for _, s := range capturedSurfaces("claude", "config") {
		t.Errorf("reset must not cover the unrendered claude/config: got %s/%s", s.Agent, s.Name)
	}
}
