package packdecl

// adapter_test.go pins the `adapter` kind's schema
// (docs/reference/protocol-resolution.md#the-three-declarations, OQ-PR1): a protocol PAIR,
// an ADDRESS, and nothing about who runs it.
//
// The ruling this file exists to keep is the one review round three reached by instances
// rather than argument — an adapter is NOT a field on `service` — so the first test is the
// shape that ruling makes legal: a conversion with no daemon anywhere in it.

import (
	"strings"
	"testing"
)

// THE DAEMONLESS ADAPTER VALIDATES. It is the three declarations' second and third
// provisioning shapes — a remote gateway you already pay for, a proxy already running on
// your host — and the coupled design could not express either.
func TestAnAdapterNeedsNoDaemon(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"https://gw.example/v1"}`,
		`{"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:8214"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("%s must validate with no service beside it, got %q", raw, probs)
		}
	}
}

// The pair and the address ARE the kind: each omission is a declaration with no answer in
// it, so each is refused by name.
func TestAnAdapterNeedsItsPairAndItsAddress(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"kind":"adapter","address":"https://gw.example/v1"}`, `needs "adapts"`},
		{`{"kind":"adapter","adapts":{"from":"openai"},"address":"https://gw.example/v1"}`,
			`adapts needs both "from" and "to"`},
		{`{"kind":"adapter","adapts":{"to":"anthropic"},"address":"https://gw.example/v1"}`,
			`adapts needs both "from" and "to"`},
		{`{"kind":"adapter","adapts":{"from":"openai","to":"anthropic"}}`, `needs "address"`},
	} {
		if probs := decodeOne(t, tc.raw); !strings.Contains(probs, tc.want) {
			t.Errorf("%s: want a problem containing %q, got %q", tc.raw, tc.want, probs)
		}
	}
}

// A PAIR WITH ONE MEMBER converts nothing AND is not inert: it would claim the pair, so a
// real adapter for it could never be selected beside it.
func TestAnAdapterMayNotAdaptAProtocolToItself(t *testing.T) {
	probs := decodeOne(t,
		`{"kind":"adapter","adapts":{"from":"openai","to":"openai"},"address":"https://gw.example/v1"}`)
	if !strings.Contains(probs, "converts nothing") {
		t.Errorf("a self-adaptation must be refused, got %q", probs)
	}
}

// THE ADDRESS OBEYS THE PROVIDER BASE_URL RULE, because it is the same fact under another
// field name: a URL a pack hands a stranger, which some agent's process will be pointed at.
// A credential in it would be in front of everyone who installs the pack.
func TestAnAdapterAddressIsCheckedLikeAProviderEndpoint(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"file:///tmp/sock"}`,
			"must be an http or https URL"},
		{`{"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"https://u:tok@gw.example/v1"}`,
			"must not carry userinfo"},
	} {
		if probs := decodeOne(t, tc.raw); !strings.Contains(probs, tc.want) {
			t.Errorf("%s: want a problem containing %q, got %q", tc.raw, tc.want, probs)
		}
	}
}

// ONE PACK MAY DECLARE SEVERAL PAIRS — the shipped bridge declares two — and the SAME pair
// twice is the collision, because the resolver takes the first match and the second
// declaration would be dead while the footprint showed two conversions at two addresses.
func TestOnePackMayDeclareSeveralPairsButNotOneTwice(t *testing.T) {
	ok := `{"name":"acme","contributes":[
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:1"},
	  {"kind":"adapter","adapts":{"from":"openai-responses","to":"anthropic"},"address":"http://127.0.0.1:2"}]}`
	if _, probs := Decode([]byte(ok)); len(probs) != 0 {
		t.Errorf("two DIFFERENT pairs in one pack must validate, got %v", probs)
	}
	dup := `{"name":"acme","contributes":[
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:1"},
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:2"}]}`
	_, probs := Decode([]byte(dup))
	if !strings.Contains(strings.Join(probs, "; "), "is declared again") {
		t.Errorf("one pair declared twice must be refused, got %v", probs)
	}
}

// `adapts` AND `address` ARE REFUSED EVERYWHERE ELSE, in `profile`'s position and for its
// reason: on another kind they are read by no consumer, so accepting them would be a
// declaration that silently does nothing.
func TestAdapterFieldsAreRefusedOnOtherKinds(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"provider","name":"zai","address":"https://gw.example/v1"}`,
		`{"kind":"service","name":"svc","jail_daemon":{"cmd":["x"]},"adapts":{"from":"a","to":"b"}}`,
	} {
		probs := decodeOne(t, raw)
		if !strings.Contains(probs, "does not take") {
			t.Errorf("%s must be refused by name, got %q", raw, probs)
		}
	}
}

// The accessor returns the declarations in order, which is the order the resolver walks.
func TestAdaptersAccessor(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"adapter","adapts":{"from":"openai","to":"anthropic"},"address":"http://127.0.0.1:1"},
	  {"kind":"adapter","adapts":{"from":"openai-responses","to":"anthropic"},"address":"http://127.0.0.1:2"}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture did not validate: %v", probs)
	}
	got := m.Adapters()
	if len(got) != 2 {
		t.Fatalf("Adapters() = %#v, want two", got)
	}
	if got[0] != (AdapterContribution{From: "openai", To: "anthropic", Address: "http://127.0.0.1:1"}) {
		t.Errorf("Adapters()[0] = %#v", got[0])
	}
	if got[1].From != "openai-responses" || got[1].Address != "http://127.0.0.1:2" {
		t.Errorf("Adapters()[1] = %#v", got[1])
	}
}
