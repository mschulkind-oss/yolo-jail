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
			// Never on absence (OQ-CS2): the profile is gone, the key stays exactly as it
			// is. Still lifted, so the file keeps it across the wholesale rewrite instead
			// of falling back to whatever stale value the capture overlay holds.
			name:      "deactivation keeps the value and the memory",
			selection: nil,
			file:      map[string]any{"model_provider": "vllm"},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{"model_provider": "vllm"},
			wantNext:  map[string]any{"model_provider": "vllm"},
		},
		{
			// A key only the record remembers, with nothing in the file: nothing to keep,
			// and nothing to write — the selection is not naming it.
			name:      "a deactivated key absent from the file is not resurrected",
			selection: nil,
			file:      map[string]any{},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{},
			wantNext:  map[string]any{"model_provider": "vllm"},
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
			// A key the record remembers beside a key the selection names: both are
			// decided, and only the selected one moves.
			name:      "a remembered key and a newly selected key are decided together",
			selection: map[string]any{"model": "qwen"},
			file:      map[string]any{"model_provider": "vllm"},
			record:    map[string]any{"model_provider": "vllm"},
			wantLift:  map[string]any{"model_provider": "vllm", "model": "qwen"},
			wantNext:  map[string]any{"model_provider": "vllm", "model": "qwen"},
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
	// of its keys are tables), so it is refused rather than flattened.
	computed := map[string]any{
		SelectionKey: map[string]any{
			"model_provider": "llamacpp",
			"nested":         map[string]any{"a": 1},
			"list":           []any{"a"},
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
