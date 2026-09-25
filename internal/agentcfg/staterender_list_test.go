package agentcfg

// staterender_list_test.go pins per-entry capture at list paths (OQ-AL1) through the real
// stateful state machine, one simulated boot at a time: ComposeStateful reads the file and
// the three sidecars, and the test persists what it returns exactly as the boot writer does.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// listHome is one surface's on-disk state across boots: the file and the three sidecars.
type listHome struct {
	file, last, overlay, listCap []byte
	lastPresent                  bool
}

// boot runs one stateful render and persists its outputs.
func (h *listHome) boot(t *testing.T, in Inputs) *StatefulOutput {
	t.Helper()
	out, err := ComposeStateful(StatefulInputs{
		Base: in, CurrentBytes: h.file, LastRenderPresent: h.lastPresent,
		LastRenderBytes: h.last, OverlayJSON: h.overlay, ListCaptureJSON: h.listCap,
	})
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	h.file = append(append([]byte(nil), out.Result.Encoded...), '\n')
	h.last = append(append([]byte(nil), out.LastRenderBytes...), '\n')
	h.lastPresent = true
	h.overlay = out.OverlayJSON
	if out.ListCaptureJSON != nil {
		h.listCap = out.ListCaptureJSON
	}
	return out
}

// edit changes the file the way an agent would between boots.
func (h *listHome) edit(t *testing.T, fn func(m map[string]any)) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(h.file, &m); err != nil {
		t.Fatal(err)
	}
	fn(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	h.file = b
}

func (h *listHome) packages(t *testing.T) []any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(h.file, &m); err != nil {
		t.Fatal(err)
	}
	a, _ := m["packages"].([]any)
	return a
}

func overlayHas(t *testing.T, overlay []byte, key string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(overlay, &m); err != nil {
		t.Fatalf("overlay %s: %v", overlay, err)
	}
	_, has := m[key]
	return has
}

func kiloList(t *testing.T) []ListContribution {
	return []ListContribution{mustList(t, "personal", "/packages", `["git:github.com/mschulkind/kilo-pi-provider"]`)}
}

const kilo = "git:github.com/mschulkind/kilo-pi-provider"

// THE MOTIVATING CASE, end to end at the engine: `pi install` appends to packages; the
// agent's entry is kept as the user's, the pack's entries never enter the capture, and
// dropping the pack removes exactly its entry.
func TestStatefulListAppendIsTheUsersAndDropRemovesThePacks(t *testing.T) {
	h := &listHome{}
	with := Inputs{Surface: listSurface(), Lists: kiloList(t)}
	h.boot(t, with) // first render: adopts nothing (no file)
	if got, want := h.packages(t), []any{"npm:owner-a", "npm:owner-b", kilo}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first render packages = %#v, want %#v", got, want)
	}

	h.edit(t, func(m map[string]any) { m["packages"] = append(m["packages"].([]any), "npm:pi-installed") })
	out := h.boot(t, with)
	if overlayHas(t, out.OverlayJSON, "packages") {
		t.Fatalf("the whole array was captured into the overlay — the freeze OQ-AL1 fixes:\n%s", out.OverlayJSON)
	}
	if !strings.Contains(string(out.ListCaptureJSON), "npm:pi-installed") ||
		strings.Contains(string(out.ListCaptureJSON), kilo) {
		t.Fatalf("list capture = %s — want the agent's entry recorded and the pack's not", out.ListCaptureJSON)
	}
	if got, want := h.packages(t), []any{"npm:owner-a", "npm:owner-b", kilo, "npm:pi-installed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the append packages = %#v, want %#v", got, want)
	}

	// The pack is dropped: its entry vanishes, the agent's stays.
	h.boot(t, Inputs{Surface: listSurface()})
	if got, want := h.packages(t), []any{"npm:owner-a", "npm:owner-b", "npm:pi-installed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after the drop packages = %#v, want %#v", got, want)
	}
}

