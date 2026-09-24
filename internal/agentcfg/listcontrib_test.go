package agentcfg

// listcontrib_test.go pins the config-list fold (Compose) and the rmw per-entry step
// (ReconcileInsertedList) — docs/design/additive-config-lists.md rules 1-4 and OQ-AL2.
// The stateful capture half is staterender_list_test.go.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// listSurface is pi's settings shape: an owner list under `packages`.
func listSurface() manifest.Surface {
	return manifest.Surface{
		Agent: "pi", Name: "settings", Codec: "json", Path: "~/.pi/agent/settings.json",
		Defaults: map[string]any{"packages": []any{"npm:owner-a", "npm:owner-b"}},
	}
}

func mustList(t *testing.T, pack, path, add string) ListContribution {
	t.Helper()
	l, err := NewListContribution(pack, path, json.RawMessage(add))
	if err != nil {
		t.Fatalf("NewListContribution(%s, %s, %s): %v", pack, path, add, err)
	}
	return l
}

func composeMap(t *testing.T, in Inputs) (*Result, map[string]any) {
	t.Helper()
	res, err := Compose(in)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	return res, res.ConfigMap()
}

// RULES 1 AND 2: the owner's entries in order, then the first occurrence of every
// contributed entry in contribution order; an entry already present (from the base or an
// earlier contribution) is not written twice, and an empty add is a no-op.
func TestComposeListAppendsDedupesInOrder(t *testing.T) {
	_, got := composeMap(t, Inputs{
		Surface: listSurface(),
		Lists: []ListContribution{
			mustList(t, "personal", "/packages", `["git:github.com/mschulkind/kilo-pi-provider","npm:owner-a"]`),
			mustList(t, "other", "/packages", `["npm:x","git:github.com/mschulkind/kilo-pi-provider"]`),
			mustList(t, "noop", "/packages", `[]`),
		},
	})
	want := []any{"npm:owner-a", "npm:owner-b", "git:github.com/mschulkind/kilo-pi-provider", "npm:x"}
	if !reflect.DeepEqual(got["packages"], want) {
		t.Fatalf("packages = %#v, want %#v", got["packages"], want)
	}
}

// Without the contribution the owner's list is exactly what renders (the "what done looks
// like" control), and an empty add creates nothing.
func TestComposeListEmptyAddCreatesNothing(t *testing.T) {
	s := listSurface()
	s.Defaults = map[string]any{"theme": "x"}
	res, got := composeMap(t, Inputs{Surface: s, Lists: []ListContribution{mustList(t, "p", "/packages", `[]`)}})
	if _, has := got["packages"]; has {
		t.Fatalf("an empty add created the path: %#v", got)
	}
	if len(res.Lists) != 0 {
		t.Fatalf("an empty add produced list provenance: %#v", res.Lists)
	}
}

// A missing path acts as [] and missing parents are created; the key the contributions
// created is labelled `config-list`, and that label is never an asserted layer.
func TestComposeListMissingPathCreatesParents(t *testing.T) {
	s := listSurface()
	s.Defaults = nil
	res, got := composeMap(t, Inputs{Surface: s, Lists: []ListContribution{
		mustList(t, "p", "/extensions/enabled", `["a"]`),
	}})
	ext, _ := got["extensions"].(map[string]any)
	if !reflect.DeepEqual(ext["enabled"], []any{"a"}) {
		t.Fatalf("extensions = %#v", got["extensions"])
	}
	if res.Provenance["extensions"] != LayerConfigList {
		t.Fatalf("provenance[extensions] = %q, want %q", res.Provenance["extensions"], LayerConfigList)
	}
	if LayerAsserted(LayerConfigList) {
		t.Fatal("LayerAsserted(config-list) = true — retirement and revert would remove the user's whole array")
	}
}

// A key that already existed keeps its own label; Result.Lists carries who added what.
func TestComposeListProvenanceNamesEachContributor(t *testing.T) {
	res, _ := composeMap(t, Inputs{Surface: listSurface(), Lists: []ListContribution{
		mustList(t, "personal", "/packages", `["kilo"]`),
	}})
	if res.Provenance["packages"] != LayerDefaults {
		t.Fatalf("provenance[packages] = %q, want defaults (the array existed below the list)",
			res.Provenance["packages"])
	}
	if len(res.Lists) != 1 || res.Lists[0].Path != "/packages" {
		t.Fatalf("Lists = %#v", res.Lists)
	}
	var sources []string
	for _, e := range res.Lists[0].Entries {
		sources = append(sources, e.Source)
	}
	want := []string{ListSourceBase, ListSourceBase, ListContributionLayer("personal")}
	if !reflect.DeepEqual(sources, want) || res.Lists[0].ReplacedBy != "" {
		t.Fatalf("sources = %v replacedBy=%q, want %v", sources, res.Lists[0].ReplacedBy, want)
	}
}

