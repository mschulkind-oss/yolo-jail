package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestBuildApprovalChangeSetReplaysAndExcludesDisclosedMountRecord(t *testing.T) {
	previous := decode(t, `{
      "install": {"nix": ["jq"]},
      "(mount sources, resolved)": ["/srv/old → /ctx/data"]
    }`)
	current := decode(t, `{
      "install": {"nix": ["jq", "ripgrep"]},
      "(mount sources, resolved)": ["/srv/new → /ctx/data"]
    }`)

	got := BuildApprovalChangeSet(previous, current, true) // Existing mount block is printed.
	if !got.ReplayVerified {
		t.Fatalf("groups did not account for the approval identity: %+v", got)
	}
	if len(got.Groups) != 1 || got.Groups[0].Path != "install.nix" {
		t.Fatalf("groups = %+v; want only install.nix, never the synthetic mount key", got.Groups)
	}
	if got.Groups[0].Verb != "changed" || !reflect.DeepEqual(got.Groups[0].AddedItems, []any{"ripgrep"}) {
		t.Fatalf("install.nix change = %+v; want one added occurrence", got.Groups[0])
	}
}

func TestBuildApprovalChangeSetDistinguishesAbsentNullAndEmptyValues(t *testing.T) {
	previous := decode(t, `{"gone":1,"nested":{"was":true},"nil":null}`)
	current := decode(t, `{"empty":[],"nil":null,"nested":{"now":true},"table":{},"gone":null}`)
	got := BuildApprovalChangeSet(previous, current, false)
	if !got.ReplayVerified {
		t.Fatalf("replay failed: %+v", got)
	}
	want := []string{"empty", "gone", "nested.now", "nested.was", "table"}
	paths := make([]string, len(got.Groups))
	for i, group := range got.Groups {
		paths[i] = group.Path
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if got.Groups[1].Verb != "changed" || !got.Groups[1].HasPrevious || !got.Groups[1].HasCurrent || got.Groups[1].Current != nil {
		t.Fatalf("explicit null was confused with absence: %+v", got.Groups[1])
	}
	if got.Groups[0].Verb != "added" || got.Groups[0].Current == nil {
		t.Fatalf("empty list addition = %+v", got.Groups[0])
	}
	if got.Groups[4].Verb != "added" || got.Groups[4].Current.(*jsonx.OrderedMap).Len() != 0 {
		t.Fatalf("empty table addition = %+v", got.Groups[4])
	}
}

func TestBuildApprovalChangeSetPreservesDuplicateListOccurrencesAndPositions(t *testing.T) {
	previous := decode(t, `{"items":["x","x","keep"]}`)
	current := decode(t, `{"items":["x","keep","x","new"]}`)
	got := BuildApprovalChangeSet(previous, current, false)
	if !got.ReplayVerified || len(got.Groups) != 1 {
		t.Fatalf("change set = %+v", got)
	}
	group := got.Groups[0]
	if group.Verb != "changed" || !reflect.DeepEqual(group.AddedItems, []any{"keep", "new"}) || !reflect.DeepEqual(group.AddedPositions, []int{2, 4}) || !reflect.DeepEqual(group.RemovedItems, []any{"keep"}) || !reflect.DeepEqual(group.RemovedPositions, []int{3}) {
		t.Fatalf("duplicate occurrences or positions lost: %+v", group)
	}
}

func TestBuildApprovalChangeSetUsesEarliestPreviousThenCurrentLCS(t *testing.T) {
	old := []any{"a", "a"}
	cur := []any{"a"}
	pairs := lcsPairs(old, cur)
	want := []approvalPair{{old: 0, cur: 0}}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("LCS = %+v, want %+v", pairs, want)
	}
}

func TestBuildApprovalChangeSetMarksPurePermutationReordered(t *testing.T) {
	got := BuildApprovalChangeSet(decode(t, `{"x":["a","b","a"]}`), decode(t, `{"x":["b","a","a"]}`), false)
	if !got.ReplayVerified || len(got.Groups) != 1 || got.Groups[0].Verb != "reordered" || !got.Groups[0].Reordered {
		t.Fatalf("pure permutation = %+v", got)
	}
}

