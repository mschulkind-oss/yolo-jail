package configedit

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
)

type highRepairCase struct {
	name, before, after string
	mutation            Mutation
	want                map[string]any
}

func highRepairCases() []highRepairCase {
	steps := func(keys ...string) []configdoc.Step {
		path := make([]configdoc.Step, len(keys))
		for i, key := range keys {
			path[i] = configdoc.Step{Key: key}
		}
		return path
	}
	row := func(index int) configdoc.Step {
		return configdoc.Step{Index: index, IsIndex: true}
	}
	return []highRepairCase{
		{
			name:     "add stays inside owning table extent",
			before:   "[parent]\nx=1\n[other]\nz=3\n[parent.child]\ny=2\n",
			after:    "[parent]\nx=1\nnew = true\n[other]\nz=3\n[parent.child]\ny=2\n",
			mutation: Mutation{Action: Add, Path: append(steps("parent"), configdoc.Step{Key: "new"}), Value: true},
			want:     map[string]any{"parent": map[string]any{"x": int64(1), "new": true, "child": map[string]any{"y": int64(2)}}, "other": map[string]any{"z": int64(3)}},
		},
		{
			name:     "nested header set stays inside owning table extent",
			before:   "[parent]\nx=1\n[other]\nz=3\n[parent.child]\ny=2\n",
			after:    "[parent]\nx=1\nchild = \"v\"\n[other]\nz=3\n",
			mutation: Mutation{Action: Set, Path: steps("parent", "child"), Value: "v"},
			want:     map[string]any{"parent": map[string]any{"x": int64(1), "child": "v"}, "other": map[string]any{"z": int64(3)}},
		},
		{
			name:     "descendant before explicit parent does not bind insertion at root",
			before:   "[parent.child]\ny=2\n[parent]\nx=1\n[tail]\nz=3\n",
			after:    "[parent]\nx=1\nchild = \"v\"\n[tail]\nz=3\n",
			mutation: Mutation{Action: Set, Path: steps("parent", "child"), Value: "v"},
			want:     map[string]any{"parent": map[string]any{"x": int64(1), "child": "v"}, "tail": map[string]any{"z": int64(3)}},
		},
		{
			name:     "root append follows reopened descendant",
			before:   "[[items]]\nname='first'\n[other]\nx=1\n[items.info]\ny=2\n",
			after:    "[[items]]\nname='first'\n[other]\nx=1\n[items.info]\ny=2\n[[items]]\nname = \"second\"\n",
			mutation: Mutation{Action: Append, Path: steps("items"), Value: map[string]any{"name": "second"}},
			want:     map[string]any{"items": []any{map[string]any{"name": "first", "info": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}, "other": map[string]any{"x": int64(1)}},
		},
		{
			name:     "nested append follows reopened descendant within indexed parent",
			before:   "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[parents.info]\nx=1\n[parents.children.details]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[parents.info]\nx=1\n[parents.children.details]\ny=2\n[[parents.children]]\nname = \"second\"\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Append, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "children"}}, Value: map[string]any{"name": "second"}},
			want:     map[string]any{"parents": []any{map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}, "info": map[string]any{"x": int64(1)}}, map[string]any{"id": int64(2)}}},
		},
		{
			name:     "middle indexed parent append stops before later parent",
			before:   "[[parents]]\nid=0\n[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[parents.info]\nx=1\n[parents.children.details]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=0\n[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[parents.info]\nx=1\n[parents.children.details]\ny=2\n[[parents.children]]\nname = \"second\"\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Append, Path: []configdoc.Step{{Key: "parents"}, row(1), {Key: "children"}}, Value: map[string]any{"name": "second"}},
			want:     map[string]any{"parents": []any{map[string]any{"id": int64(0)}, map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}, "info": map[string]any{"x": int64(1)}}, map[string]any{"id": int64(2)}}},
		},
		{
			name:     "indexed parent Set growth follows reopened descendant",
			before:   "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[parents.info]\nx=1\n[parents.children.details]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[parents.info]\nx=1\n[parents.children.details]\ny=2\n[[parents.children]]\nname = \"second\"\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, row(0)}, Value: map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}, "info": map[string]any{"x": int64(1)}}},
			want:     map[string]any{"parents": []any{map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}, "info": map[string]any{"x": int64(1)}}, map[string]any{"id": int64(2)}}},
		},
		{
			name:     "Set synthesizes quoted implicit parent below indexed ancestry",
			before:   "[[parents]]\nid=1\n[parents.\"special.group\".child]\nx=2\n[tail]\nz=3\n",
			after:    "[[parents]]\nid=1\n[parents.\"special.group\"]\nchild = \"v\"\n[tail]\nz=3\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "special.group"}, {Key: "child"}}, Value: "v"},
			want:     map[string]any{"parents": []any{map[string]any{"id": int64(1), "special.group": map[string]any{"child": "v"}}}, "tail": map[string]any{"z": int64(3)}},
		},
		{
			name:     "Set synthesizes implicit parent below indexed ancestry",
			before:   "[[parents]]\nid=1\n[parents.group.child]\nx=2\n[tail]\nz=3\n",
			after:    "[[parents]]\nid=1\n[parents.group]\nchild = \"v\"\n[tail]\nz=3\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "group"}, {Key: "child"}}, Value: "v"},
			want:     map[string]any{"parents": []any{map[string]any{"id": int64(1), "group": map[string]any{"child": "v"}}}, "tail": map[string]any{"z": int64(3)}},
		},
		{
			name:     "singleton Remove synthesizes implicit parent below indexed ancestry",
			before:   "[[parents]]\nid=1\n[[parents.group.children]]\nname='only'\n[tail]\nz=3\n",
			after:    "[[parents]]\nid=1\n[parents.group]\nchildren = []\n[tail]\nz=3\n",
			mutation: Mutation{Action: Remove, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "group"}, {Key: "children"}, configdoc.Step{Index: 0, IsIndex: true}}},
			want:     map[string]any{"parents": []any{map[string]any{"id": int64(1), "group": map[string]any{"children": []any{}}}}, "tail": map[string]any{"z": int64(3)}},
		},
		{
			name:     "append copies header and field indentation spaces CRLF",
			before:   "  [[items]]\r\n\tname='first'\r\n",
			after:    "  [[items]]\r\n\tname='first'\r\n  [[items]]\r\n\tname = \"second\"\r\n",
			mutation: Mutation{Action: Append, Path: steps("items"), Value: map[string]any{"name": "second"}},
			want:     map[string]any{"items": []any{map[string]any{"name": "first"}, map[string]any{"name": "second"}}},
		},
		{
			name:     "append copies tab header and field indentation",
			before:   "\t[[items]]\n\t\tname='first'\n",
			after:    "\t[[items]]\n\t\tname='first'\n\t[[items]]\n\t\tname = \"second\"\n",
			mutation: Mutation{Action: Append, Path: steps("items"), Value: map[string]any{"name": "second"}},
			want:     map[string]any{"items": []any{map[string]any{"name": "first"}, map[string]any{"name": "second"}}},
		},
		{
			name:     "Set chooses earliest eligible descendant after explicit indexed parent",
			before:   "[[parents]]\nid=1\n[parents.group.child]\nx=2\n[parents.group]\nkeep=3\n[parents.group.info]\nz=4\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[parents.group]\nkeep=3\nchild = \"v\"\n[parents.group.info]\nz=4\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "group"}, {Key: "child"}}, Value: "v"},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "group": map[string]any{"keep": int64(3), "child": "v", "info": map[string]any{"z": int64(4)}}},
				map[string]any{"id": int64(2)},
			}},
		},
		{
			name:     "Add chooses earliest eligible descendant after explicit indexed parent",
			before:   "[[parents]]\nid=1\n[parents.group.child]\nx=2\n[parents.group]\nkeep=3\n[parents.group.info]\nz=4\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[parents.group.child]\nx=2\n[parents.group]\nkeep=3\nnew = true\n[parents.group.info]\nz=4\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Add, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "group"}, {Key: "new"}}, Value: true},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "group": map[string]any{"keep": int64(3), "new": true, "child": map[string]any{"x": int64(2)}, "info": map[string]any{"z": int64(4)}}},
				map[string]any{"id": int64(2)},
			}},
		},
		{
			name:     "root append follows reopened descendant after unrelated root table",
			before:   "[[items]]\nname='first'\n[other]\nx=1\n[items.info]\ny=2\n",
			after:    "[[items]]\nname='first'\n[other]\nx=1\n[items.info]\ny=2\n[[items]]\nname = \"second\"\n",
			mutation: Mutation{Action: Append, Path: steps("items"), Value: map[string]any{"name": "second"}},
			want:     map[string]any{"items": []any{map[string]any{"name": "first", "info": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}, "other": map[string]any{"x": int64(1)}},
		},
		{
			name:     "nested append crosses unrelated root header but stops at next owner row",
			before:   "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[other]\nx=1\n[parents.children.details]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[other]\nx=1\n[parents.children.details]\ny=2\n[[parents.children]]\nname = \"second\"\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Append, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "children"}}, Value: map[string]any{"name": "second"}},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}},
				map[string]any{"id": int64(2)},
			}, "other": map[string]any{"x": int64(1)}},
		},
		{
			name:     "indexed parent Set growth crosses unrelated root header but stops at next owner row",
			before:   "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[other]\nx=1\n[parents.children.details]\ny=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[[parents.children]]\nname='first'\n[other]\nx=1\n[parents.children.details]\ny=2\n[[parents.children]]\nname = \"second\"\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, row(0)}, Value: map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}}},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "children": []any{map[string]any{"name": "first", "details": map[string]any{"y": int64(2)}}, map[string]any{"name": "second"}}},
				map[string]any{"id": int64(2)},
			}, "other": map[string]any{"x": int64(1)}},
		},
		{
			name:     "Set synthesizes implicit indexed parent after unrelated root header",
			before:   "[[parents]]\nid=1\n[other]\nz=3\n[parents.group.child]\nx=2\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[other]\nz=3\n[parents.group]\nchild = \"v\"\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Set, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "group"}, {Key: "child"}}, Value: "v"},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "group": map[string]any{"child": "v"}},
				map[string]any{"id": int64(2)},
			}, "other": map[string]any{"z": int64(3)}},
		},
		{
			name:     "singleton Remove synthesizes implicit indexed parent after unrelated root header",
			before:   "[[parents]]\nid=1\n[other]\nz=3\n[[parents.group.children]]\nname='only'\n[[parents]]\nid=2\n",
			after:    "[[parents]]\nid=1\n[other]\nz=3\n[parents.group]\nchildren = []\n[[parents]]\nid=2\n",
			mutation: Mutation{Action: Remove, Path: []configdoc.Step{{Key: "parents"}, row(0), {Key: "group"}, {Key: "children"}, {Index: 0, IsIndex: true}}},
			want: map[string]any{"parents": []any{
				map[string]any{"id": int64(1), "group": map[string]any{"children": []any{}}},
				map[string]any{"id": int64(2)},
			}, "other": map[string]any{"z": int64(3)}},
		},
		{
			name:     "new fields inherit indentation from existing sibling spans",
			before:   "  [[items]]\n\tname='first'\n",
			after:    "  [[items]]\n\tname='first'\n  [[items]]\n\tname = \"second\"\n\tz = true\n",
			mutation: Mutation{Action: Append, Path: steps("items"), Value: map[string]any{"name": "second", "z": true}},
			want:     map[string]any{"items": []any{map[string]any{"name": "first"}, map[string]any{"name": "second", "z": true}}},
		},
	}
}

func TestConfigEditHighRepairPlanEdit(t *testing.T) {
	for _, tc := range highRepairCases() {
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
				got, _ := configdoc.EncodeValue(parsed.Values, configdoc.TOML)
				wantBytes, _ := configdoc.EncodeValue(want, configdoc.TOML)
				t.Fatalf("PlanEdit values = %s, err %v; want independently built %s", got, err, wantBytes)
			}
		})
	}
}

func TestConfigEditHighRepairApply(t *testing.T) {
	for _, tc := range highRepairCases() {
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
				gotValue, _ := configdoc.EncodeValue(parsed.Values, configdoc.TOML)
				wantBytes, _ := configdoc.EncodeValue(want, configdoc.TOML)
				t.Fatalf("Apply values = %s, err %v; want independently built %s", gotValue, err, wantBytes)
			}
		})
	}
}