// RULE 3: a non-array at the path, or a non-object parent, refuses the render and names
// the surface, the path and the pack.
func TestComposeListTypeConflictRefuses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		defaults map[string]any
		path     string
	}{
		{"scalar at the path", map[string]any{"packages": "npm:a"}, "/packages"},
		{"object at the path", map[string]any{"packages": map[string]any{}}, "/packages"},
		{"scalar parent", map[string]any{"ext": "off"}, "/ext/enabled"},
		{"array parent", map[string]any{"ext": []any{"x"}}, "/ext/enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := listSurface()
			s.Defaults = tc.defaults
			_, err := Compose(Inputs{Surface: s, Lists: []ListContribution{mustList(t, "kilo", tc.path, `["k"]`)}})
			if err == nil {
				t.Fatal("Compose accepted a type conflict at a list path")
			}
			for _, want := range []string{"pi/settings", tc.path, "kilo"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %q", err, want)
				}
			}
		})
	}
}

// OQ-AL2: an ordinary config-overlay replacing the array drops the owner's entries, and a
// later list contribution still re-adds its entry, because additions follow every overlay.
func TestComposeListReAddsAfterOverlayReplacement(t *testing.T) {
	_, got := composeMap(t, Inputs{
		Surface:  listSurface(),
		Overlays: []Overlay{{Pack: "replacer", Data: map[string]any{"packages": []any{"npm:only"}}}},
		Lists:    []ListContribution{mustList(t, "kilo", "/packages", `["npm:owner-a","kilo"]`)},
	})
	want := []any{"npm:only", "npm:owner-a", "kilo"}
	if !reflect.DeepEqual(got["packages"], want) {
		t.Fatalf("packages = %#v, want %#v", got["packages"], want)
	}
}

// RULE 4: the capture overlay, computed and managed still replace the assembled array, and
// the provenance says which one did.
func TestComposeListHigherLayersReplaceTheAssembledArray(t *testing.T) {
	lists := []ListContribution{mustList(t, "kilo", "/packages", `["kilo"]`)}
	for _, tc := range []struct {
		name  string
		in    Inputs
		want  any
		layer string
	}{
		{"capture overlay", Inputs{Overlay: map[string]any{"packages": []any{"mine"}}}, []any{"mine"}, layerOverlay},
		{"computed", Inputs{Computed: map[string]any{"packages": []any{"computed"}}}, []any{"computed"}, layerComputed},
		{"managed", Inputs{}, []any{"pinned"}, layerManaged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Surface = listSurface()
			if tc.layer == layerManaged {
				in.Surface.Managed = map[string]any{"packages": []any{"pinned"}}
			}
			in.Lists = lists
			res, got := composeMap(t, in)
			if !reflect.DeepEqual(got["packages"], tc.want) {
				t.Fatalf("packages = %#v, want %#v", got["packages"], tc.want)
			}
			if len(res.Lists) != 1 || res.Lists[0].ReplacedBy != tc.layer {
				t.Fatalf("Lists = %#v, want ReplacedBy %q", res.Lists, tc.layer)
			}
		})
	}
}

// ELEMENT IDENTITY across decoders: a TOML file's integer and a pack.json's float are one
// entry, so the contribution is not appended a second time.
func TestComposeListTOMLIntEqualsJSONFloat(t *testing.T) {
	s := manifest.Surface{Agent: "codex", Name: "config", Codec: "toml", Path: "~/.codex/config.toml"}
	_, got := composeMap(t, Inputs{
		Surface:   s,
		HostBytes: []byte("ports = [1, 2]\n"),
		Lists:     []ListContribution{mustList(t, "p", "/ports", `[2, 3]`)},
	})
	arr, _ := got["ports"].([]any)
	if len(arr) != 3 {
		t.Fatalf("ports = %#v, want three entries (2 is the same entry in both decoders)", got["ports"])
	}
}

