package agentcfg

import (
	"reflect"
	"strings"
	"testing"
)

// selection_test.go pins the edge-triggered apply itself — the decision table, not the
// file I/O around it. Every row is one of the §5.1 behaviors: write on activation, never
// on absence, and a user's interactive edit standing until a NEW selection value differs
// from the last one yolo wrote. The boot-path pin that these decisions are REACHED lives
// in internal/entrypoint (selectionapply_test.go); this table is what makes a wrong
// decision diagnosable one row at a time rather than as "the file changed".

func TestApplySelectionDecides(t *testing.T) {
	cases := []struct {
		name      string
		selection map[string]any
		file      map[string]any
		record    map[string]any
		wantLift  map[string]any
		wantNext  map[string]any
	}{
		{
			// §5.1 write on activation: the key is not in the file, so nothing of the
			// user's is in the way.
			name:      "activation writes the selected value",
			selection: map[string]any{"model_provider": "llamacpp"},
			file:      map[string]any{},
			wantLift:  map[string]any{"model_provider": "llamacpp"},
			wantNext:  map[string]any{"model_provider": "llamacpp"},
		},
		{
			// The same selection again writes nothing new — but still lifts, because a
			// key the selection owns must outrank whatever the capture overlay holds.
			name:      "a re-render of the same selection is a no-op that still lifts",
			selection: map[string]any{"model_provider": "llamacpp"},
			file:      map[string]any{"model_provider": "llamacpp"},
			record:    map[string]any{"model_provider": "llamacpp"},
			wantLift:  map[string]any{"model_provider": "llamacpp"},
			wantNext:  map[string]any{"model_provider": "llamacpp"},
		},
		{
			// THE case: the user changed the model interactively mid-session. Re-asserting
			// here is the hazard §5.1 exists to refuse (OQ-CS2).
			name:      "a user edit survives the same selection",
			selection: map[string]any{"model_provider": "llamacpp"},
			file:      map[string]any{"model_provider": "mine"},
			record:    map[string]any{"model_provider": "llamacpp"},
			wantLift:  map[string]any{"model_provider": "mine"},
			wantNext:  map[string]any{"model_provider": "llamacpp"},
		},
		{
			// ... and the guard has an exit: a NEW selection value differs from the last
			// value yolo wrote, which is an explicit choice outranking a stale interactive
			// one.
			name:      "a changed selection outranks a stale user edit",
			selection: map[string]any{"model_provider": "vllm"},
			file:      map[string]any{"model_provider": "mine"},
			record:    map[string]any{"model_provider": "llamacpp"},
			wantLift:  map[string]any{"model_provider": "vllm"},
			wantNext:  map[string]any{"model_provider": "vllm"},
		},
		{
			// Deactivation (OQ-PSW2): the profile is gone, and yolo wrote this value.
			// The key is cleared (omitted from lift) and dropped from next so the file
			// falls back to native/host defaults.
			name:      "deactivation clears yolo write and drops record",
			selection: nil,
			file:      map[string]any{"model_provider": "vllm"},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{},
			wantNext:  map[string]any{},
		},
		{
			// Deactivation when the user edited the key interactively: user's edit
			// is preserved, and the record keeps what yolo wrote so future selections
			// know the user modified it.
			name:      "deactivation preserves user edit",
			selection: nil,
			file:      map[string]any{"model_provider": "mine"},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{"model_provider": "mine"},
			wantNext:  map[string]any{"model_provider": "vllm"},
		},
		{
			// A key only the record remembers, with nothing in the file: nothing to keep,
			// and dropped from the record.
			name:      "a deactivated key absent from the file is dropped from record",
			selection: nil,
			file:      map[string]any{},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{},
			wantNext:  map[string]any{},
		},
		{
			// The key is not in the file, so the activation rule fires even though yolo
			// wrote it before: the user REMOVED it, and a standing selection puts it back.
			// This is also the `yolo config reset` path, which truncates the file to the
			// pure render.
			name:      "a removed key is re-activated by a standing selection",
			selection: map[string]any{"model_provider": "llamacpp"},
			file:      map[string]any{},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{"model_provider": "llamacpp"},
			wantNext:  map[string]any{"model_provider": "llamacpp"},
		},
		{
			// No record means no claim: a key yolo never wrote is the user's, and nothing
			// in this table hands it to yolo — there is no "new selection" to compare
			// against the last write that never happened.
			name:      "a key yolo never wrote is kept even when a selection names it",
			selection: map[string]any{"model_provider": "llamacpp"},
			file:      map[string]any{"model_provider": "mine"},
			record:    nil,
			wantLift:  map[string]any{"model_provider": "mine"},
			wantNext:  map[string]any{},
		},
		{
			// OQ-PSW2: when a new selection names one key (model) and omits another key
			// yolo previously wrote (model_provider), the omitted key is cleared because
			// the file holds yolo's write.
			name:      "an omitted key is cleared while a newly selected key is written",
			selection: map[string]any{"model": "qwen"},
			file:      map[string]any{"model_provider": "vllm"},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{"model": "qwen"},
			wantNext:  map[string]any{"model": "qwen"},
		},
		{
			// 8080 arrives as int64 from the derive and from the TOML file, and as float64
			// from the JSON record. A comparison that read those as different values would
			// hand the user's own port back to yolo as if they had edited it.
			name:      "an integer selection is one choice across three decoders",
			selection: map[string]any{"port": int64(8080)},
			file:      map[string]any{"port": int64(8080)},
			record:    map[string]any{"port": float64(8080)},
			wantLift:  map[string]any{"port": int64(8080)},
			wantNext:  map[string]any{"port": float64(8080)},
		},
		{
			// A non-scalar in the file is not yolo's business and is not lifted; the
			// namespace carries scalars only, so the lift must not become a path a table
			// travels into the computed layer by.
			name:      "a non-scalar file value is left alone",
			selection: nil,
			file:      map[string]any{"model_provider": map[string]any{"nested": true}},
			record:    map[string]any{"model_provider": "llamacpp"},
			wantLift:  map[string]any{},
			wantNext:  map[string]any{"model_provider": "llamacpp"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lift, next := ApplySelection(tc.selection, tc.file, tc.record)
			if !reflect.DeepEqual(lift, tc.wantLift) {
				t.Errorf("lift = %v, want %v", lift, tc.wantLift)
			}
			if !reflect.DeepEqual(next, tc.wantNext) {
				t.Errorf("record = %v, want %v", next, tc.wantNext)
			}
		})
	}
}

