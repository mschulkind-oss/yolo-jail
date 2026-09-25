package config

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestReservedSurfacePathsCoverEveryComposedSurface asserts the reservation list —
// the surface destinations a host_files entry may not clobber — covers every surface
// yolo composes.
//
// It used to be a drift check between two hand-kept lists: the reservation literal in
// hostfiles.go and the Go surface literals in agentcfg. Both sides changed. The paths are
// now READ from the embedded packs (packload has no Lua-VM dependency, so the duplicate
// was unnecessary), and the agent surfaces moved into those packs. What is left to check is
// the part that could still silently break: that core's OWN surfaces — which are not in any
// pack and so are still a literal — are all reserved.
//
// The failure this guards is worth naming: an unreserved surface path lets a user
// host_files entry render the same file the prism renders, and whichever writer runs
// second wins. Nothing errors; the file is just wrong.
func TestReservedSurfacePathsCoverEveryComposedSurface(t *testing.T) {
	reserved := map[string]bool{}
	// No pack selected: core's own surfaces belong to no pack, so they are reserved whatever
	// the selection (OQ-BH15).
	for _, p := range selectedSurfacePaths(nil) {
		reserved[p] = true
	}
	for _, s := range agentcfg.BuiltinManifest().Surfaces() {
		if !reserved[s.Path] {
			t.Errorf("core surface %s/%s at %s is not reserved — a host_files entry could "+
				"render the same file", s.Agent, s.Name, s.Path)
		}
	}
}

// TestReservedSurfacePathsIncludePackSurfaces proves the reservation list actually reads
// the packs it is given, rather than silently falling back to core's literal.
//
// Without this, a bug that made MaterializeEmbedded fail would leave every pack surface
// unreserved while the test above still passed — the list would shrink to one entry and
// nothing would notice.
func TestReservedSurfacePathsIncludePackSurfaces(t *testing.T) {
	all := selectedSurfacePaths(packload.Embedded())
	reserved := map[string]bool{}
	for _, p := range all {
		reserved[p] = true
	}
	// A path that can only come from a pack declaration.
	if !reserved["~/.claude/settings.json"] {
		t.Errorf("pack surface paths are not reserved; list = %v", all)
	}
	if len(reserved) < 5 {
		t.Errorf("reservation list looks like it fell back to core only: %v", all)
	}
}
