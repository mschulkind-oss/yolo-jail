package configedit

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestPlanEditSetChangesCommentedStructureWithoutMovingComments(t *testing.T) {
	before := []byte("{\r\n  keep: true,\r\n  box: {\r\n    remove: 1, // first\r\n    /* second */\r\n    list: [1, 2],\r\n  },\r\n  tail: '# literal',\r\n}\r\n")
	path := []configdoc.Step{{Key: "box"}}
	value := map[string]any{"added": "x", "list": []any{1, 2, 3}}
	plan, err := PlanEdit(before, configdoc.JSONC, Mutation{Action: Set, Path: path, Value: value})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plan.Updated[:bytes.Index(before, []byte("box: "))], before[:bytes.Index(before, []byte("box: "))]) || !bytes.Equal(plan.Updated[len(plan.Updated)-len("  tail: '# literal',\r\n}\r\n"):], []byte("  tail: '# literal',\r\n}\r\n")) {
		t.Fatalf("bytes outside target changed: %q", plan.Updated)
	}
	assertOrderedComments(t, before, plan.Updated, configdoc.JSONC)
	got, err := configdoc.Parse(plan.Updated, configdoc.JSONC)
	if err != nil {
		t.Fatalf("invalid result: %v\n%s", err, plan.Updated)
	}
	box, _ := got.Values.Get("box")
	want, err := semanticValue(value, configdoc.JSONC)
	if err != nil {
		t.Fatal(err)
	}
	if !sameSemantic(box, want) {
		t.Fatalf("box = %#v, want %#v", box, want)
	}
}