// A user deleting a pack-added entry is a per-entry removal the capture holds — across
// renders, and across the pack being dropped and re-added.
func TestStatefulListUserRemovalPersistsAcrossDropAndReAdd(t *testing.T) {
	h := &listHome{}
	with := Inputs{Surface: listSurface(), Lists: kiloList(t)}
	h.boot(t, with)
	h.edit(t, func(m map[string]any) {
		var kept []any
		for _, e := range m["packages"].([]any) {
			if e != kilo {
				kept = append(kept, e)
			}
		}
		m["packages"] = kept
	})
	h.boot(t, with)
	h.boot(t, with)
	if got := h.packages(t); containsEntry(got, kilo) {
		t.Fatalf("a deleted contributed entry came back: %#v", got)
	}
	h.boot(t, Inputs{Surface: listSurface()})
	h.boot(t, with)
	if got := h.packages(t); containsEntry(got, kilo) {
		t.Fatalf("the removal did not survive a drop and re-add: %#v", got)
	}
}

// Removing an OWNER entry is per entry too, and an entry first contributed later still
// appears — "an array emptied in-jail is a per-entry capture instead"
// (docs/reference/pack-system.md#config-list-capture).
func TestStatefulListEmptiedArrayStillShowsLaterContributions(t *testing.T) {
	h := &listHome{}
	h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	h.edit(t, func(m map[string]any) { m["packages"] = []any{} })
	h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	if got := h.packages(t); len(got) != 0 {
		t.Fatalf("an emptied array came back with %#v", got)
	}
	later := append(kiloList(t), mustList(t, "late", "/packages", `["npm:late"]`))
	h.boot(t, Inputs{Surface: listSurface(), Lists: later})
	if got := h.packages(t); !reflect.DeepEqual(got, []any{"npm:late"}) {
		t.Fatalf("packages = %#v, want only the later contribution", got)
	}
}

// Deleting the KEY, or replacing it with a non-array, stays a whole-value capture that
// outranks the contributions, with a boot note naming `yolo config reset`.
func TestStatefulListDeletionAndNonArrayAreWholeValue(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(m map[string]any)
		want any
	}{
		{"deleted", func(m map[string]any) { delete(m, "packages") }, nil},
		{"non-array", func(m map[string]any) { m["packages"] = "none" }, "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &listHome{}
			with := Inputs{Surface: listSurface(), Lists: kiloList(t)}
			h.boot(t, with)
			h.edit(t, tc.edit)
			out := h.boot(t, with)
			var m map[string]any
			_ = json.Unmarshal(h.file, &m)
			if !reflect.DeepEqual(m["packages"], tc.want) {
				t.Fatalf("packages = %#v, want %#v", m["packages"], tc.want)
			}
			if len(out.ListNotes) == 0 || !strings.Contains(out.ListNotes[0], "yolo config reset pi/settings") {
				t.Fatalf("no boot note for the masking capture: %v", out.ListNotes)
			}
		})
	}
}

// MIGRATION: a whole array an older yolo captured at what is now a list path converts on
// the first steady-state render — add = O − B, remove = B − O — and is deleted from the
// overlay. The result: B's order, then O's additions, then the contributions.
func TestStatefulListMigratesLegacyWholeArrayCapture(t *testing.T) {
	// The old whole-array capture: the user removed owner-b and added mine; the overlay holds
	// the whole array and the last render equals it (the capture won).
	legacy := []byte(`{"packages":["mine","npm:owner-a"]}`)
	lastRender := []byte(`{"packages":["mine","npm:owner-a"]}`)
	h := &listHome{file: lastRender, last: lastRender, lastPresent: true, overlay: legacy}
	out := h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	if overlayHas(t, out.OverlayJSON, "packages") {
		t.Fatalf("the legacy array survived in the overlay: %s", out.OverlayJSON)
	}
	recs := ParseListCapture(out.ListCaptureJSON)
	if got := recs["/packages"]; !reflect.DeepEqual(got.Add, []any{"mine"}) ||
		!reflect.DeepEqual(got.Remove, []any{"npm:owner-b"}) {
		t.Fatalf("converted record = %#v, want add [mine] remove [npm:owner-b]", got)
	}
	if got, want := h.packages(t), []any{"npm:owner-a", kilo, "mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packages after migration = %#v, want %#v", got, want)
	}
	if len(out.ListNotes) == 0 || !strings.Contains(out.ListNotes[0], "converted") {
		t.Fatalf("the conversion was not reported: %v", out.ListNotes)
	}
}

