package packoverlay

// registration_test.go pins where a REGISTERING files slot's entries are placed: as config-list
// entries of the pack whose tree landed, on the slot owner's surface, ahead of that pack's own
// lists (docs/design/pack-pi-resources.md §3.3, PR-D2), and refused when the slot names a surface
// its own pack does not declare.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// slotOwner declares pi/settings and a slot registering into `surface`.
func slotOwner(surface string) *packload.Pack {
	raw, _ := json.Marshal([]map[string]any{{
		"agent": "pi", "name": "settings", "codec": "json", "path": "~/.pi/agent/settings.json",
	}})
	return &packload.Pack{Name: "pi", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindConfig, Raw: raw},
		{Kind: packdecl.KindFiles, Agent: "pi", Into: ".pi/agent/yolo-packs",
			Register: &packdecl.FilesRegister{Surface: surface, Path: "/packages"}},
	}}}
}

// folderPack addresses pi with one tree, and appends one entry of its own.
func folderPack(name string) *packload.Pack {
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Agents: []string{"pi"}, From: "files/pi"},
		{Kind: packdecl.KindConfigList, Surface: "pi/settings", Path: "/packages",
			Add: json.RawMessage(`["npm:own"]`)},
	}}}
}

func TestATreeInARegisteringSlotIsAConfigListEntryOfItsPack(t *testing.T) {
	set := Collect([]*packload.Pack{slotOwner("pi/settings"), folderPack("matt")}, false, nil)
	if len(set.Problems) != 0 {
		t.Fatalf("problems: %v", set.Problems)
	}
	want := []agentcfg.ListContribution{
		{Pack: "matt", Path: "/packages", Add: []any{"~/.pi/agent/yolo-packs/matt"}},
		{Pack: "matt", Path: "/packages", Add: []any{"npm:own"}},
	}
	if got := set.ListsFor("pi", "settings"); !reflect.DeepEqual(got, want) {
		t.Fatalf("lists on pi/settings = %+v\nwant %+v", got, want)
	}
	if applied := set.AppliedLists(); len(applied) != 1 || !reflect.DeepEqual(applied[0].Packs, []string{"matt"}) {
		t.Errorf("the applied-lists report does not name the tree's pack: %+v", applied)
	}
}

// The pack dropped: nothing of it is placed, so the fold forgets its entry.
func TestADroppedTreeIsNotListed(t *testing.T) {
	set := Collect([]*packload.Pack{slotOwner("pi/settings")}, false, nil)
	if got := set.ListsFor("pi", "settings"); len(got) != 0 {
		t.Fatalf("lists with no tree addressing the slot = %+v", got)
	}
}

func TestASlotRegisteringIntoAnotherPacksSurfaceIsAProblem(t *testing.T) {
	for _, surface := range []string{"claude/settings", "not-an-identity"} {
		set := Collect([]*packload.Pack{slotOwner(surface), folderPack("matt")}, false, nil)
		if len(set.Problems) != 1 || !strings.Contains(set.Problems[0], "files slot .pi/agent/yolo-packs") {
			t.Errorf("register on %q: problems = %v, want one naming the slot", surface, set.Problems)
		}
		for _, l := range set.ListsFor("pi", "settings") {
			if l.Add[0] != "npm:own" {
				t.Errorf("register on %q placed an entry anyway: %+v", surface, l)
			}
		}
	}
}