// TestApplySelectionNoSelectionNoRecordIsNil pins the shape every unaffected surface
// takes: a derive that emits no selection and a surface with no record produce no lift
// and no record, which is what keeps the mechanism invisible to every surface that does
// not use it.
func TestApplySelectionNoSelectionNoRecordIsNil(t *testing.T) {
	lift, next := ApplySelection(nil, map[string]any{"model_provider": "mine"}, nil)
	if lift != nil || next != nil {
		t.Errorf("ApplySelection with no selection and no record = %v, %v; want nil, nil", lift, next)
	}
}

func TestTakeSelectionSplits(t *testing.T) {
	computed := map[string]any{
		"mcp_servers":     map[string]any{"a": map[string]any{"command": "x"}},
		SelectionKey:      map[string]any{"model_provider": "llamacpp", "model": "llama"},
		"model_providers": map[string]any{"llamacpp": map[string]any{"base_url": "u"}},
	}
	rest, selection, problems := TakeSelection(computed)
	if len(problems) != 0 {
		t.Errorf("problems = %v, want none", problems)
	}
	if want := []string{"mcp_servers", "model_providers"}; !reflect.DeepEqual(sortedMapKeys(rest), want) {
		t.Errorf("rest keys = %v, want %v", sortedMapKeys(rest), want)
	}
	want := map[string]any{"model_provider": "llamacpp", "model": "llama"}
	if !reflect.DeepEqual(selection, want) {
		t.Errorf("selection = %v, want %v", selection, want)
	}
	// The input must be untouched: the caller may still be holding the layer for the
	// RMW path.
	if _, present := computed[SelectionKey]; !present {
		t.Error("TakeSelection mutated its input")
	}
}

