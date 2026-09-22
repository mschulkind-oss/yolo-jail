package config

// hostfilesurface_test.go covers the two-writers refusal OQ-LM6 ruled
// (docs/research/local-model-endpoints.md): a host_files entry at a path a selected pack also
// composes is a FATAL config error, not a merge and not a precedence rule.
//
// The case that matters is a CONFIGURED pack, which checkHostFiles structurally cannot see —
// resolving one needs the pack store, so builtinSurfacePaths covers embedded packs only.

import (
	"strings"
	"testing"
)

func TestSurfaceCollisionsRefusesATwoWriterDestination(t *testing.T) {
	entries := []HostFileEntry{
		{Path: ".pi/agent/models.json"},
		{Path: ".config/starship.toml"},
	}
	// "~/"-prefixed, as a pack's Surface carries it.
	got := SurfaceCollisions(entries, []string{"~/.pi/agent/models.json"})
	if len(got) != 1 {
		t.Fatalf("want exactly the colliding entry refused, got %v", got)
	}
	if !strings.Contains(got[0], ".pi/agent/models.json") {
		t.Errorf("the refusal must name the destination; got %q", got[0])
	}
	// It must offer both ways out, since yolo cannot know which the user wants.
	for _, want := range []string{"Drop the host_files entry", "deselect the pack"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("the refusal must name a remedy (%q); got %q", want, got[0])
		}
	}
	// And it must not imply a winner was chosen.
	for _, forbidden := range []string{"wins", "takes precedence", "overrides"} {
		if strings.Contains(got[0], forbidden) {
			t.Errorf("the ruling refuses rather than resolving; found %q in %q", forbidden, got[0])
		}
	}
}

// Both spellings are accepted, because the callers read surface paths off different structures and
// a normalization bug here reads as "no collision" — the silent second writer the ruling forbids.
func TestSurfaceCollisionsAcceptsEitherSpelling(t *testing.T) {
	entries := []HostFileEntry{{Path: ".pi/agent/models.json"}}
	for _, spelling := range []string{"~/.pi/agent/models.json", ".pi/agent/models.json"} {
		if got := SurfaceCollisions(entries, []string{spelling}); len(got) != 1 {
			t.Errorf("surface path %q did not collide — a normalization miss here is a SILENT "+
				"second writer, which is the defect this refusal exists to stop", spelling)
		}
	}
}

// No collision, no output — the overwhelmingly common case, and it must cost nothing.
func TestSurfaceCollisionsIsQuietWhenNothingOverlaps(t *testing.T) {
	entries := []HostFileEntry{{Path: ".config/starship.toml"}}
	if got := SurfaceCollisions(entries, []string{"~/.claude/settings.json"}); got != nil {
		t.Errorf("want no collisions, got %v", got)
	}
	if got := SurfaceCollisions(nil, []string{"~/.claude/settings.json"}); got != nil {
		t.Errorf("no entries cannot collide, got %v", got)
	}
	if got := SurfaceCollisions(entries, nil); got != nil {
		t.Errorf("no surfaces cannot collide, got %v", got)
	}
}