// A legacy TOMBSTONE at the path is not converted: it stays a whole-value deletion and
// the boot says so.
func TestStatefulListLegacyTombstoneStaysAndIsNoted(t *testing.T) {
	last := []byte(`{}`)
	h := &listHome{file: last, last: last, lastPresent: true, overlay: []byte(`{"packages":null}`)}
	out := h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	if got := h.packages(t); got != nil {
		t.Fatalf("a captured deletion was overridden by the list: %#v", got)
	}
	if len(out.ListNotes) == 0 {
		t.Fatal("no note for the masking tombstone")
	}
}

// ADOPTION (no trusted last render): only adds, measured against the fold below the
// contributions — so the user's own entry that a pack ALSO contributes is theirs, and
// survives the pack being dropped.
func TestStatefulListAdoptionKeepsTheUsersMatchingEntry(t *testing.T) {
	h := &listHome{file: []byte(`{"packages":["npm:owner-a","npm:owner-b","` + kilo + `","mine"]}`)}
	out := h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	if !out.FirstMigration || overlayHas(t, out.OverlayJSON, "packages") {
		t.Fatalf("adoption put the whole array in the overlay: %s", out.OverlayJSON)
	}
	if got := ParseListCapture(out.ListCaptureJSON)["/packages"]; !reflect.DeepEqual(got.Add, []any{kilo, "mine"}) {
		t.Fatalf("adopted record = %#v, want add [kilo mine]", got)
	}
	h.boot(t, Inputs{Surface: listSurface()})
	if got, want := h.packages(t), []any{"npm:owner-a", "npm:owner-b", kilo, "mine"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after drop packages = %#v, want %#v — the user's independently declared entry must stay", got, want)
	}
}

// THE LAYER-LESS CAPTURE (captureSurfaceAt / capture-on-terminate): no contributions are
// passed, so the list paths come from the sidecar alone — and the whole array must still not
// be captured into the overlay.
func TestStatefulListLayerlessCaptureLearnsPathsFromTheSidecar(t *testing.T) {
	h := &listHome{}
	h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	h.edit(t, func(m map[string]any) { m["packages"] = append(m["packages"].([]any), "npm:added") })
	out, err := ComposeStateful(StatefulInputs{
		Base:              Inputs{Surface: listSurface()},
		CurrentBytes:      h.file,
		LastRenderPresent: true, LastRenderBytes: h.last,
		OverlayJSON: h.overlay, ListCaptureJSON: h.listCap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if overlayHas(t, out.OverlayJSON, "packages") {
		t.Fatalf("a layer-less capture froze the whole array: %s", out.OverlayJSON)
	}
	if !strings.Contains(string(out.ListCaptureJSON), "npm:added") {
		t.Fatalf("the layer-less capture lost the entry: %s", out.ListCaptureJSON)
	}
}

// A surface with no list path writes no list-capture sidecar and composes exactly as before.
func TestStatefulListAbsentMeansNoSidecar(t *testing.T) {
	h := &listHome{}
	out := h.boot(t, Inputs{Surface: listSurface()})
	if out.ListCaptureJSON != nil || len(out.Result.Lists) != 0 {
		t.Fatalf("a surface with no list wrote a list capture: %s", out.ListCaptureJSON)
	}
}

// Every live path gets an entry, empty or not; a corrupt sidecar reads as absent.
func TestStatefulListSidecarShape(t *testing.T) {
	h := &listHome{listCap: []byte("{not json")}
	out := h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	var got map[string]map[string][]any
	if err := json.Unmarshal(out.ListCaptureJSON, &got); err != nil {
		t.Fatalf("sidecar %s: %v", out.ListCaptureJSON, err)
	}
	rec, ok := got["/packages"]
	if !ok || rec["add"] == nil || rec["remove"] == nil || len(rec["add"])+len(rec["remove"]) != 0 {
		t.Fatalf("sidecar = %s, want an empty record for the live path", out.ListCaptureJSON)
	}
}

// NARROWING: a managed layer holding the path makes the record dead, so it is dropped.
func TestStatefulListRecordNarrowedByManaged(t *testing.T) {
	h := &listHome{}
	h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	h.edit(t, func(m map[string]any) { m["packages"] = append(m["packages"].([]any), "npm:added") })
	s := listSurface()
	s.Managed = map[string]any{"packages": []any{"pinned"}}
	out := h.boot(t, Inputs{Surface: s, Lists: kiloList(t)})
	if n := ListCaptureEntryCount(out.ListCaptureJSON); n != 0 {
		t.Fatalf("a record under managed survived: %s", out.ListCaptureJSON)
	}
	if got := h.packages(t); !reflect.DeepEqual(got, []any{"pinned"}) {
		t.Fatalf("managed did not win: %#v", got)
	}
}

// nestedListSurface puts the list path one object deep, so an ANCESTOR can be deleted or
// replaced while the path itself is never touched directly.
func nestedListSurface() Inputs {
	return Inputs{
		Surface: manifest.Surface{Agent: "pi", Name: "settings", Codec: "json", Path: "~/.pi/agent/settings.json",
			Defaults: map[string]any{"a": map[string]any{"packages": []any{"d1"}}}},
	}
}

func nestedPackages(t *testing.T, h *listHome) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(h.file, &m); err != nil {
		t.Fatal(err)
	}
	a, _ := m["a"].(map[string]any)
	return a["packages"]
}

// DELETE, THEN RECREATE. The user removes a contributed entry (a per-entry removal), deletes
// the key (a whole-value capture that masks the list), then recreates the array with one
// entry of their own. The next boot must render what the user wrote — not the whole assembled
// list, and not the contributed entry they removed one by one — and the captured deletion must
// be gone from the overlay. It then has to hold across a drop and a re-add.
func TestStatefulListDeleteThenRecreateKeepsWhatTheUserWrote(t *testing.T) {
	h := &listHome{}
	with := Inputs{Surface: listSurface(), Lists: kiloList(t)}
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { m["packages"] = []any{"npm:owner-a", "npm:owner-b"} })
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { delete(m, "packages") })
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { m["packages"] = []any{"npm:x"} })
	out := h.boot(t, with)
	if overlayHas(t, out.OverlayJSON, "packages") {
		t.Fatalf("the captured deletion survived the array reappearing: %s", out.OverlayJSON)
	}
	for _, step := range []struct {
		name string
		in   Inputs
	}{{"recreated", with}, {"booted again", with}, {"pack dropped", Inputs{Surface: listSurface()}}, {"re-added", with}} {
		if step.name != "recreated" {
			h.boot(t, step.in)
		}
		if got := h.packages(t); !reflect.DeepEqual(got, []any{"npm:x"}) {
			t.Fatalf("%s: packages = %#v, want exactly what the user wrote, [npm:x]\nlist capture: %s",
				step.name, got, h.listCap)
		}
	}
}

