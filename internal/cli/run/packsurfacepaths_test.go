package run

// packsurfacepaths_test.go covers the collector feeding OQ-LM6's two-writers refusal
// (docs/research/local-model-endpoints.md).
//
// ⚠ SCOPE, stated rather than implied: this pins the COLLECTOR, not the refusal's call site in
// runContainer. That call sits after the image load and before the argv assembly, on a path that
// needs a real container to reach, so it is integration territory — deleting the
// `config.SurfaceCollisions` call would leave this file green. The predicate itself is pinned in
// internal/config (hostfilesurface_test.go); what is unpinned is the wiring between them, and
// saying so is better than a test that looks like it covers it.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestPackSurfacePathsCollectsEveryComposedDestination(t *testing.T) {
	m, probs := packdecl.Decode([]byte(`{
  "name": "p",
  "contributes": [
    {
      "kind": "config",
      "config": [
        {"agent": "pi", "codec": "json", "mode": "computed", "name": "models",
         "path": "~/.pi/agent/models.json"}
      ]
    }
  ]
}`))
	if len(probs) > 0 {
		t.Fatalf("fixture manifest did not decode: %v", probs)
	}
	got := packSurfacePaths([]*packload.Pack{{Name: "p", Decl: m}})
	if want := []string{"~/.pi/agent/models.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("packSurfacePaths = %#v, want %#v", got, want)
	}
}

// A nil pack and a pack whose surfaces do not resolve contribute NOTHING rather than failing:
// a pack that cannot say what it composes cannot be shown to collide with anything, and erring the
// other way would turn an unrelated pack defect into a host_files error.
func TestPackSurfacePathsIgnoresWhatItCannotResolve(t *testing.T) {
	if got := packSurfacePaths([]*packload.Pack{nil}); got != nil {
		t.Errorf("a nil pack must contribute nothing, got %#v", got)
	}
	if got := packSurfacePaths(nil); got != nil {
		t.Errorf("no packs must contribute nothing, got %#v", got)
	}
}