func TestTakeSelectionRefusesNonScalars(t *testing.T) {
	// A table under the namespace is the one shape that could be mistaken for a
	// yolo-managed table (hostTableKeys's sentinel probe asks the derive exactly which
	// of its keys are tables), so it is refused rather than flattened, and so is an
	// array that holds one. An array of scalars is a leaf and passes (pi's
	// enabledModels; TestTakeSelectionKeepsAnArrayOfScalars).
	computed := map[string]any{
		SelectionKey: map[string]any{
			"model_provider": "llamacpp",
			"nested":         map[string]any{"a": 1},
			"list_of_tables": []any{map[string]any{"a": 1}},
		},
	}
	rest, selection, problems := TakeSelection(computed)
	if _, present := rest[SelectionKey]; present {
		t.Error("the reserved key survived into rest")
	}
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want two (one per refused value)", problems)
	}
	for _, p := range problems {
		if !strings.Contains(p, SelectionKey) {
			t.Errorf("problem %q does not name the namespace", p)
		}
	}
	if got := selection["model_provider"]; got != "llamacpp" {
		t.Errorf("the scalar sibling was refused along with the tables: %v", selection)
	}
	if _, present := selection["nested"]; present {
		t.Error("a table under the namespace was lifted")
	}
}

func TestTakeSelectionRefusesANonTableBody(t *testing.T) {
	rest, selection, problems := TakeSelection(map[string]any{SelectionKey: "llamacpp"})
	if selection != nil {
		t.Errorf("selection = %v, want nil", selection)
	}
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one", problems)
	}
	if _, present := rest[SelectionKey]; present {
		t.Error("the reserved key survived into rest")
	}
}

func TestTakeSelectionWithoutTheKeyIsTheIdentity(t *testing.T) {
	computed := map[string]any{"mcp_servers": map[string]any{}}
	rest, selection, problems := TakeSelection(computed)
	if selection != nil || problems != nil {
		t.Errorf("selection/problems = %v, %v; want nil, nil", selection, problems)
	}
	if !reflect.DeepEqual(rest, computed) {
		t.Errorf("rest = %v, want the input unchanged", rest)
	}
}

func TestTakeSelectionOfNilIsNil(t *testing.T) {
	rest, selection, problems := TakeSelection(nil)
	if rest != nil || selection != nil || problems != nil {
		t.Errorf("TakeSelection(nil) = %v, %v, %v; want nil, nil, nil", rest, selection, problems)
	}
}

// TestDropSelectionKeepsTheNamespaceOutOfANonStatefulFile pins the drop half: a
// surface that cannot apply the namespace must not write a literal `selection` table
// into the agent's file — and must say so rather than drop it silently.
func TestDropSelectionKeepsTheNamespaceOutOfANonStatefulFile(t *testing.T) {
	rest, problems := DropSelection(map[string]any{
		"mcp_servers": map[string]any{},
		SelectionKey:  map[string]any{"model_provider": "llamacpp"},
	})
	if _, present := rest[SelectionKey]; present {
		t.Error("the reserved key survived the drop")
	}
	if _, present := rest["mcp_servers"]; !present {
		t.Error("the drop took an ordinary computed key with it")
	}
	if len(problems) != 1 || !strings.Contains(problems[0], SelectionKey) {
		t.Errorf("problems = %v, want one naming the namespace", problems)
	}

	rest, problems = DropSelection(map[string]any{"mcp_servers": map[string]any{}})
	if problems != nil {
		t.Errorf("problems = %v, want none", problems)
	}
	if _, present := rest["mcp_servers"]; !present {
		t.Error("a surface with no selection lost its computed keys")
	}
}

func TestParseSelectionRecord(t *testing.T) {
	if got := ParseSelectionRecord(nil); got != nil {
		t.Errorf("ParseSelectionRecord(nil) = %v, want nil", got)
	}
	if got := ParseSelectionRecord([]byte("not json")); got != nil {
		t.Errorf("an unparseable record = %v, want nil (a broken record claims nothing)", got)
	}
	got := ParseSelectionRecord([]byte(`{"model_provider":"llamacpp","port":8080}`))
	want := map[string]any{"model_provider": "llamacpp", "port": float64(8080)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("record = %v, want %v", got, want)
	}
}

