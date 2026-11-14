package configedit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

type structuralCase struct {
	name, before, after string
	format              configdoc.Format
	mutation            Mutation
	path                []configdoc.Step
	value               any
}

func structuralCompletionCases() []structuralCase {
	return []structuralCase{
		{name: "toml header to scalar", format: configdoc.TOML, before: "[settings]\nvalue = 1\n\n[other]\nvalue = 2\n", after: "settings = \"off\"\n\n[other]\nvalue = 2\n", path: []configdoc.Step{{Key: "settings"}}, value: "off"},
		{name: "toml header to array", format: configdoc.TOML, before: "[settings]\nvalue = 1\n[other]\nvalue = 2\n", after: "settings = [\"x\"]\n[other]\nvalue = 2\n", path: []configdoc.Step{{Key: "settings"}}, value: []any{"x"}},
		{name: "toml root header conversion after table", format: configdoc.TOML, before: "[other]\nvalue = 2\n[settings]\nvalue = 1\n", after: "settings = \"off\"\n[other]\nvalue = 2\n", path: []configdoc.Step{{Key: "settings"}}, value: "off"},
		{name: "commented toml array to inline table", format: configdoc.TOML, before: "value = [1, # keep-toml\n  2]\nother = 4\n", after: "value = { next = 3 } # keep-toml\n  \nother = 4\n", path: []configdoc.Step{{Key: "value"}}, value: map[string]any{"next": 3}},
		{name: "commented jsonc object to array", format: configdoc.JSONC, before: "{ value: { old: 1, /* keep-jsonc */ nested: true }, tail: 4 }\n", after: "{ value:           /* keep-jsonc */               [\n  1,\n  2\n], tail: 4 }\n", path: []configdoc.Step{{Key: "value"}}, value: []any{1, 2}},
		{name: "nested indexed parent growth", format: configdoc.TOML, before: "[[parents]]\nname='first'\n[[parents.children]]\nname='old'\n[[parents]]\nname='second'\n", after: "[[parents]]\nname='first'\n[[parents.children]]\nname='old'\n[[parents.children]]\nname = \"new\"\n[[parents]]\nname='second'\n", path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}}, value: map[string]any{"name": "first", "children": []any{map[string]any{"name": "old"}, map[string]any{"name": "new"}}}},
		{name: "nested indexed parent growth quoted and interleaved", format: configdoc.TOML, before: "[[parents]]\nname='first'\n[[parents.\"special.key\"]]\nname='old'\n[parents.info]\nnote='keep'\n[[parents]]\nname='second'\n", after: "[[parents]]\nname='first'\n[[parents.\"special.key\"]]\nname='old'\n[[parents.\"special.key\"]]\nname = \"new\"\n[parents.info]\nnote='keep'\n[[parents]]\nname='second'\n", path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}}, value: map[string]any{"name": "first", "special.key": []any{map[string]any{"name": "old"}, map[string]any{"name": "new"}}, "info": map[string]any{"note": "keep"}}},
		{name: "nested indexed parent growth crlf", format: configdoc.TOML, before: "[[parents]]\r\nname='first'\r\n[[parents.children]]\r\nname='old'\r\n[[parents]]\r\nname='second'\r\n", after: "[[parents]]\r\nname='first'\r\n[[parents.children]]\r\nname='old'\r\n[[parents.children]]\r\nname = \"new\"\r\n[[parents]]\r\nname='second'\r\n", path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}}, value: map[string]any{"name": "first", "children": []any{map[string]any{"name": "old"}, map[string]any{"name": "new"}}}},
		{name: "array-table Set keeps duplicate ordered rows", format: configdoc.TOML, before: "[[items]]\nname='same'\n", after: "[[items]]\nname='same'\n[[items]]\nname = \"same\"\n", path: []configdoc.Step{{Key: "items"}}, value: []any{map[string]any{"name": "same"}, map[string]any{"name": "same"}}},
		{name: "nested final singleton removal", format: configdoc.TOML, before: "[[parents]]\nname='first'\n[[parents.children]]\nname='only'\n[[parents]]\nname='second'\n[[parents.children]]\nname='keep'\n", after: "[[parents]]\nname='first'\nchildren = []\n[[parents]]\nname='second'\n[[parents.children]]\nname='keep'\n", path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}, {Key: "children"}, {Index: 0, IsIndex: true}}, mutation: Mutation{Action: Remove, Path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}, {Key: "children"}, {Index: 0, IsIndex: true}}}},
		{name: "root header-array append", format: configdoc.TOML, before: "[[items]]\nname='first'\n[tail]\nvalue=2\n", after: "[[items]]\nname='first'\n[[items]]\nname = \"second\"\n[tail]\nvalue=2\n", path: []configdoc.Step{{Key: "items"}}, value: map[string]any{"name": "second"}},
		{name: "nested header-array append", format: configdoc.TOML, before: "[[parents]]\nname='first'\n[[parents.children]]\nname='old'\n[[parents]]\nname='second'\n", after: "[[parents]]\nname='first'\n[[parents.children]]\nname='old'\n[[parents.children]]\nname = \"new\"\n[[parents]]\nname='second'\n", path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}, {Key: "children"}}, value: map[string]any{"name": "new"}},
	}
}

