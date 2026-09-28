package packdecl

// listentoken_test.go pins the one interpolation an env value takes, `{listen}`
// (docs/plans/notch-convergence.md NC-D41): legal beside `served_by`, which names the daemon
// whose served address it resolves to, and refused without it, where it would name nothing.

import (
	"strings"
	"testing"
)

func TestTheListenTokenNeedsServedBy(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"env","served_by":"acme","vars":{"ACME_URL":"http://{listen}/v1"}}]}`))
	if len(probs) != 0 {
		t.Errorf("a served pointer naming {listen} was refused: %v", probs)
	}
	_, probs = Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"env","vars":{"ACME_URL":"http://{listen}/v1"}}]}`))
	if !strings.Contains(strings.Join(probs, "\n"), `declare "served_by"`) {
		t.Errorf("an unserved {listen} decoded: %v", probs)
	}
}
