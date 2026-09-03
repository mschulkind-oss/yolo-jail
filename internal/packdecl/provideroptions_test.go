package packdecl

// provideroptions_test.go pins the `options` half of a `kind: "provider"` contribution —
// the profile surface a provider DECLARES (docs/reference/providers.md §5.2,
// OQ-CS4), flat name → default value (OQ-CS7). The decode cases here are the ones the
// field exists for: a null is a DECLARED option with no default, and that is the one
// reading of null this config does not share with merge-patch, so it gets its own test
// rather than a line in another test.

import (
	"strings"
	"testing"
)

// A value that is neither a string nor null is an author's typo, and a silent false
// would turn it into an option that quietly has no default — so the decoder refuses it.
func TestProviderOptionsRefuseNonStringValues(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"provider","name":"acme","options":{"strict":true}}]}`))
	if len(probs) == 0 {
		t.Fatal("a boolean option value should be refused, got no problems")
	}
	joined := strings.Join(probs, "; ")
	if !contains(joined, "cannot unmarshal") {
		t.Errorf("a boolean option value should be a decode refusal naming the shape, got %q", joined)
	}
}

// The same manifest WITHOUT the boolean: both legal shapes read back through the
// accessor, with Defaulted carrying the string/null distinction.
func TestProviderOptionsRoundTrip(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"provider","name":"acme",
	   "endpoints":{"openai":{"base_url":"https://api.acme.dev/v4"}},
	   "options":{"model":"default","thinking":null}}]}`))
	if len(probs) != 0 {
		t.Fatalf("options should decode cleanly, got: %v", probs)
	}
	got := m.Providers()
	if len(got) != 1 {
		t.Fatalf("want the one provider, got %d", len(got))
	}
	opts := got[0].Options
	if len(opts) != 2 {
		t.Fatalf("want two declared options, got %+v", opts)
	}
	if !opts["model"].Defaulted || opts["model"].Value != "default" {
		t.Errorf("a string option must read back as a default: %+v", opts["model"])
	}
	if opts["thinking"].Defaulted {
		t.Errorf("a null option is DECLARED with no default, not unset: %+v", opts["thinking"])
	}
	if opts["thinking"].Value != "" {
		t.Errorf("a defaultless option carries no value: %+v", opts["thinking"])
	}
}

// An EMPTY string is a real default — the option defaults to "" — and must not collapse
// into the null reading, which is the whole reason the type carries a bit instead of
// using the zero value as "no default".
func TestProviderOptionsEmptyStringIsADefault(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"provider","name":"acme","options":{"thinking":""}}]}`))
	if len(probs) != 0 {
		t.Fatalf("an empty-string default should decode cleanly, got: %v", probs)
	}
	got := m.Providers()[0].Options["thinking"]
	if !got.Defaulted || got.Value != "" {
		t.Errorf(`options.thinking: "" is a default of the empty string, got %+v`, got)
	}
}

// A provider declaring no options has none — the nil-ness the profile resolution keys
// off (a provider that declares no surface imposes no key census on its profiles).
func TestProviderWithoutOptionsHasNone(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"provider","name":"acme","endpoints":{"openai":{"base_url":"https://api.acme.dev/v4"}}}]}`))
	if len(probs) != 0 {
		t.Fatalf("a provider without options should decode cleanly, got: %v", probs)
	}
	if got := m.Providers()[0].Options; got != nil {
		t.Errorf("no options declared should read back nil, got %+v", got)
	}
}

// An option with no NAME declares a key no profile can ever spell, and the downstream
// refusal would quote it — so the manifest layer refuses it where the author can fix it.
func TestProviderOptionWithEmptyNameIsRefused(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"provider","name":"acme","options":{"":"default"}}]}`))
	if len(probs) == 0 {
		t.Fatal("an empty option name should be a validation error")
	}
	if !contains(strings.Join(probs, "; "), "empty option name") {
		t.Errorf("the refusal should name the empty option name, got %v", probs)
	}
}