func TestPlanEditSetChangesCommentedArrayLength(t *testing.T) {
	before := []byte("{ items: [1, // keep\n  2], stable: true }\n")
	plan, err := PlanEdit(before, configdoc.JSONC, Mutation{Action: Set, Path: []configdoc.Step{{Key: "items"}}, Value: []any{2, 3, 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(plan.Updated, []byte("{ items: ")) || !bytes.HasSuffix(plan.Updated, []byte(", stable: true }\n")) {
		t.Fatalf("unrequested array neighbors changed: %q", plan.Updated)
	}
	assertOrderedComments(t, before, plan.Updated, configdoc.JSONC)
	got, err := configdoc.Parse(plan.Updated, configdoc.JSONC)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := got.Values.Get("items")
	want, _ := semanticValue([]any{2, 3, 2}, configdoc.JSONC)
	if !sameSemantic(items, want) {
		t.Fatalf("items = %#v, want %#v", items, want)
	}
}

func TestPlanEditSetChangesCommentedContainerKind(t *testing.T) {
	cases := []struct {
		name, source string
		value        any
		wantPath     []configdoc.Step
	}{
		{"object to array", "{ box: { old: 1, /* keep-object */ nested: true }, tail: 1 }", []any{1, 2}, []configdoc.Step{{Key: "box"}}},
		{"array to object", "{ box: [1, /* keep-array */ 2], tail: 1 }", map[string]any{"next": 3}, []configdoc.Step{{Key: "box"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := []byte(tc.source)
			plan, err := PlanEdit(before, configdoc.JSONC, Mutation{Action: Set, Path: tc.wantPath, Value: tc.value})
			if err != nil {
				t.Fatal(err)
			}
			assertOrderedComments(t, before, plan.Updated, configdoc.JSONC)
			comment := []byte("/* keep-object */")
			if tc.name == "array to object" {
				comment = []byte("/* keep-array */")
			}
			if bytes.Index(before, comment) != bytes.Index(plan.Updated, comment) {
				t.Fatalf("comment moved from byte %d to %d", bytes.Index(before, comment), bytes.Index(plan.Updated, comment))
			}
			if !bytes.HasSuffix(plan.Updated, []byte(", tail: 1 }")) {
				t.Fatalf("unrequested suffix changed: %q", plan.Updated)
			}
			got, err := configdoc.Parse(plan.Updated, configdoc.JSONC)
			if err != nil {
				t.Fatalf("invalid result: %v\n%s", err, plan.Updated)
			}
			actual, _ := got.Values.Get("box")
			want, err := semanticValue(tc.value, configdoc.JSONC)
			if err != nil {
				t.Fatal(err)
			}
			if !sameSemantic(actual, want) {
				t.Fatalf("value %#v, want %#v", actual, want)
			}
		})
	}
}

func TestPlanEditSetTOMLAssignedKindWithoutInternalComment(t *testing.T) {
	before := []byte("value = [1, 2]\nother = 3\n")
	plan, err := PlanEdit(before, configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "value"}}, Value: map[string]any{"next": 3}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(plan.Updated, []byte("other = 3\n")) {
		t.Fatalf("unrequested assignment changed: %q", plan.Updated)
	}
	if _, err := configdoc.Parse(plan.Updated, configdoc.TOML); err != nil {
		t.Fatalf("invalid: %v\\n%s", err, plan.Updated)
	}
}

func TestPlanEditSetTOMLContainerKindPreservesComments(t *testing.T) {
	before := []byte("value = [1, # retain\r\n  2]\r\nother = 3\r\n")
	plan, err := PlanEdit(before, configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "value"}}, Value: map[string]any{"next": 3}})
	if err != nil {
		t.Fatalf("expected commented compound-kind change to succeed: %v", err)
	}
	assertOrderedComments(t, before, plan.Updated, configdoc.TOML)
	wantValue, err := semanticValue(map[string]any{"next": int64(3)}, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	assertTOMLEditMeaning(t, plan.Updated, "value", wantValue, "other", int64(3))
}

func TestPlanEditSetTOMLHeaderKindChange(t *testing.T) {
	plan, err := PlanEdit([]byte("[settings]\nvalue=1\n"), configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "settings"}}, Value: []any{}})
	if err != nil {
		t.Fatalf("expected table-to-array change to succeed: %v", err)
	}
	got, err := configdoc.Parse(plan.Updated, configdoc.TOML)
	if err != nil {
		t.Fatalf("invalid result: %v\n%s", err, plan.Updated)
	}
	value, ok := got.Values.Get("settings")
	items, valid := value.([]any)
	if !ok || !valid || len(items) != 0 {
		t.Fatalf("settings = %#v (exists=%t), want empty array", value, ok)
	}
}

func TestPlanEditSetChangesTOMLArrayTableStructure(t *testing.T) {
	before := []byte("root=1\r\n[[items]]\r\nname='first'\r\n# first note\r\n[other]\r\nvalue=2\r\n")
	value := []any{map[string]any{"name": "first"}, map[string]any{"name": "second", "enabled": true}}
	plan, err := PlanEdit(before, configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "items"}}, Value: value})
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedComments(t, before, plan.Updated, configdoc.TOML)
	if !bytes.HasSuffix(plan.Updated, []byte("[other]\r\nvalue=2\r\n")) {
		t.Fatalf("unrequested table bytes changed: %q", plan.Updated)
	}
	got, err := configdoc.Parse(plan.Updated, configdoc.TOML)
	if err != nil {
		t.Fatalf("invalid: %v\\n%s", err, plan.Updated)
	}
	actual, _ := got.Values.Get("items")
	want, _ := semanticValue(value, configdoc.TOML)
	if !sameSemantic(actual, want) {
		t.Fatalf("items %#v, want %#v", actual, want)
	}
}