// The same sequence one level down: deleting an ANCESTOR of the list path and recreating it
// must not leave the ancestor's tombstone masking the recreated array for ever.
func TestStatefulListRecreatedAncestorEndsItsTombstone(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recreate map[string]any
	}{
		{"array only", map[string]any{"packages": []any{"x"}}},
		{"with a sibling", map[string]any{"packages": []any{"x"}, "other": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &listHome{}
			with := nestedListSurface()
			with.Lists = []ListContribution{mustList(t, "p", "/a/packages", `["k"]`)}
			h.boot(t, with)
			h.edit(t, func(m map[string]any) { delete(m, "a") })
			h.boot(t, with)
			h.edit(t, func(m map[string]any) { m["a"] = tc.recreate })
			h.boot(t, with)
			h.boot(t, with)
			if got := nestedPackages(t, h); !reflect.DeepEqual(got, []any{"x"}) {
				t.Fatalf("a/packages = %#v, want [x]; file %s overlay %s list capture %s", got, h.file, h.overlay, h.listCap)
			}
			var m map[string]any
			_ = json.Unmarshal(h.file, &m)
			if a, _ := m["a"].(map[string]any); a["other"] != tc.recreate["other"] {
				t.Fatalf("the recreated ancestor's siblings differ from what the user wrote: %s", h.file)
			}
		})
	}
}

