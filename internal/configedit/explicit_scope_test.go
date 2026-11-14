package configedit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

type explicitScopeCase struct {
	name, before, after string
	mutation            Mutation
	want                map[string]any
}

func explicitScopeCases() []explicitScopeCase {
	indexedGroup := []configdoc.Step{
		{Key: "parents"},
		{Index: 0, IsIndex: true},
		{Key: "group"},
	}
	return []explicitScopeCase{
		{
			name:     "Set inserts in explicit indexed table before unrelated header",
			before:   "[[parents]]\nid=1\n[parents.group]\nx=1\n[other]\nz=3\n[parents.group.child]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[parents.group]\nx=1\nchild = \"value\"\n[other]\nz=3\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Set, Path: append(append([]configdoc.Step(nil), indexedGroup...), configdoc.Step{Key: "child"}), Value: "value"},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "group": map[string]any{"x": int64(1), "child": "value"}},
				map[string]any{"id": int64(2)},
			}, "other": map[string]any{"z": int64(3)}},
		},
		{
			name:     "Add inserts in explicit indexed table before unrelated header",
			before:   "[[parents]]\nid=1\n[parents.group]\nx=1\n[other]\nz=3\n[parents.group.child]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[parents.group]\nx=1\nnew = true\n[other]\nz=3\n[parents.group.child]\ny=2\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Add, Path: append(append([]configdoc.Step(nil), indexedGroup...), configdoc.Step{Key: "new"}), Value: true},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "group": map[string]any{"x": int64(1), "new": true, "child": map[string]any{"y": int64(2)}}},
				map[string]any{"id": int64(2)},
			}, "other": map[string]any{"z": int64(3)}},
		},
	}
}

func TestConfigEditExplicitIndexedScopePlanEdit(t *testing.T) {
	for _, tc := range explicitScopeCases() {
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
			if err != nil || !semanticEqual(parsed.Values, want) {
				t.Fatalf("PlanEdit values = %#v, err %v; want independently built %#v", parsed.Values, err, want)
			}
		})
	}
}

func TestConfigEditExplicitIndexedScopeApply(t *testing.T) {
	for _, tc := range explicitScopeCases() {
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
			if err != nil || !semanticEqual(parsed.Values, want) {
				t.Fatalf("Apply values = %#v, err %v; want independently built %#v", parsed.Values, err, want)
			}
		})
	}
}
