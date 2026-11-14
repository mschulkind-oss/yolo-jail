package configedit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

type scopeBindingCase struct {
	name, before, after string
	mutation            Mutation
	want                map[string]any
}

func scopeBindingCases() []scopeBindingCase {
	return []scopeBindingCase{
		{
			name:     "nested header set after info table",
			before:   "[parent]\nx=1\n[parent.info]\n# keep-info\na=3\n[parent.child]\n# keep-child\ny=2\n[[parent.siblings]]\nname='one'\n",
			after:    "[parent]\nx=1\nchild = \"value\"\n[parent.info]\n# keep-info\na=3\n# keep-child\n[[parent.siblings]]\nname='one'\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parent"}, {Key: "child"}}, Value: "value"},
			want:     map[string]any{"parent": map[string]any{"x": int64(1), "info": map[string]any{"a": int64(3)}, "child": "value", "siblings": []any{map[string]any{"name": "one"}}}},
		},
		{
			name:     "implicit dotted parent add",
			before:   "[a.b]\nkeep=1\n[tail]\nz=2\n",
			after:    "a.new = true\n[a.b]\nkeep=1\n[tail]\nz=2\n",
			mutation: Mutation{Action: Add, Path: []configdoc.Step{{Key: "a"}, {Key: "new"}}, Value: true},
			want:     map[string]any{"a": map[string]any{"b": map[string]any{"keep": int64(1)}, "new": true}, "tail": map[string]any{"z": int64(2)}},
		},
		{
			name:     "quoted dotted nested header set",
			before:   "[a.\"special.key\".info]\nkeep=1\n[a.\"special.key\".child]\nold=2\n[tail]\nz=3\n",
			after:    "[a.\"special.key\"]\nchild = \"done\"\n[a.\"special.key\".info]\nkeep=1\n[tail]\nz=3\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "a"}, {Key: "special.key"}, {Key: "child"}}, Value: "done"},
			want:     map[string]any{"a": map[string]any{"special.key": map[string]any{"info": map[string]any{"keep": int64(1)}, "child": "done"}}, "tail": map[string]any{"z": int64(3)}},
		},
		{
			name:     "root set follows array header",
			before:   "[[items]]\nname='one'\n[settings]\nold=2\n# keep-settings\n[tail]\nz=3\n",
			after:    "settings = \"off\"\n[[items]]\nname='one'\n# keep-settings\n[tail]\nz=3\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "settings"}}, Value: "off"},
			want:     map[string]any{"items": []any{map[string]any{"name": "one"}}, "settings": "off", "tail": map[string]any{"z": int64(3)}},
		},
		{
			name:     "nested final singleton removal in indexed parent",
			before:   "[[parents]]\nid=1\n[parents.info]\n# keep-info\nnote='safe'\n[[parents.children]]\n# keep-child\nname='only'\n[[parents.other]]\nname='other'\n[[parents]]\nid=2\n[[parents.children]]\nname='keep'\n",
			after:    "[[parents]]\nid=1\nchildren = []\n[parents.info]\n# keep-info\nnote='safe'\n# keep-child\n[[parents.other]]\nname='other'\n[[parents]]\nid=2\n[[parents.children]]\nname='keep'\n",
			mutation: Mutation{Action: Remove, Path: []configdoc.Step{{Key: "parents"}, {Index: 0, IsIndex: true}, {Key: "children"}, {Index: 0, IsIndex: true}}},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "info": map[string]any{"note": "safe"}, "children": []any{}, "other": []any{map[string]any{"name": "other"}}},
				map[string]any{"id": int64(2), "children": []any{map[string]any{"name": "keep"}}},
			}},
		},
	}
}

func TestConfigEditTOMLScopeBindingPlanEdit(t *testing.T) {
	for _, tc := range scopeBindingCases() {
		t.Run(tc.name, func(t *testing.T) {
			want, err := semanticValue(tc.want, configdoc.TOML)
			if err != nil {
				t.Fatalf("prepare independent intended values: %v", err)
			}
			plan, err := PlanEdit([]byte(tc.before), configdoc.TOML, tc.mutation)
			if err != nil {
				t.Fatalf("PlanEdit: %v", err)
			}
			if !bytes.Equal(plan.Updated, []byte(tc.after)) {
				t.Fatalf("PlanEdit bytes differ\nwant %q\n got %q", tc.after, plan.Updated)
			}
			parsed, err := configdoc.Parse(plan.Updated, configdoc.TOML)
			if err != nil {
				t.Fatalf("updated document does not parse: %v", err)
			}
			if !semanticEqual(parsed.Values, want) {
				t.Fatalf("PlanEdit values = %#v, want independently built %#v", parsed.Values, want)
			}
		})
	}
}

func TestConfigEditTOMLScopeBindingApply(t *testing.T) {
	for _, tc := range scopeBindingCases() {
		t.Run(tc.name, func(t *testing.T) {
			want, err := semanticValue(tc.want, configdoc.TOML)
			if err != nil {
				t.Fatalf("prepare independent intended values: %v", err)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.before), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(path, configdoc.TOML, tc.mutation, ApplyOptions{}); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, []byte(tc.after)) {
				t.Fatalf("Apply bytes differ\nwant %q\n got %q", tc.after, got)
			}
			parsed, err := configdoc.Parse(got, configdoc.TOML)
			if err != nil {
				t.Fatalf("applied document does not parse: %v", err)
			}
			if !semanticEqual(parsed.Values, want) {
				t.Fatalf("Apply values = %#v, want independently built %#v", parsed.Values, want)
			}
		})
	}
}
