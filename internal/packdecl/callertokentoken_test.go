package packdecl

// callertokentoken_test.go pins `{caller_token}` (loopholedecl.TokenCallerToken,
// docs/reference/providers.md OQ-CN7 (c)): legal only as the whole value of a
// profile-gated contribution `served_by` a daemon, because the token it resolves to is scoped
// to the agents such a contribution reaches, and a client sends it verbatim.

import (
	"strings"
	"testing"
)

func TestTheCallerTokenTokenNeedsServedByAGateAndTheWholeValue(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"env","profile":"p","served_by":"acme","vars":{"ACME_TOKEN":"{caller_token}"}}]}`))
	if len(probs) != 0 {
		t.Errorf("a gated, served, whole-value {caller_token} was refused: %v", probs)
	}
	for want, body := range map[string]string{
		`declare "served_by"`:     `{"kind":"env","profile":"p","vars":{"ACME_TOKEN":"{caller_token}"}}`,
		`with no gate`:            `{"kind":"env","served_by":"acme","vars":{"ACME_TOKEN":"{caller_token}"}}`,
		"must be the whole value": `{"kind":"env","profile":"p","served_by":"acme","vars":{"ACME_TOKEN":"Bearer {caller_token}"}}`,
	} {
		_, probs := Decode([]byte(`{"name":"x","contributes":[` + body + `]}`))
		if !strings.Contains(strings.Join(probs, "\n"), want) {
			t.Errorf("%s decoded without %q: %v", body, want, probs)
		}
	}
}
