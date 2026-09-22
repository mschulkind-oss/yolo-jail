package packdecl

// protocols_test.go pins the AGENT'S HALF of the pairing
// (docs/reference/protocol-resolution.md#the-three-declarations, build step 2): the
// `protocols` list a `program` contribution declares, the kinds that may not carry it,
// and the shapes it may not have.
//
// STEP 2 LANDS INERT BY DESIGN — nothing reads the field yet — so what these tests can
// assert is exactly the declaration: it decodes, it reaches the accessor the resolver will
// use, and the ways of writing it wrong are refused at authoring time rather than accepted
// and ignored. The inertness itself is pinned separately (TestProtocolsAreInert), because
// "landing it changed no launch" is the step's own acceptance criterion and a test is the
// only thing that can say so.

import (
	"strings"
	"testing"
)

// The declaration validates on `program`, one protocol or several.
func TestProtocolsValidateOnProgram(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"program","bin":"claude","via":"installer","url":"https://x/i.sh","protocols":["anthropic"]}`,
		`{"kind":"program","bin":"copilot","via":"npm","package":"@github/copilot","protocols":["anthropic","openai"]}`,
		// An undeclared list is the compatibility shape and must stay silent.
		`{"kind":"program","bin":"agy","via":"npm","package":"agy"}`,
	} {
		if probs := decodeOne(t, raw); probs != "" {
			t.Errorf("%s must validate, got %q", raw, probs)
		}
	}
}

// EVERY OTHER KIND REFUSES IT, and the refusal names the kind. The field is a fact about a
// PROGRAM — which wires that binary's process can be pointed at — so a `provider`, a
// `service` or a `requires` carrying one has written a list no consumer will ever read,
// which is the accepted-and-ignored shape this schema refuses everywhere.
func TestProtocolsRefusedOnEveryOtherKind(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"provider","name":"zai","protocols":["anthropic"]}`,
		`{"kind":"requires","bin":"jq","protocols":["openai"]}`,
		`{"kind":"service","name":"wire-bridge","jail_daemon":{"cmd":["yolo-jaild","wire-bridge"]},"protocols":["openai"]}`,
		`{"kind":"skills","into":".claude/skills","protocols":["openai"]}`,
	} {
		probs := decodeOne(t, raw)
		if !strings.Contains(probs, `does not take "protocols"`) {
			t.Errorf("%s must be refused by name, got %q", raw, probs)
		}
	}
}

// AN EMPTY LIST IS REFUSED, with both fixes in the message — platformsProblems' rule, for
// its reason. Honoring it literally makes the agent resolve nothing anywhere; honoring it
// loosely ignores what the author wrote. Neither is a good silence.
func TestEmptyProtocolListIsRefused(t *testing.T) {
	probs := decodeOne(t, `{"kind":"program","bin":"claude","via":"installer","url":"https://x/i.sh","protocols":[]}`)
	if !strings.Contains(probs, "declares that this program speaks nothing") {
		t.Errorf("an empty protocols list must be refused with both fixes named, got %q", probs)
	}
}

// An empty ENTRY is refused: a protocol name is the key a provider files an endpoint
// under, and "" is no key.
func TestEmptyProtocolEntryIsRefused(t *testing.T) {
	probs := decodeOne(t, `{"kind":"program","bin":"claude","via":"installer","url":"https://x/i.sh","protocols":["anthropic",""]}`)
	if !strings.Contains(probs, "protocols[1]: empty entry") {
		t.Errorf("an empty entry must be refused at its index, got %q", probs)
	}
}

// A REPEAT IS REFUSED rather than deduplicated: the list is a preference ORDER, so one
// protocol named twice states two preferences for one wire and there is no reading of it
// that is not the author's mistake.
func TestRepeatedProtocolIsRefused(t *testing.T) {
	probs := decodeOne(t, `{"kind":"program","bin":"pi","via":"npm","package":"p","protocols":["openai","anthropic","openai"]}`)
	if !strings.Contains(probs, `"openai" is already declared at [0]`) {
		t.Errorf("a repeated protocol must be refused naming the first position, got %q", probs)
	}
}

// THE VOCABULARY IS OPEN, exactly as the `endpoints` key set is: a protocol core has never
// heard of resolves to nothing, which is inert — and closing it here would make a third
// protocol the `tier` incident again.
func TestProtocolVocabularyIsOpen(t *testing.T) {
	raw := `{"kind":"program","bin":"future","via":"npm","package":"f","protocols":["grpc-inference"]}`
	if probs := decodeOne(t, raw); probs != "" {
		t.Errorf("an unknown protocol name must be accepted (open vocabulary), got %q", probs)
	}
}

// The accessor is keyed by BIN and returns the declared order verbatim — the resolver's
// only way in, and the same bin-ownership identity NativeCapabilities answers to.
func TestSpokenProtocolsByBin(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
		{"kind":"program","bin":"copilot","via":"npm","package":"@github/copilot","protocols":["anthropic","openai"]},
		{"kind":"program","bin":"agy","via":"npm","package":"agy"}
	]}`))
	if len(probs) > 0 {
		t.Fatalf("fixture did not validate: %v", probs)
	}
	got := m.SpokenProtocols("copilot")
	if len(got) != 2 || got[0] != "anthropic" || got[1] != "openai" {
		t.Errorf("SpokenProtocols(copilot) = %v, want [anthropic openai] in declared order", got)
	}
	// NIL IS "UNCONSTRAINED", not "speaks nothing" — the distinction the resolver depends
	// on, since the empty list is unrepresentable (refused above).
	if got := m.SpokenProtocols("agy"); got != nil {
		t.Errorf("SpokenProtocols(agy) = %v, want nil for a program that declares none", got)
	}
	if got := m.SpokenProtocols("nobody"); got != nil {
		t.Errorf("SpokenProtocols(nobody) = %v, want nil for a bin this pack does not install", got)
	}
	if got := m.SpokenProtocols(""); got != nil {
		t.Errorf(`SpokenProtocols("") = %v, want nil`, got)
	}
}
