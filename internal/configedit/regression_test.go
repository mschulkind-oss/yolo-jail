package configedit

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestConfigEditNestedArrayRemovalPlanAndApply(t *testing.T) {
	before := []byte("matrix = [[[1]], [2]]\nkeep = \"untouched\"\n")
	want := []byte("matrix = [ [2]]\nkeep = \"untouched\"\n")
	mutation := Mutation{Action: Remove, Path: []configdoc.Step{{Key: "matrix"}, {Index: 0, IsIndex: true}}}
	plan, err := PlanEdit(before, configdoc.TOML, mutation)
	if err != nil {
		t.Fatalf("PlanEdit: %v", err)
	}
	if !bytes.Equal(plan.Updated, want) {
		t.Fatalf("PlanEdit bytes\nwant %q\n got %q", want, plan.Updated)
	}
	assertTOMLEditMeaning(t, plan.Updated, "matrix", []any{[]any{int64(2)}}, "keep", "untouched")

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || !bytes.Equal(applied.Updated, want) {
		t.Fatalf("Apply bytes\nwant %q\n got %q", want, got)
	}
	assertTOMLEditMeaning(t, got, "matrix", []any{[]any{int64(2)}}, "keep", "untouched")
}

func TestConfigEditCommentedNestedArrayGrowthPlanAndApply(t *testing.T) {
	before := []byte("matrix = [\n  [[1]], # keep this comment\n  [2],\n]\nkeep = \"untouched\"\n")
	wantBytes := []byte("matrix = [\n  [[1]], # keep this comment\n  [2],\n  [2],\n]\nkeep = \"untouched\"\n")
	want := []any{[]any{[]any{int64(1)}}, []any{int64(2)}, []any{int64(2)}}
	mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "matrix"}}, Value: []any{[]any{[]any{1}}, []any{2}, []any{2}}}
	plan, err := PlanEdit(before, configdoc.TOML, mutation)
	if err != nil {
		t.Fatalf("PlanEdit: %v", err)
	}
	if !strings.Contains(string(plan.Updated), "# keep this comment") {
		t.Fatalf("comment lost: %q", plan.Updated)
	}
	if !bytes.Equal(plan.Updated, wantBytes) {
		t.Fatalf("PlanEdit bytes\nwant %q\n got %q", wantBytes, plan.Updated)
	}
	assertOrderedComments(t, before, plan.Updated, configdoc.TOML)
	assertTOMLEditMeaning(t, plan.Updated, "matrix", want, "keep", "untouched")

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantBytes) {
		t.Fatalf("Apply bytes\nwant %q\n got %q", wantBytes, got)
	}
	assertOrderedComments(t, before, got, configdoc.TOML)
	assertTOMLEditMeaning(t, got, "matrix", want, "keep", "untouched")
}

func TestConfigEditIndentedTOMLHeaderRemovalPlanAndApply(t *testing.T) {
	cases := []struct {
		name, before, want string
	}{
		{"spaces", "  [gone]\nvalue=1\n[stay]\nvalue=2\n", "[stay]\nvalue=2\n"},
		{"tabs", "\t[gone]\nvalue=1\n[stay]\nvalue=2\n", "[stay]\nvalue=2\n"},
		{"crlf", "  [gone]\r\nvalue=1\r\n[stay]\r\nvalue=2\r\n", "[stay]\r\nvalue=2\r\n"},
		{"empty table", "  [gone]\n[stay]\nvalue=2\n", "[stay]\nvalue=2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, want := []byte(tc.before), []byte(tc.want)
			mutation := Mutation{Action: Remove, Path: []configdoc.Step{{Key: "gone"}}}
			plan, err := PlanEdit(before, configdoc.TOML, mutation)
			if err != nil {
				t.Fatalf("PlanEdit: %v", err)
			}
			if !bytes.Equal(plan.Updated, want) {
				t.Fatalf("PlanEdit bytes\nwant %q\n got %q", want, plan.Updated)
			}
			assertTOMLHeaderRemoved(t, plan.Updated)

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Apply bytes\nwant %q\n got %q", want, got)
			}
			assertTOMLHeaderRemoved(t, got)
		})
	}
}

func TestConfigEditIndentedTOMLArrayTableSetGrowthPlanAndApply(t *testing.T) {
	cases := []struct {
		name, before, want string
	}{
		{"spaces", "  [[items]]\nname='first'\n[stay]\nvalue=4\n", "  [[items]]\nname='first'\n  [[items]]\nname = \"second\"\n[stay]\nvalue=4\n"},
		{"tabs", "\t[[items]]\nname='first'\n[stay]\nvalue=4\n", "\t[[items]]\nname='first'\n\t[[items]]\nname = \"second\"\n[stay]\nvalue=4\n"},
		{"crlf", "  [[items]]\r\nname='first'\r\n[stay]\r\nvalue=4\r\n", "  [[items]]\r\nname='first'\r\n  [[items]]\r\nname = \"second\"\r\n[stay]\r\nvalue=4\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := []byte(tc.before)
			want := []byte(tc.want)
			mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "items"}}, Value: []any{map[string]any{"name": "first"}, map[string]any{"name": "second"}}}
			plan, err := PlanEdit(before, configdoc.TOML, mutation)
			if err != nil {
				t.Fatalf("PlanEdit: %v", err)
			}
			if !bytes.Equal(plan.Updated, want) {
				t.Fatalf("PlanEdit bytes\nwant %q\n got %q", want, plan.Updated)
			}
			assertTOMLArrayTableGrowth(t, plan.Updated)

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("Apply bytes\nwant %q\n got %q", want, got)
			}
			assertTOMLArrayTableGrowth(t, got)
		})
	}
}