// An ancestor replaced by a NON-OBJECT and later restored: the contributed entry the restored
// array still holds is yolo's, not the user's, so dropping the pack removes it.
func TestStatefulListRestoredAncestorNeverAdoptsContributions(t *testing.T) {
	h := &listHome{}
	with := nestedListSurface()
	with.Lists = []ListContribution{mustList(t, "p", "/a/packages", `["k"]`)}
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { m["a"] = "off" })
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { m["a"] = map[string]any{"packages": []any{"d1", "k", "x"}} })
	out := h.boot(t, with)
	if strings.Contains(string(out.OverlayJSON), "packages") {
		t.Fatalf("the restored array was captured whole into the overlay: %s", out.OverlayJSON)
	}
	h.boot(t, with)
	if got := nestedPackages(t, h); !reflect.DeepEqual(got, []any{"d1", "k", "x"}) {
		t.Fatalf("with the pack a/packages = %#v, want [d1 k x]", got)
	}
	for _, n := range out.ListNotes {
		if strings.Contains(n, "converted") {
			t.Fatalf("a restore was reported as an older yolo's capture: %q", n)
		}
	}
	h.boot(t, nestedListSurface())
	if got := nestedPackages(t, h); !reflect.DeepEqual(got, []any{"d1", "x"}) {
		t.Fatalf("after the drop a/packages = %#v, want [d1 x] — the pack's entry leaked into the user's record", got)
	}
}

// An in-jail REORDER (or de-duplication) at a list path cannot be kept — the record is by
// presence — so the boot must SAY so rather than silently put the old order back.
func TestStatefulListReorderIsReportedNotSilentlyLost(t *testing.T) {
	h := &listHome{}
	with := Inputs{Surface: listSurface(), Lists: kiloList(t)}
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { m["packages"] = []any{kilo, "npm:owner-a", "npm:owner-b"} })
	out := h.boot(t, with)
	found := false
	for _, n := range out.ListNotes {
		if strings.Contains(n, "order") && strings.Contains(n, "/packages") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a reorder at a list path was discarded without a note: %v", out.ListNotes)
	}
	// A pure append keeps the order and says nothing about it.
	h.edit(t, func(m map[string]any) { m["packages"] = append(m["packages"].([]any), "npm:y") })
	if out := h.boot(t, with); len(out.ListNotes) != 0 {
		t.Fatalf("an append was reported as a reorder: %v", out.ListNotes)
	}
}

// NARROWING, the computed half: a computed layer holding the path makes the record dead.
func TestStatefulListRecordNarrowedByComputed(t *testing.T) {
	h := &listHome{}
	h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t)})
	h.edit(t, func(m map[string]any) { m["packages"] = append(m["packages"].([]any), "npm:added") })
	out := h.boot(t, Inputs{Surface: listSurface(), Lists: kiloList(t),
		Computed: map[string]any{"packages": []any{"generated"}}})
	if n := ListCaptureEntryCount(out.ListCaptureJSON); n != 0 {
		t.Fatalf("a record under computed survived: %s", out.ListCaptureJSON)
	}
	if got := h.packages(t); !reflect.DeepEqual(got, []any{"generated"}) {
		t.Fatalf("computed did not win: %#v", got)
	}
}

// PROVENANCE: a key whose array a captured record changed is the user's, labelled `overlay`
// exactly as the whole-array capture labelled it — never the lower layer's label, which a
// host revert reads as "yolo wrote all of this" and deletes. That holds for a key the record
// CREATED too.
func TestStatefulListCapturedKeyIsLabelledOverlay(t *testing.T) {
	h := &listHome{}
	with := Inputs{Surface: listSurface(), Lists: kiloList(t)}
	h.boot(t, with)
	h.edit(t, func(m map[string]any) { m["packages"] = append(m["packages"].([]any), "npm:mine") })
	out := h.boot(t, with)
	if got := out.Result.Provenance["packages"]; got != layerOverlay {
		t.Fatalf("packages provenance = %q after a captured append, want %q", got, layerOverlay)
	}
	if LayerAsserted(out.Result.Provenance["packages"]) {
		t.Fatal("a captured list key reads as asserted")
	}

	// Created by the record alone: no layer below holds the key.
	res, err := Compose(Inputs{Surface: manifest.Surface{Agent: "pi", Name: "settings", Codec: "json",
		Path: "~/.pi/agent/settings.json"},
		ListCapture: map[string]ListRecord{"/extra": {Add: []any{"mine"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Provenance["extra"]; got != layerOverlay {
		t.Fatalf("a key only the list capture created is labelled %q, want %q", got, layerOverlay)
	}
}
