package packdecl

import (
	"strings"
	"testing"
)

// `unlisted_background_models` is a program's fact (docs/design/wire-bridge-gateway.md WG-I41):
// the accessor answers it by bin, and any other kind that carries it is refused, a declaration
// no consumer reads being the accepted-and-ignored shape this schema refuses everywhere.
func TestUnlistedBackgroundModelsIsAProgramsFact(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"@acme/acme","unlisted_background_models":true},
	  {"kind":"program","bin":"other","via":"npm","package":"@acme/other"}]}`))
	if len(probs) != 0 {
		t.Fatalf("fixture: %v", probs)
	}
	if !m.SendsUnlistedModels("acme") || m.SendsUnlistedModels("other") || m.SendsUnlistedModels("absent") {
		t.Errorf("SendsUnlistedModels: acme %v, other %v, absent %v; want true, false, false",
			m.SendsUnlistedModels("acme"), m.SendsUnlistedModels("other"), m.SendsUnlistedModels("absent"))
	}
	_, probs = Decode([]byte(`{"name":"acme","contributes":[
	  {"kind":"env","env":{"A":"b"},"unlisted_background_models":true}]}`))
	if !strings.Contains(strings.Join(probs, "\n"), `does not take "unlisted_background_models"`) {
		t.Errorf("an env contribution carrying it must be refused, got %v", probs)
	}
}
