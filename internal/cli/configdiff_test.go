package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
)

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
		if surfaceMode(s) != "capture" {
			t.Errorf("reset considered non-capture surface %s/%s", s.Agent, s.Name)
		}
	}
	// claude/config is unrendered, so reset must never pick it up.
	for _, s := range capturedSurfaces("claude", "config") {
		t.Errorf("reset must not cover the unrendered claude/config: got %s/%s", s.Agent, s.Name)
	}
}

// Ruling 1 / B1: `reset` must survive adopt-on-first-migration.
//
// Deleting the two sidecars used to BE the discard: no baseline meant the next boot
// re-seeded from scratch. B1 changed that path to ADOPT the on-disk file, and "no
// baseline" is indistinguishable from "the user asked to discard" — so reset must
// also truncate the surface, or reset → no baseline → adopt resurrects the very
// edits the user discarded and reset is a silent no-op.
//
// This asserts the truncation half directly: an edited surface goes back to its
// pure render, so there is nothing left for the next boot to adopt.
func TestResetTruncatesSurfaceSoAdoptionFindsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s, ok := agentcfg.BuiltinManifest().Lookup("copilot", "config")
	if !ok {
		t.Fatal("missing copilot/config")
	}
	path := expandHome(s.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// An "edited" surface carrying a key yolo does not assert.
	if err := os.WriteFile(path, []byte(`{"yolo":true,"myEdit":"keep-me-not"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := truncateSurfaceToPureRender(s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "myEdit") {
		t.Errorf("reset left the discarded edit in the file, so the next boot would adopt it back:\n%s", data)
	}
	if !strings.Contains(string(data), "yolo") {
		t.Errorf("reset must leave the PURE RENDER, not an empty file:\n%s", data)
	}
}

// An absent surface stays absent: reset discards edits, it does not create files
// the jail has not written yet.
func TestResetTruncationLeavesAbsentFileAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, _ := agentcfg.BuiltinManifest().Lookup("copilot", "config")
	if err := truncateSurfaceToPureRender(s); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expandHome(s.Path)); !os.IsNotExist(err) {
		t.Errorf("reset created a file that did not exist (stat err = %v)", err)
	}
}
