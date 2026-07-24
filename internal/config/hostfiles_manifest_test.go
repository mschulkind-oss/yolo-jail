package config

import (
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
)

// TestBuiltinSurfacePathsMatchManifest keeps builtinSurfacePaths — the hand-kept
// list of yolo-composed surface destinations that a host_files entry may not
// clobber — honest against the real manifest.
//
// The list is duplicated in hostfiles.go precisely so internal/config does not
// import internal/agentcfg (which pulls in the Lua VM) at RUNTIME. This test lives
// in TEST code, where that import is free, and fails the moment a builtin surface
// is added, removed, or repathed without the reservation list following — which
// would otherwise let a user entry silently render the same file as the prism.
func TestBuiltinSurfacePathsMatchManifest(t *testing.T) {
	var want []string
	for _, s := range agentcfg.BuiltinManifest().Surfaces() {
		want = append(want, s.Path)
	}
	sort.Strings(want)

	got := append([]string(nil), builtinSurfacePaths...)
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("builtinSurfacePaths has %d entries, manifest has %d\n got:  %v\n want: %v",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("mismatch at %d: got %q, want %q", i, got[i], want[i])
		}
	}
}
