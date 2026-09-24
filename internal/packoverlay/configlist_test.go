package packoverlay

// configlist_test.go pins the config-list pass of Collect: cross-pack placement in pack
// then declaration order, the owner check (R2: inert and reported, by kind), and malformed
// declarations as Problems.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func listPack(name string, lists ...packdecl.Contribution) *packload.Pack {
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{Contributes: lists}}
}

func listContribution(target, path, add string) packdecl.Contribution {
	return packdecl.Contribution{Kind: packdecl.KindConfigList, Surface: target, Path: path,
		Add: json.RawMessage(add)}
}

func TestConfigListResolvesOntoOwnerInPackOrder(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		listPack("first", listContribution("claude/settings", "/extra", `["a","b"]`)),
		listPack("second", listContribution("claude/settings", "/extra", `["c"]`),
			listContribution("claude/settings", "/other", `[1]`)),
	}, true, nil)
	if len(set.Problems) != 0 || len(set.Orphans) != 0 {
		t.Fatalf("problems=%v orphans=%+v", set.Problems, set.Orphans)
	}
	got := set.ListsFor("claude", "settings")
	var order []string
	for _, l := range got {
		order = append(order, l.Pack+" "+l.Path)
	}
	want := []string{"first /extra", "second /extra", "second /other"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("lists = %v, want %v", order, want)
	}
	if !reflect.DeepEqual(got[0].Add, []any{"a", "b"}) {
		t.Fatalf("Add not decoded: %#v", got[0].Add)
	}
	applied := set.AppliedLists()
	if len(applied) != 1 || !reflect.DeepEqual(applied[0].Packs, []string{"first", "second"}) {
		t.Fatalf("AppliedLists = %+v", applied)
	}
	if set.For("claude", "settings") != nil {
		t.Fatal("a config-list leaked into the overlay layers")
	}
}

func TestConfigListWithoutOwnerIsInertAndReportedByKind(t *testing.T) {
	set := Collect([]*packload.Pack{
		listPack("personal", listContribution("pi/settings", "/packages", `["kilo"]`)),
	}, true, nil)
	if got := set.ListsFor("pi", "settings"); got != nil {
		t.Fatalf("an ownerless list was placed: %+v", got)
	}
	if len(set.Orphans) != 1 || set.Orphans[0].KindName() != string(packdecl.KindConfigList) {
		t.Fatalf("orphans = %+v, want one config-list orphan", set.Orphans)
	}
	if !strings.Contains(set.Orphans[0].Reason(), "`pi` pack is not selected") {
		t.Fatalf("reason = %q, want the shipped owner named", set.Orphans[0].Reason())
	}
}

func TestConfigListMalformedIsAProblem(t *testing.T) {
	set := Collect([]*packload.Pack{
		ownerPack("claude"),
		listPack("bad", listContribution("not-an-id", "/x", `["a"]`),
			listContribution("claude/settings", "", `["a"]`),
			listContribution("claude/settings", "/x", `"a"`)),
	}, true, nil)
	if len(set.Problems) != 3 {
		t.Fatalf("problems = %v, want three", set.Problems)
	}
	if set.ListsFor("claude", "settings") != nil {
		t.Fatal("a malformed list was placed")
	}
}
