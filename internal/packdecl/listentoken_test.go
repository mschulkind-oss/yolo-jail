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

// A PACK SERVICE DECLARES NO LISTEN ADDRESS, so `{listen}` names nothing in its daemons' argv:
// the payload would hand the daemon an empty string (`--listen ""`). The addresses a service
// answers at are its adapters' `address` and its `via_address`, which core moves itself. Both
// daemon halves refuse the token.
func TestAServiceDaemonRefusesTheListenToken(t *testing.T) {
	for field, body := range map[string]string{
		"jail_daemon": `{"name":"x","contributes":[{"kind":"service","name":"acme",
		  "jail_daemon":{"cmd":["acme-bridge","--listen","{listen}"]}}]}`,
		"host_daemon": `{"name":"x","contributes":[{"kind":"service","name":"acme",
		  "host_daemon":{"cmd":["acme-bridge","--listen","{listen}"]}}]}`,
	} {
		_, probs := Decode([]byte(body))
		got := strings.Join(probs, "\n")
		if !strings.Contains(got, field+".cmd") || !strings.Contains(got, "{listen}") ||
			!strings.Contains(got, "via_address") {
			t.Errorf("a service %s naming {listen} was not refused in the author's terms: %v", field, probs)
		}
	}
	if _, probs := Decode([]byte(`{"name":"x","contributes":[{"kind":"service","name":"acme",
	  "jail_daemon":{"cmd":["acme-bridge"]}}]}`)); len(probs) != 0 {
		t.Errorf("a service with no {listen} was refused: %v", probs)
	}
}

// AN ENV POINTER SERVED BY THIS PACK'S OWN SERVICE cannot name `{listen}`: the service has no
// listen address to resolve it to, so the pointer would be withheld on every launch. Refused at
// decode, naming what to write instead. The same pointer served by a loophole daemon (a name
// this manifest does not declare as a service) stays legal, as TestTheListenTokenNeedsServedBy
// pins.
func TestAPointerServedByAServiceRefusesTheListenToken(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"service","name":"acme","jail_daemon":{"cmd":["acme-bridge"]},
	   "via_address":"http://127.0.0.1:9100"},
	  {"kind":"env","served_by":"acme","vars":{"ACME_URL":"http://{listen}/v1"}}]}`))
	got := strings.Join(probs, "\n")
	if !strings.Contains(got, `"acme"`) || !strings.Contains(got, "{listen}") ||
		!strings.Contains(got, "pack service") {
		t.Errorf("a pointer served by a service and naming {listen} decoded: %v", probs)
	}
	if _, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"service","name":"acme","jail_daemon":{"cmd":["acme-bridge"]}},
	  {"kind":"env","served_by":"acme","vars":{"ACME_URL":"http://127.0.0.1:9100/v1"}}]}`)); len(probs) != 0 {
		t.Errorf("a literal pointer served by a service was refused: %v", probs)
	}
}