func TestConfigEditNestedArrayTableGrowthPlanAndApply(t *testing.T) {
	before := []byte("[[parents]]\nname='first'\n[[parents.children]]\nname='only'\n[[parents]]\nname='second'\n")
	mutation := Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}}, Value: map[string]any{"name": "first", "children": []any{map[string]any{"name": "only"}, map[string]any{"name": "added"}}}}
	plan, err := PlanEdit(before, configdoc.TOML, mutation)
	if err != nil {
		t.Fatalf("PlanEdit nested growth: %v", err)
	}
	wantBytes := []byte("[[parents]]\nname='first'\n[[parents.children]]\nname='only'\n[[parents.children]]\nname = \"added\"\n[[parents]]\nname='second'\n")
	if !bytes.Equal(plan.Updated, wantBytes) {
		t.Fatalf("PlanEdit bytes\nwant %q\n got %q", wantBytes, plan.Updated)
	}
	wantValues, err := semanticValue(map[string]any{"parents": []any{map[string]any{"name": "first", "children": []any{map[string]any{"name": "only"}, map[string]any{"name": "added"}}}, map[string]any{"name": "second"}}}, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := configdoc.Parse(plan.Updated, configdoc.TOML)
	if err != nil || !semanticEqual(parsed.Values, wantValues) {
		t.Fatalf("PlanEdit values = %#v, err %v; want %#v", parsed, err, wantValues)
	}
	path := filepath.Join(t.TempDir(), "nested.toml")
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply nested growth: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, wantBytes) || !bytes.Equal(applied.Updated, wantBytes) {
		t.Fatalf("Apply bytes\nwant %q\n got %q (reported %q)", wantBytes, got, applied.Updated)
	}
	parsed, err = configdoc.Parse(got, configdoc.TOML)
	if err != nil || !semanticEqual(parsed.Values, wantValues) {
		t.Fatalf("Apply values = %#v, err %v; want %#v", parsed, err, wantValues)
	}
}

func assertTOMLEditMeaning(t *testing.T, data []byte, key string, want any, otherKey string, otherWant any) {
	t.Helper()
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		t.Fatalf("parse result: %v\n%s", err, data)
	}
	got, ok := doc.Values.Get(key)
	if !ok || !sameSemantic(got, want) {
		t.Fatalf("%s = %#v (exists=%t), want %#v", key, got, ok, want)
	}
	other, ok := doc.Values.Get(otherKey)
	if !ok || !sameSemantic(other, otherWant) {
		t.Fatalf("%s = %#v (exists=%t), want %#v", otherKey, other, ok, otherWant)
	}
}

func assertTOMLHeaderRemoved(t *testing.T, data []byte) {
	t.Helper()
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		t.Fatalf("parse result: %v\n%s", err, data)
	}
	if _, ok := doc.Values.Get("gone"); ok {
		t.Fatalf("removed header remains: %q", data)
	}
	stay, ok := doc.Values.Get("stay")
	m, valid := stay.(*jsonx.OrderedMap)
	if !ok || !valid {
		t.Fatalf("following header missing/rebound: %#v", doc.Values)
	}
	value, _ := m.Get("value")
	if value != int64(2) {
		t.Fatalf("following value = %#v", value)
	}
}

func assertTOMLArrayTableGrowth(t *testing.T, data []byte) {
	t.Helper()
	doc, err := configdoc.Parse(data, configdoc.TOML)
	if err != nil {
		t.Fatalf("parse result: %v\n%s", err, data)
	}
	items, ok := doc.Values.Get("items")
	rows, valid := items.([]any)
	if !ok || !valid || len(rows) != 2 {
		t.Fatalf("items = %#v, want two rows", items)
	}
	first, ok1 := rows[0].(*jsonx.OrderedMap)
	second, ok2 := rows[1].(*jsonx.OrderedMap)
	if !ok1 || !ok2 {
		t.Fatalf("rows = %#v", rows)
	}
	firstName, _ := first.Get("name")
	secondName, _ := second.Get("name")
	if firstName != "first" || secondName != "second" {
		t.Fatalf("row names = %#v, %#v", firstName, secondName)
	}
	stay, ok := doc.Values.Get("stay")
	m, valid := stay.(*jsonx.OrderedMap)
	if !ok || !valid {
		t.Fatalf("following table missing/rebound: %#v", doc.Values)
	}
	value, _ := m.Get("value")
	if value != int64(4) {
		t.Fatalf("following table value = %#v", value)
	}
}