// A record the writer cannot have produced — the writer only ever persists what
// TakeSelection let through, which is scalars and arrays of scalars — must claim nothing
// rather than feed a table into ApplySelection, where it would flow into `next` and be
// written back as the record of a write yolo never made. An array of scalars is one the
// writer does produce (pi's enabledModels), so it is kept, its numbers normalized.
func TestParseSelectionRecordDropsNonScalars(t *testing.T) {
	got := ParseSelectionRecord([]byte(
		`{"model_provider":"llamacpp","nested":{"a":1},"list":[1,2],"tables":[{"a":1}],"nothing":null}`))
	want := map[string]any{"model_provider": "llamacpp", "list": []any{float64(1), float64(2)}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("record = %v, want the scalar and scalar-array keys alone %v", got, want)
	}
	if got := ParseSelectionRecord([]byte(`{"nested":{"a":1}}`)); got != nil {
		t.Errorf("a record with no scalar at all = %v, want nil (nothing is trustworthy)",
			got)
	}
}

// TestTakeSelectionKeepsAnArrayOfScalars pins the one non-scalar shape the namespace
// carries: pi's enabledModels, an array that is a leaf everywhere it is read.
func TestTakeSelectionKeepsAnArrayOfScalars(t *testing.T) {
	list := []any{"zai/glm-5.3", "zai/glm-4.6"}
	_, selection, problems := TakeSelection(map[string]any{
		SelectionKey: map[string]any{"defaultProvider": "zai", "enabledModels": list},
	})
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none for an array of scalars", problems)
	}
	if !reflect.DeepEqual(selection["enabledModels"], list) {
		t.Errorf("enabledModels = %v, want %v kept", selection["enabledModels"], list)
	}
}