func mutationForStructuralCase(tc structuralCase) Mutation {
	if tc.mutation.Action != 0 {
		return tc.mutation
	}
	action := Set
	if tc.name == "root header-array append" || tc.name == "nested header-array append" {
		action = Append
	}
	return Mutation{Action: action, Path: tc.path, Value: tc.value}
}

func TestConfigEditStructuralCompletionPlan(t *testing.T) {
	for _, tc := range structuralCompletionCases() {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanEdit([]byte(tc.before), tc.format, mutationForStructuralCase(tc))
			if err != nil {
				t.Fatalf("PlanEdit: %v", err)
			}
			if !bytes.Equal(plan.Updated, []byte(tc.after)) {
				t.Fatalf("bytes differ\nwant %q\n got %q", tc.after, plan.Updated)
			}
			assertStructuralCompletionMeaning(t, plan.Updated, tc)
		})
	}
}

func TestConfigEditStructuralCompletionApply(t *testing.T) {
	for _, tc := range structuralCompletionCases() {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(path, []byte(tc.before), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(path, tc.format, mutationForStructuralCase(tc), ApplyOptions{}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(tc.after)) {
				t.Fatalf("Apply bytes differ\nwant %q\n got %q", tc.after, got)
			}
			assertStructuralCompletionMeaning(t, got, tc)
		})
	}
}

func assertStructuralCompletionMeaning(t *testing.T, data []byte, tc structuralCase) {
	t.Helper()
	want, err := semanticValue(expectedStructuralDocument(tc.name), tc.format)
	if err != nil {
		t.Fatalf("prepare independent expected values: %v", err)
	}
	parsed, err := configdoc.Parse(data, tc.format)
	if err != nil {
		t.Fatalf("updated document does not parse: %v", err)
	}
	if !semanticEqual(parsed.Values, want) {
		t.Fatalf("document values = %#v, want %#v", parsed.Values, want)
	}
}

func expectedStructuralDocument(name string) map[string]any {
	switch name {
	case "toml header to scalar", "toml root header conversion after table":
		return map[string]any{"settings": "off", "other": map[string]any{"value": int64(2)}}
	case "toml header to array":
		return map[string]any{"settings": []any{"x"}, "other": map[string]any{"value": int64(2)}}
	case "commented toml array to inline table":
		return map[string]any{"value": map[string]any{"next": int64(3)}, "other": int64(4)}
	case "commented jsonc object to array":
		return map[string]any{"value": []any{int64(1), int64(2)}, "tail": int64(4)}
	case "nested indexed parent growth", "nested indexed parent growth crlf":
		return map[string]any{"parents": []any{map[string]any{"name": "first", "children": []any{map[string]any{"name": "old"}, map[string]any{"name": "new"}}}, map[string]any{"name": "second"}}}
	case "nested indexed parent growth quoted and interleaved":
		return map[string]any{"parents": []any{map[string]any{"name": "first", "special.key": []any{map[string]any{"name": "old"}, map[string]any{"name": "new"}}, "info": map[string]any{"note": "keep"}}, map[string]any{"name": "second"}}}
	case "array-table Set keeps duplicate ordered rows":
		return map[string]any{"items": []any{map[string]any{"name": "same"}, map[string]any{"name": "same"}}}
	case "nested final singleton removal":
		return map[string]any{"parents": []any{map[string]any{"name": "first", "children": []any{}}, map[string]any{"name": "second", "children": []any{map[string]any{"name": "keep"}}}}}
	case "root header-array append":
		return map[string]any{"items": []any{map[string]any{"name": "first"}, map[string]any{"name": "second"}}, "tail": map[string]any{"value": int64(2)}}
	case "nested header-array append":
		return map[string]any{"parents": []any{map[string]any{"name": "first", "children": []any{map[string]any{"name": "old"}, map[string]any{"name": "new"}}}, map[string]any{"name": "second"}}}
	default:
		panic("missing structural expected values for " + name)
	}
}

func TestConfigEditHeaderArrayAppendDuplicateIsNoop(t *testing.T) {
	cases := []struct {
		name, source string
		path         []configdoc.Step
	}{
		{"root", "[[items]]\nname='same'\n", []configdoc.Step{{Key: "items"}}},
		{"nested", "[[parents]]\nname='first'\n[[parents.children]]\nname='same'\n", []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}, {Key: "children"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mutation := Mutation{Action: Append, Path: tc.path, Value: map[string]any{"name": "same"}}
			plan, err := PlanEdit([]byte(tc.source), configdoc.TOML, mutation)
			if err != nil {
				t.Fatalf("PlanEdit duplicate: %v", err)
			}
			if !plan.Noop || !bytes.Equal(plan.Updated, []byte(tc.source)) {
				t.Fatalf("duplicate append plan = noop %t, bytes %q", plan.Noop, plan.Updated)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.source), 0600); err != nil {
				t.Fatal(err)
			}
			beforeInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(path, configdoc.TOML, mutation, ApplyOptions{}); err != nil {
				t.Fatalf("Apply duplicate: %v", err)
			}
			afterInfo, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(beforeInfo, afterInfo) || !bytes.Equal(got, []byte(tc.source)) {
				t.Fatalf("duplicate append wrote target: %q", got)
			}
		})
	}
}
