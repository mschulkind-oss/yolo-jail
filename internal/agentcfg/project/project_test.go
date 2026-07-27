package project

import (
	"reflect"
	"strings"
	"testing"
)

// C6 IS THIS TEST. The operation set is only worth building if it reproduces the
// projections that actually exist — if any of them needs an escape hatch, the op set
// is wrong and it is far cheaper to learn that here than after packs ship.
//
// Each case below is transcribed from the live Go builder it must replace, and
// asserts the EXACT output shape that builder produces.

// codex: near-passthrough of {command, args, env}, with args defaulting to [] and
// env included only when non-empty (buildCodexMCPServers, codex.go:15).
func codexMCPProjection() Projection {
	return Projection{Ops: []Op{
		{Copy: &CopyOp{From: "command"}},
		{Copy: &CopyOp{From: "args"}},
		{Default: &InjectOp{To: "args", Value: []any{}}},
		{Copy: &CopyOp{From: "env", OmitEmpty: true}},
	}}
}

// opencode: RENAME env→environment, FOLD command+args into one array, INJECT two
// constants (buildOpencodeMCPServers, agent_configs.go:107).
func opencodeMCPProjection() Projection {
	return Projection{Ops: []Op{
		{Inject: &InjectOp{To: "type", Value: "local"}},
		{Fold: &FoldOp{Froms: []string{"command", "args"}, To: "command"}},
		{Inject: &InjectOp{To: "enabled", Value: true}},
		{Copy: &CopyOp{From: "env", To: "environment", OmitEmpty: true}},
	}}
}

// copilot LSP: defaults for args/fileExtensions, and command OMITTED when absent
// rather than emitted as null (buildCopilotLSPServers, prism.go:477).
func copilotLSPProjection() Projection {
	return Projection{Ops: []Op{
		{Copy: &CopyOp{From: "args"}},
		{Default: &InjectOp{To: "args", Value: []any{}}},
		{Copy: &CopyOp{From: "fileExtensions"}},
		{Default: &InjectOp{To: "fileExtensions", Value: map[string]any{}}},
		{Copy: &CopyOp{From: "command", OmitEmpty: true}},
	}}
}

func TestCodexMCPProjectionMatchesGoBuilder(t *testing.T) {
	in := map[string]any{
		"withEnv": map[string]any{
			"command": "/bin/srv", "args": []any{"a", "b"},
			"env": map[string]any{"K": "V"},
		},
		"bare": map[string]any{"command": "/bin/x"},
		// An explicitly EMPTY env must be omitted, matching envMap.Len() > 0.
		"emptyEnv": map[string]any{"command": "/bin/y", "env": map[string]any{}},
	}
	got, err := codexMCPProjection().Apply(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"withEnv": map[string]any{
			"command": "/bin/srv", "args": []any{"a", "b"},
			"env": map[string]any{"K": "V"},
		},
		// args defaults to [] when absent — the Go builder always sets it.
		"bare":     map[string]any{"command": "/bin/x", "args": []any{}},
		"emptyEnv": map[string]any{"command": "/bin/y", "args": []any{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("codex projection mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestOpencodeMCPProjectionMatchesGoBuilder(t *testing.T) {
	in := map[string]any{
		"srv": map[string]any{
			"command": "/bin/node", "args": []any{"a", "b"},
			"env": map[string]any{"K": "V"},
		},
		"bare": map[string]any{"command": "/bin/x"},
	}
	got, err := opencodeMCPProjection().Apply(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"srv": map[string]any{
			"type": "local", "enabled": true,
			"command":     []any{"/bin/node", "a", "b"},
			"environment": map[string]any{"K": "V"},
		},
		"bare": map[string]any{
			"type": "local", "enabled": true,
			"command": []any{"/bin/x"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("opencode projection mismatch:\n got: %#v\nwant: %#v", got, want)
	}
}

// The hardest case, and the one a naive design gets wrong: an ABSENT command must be
// OMITTED, not emitted as null. A null leaf is an RFC-7386 tombstone the compose
// engine treats as a DELETION, so emitting null would delete the key from whatever
// the projection merges over — turning "nothing to say" into "remove this".
func TestCopilotLSPProjectionOmitsRatherThanNulls(t *testing.T) {
	in := map[string]any{
		"gopls": map[string]any{
			"command": "gopls", "args": []any{"serve"},
			"fileExtensions": map[string]any{"go": true},
		},
		"commandless": map[string]any{"args": []any{"x"}},
	}
	got, err := copilotLSPProjection().Apply(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"gopls": map[string]any{
			"command": "gopls", "args": []any{"serve"},
			"fileExtensions": map[string]any{"go": true},
		},
		"commandless": map[string]any{
			"args": []any{"x"}, "fileExtensions": map[string]any{},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("copilot LSP projection mismatch:\n got: %#v\nwant: %#v", got, want)
	}
	// Explicitly: the key is ABSENT, not present-with-nil.
	if _, present := got["commandless"].(map[string]any)["command"]; present {
		t.Error("an absent command must be OMITTED — a null leaf is a tombstone that deletes downstream")
	}
}

// A false bool and a zero number are REAL values, not empties. Treating them as
// absent is a classic silent-data-loss bug (an agent setting `enabled: false` would
// have it dropped).
func TestOmitEmptyKeepsFalseAndZero(t *testing.T) {
	p := Projection{Ops: []Op{
		{Copy: &CopyOp{From: "flag", OmitEmpty: true}},
		{Copy: &CopyOp{From: "count", OmitEmpty: true}},
	}}
	got, err := p.Apply(map[string]any{"e": map[string]any{"flag": false, "count": 0}})
	if err != nil {
		t.Fatal(err)
	}
	entry := got["e"].(map[string]any)
	if entry["flag"] != false || entry["count"] != 0 {
		t.Errorf("false/zero must survive OmitEmpty: %#v", entry)
	}
}

// KeySuffix namespaces what a projection emits, without touching values.
func TestKeySuffixRenamesEntries(t *testing.T) {
	p := Projection{KeySuffix: "-lsp", Ops: []Op{{Copy: &CopyOp{From: "command"}}}}
	got, err := p.Apply(map[string]any{"gopls": map[string]any{"command": "gopls"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["gopls-lsp"]; !ok {
		t.Errorf("KeySuffix not applied: %#v", got)
	}
}

// Validate must report EVERY problem, not the first: an author fixing a projection
// wants the whole list rather than one edit-check cycle per mistake.
func TestValidateReportsAllProblems(t *testing.T) {
	p := Projection{Ops: []Op{
		{},                            // nothing set
		{Copy: &CopyOp{}},             // missing from
		{Fold: &FoldOp{}},             // missing froms and to
		{Inject: &InjectOp{Value: 1}}, // missing to
		{Copy: &CopyOp{From: "a"}, Inject: &InjectOp{To: "b"}}, // two ops
	}}
	problems := p.Validate()
	if len(problems) < 5 {
		t.Errorf("expected a problem per mistake, got %d: %v", len(problems), problems)
	}
	// A valid projection reports nothing.
	if got := codexMCPProjection().Validate(); len(got) != 0 {
		t.Errorf("valid projection reported problems: %v", got)
	}
}

// A non-object entry cannot be reshaped field-wise; erroring NAMES it so the author
// can see which, where skipping silently would just lose it.
func TestApplyErrorsOnNonObjectEntry(t *testing.T) {
	_, err := codexMCPProjection().Apply(map[string]any{"bad": "not-an-object"})
	if err == nil {
		t.Fatal("expected an error for a non-object entry")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error should name the entry: %v", err)
	}
}
