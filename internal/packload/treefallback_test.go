package packload

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// treefallback_test.go pins ApplyTreeFallbacks (docs/design/pi-extension-store-builds.md XB-D7): a
// taken fallback replaces the tree's list entry in the contributing pack's own config-lists and
// posture lists, in place and exactly, and nothing else — another pack's identical entry, a tree
// that was handed, an extension with no fallback — and the packs it was handed are never written.

func rawList(t *testing.T, entries ...any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func listOf(t *testing.T, raw json.RawMessage) []any {
	t.Helper()
	var out []any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestATakenFallbackReplacesTheTreesEntryInItsOwnPacksListsAlone(t *testing.T) {
	const tree = "~/.pi/agent/yolo-ext/web/node_modules/pi-web-access"
	surfaceOwner := &Pack{Name: "pi", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfig, Raw: json.RawMessage(`[{"agent":"pi","name":"settings","codec":"json",` +
			`"path":"~/.pi/agent/settings.json"}]`)},
	}}}
	matt := &Pack{Name: "matt", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Into: ".pi/agent/yolo-ext/web", Source: "npm:pi-web-access", Fallback: "npm:pi-web-access"},
		{Kind: packdecl.KindFiles, Into: ".pi/agent/yolo-ext/goal", Source: "git+https://h/o/goal?ref=main"},
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages",
			Add: rawList(t, "npm:a", tree, 7, "~/.pi/agent/yolo-ext/goal")},
		{Kind: packdecl.KindAutonomy, Guarded: &packdecl.AutonomyPosture{Lists: []packdecl.PostureList{
			{Surface: "pi/settings", Path: "/packages", Add: rawList(t, tree)},
		}}},
	}}}
	other := &Pack{Name: "other", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages", Add: rawList(t, tree)},
	}}}
	packs := []*Pack{surfaceOwner, matt, other}
	before := listOf(t, matt.Decl.Contributes[2].Add)

	out, taken := ApplyTreeFallbacks(packs, func(Fork) bool { return false })
	if len(taken) != 1 || taken[0].Key() != "matt/web" {
		t.Fatalf("taken = %+v, want the one extension with a fallback", taken)
	}
	if got := listOf(t, out[1].Decl.Contributes[2].Add); !reflect.DeepEqual(got,
		[]any{"npm:a", "npm:pi-web-access", float64(7), "~/.pi/agent/yolo-ext/goal"}) {
		t.Errorf("the config-list = %v, want the fallback in the tree's place and the rest as written", got)
	}
	if got := listOf(t, out[1].Decl.Contributes[3].Guarded.Lists[0].Add); !reflect.DeepEqual(got, []any{"npm:pi-web-access"}) {
		t.Errorf("the guarded posture list = %v, want the fallback", got)
	}
	if out[2] != other || out[0] != surfaceOwner {
		t.Error("a pack that contributes no taken fallback was copied or changed")
	}
	if got := listOf(t, matt.Decl.Contributes[2].Add); !reflect.DeepEqual(got, before) ||
		string(matt.Decl.Contributes[3].Guarded.Lists[0].Add) != string(rawList(t, tree)) {
		t.Error("the packs handed in were written")
	}
	if handed, none := ApplyTreeFallbacks(packs, func(Fork) bool { return true }); none != nil || handed[1] != matt {
		t.Error("a tree that was handed still took its fallback")
	}
}
