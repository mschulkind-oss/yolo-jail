package packdecl

import (
	"strings"
	"testing"
)

// modelvendor_test.go pins the SHAPE of a model's maker, the `vendor` fact a provider serving
// several makers' models declares per entry (docs/design/bedrock-plumbing.md OQ-BR9). The
// vocabulary is open; only a value no derive could ever match by equality is refused.

func TestValidModelVendor(t *testing.T) {
	for _, ok := range []string{"anthropic", "openai", "moonshotai", "z.ai", "mistral_ai", "x-ai", "01ai"} {
		if !ValidModelVendor(ok) {
			t.Errorf("%q is a maker's name and must be accepted", ok)
		}
	}
	for _, bad := range []string{"", "OpenAI", "open ai", "-openai", ".x", "anthropic\n", "ä"} {
		if ValidModelVendor(bad) {
			t.Errorf("%q must be refused: derives compare the maker by equality", bad)
		}
	}
}

// A PACK'S DECLARATION IS SHAPE-CHECKED at load, naming the entry, the way a user's object-form
// entry is by config.validateModelEntry; an entry with no vendor stays legal (it is offered to
// every agent).
func TestAProviderModelVendorIsShapeChecked(t *testing.T) {
	_, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"provider","name":"acme","models":{"m":"m","n":"n"},
	   "model_options":{"m":{"vendor":"Acme Corp"},"n":{"name":"N"}}}]}`))
	joined := strings.Join(probs, "; ")
	if !strings.Contains(joined, `model_options.m.vendor "Acme Corp" is not one lowercase token`) {
		t.Errorf("a malformed vendor must be refused naming the entry, got %q", joined)
	}
	if strings.Contains(joined, "model_options.n") {
		t.Errorf("an entry with no vendor is legal, got %q", joined)
	}
}
