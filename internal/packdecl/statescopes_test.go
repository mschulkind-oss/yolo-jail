package packdecl

import (
	"strings"
	"testing"
)

// A path one pack declares as `state` at BOTH scopes is refused: the launch binds the workspace
// state from the per-workspace overlay and the machine state from the machine store, both at
// /home/agent/<at>, and podman refuses that as "duplicate mount destination" naming no pack. It
// is also the manifest the shared-dir hook refusal must never advise writing (PC-D15).
func TestDecodeRefusesAStateDeclaredAtBothScopes(t *testing.T) {
	_, problems := Decode([]byte(`{"name":"p","contributes":[
		{"kind":"state","at":".x-shared"},
		{"kind":"state","at":".x-shared","scope":"machine","because":"b"},
		{"kind":"hook","hook":"shared_credentials","from":".x/c.json","at":".x-shared"}]}`))
	got := strings.Join(problems, "\n")
	for _, want := range []string{"contributes[1]", "contributes[0]", `".x-shared"`, "both", `"scope"`} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, got)
		}
	}
}

// The same path twice at ONE scope is one bind (packload's union deduplicates it), so it is not
// this refusal's business.
func TestDecodeAcceptsAStateRepeatedAtOneScope(t *testing.T) {
	for _, scope := range []string{`"scope":"workspace"`, `"scope":"machine","because":"b"`} {
		_, problems := Decode([]byte(`{"name":"p","contributes":[
			{"kind":"state","at":".x",` + scope + `},{"kind":"state","at":".x",` + scope + `}]}`))
		if len(problems) != 0 {
			t.Errorf("%s: refused a repeat at one scope: %v", scope, problems)
		}
	}
}

// When the hook's `at` is already a WORKSPACE state of the pack, the next step is to change that
// state's scope, not to add a second state at the path: the second state is the both-scopes
// manifest above, which fails every podman launch. Following the advice must yield a manifest
// Decode accepts.
func TestTheHookRefusalOverAWorkspaceStateSaysChangeItsScope(t *testing.T) {
	m, problems := Decode([]byte(`{"name":"p","contributes":[
		{"kind":"state","at":".x-shared"},
		{"kind":"hook","hook":"shared_directory","from":".x/c","at":".x-shared"}]}`))
	got := strings.Join(problems, "\n")
	if want := m.UndeclaredHookStateProblem(".x-shared"); !strings.Contains(got, want) {
		t.Fatalf("Decode does not refuse with the pack's sentence %q:\n%s", want, got)
	}
	if strings.Contains(got, `add {"kind": "state"`) {
		t.Errorf("advises adding a second state at a path the pack declares at workspace scope:\n%s", got)
	}
	if !strings.Contains(got, `"scope"`) || !strings.Contains(got, `"machine"`) {
		t.Errorf("does not say to set the existing state's scope to machine:\n%s", got)
	}
	// Followed: the existing state now machine-scoped.
	if _, problems := Decode([]byte(`{"name":"p","contributes":[
		{"kind":"state","at":".x-shared","scope":"machine","because":"b"},
		{"kind":"hook","hook":"shared_directory","from":".x/c","at":".x-shared"}]}`)); len(problems) != 0 {
		t.Errorf("the advised manifest is refused: %v", problems)
	}
	// With no state at the path, adding one is still the advice, and following it is accepted.
	none, _ := Decode([]byte(`{"name":"p","contributes":[]}`))
	if s := none.UndeclaredHookStateProblem(".x-shared"); !strings.Contains(s, `add {"kind": "state"`) {
		t.Errorf("with no state at the path, the advice no longer says to add one: %s", s)
	}
}