func TestPlanEditRemoveTOMLTableSubtreeAndArrayTableItem(t *testing.T) {
	tests := []struct {
		name   string
		before string
		path   []configdoc.Step
		want   string
	}{
		{"nested subtree", "root = 1\r\n[outer]\r\nkeep = 2\r\n# subtree comment\r\n[outer.child]\r\nvalue = 3\r\n[after]\r\nnext = 4", []configdoc.Step{{Key: "outer"}}, "root = 1\r\n# subtree comment\r\n[after]\r\nnext = 4"},
		{"array table item", "root = 1\n[[items]]\nname = 'first'\n# first note\n[[items]]\nname = 'second'\n[after]\nnext = 4\n", []configdoc.Step{{Key: "items"}, {Index: 0, IsIndex: true}}, "root = 1\n# first note\n[[items]]\nname = 'second'\n[after]\nnext = 4\n"},
		{"interleaved subtree", "[outer.a]\nx=1\n[other]\ny=2\n# later child\n[outer.b]\nz=3\n", []configdoc.Step{{Key: "outer"}}, "[other]\ny=2\n# later child\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanEdit([]byte(tc.before), configdoc.TOML, Mutation{Action: Remove, Path: tc.path})
			if err != nil {
				t.Fatal(err)
			}
			if string(plan.Updated) != tc.want {
				t.Fatalf("bytes differ\nwant %q\n got %q", tc.want, plan.Updated)
			}
			got, err := configdoc.Parse(plan.Updated, configdoc.TOML)
			if err != nil {
				t.Fatalf("invalid result: %v\n%s", err, plan.Updated)
			}
			if strings.Contains(string(plan.Updated), "value = 3") || strings.Contains(string(plan.Updated), "name = 'first'") {
				t.Fatalf("deleted table bytes remain: %s", plan.Updated)
			}
			if !bytes.Contains(plan.Updated, []byte("# ")) {
				t.Fatalf("comment removed: %s", plan.Updated)
			}
			if strings.Contains(tc.name, "interleaved") {
				other, ok := got.Values.Get("other")
				if !ok {
					t.Fatalf("unrelated table missing/rebound: %#v", got.Values)
				}
				om, ok := other.(*jsonx.OrderedMap)
				if !ok {
					t.Fatalf("unrelated table invalid: %#v", other)
				}
				v, _ := om.Get("y")
				if v != int64(2) {
					t.Fatalf("unrelated value changed: %#v", v)
				}
			} else {
				if _, ok := got.Values.Get("after"); !ok {
					t.Fatalf("following table missing/rebound: %#v", got.Values)
				}
			}
			if strings.Contains(tc.name, "subtree") {
				if _, ok := got.Values.Get("outer"); ok {
					t.Fatalf("removed subtree remains: %#v", got.Values)
				}
			} else {
				items, _ := got.Values.Get("items")
				rows, ok := items.([]any)
				if !ok || len(rows) != 1 {
					t.Fatalf("array table result: %#v", items)
				}
			}
		})
	}
}