func TestBuildApprovalChangeSetTreatsChangedArrayTablesAsRemoveAndAdd(t *testing.T) {
	previous := decode(t, `{"rules":[{"z":1,"a":2},{"name":"keep"}]}`)
	current := decode(t, `{"rules":[{"name":"new"},{"name":"keep"}]}`)
	got := BuildApprovalChangeSet(previous, current, false)
	if !got.ReplayVerified || len(got.Groups) != 2 {
		t.Fatalf("change set = %+v", got)
	}
	if got.Groups[0].Path != "rules[1]" || got.Groups[0].Verb != "removed" || got.Groups[1].Path != "rules[1]" || got.Groups[1].Verb != "added" {
		t.Fatalf("array-table operations = %+v", got.Groups)
	}
}

func TestBuildApprovalChangeSetSortsEscapedMapPathsAndRetainsLegacyNames(t *testing.T) {
	previous := decode(t, `{}`)
	current := decode(t, `{"resources":{"cpu":1},"packages":["jq"],"a.b":true,"requests":{"git":true},"repos":{"origin":"o/r"}}`)
	got := BuildApprovalChangeSet(previous, current, false)
	if !got.ReplayVerified {
		t.Fatalf("change set does not replay: %+v", got)
	}
	want := []string{`"a.b"`, "packages", "repos", "requests", "resources"}
	paths := make([]string, len(got.Groups))
	for i, group := range got.Groups {
		paths[i] = group.Path
	}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("paths = %v, want %v", paths, want)
	}
	if got.Groups[1].Path != "packages" {
		t.Fatalf("legacy path renamed: %+v", got.Groups[1])
	}
}

func TestBuildApprovalChangeSetOnlyCreditsMountRecordWhenDisclosed(t *testing.T) {
	previous := decode(t, `{"(mount sources, resolved)":["/old"]}`)
	current := decode(t, `{"(mount sources, resolved)":["/new"]}`)
	for _, tc := range []struct {
		name                 string
		disclose, wantReplay bool
	}{{"printed", true, true}, {"not printed", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildApprovalChangeSet(previous, current, tc.disclose)
			if got.ReplayVerified != tc.wantReplay || len(got.Groups) != 0 {
				t.Fatalf("change set = %+v", got)
			}
		})
	}
}

func TestBuildApprovalChangeSetDoesNotMutateInputs(t *testing.T) {
	previous := decode(t, `{"items":["a",{"nested":true}]}`)
	current := decode(t, `{"items":[{"nested":false},"b"]}`)
	beforeOld, _ := SnapshotJSON(previous)
	beforeCurrent, _ := SnapshotJSON(current)
	got := BuildApprovalChangeSet(previous, current, false)
	afterOld, _ := SnapshotJSON(previous)
	afterCurrent, _ := SnapshotJSON(current)
	if !got.ReplayVerified || beforeOld != afterOld || beforeCurrent != afterCurrent {
		t.Fatalf("inputs mutated or replay failed: %+v", got)
	}
}

func TestBuildApprovalChangeSetDoesNotElideManyPaths(t *testing.T) {
	old := jsonx.NewOrderedMap()
	cur := jsonx.NewOrderedMap()
	for i := 0; i < 200; i++ {
		cur.Set("k"+strings.Repeat("0", 3)+string(rune('a'+i%26))+string(rune('A'+i/26)), i)
	}
	got := BuildApprovalChangeSet(old, cur, false)
	if !got.ReplayVerified || len(got.Groups) != 200 {
		t.Fatalf("groups=%d replay=%v, want every path", len(got.Groups), got.ReplayVerified)
	}
	for i, group := range got.Groups {
		if group.Number != i+1 {
			t.Fatalf("group %d numbered %d", i, group.Number)
		}
	}
}