// A keyless surface has no path to address: Compose refuses rather than guessing.
func TestComposeListRefusesKeylessSurface(t *testing.T) {
	s := manifest.Surface{Agent: "x", Name: "lines", Codec: "lines", Path: "~/.x"}
	if _, err := Compose(Inputs{Surface: s, Lists: []ListContribution{mustList(t, "p", "/a", `["b"]`)}}); err == nil {
		t.Fatal("Compose accepted a config-list on a keyless surface")
	}
}

func TestNewListContributionRefusesRootAndNull(t *testing.T) {
	for _, tc := range []struct{ path, add string }{
		{"", `["a"]`}, {"packages", `["a"]`}, {"/packages", `["a", null]`}, {"/packages", `{"a":1}`},
	} {
		if _, err := NewListContribution("p", tc.path, json.RawMessage(tc.add)); err == nil {
			t.Errorf("NewListContribution(%q, %s) accepted", tc.path, tc.add)
		}
	}
}

// THE REFUSAL TABLE: every composing mechanism captures per entry now; a keyless surface and
// an unknown mechanism do not, and the refusal names the surface and its mode.
func TestListCaptureRefusal(t *testing.T) {
	obj := listSurface()
	for _, m := range []string{manifest.ModeStateful, manifest.ModeComputed, manifest.ModeRMW} {
		if r := ListCaptureRefusal(m, obj); r != "" {
			t.Errorf("mechanism %s refused: %s", m, r)
		}
	}
	if r := ListCaptureRefusal("guest-thing", obj); !strings.Contains(r, "pi/settings") ||
		!strings.Contains(r, "guest-thing") {
		t.Errorf("unknown mechanism refusal = %q", r)
	}
	lines := manifest.Surface{Agent: "x", Name: "hosts", Codec: "lines", Path: "~/.x", Mode: manifest.ModeStateful}
	if r := ListCaptureRefusal(manifest.ModeStateful, lines); !strings.Contains(r, "x/hosts") ||
		!strings.Contains(r, "stateful") {
		t.Errorf("keyless refusal = %q, want it to name the surface and its mode", r)
	}
}

// ── rmw ──────────────────────────────────────────────────────────────────────────────────

func TestReconcileInsertedListFirstRenderRecordsOnlyWhatItAppends(t *testing.T) {
	// The file already holds the user's "k" — a matching contribution is theirs, unrecorded.
	next, rec, changed := ReconcileInsertedList([]any{"u", "k"}, true, []any{"u", "k"},
		[]any{"k", "new"}, ListInsertRecord{})
	if !changed || !reflect.DeepEqual(next, []any{"u", "k", "new"}) {
		t.Fatalf("next = %#v changed=%v", next, changed)
	}
	if !reflect.DeepEqual(rec.Inserted, []any{"new"}) {
		t.Fatalf("inserted = %#v, want only the appended entry (k was the user's)", rec.Inserted)
	}
}

func TestReconcileInsertedListUserRemovalIsDeclined(t *testing.T) {
	// yolo inserted "k" last time; the user removed it since.
	next, rec, _ := ReconcileInsertedList([]any{"u"}, true, []any{"u"}, []any{"k"},
		ListInsertRecord{Inserted: []any{"k"}})
	if !reflect.DeepEqual(next, []any{"u"}) {
		t.Fatalf("a declined entry came back: %#v", next)
	}
	if !reflect.DeepEqual(rec.Declined, []any{"k"}) || len(rec.Inserted) != 0 {
		t.Fatalf("record = %#v", rec)
	}
	// And stays declined on the next render.
	next, _, _ = ReconcileInsertedList(next, true, next, []any{"k"}, rec)
	if !reflect.DeepEqual(next, []any{"u"}) {
		t.Fatalf("declined entry re-added on the second render: %#v", next)
	}
}

func TestReconcileInsertedListDroppedPackIsRemoved(t *testing.T) {
	next, rec, changed := ReconcileInsertedList([]any{"u", "k"}, true, []any{"u", "k"}, nil,
		ListInsertRecord{Inserted: []any{"k"}})
	if !changed || !reflect.DeepEqual(next, []any{"u"}) || len(rec.Inserted) != 0 {
		t.Fatalf("next = %#v rec = %#v", next, rec)
	}
}

func TestReconcileInsertedListAbsentKeyDeclinesNothing(t *testing.T) {
	next, rec, _ := ReconcileInsertedList(nil, false, nil, []any{"k"}, ListInsertRecord{Inserted: []any{"k"}})
	if !reflect.DeepEqual(next, []any{"k"}) || len(rec.Declined) != 0 {
		t.Fatalf("an absent key declined the entry: next=%#v rec=%#v", next, rec)
	}
}