func TestPlanEditRemoveTOMLImplicitDottedAndInlineTables(t *testing.T) {
	cases := []struct {
		name, source string
		path         []configdoc.Step
	}{
		{"quoted dotted key", "outer.\"special.key\".leaf = 1 # retain\r\nother = 2", []configdoc.Step{{Key: "outer"}}},
		{"inline table", "inline = { \"a.b\" = [1, 2] }\nother = 3\n", []configdoc.Step{{Key: "inline"}}},
		{"array of inline tables", "entries = [{name = 'one'}, {name = 'two'}]\nother = 3\n", []configdoc.Step{{Key: "entries"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := []byte(tc.source)
			plan, err := PlanEdit(before, configdoc.TOML, Mutation{Action: Remove, Path: tc.path})
			if err != nil {
				t.Fatal(err)
			}
			assertOrderedComments(t, before, plan.Updated, configdoc.TOML)
			if tc.name == "quoted dotted key" && !bytes.HasSuffix(plan.Updated, []byte("other = 2")) {
				t.Fatalf("unrequested dotted-key suffix changed: %q", plan.Updated)
			}
			got, err := configdoc.Parse(plan.Updated, configdoc.TOML)
			if err != nil {
				t.Fatalf("invalid result: %v\n%s", err, plan.Updated)
			}
			if _, ok := got.Values.Get(tc.path[0].Key); ok {
				t.Fatalf("requested dotted/inline structure remains: %#v", got.Values)
			}
			other, _ := got.Values.Get("other")
			if other == nil {
				t.Fatalf("unrequested assignment lost: %#v", got.Values)
			}
		})
	}
}

func TestPlanEditRemoveSingletonArrayTableItemLeavesEmptyArray(t *testing.T) {
	before := []byte("root = 1\r\n[[items]]\r\nname = 'only' # retain\r\n[after]\r\nvalue = 2\r\n")
	path := []configdoc.Step{{Key: "items"}, {Index: 0, IsIndex: true}}
	plan, err := PlanEdit(before, configdoc.TOML, Mutation{Action: Remove, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedComments(t, before, plan.Updated, configdoc.TOML)
	if !bytes.HasSuffix(plan.Updated, []byte("[after]\r\nvalue = 2\r\n")) {
		t.Fatalf("unrequested following table bytes changed: %q", plan.Updated)
	}
	got, err := configdoc.Parse(plan.Updated, configdoc.TOML)
	if err != nil {
		t.Fatalf("invalid: %v\n%s", err, plan.Updated)
	}
	items, ok := got.Values.Get("items")
	if !ok || !sameSemantic(items, []any{}) {
		t.Fatalf("singleton removal value %#v, exists=%t", items, ok)
	}
	after, _ := got.Values.Get("after")
	am, ok := after.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("following header lost/rebound: %#v", got.Values)
	}
	v, _ := am.Get("value")
	if v != int64(2) {
		t.Fatalf("following value %#v", v)
	}
}

func TestPlanEditRemoveSingletonArrayTableKey(t *testing.T) {
	before := []byte("[[items]]\nname='only'\n[after]\nvalue=2\n")
	plan, err := PlanEdit(before, configdoc.TOML, Mutation{Action: Remove, Path: []configdoc.Step{{Key: "items"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(plan.Updated, []byte("[after]\nvalue=2\n")) {
		t.Fatalf("unrequested following table bytes changed: %q", plan.Updated)
	}
	if _, err := configdoc.Parse(plan.Updated, configdoc.TOML); err != nil {
		t.Fatalf("invalid result: %v\n%s", err, plan.Updated)
	}
	if strings.Contains(string(plan.Updated), "[[items]]") || strings.Contains(string(plan.Updated), "name=") {
		t.Fatalf("whole key remains: %s", plan.Updated)
	}
}

func TestConfigEditApplySetStructure(t *testing.T) {
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, "config.jsonc")
	before := []byte("{\n  box: {\n    old: 1, // keep-comment\n    stable: 2,\n  },\n  tail: true\n}\n")
	if err := os.WriteFile(jsonPath, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(jsonPath, configdoc.JSONC, Mutation{Action: Set, Path: []configdoc.Step{{Key: "box"}}, Value: map[string]any{"stable": 2, "new": 3}}, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedComments(t, before, got, configdoc.JSONC)
	plan, err := PlanEdit(got, configdoc.JSONC, Mutation{Action: Set, Path: []configdoc.Step{{Key: "box"}}, Value: map[string]any{"stable": 2, "new": 3}})
	if err != nil || !plan.Noop {
		t.Fatalf("repeat set not noop: plan=%#v err=%v", plan, err)
	}
}

func TestConfigEditApplyRemoveTOMLTable(t *testing.T) {
	dir := t.TempDir()
	tomlPath := filepath.Join(dir, "config.toml")
	toml := []byte("[gone]\nvalue = 1\n# keep-note\n[stay]\nvalue = 2\n")
	if err := os.WriteFile(tomlPath, toml, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(tomlPath, configdoc.TOML, Mutation{Action: Remove, Path: []configdoc.Step{{Key: "gone"}}}, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configdoc.Parse(written, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Values.Get("gone"); ok {
		t.Fatalf("removed TOML table remains: %s", written)
	}
	stay, _ := parsed.Values.Get("stay")
	m, ok := stay.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("following keys rebound: %#v", parsed.Values)
	}
	v, _ := m.Get("value")
	if v != int64(2) {
		t.Fatalf("following table value = %#v", v)
	}
	assertOrderedComments(t, toml, written, configdoc.TOML)
	if !bytes.HasSuffix(written, []byte("[stay]\nvalue = 2\n")) {
		t.Fatalf("unrequested following table bytes changed: %q", written)
	}
}

func TestConfigEditApplyTOMLCommentedKindChangePreservesComment(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	before := []byte("value=[1, # stay\n2]\nother = 2\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := Apply(path, configdoc.TOML, Mutation{Action: Set, Path: []configdoc.Step{{Key: "value"}}, Value: map[string]any{"new": true}}, ApplyOptions{})
	if err != nil {
		t.Fatalf("expected commented structural Apply to succeed: %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	assertOrderedComments(t, before, got, configdoc.TOML)
	wantValue, err := semanticValue(map[string]any{"new": true}, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	assertTOMLEditMeaning(t, got, "value", wantValue, "other", int64(2))
	if !bytes.Equal(got, plan.Updated) {
		t.Fatalf("Apply bytes differ from plan: %q vs %q", got, plan.Updated)
	}
}

func TestConfigEditApplyRemoveArrayTableItem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "array.toml")
	before := []byte("[[items]]\nname='first'\n# retain\n[[items]]\nname='second'\n[stay]\nvalue=4\n")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(path, configdoc.TOML, Mutation{Action: Remove, Path: []configdoc.Step{{Key: "items"}, {Index: 0, IsIndex: true}}}, ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedComments(t, before, got, configdoc.TOML)
	if !bytes.HasSuffix(got, []byte("[stay]\nvalue=4\n")) {
		t.Fatalf("unrequested following section bytes changed: %q", got)
	}
	parsed, err := configdoc.Parse(got, configdoc.TOML)
	if err != nil {
		t.Fatalf("invalid result: %v\\n%s", err, got)
	}
	items, exists := parsed.Values.Get("items")
	rows, valid := items.([]any)
	if !exists || !valid || len(rows) != 1 {
		t.Fatalf("array-table result %#v", items)
	}
	stay, _ := parsed.Values.Get("stay")
	sm, ok := stay.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("following section rebound: %#v", parsed.Values)
	}
	v, _ := sm.Get("value")
	if v != int64(4) {
		t.Fatalf("unrequested value changed: %#v", v)
	}
}

func sameSemantic(a, b any) bool {
	am, aok := a.(*jsonx.OrderedMap)
	bm, bok := b.(*jsonx.OrderedMap)
	if aok || bok {
		if !aok || !bok || am.Len() != bm.Len() {
			return false
		}
		for _, key := range am.Keys() {
			av, _ := am.Get(key)
			bv, ok := bm.Get(key)
			if !ok || !sameSemantic(av, bv) {
				return false
			}
		}
		return true
	}
	aa, aok := a.([]any)
	ba, bok := b.([]any)
	if aok || bok {
		if !aok || !bok || len(aa) != len(ba) {
			return false
		}
		for i := range aa {
			if !sameSemantic(aa[i], ba[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func assertOrderedComments(t *testing.T, before, after []byte, format configdoc.Format) {
	t.Helper()
	a, err := configdoc.Parse(before, format)
	if err != nil {
		t.Fatal(err)
	}
	b, err := configdoc.Parse(after, format)
	if err != nil {
		t.Fatal(err)
	}
	collect := func(data []byte, doc *configdoc.Document) [][]byte {
		out := [][]byte{}
		for _, s := range doc.Index.Comments() {
			out = append(out, append([]byte(nil), data[s.Start:s.End]...))
		}
		return out
	}
	if !reflect.DeepEqual(collect(before, a), collect(after, b)) {
		t.Fatalf("comment bytes/order changed: before %q after %q", collect(before, a), collect(after, b))
	}
}