// TestApplySelectionDecidesArrays is TestApplySelectionDecides's table for an ARRAY value:
// the same five rules, compared by value and in order, with numbers normalized so a
// derive's int64 and a record's float64 read as one choice.
func TestApplySelectionDecidesArrays(t *testing.T) {
	yolo := []any{"zai/glm-5.3", "zai/glm-4.6"}
	moved := []any{"kilo/deepseek"}
	mine := []any{"zai/glm-5.3-flash"}
	cases := []struct {
		name                    string
		selection, file, record map[string]any
		wantLift, wantNext      map[string]any
	}{
		{name: "activation writes the array",
			selection: map[string]any{"enabledModels": yolo}, file: map[string]any{},
			wantLift: map[string]any{"enabledModels": yolo}, wantNext: map[string]any{"enabledModels": yolo}},
		{name: "a same-selection re-render writes nothing new",
			selection: map[string]any{"enabledModels": yolo}, file: map[string]any{"enabledModels": yolo},
			record:   map[string]any{"enabledModels": yolo},
			wantLift: map[string]any{"enabledModels": yolo}, wantNext: map[string]any{"enabledModels": yolo}},
		{name: "a changed selection moves a yolo-written array",
			selection: map[string]any{"enabledModels": moved}, file: map[string]any{"enabledModels": yolo},
			record:   map[string]any{"enabledModels": yolo},
			wantLift: map[string]any{"enabledModels": moved}, wantNext: map[string]any{"enabledModels": moved}},
		{name: "a user-edited array survives the same selection",
			selection: map[string]any{"enabledModels": yolo}, file: map[string]any{"enabledModels": mine},
			record:   map[string]any{"enabledModels": yolo},
			wantLift: map[string]any{"enabledModels": mine}, wantNext: map[string]any{"enabledModels": yolo}},
		{name: "a reorder is a user edit, not the same choice",
			selection: map[string]any{"enabledModels": yolo},
			file:      map[string]any{"enabledModels": []any{"zai/glm-4.6", "zai/glm-5.3"}},
			record:    map[string]any{"enabledModels": yolo},
			wantLift:  map[string]any{"enabledModels": []any{"zai/glm-4.6", "zai/glm-5.3"}},
			wantNext:  map[string]any{"enabledModels": yolo}},
		{name: "deactivation clears a yolo-written array",
			file: map[string]any{"enabledModels": yolo}, record: map[string]any{"enabledModels": yolo},
			wantLift: map[string]any{}, wantNext: map[string]any{}},
		{name: "deactivation keeps a user-edited array",
			file: map[string]any{"enabledModels": mine}, record: map[string]any{"enabledModels": yolo},
			wantLift: map[string]any{"enabledModels": mine}, wantNext: map[string]any{"enabledModels": yolo}},
		{name: "an unrecorded file array equal to the selection is adopted",
			selection: map[string]any{"defaultProvider": "zai", "enabledModels": yolo},
			file:      map[string]any{"defaultProvider": "zai", "enabledModels": yolo},
			record:    map[string]any{"defaultProvider": "zai"},
			wantLift:  map[string]any{"defaultProvider": "zai", "enabledModels": yolo},
			wantNext:  map[string]any{"defaultProvider": "zai", "enabledModels": yolo}},
		{name: "an unrecorded scalar equal to the selection is adopted too",
			selection: map[string]any{"model": "glm-5.3"}, file: map[string]any{"model": "glm-5.3"},
			wantLift: map[string]any{"model": "glm-5.3"}, wantNext: map[string]any{"model": "glm-5.3"}},
		{name: "an unrecorded file array that differs stays the user's",
			selection: map[string]any{"enabledModels": yolo}, file: map[string]any{"enabledModels": mine},
			wantLift: map[string]any{"enabledModels": mine}, wantNext: map[string]any{}},
		{name: "numbers normalize inside an array",
			selection: map[string]any{"ports": []any{int64(8080)}}, file: map[string]any{"ports": []any{float64(8080)}},
			record:   map[string]any{"ports": []any{float64(8080)}},
			wantLift: map[string]any{"ports": []any{float64(8080)}}, wantNext: map[string]any{"ports": []any{float64(8080)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lift, next := ApplySelection(c.selection, c.file, c.record)
			if !reflect.DeepEqual(lift, c.wantLift) {
				t.Errorf("lift = %v, want %v", lift, c.wantLift)
			}
			if !reflect.DeepEqual(next, c.wantNext) {
				t.Errorf("next = %v, want %v", next, c.wantNext)
			}
		})
	}
}

// TestHostOwnedKeysNamesOnlyTheHostsValues pins the OQ-SW1 predicate: a file value is the
// host's when it equals the host layer now, or is unchanged since a previous render whose
// provenance named the host; anything else, a scalar or an array, is the user's.
func TestHostOwnedKeysNamesOnlyTheHostsValues(t *testing.T) {
	file := map[string]any{
		"defaultModel":    "host-m",
		"defaultProvider": "stale-host",
		"enabledModels":   []any{"a/x", "a/y"},
		"edited":          "mine",
		"table":           map[string]any{"k": "v"},
	}
	host := map[string]any{"defaultModel": "host-m", "enabledModels": []any{"a/x", "a/y"}, "edited": "host-e"}
	last := map[string]any{"defaultProvider": "stale-host", "edited": "host-e"}
	prov := map[string]string{"defaultProvider": LayerHost, "edited": LayerHost}

	got := HostOwnedKeys(file, host, last, prov)
	for _, k := range []string{"defaultModel", "defaultProvider", "enabledModels"} {
		if !got[k] {
			t.Errorf("%s is the host's value and was not named: %v", k, got)
		}
	}
	for _, k := range []string{"edited", "table"} {
		if got[k] {
			t.Errorf("%s is not the host's value (an in-jail edit, or not a selection value) and was named: %v", k, got)
		}
	}
	if HostOwnedKeys(file, nil, nil, nil) != nil {
		t.Error("with no host layer and no provenance nothing is the host's")
	}
}

// TestApplySelectionOverOutranksAHostValue: an unrecorded host value is overridden and
// recorded; the same value NOT marked as the host's is still read as the user's.
func TestApplySelectionOverOutranksAHostValue(t *testing.T) {
	sel := map[string]any{"defaultModel": "glm-5.3", "enabledModels": []any{"zai/glm-5.3"}}
	file := map[string]any{"defaultModel": "host-m", "enabledModels": []any{"a/x"}}

	lift, next := ApplySelectionOver(sel, file, nil, map[string]bool{"defaultModel": true, "enabledModels": true})
	for k, v := range sel {
		if !sameScalar(lift[k], v) || !sameScalar(next[k], v) {
			t.Errorf("%s: lift %v record %v, want the selection %v over the host's value", k, lift[k], next[k], v)
		}
	}
	lift, next = ApplySelectionOver(sel, file, nil, nil)
	if !sameScalar(lift["defaultModel"], "host-m") || next["defaultModel"] != nil {
		t.Errorf("unmarked, the file's value is the user's: lift %v record %v", lift["defaultModel"], next["defaultModel"])
	}
}
