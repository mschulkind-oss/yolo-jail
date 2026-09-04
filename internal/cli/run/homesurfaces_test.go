package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestPodmanBindsEveryCaptureSurfaceWhereCaptureLooksForIt pins the correspondence that makes
// install-capture's surface set correct rather than merely plausible.
//
// paths.HomeSurfaces() carries two names for one directory: the host-side
// <ws>/.yolo/home/<Subtree> that prune dedupes, and the $HOME/<HomeRel> that the capture driver
// walks inside the jail. Nothing in either of those packages can check that the two are the same
// directory — only the podman argv joins them. So this test reads the real argv and asserts the
// bind, which means the pair list cannot drift from the mount it describes without going red
// here. (The golden argv test next door pins the argv verbatim; this one pins WHY those three
// lines say what they say, and survives a reordering that the golden test would just relearn.)
func TestPodmanBindsEveryCaptureSurfaceWhereCaptureLooksForIt(t *testing.T) {
	ws := "/ws"
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions(ws, home)

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	got := o.assembleRunCmd(&assembleInput{
		cfg:          newConfig("agents", []any{"claude"}, "security", sec),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "unknown",
		mountTargets: map[string]struct{}{},
	})

	for _, s := range paths.HomeSurfaces() {
		want := "/ws/.yolo/home/" + s.Subtree + ":/home/agent/" + s.HomeRel
		if !slices.Contains(got, want) {
			t.Errorf("capture surface %+v is not bound: no %q in the argv\n%s",
				s, want, strings.Join(got, " "))
		}
	}
}
