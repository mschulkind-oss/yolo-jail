package entrypoint

// bedrock_model_list_test.go pins the ONE Bedrock model list (docs/design/bedrock-plumbing.md
// OQ-BR9, ruled 2026-09-29): packs/bedrock/pack.json declares every model family on the one
// `bedrock` provider, each entry naming its maker as `vendor`, and each agent that binds Bedrock
// expands it through one helper, callableModels, filtered to what that agent's own client can
// call. The consumers' renders are pinned beside each binding; this file pins the declaration's
// shape and the helper's one text.

import (
	"regexp"
	"strconv"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrockBindingDerives are the derives that carry the helper: the agent packs whose own
// Bedrock client a `-p bedrock` launch configures.
var bedrockBindingDerives = []string{"claude", "codex", "opencode", "pi"}

// THE HELPER IS ONE TEXT IN FOUR FILES. A derive cannot load another file, so callableModels
// and callableModel are copied into each consumer's derive.lua; a copy edited alone would give
// two agents two answers to "which models of this provider can I call", which is the drift a
// single declaration exists to end.
func TestBedrockModelListHelperIsIdenticalInEveryDerive(t *testing.T) {
	helper := regexp.MustCompile(`(?s)-- THE MODELS OF A MULTI-MAKER PROVIDER THIS AGENT CAN CALL\..*?\nlocal function callableModel\(p, list, profile, pick\)\n.*?\nend\n`)
	var first, firstPack string
	for _, pack := range bedrockBindingDerives {
		p, err := embeddedPack(pack)
		if err != nil {
			t.Fatal(err)
		}
		matches := helper.FindAllString(packload.DeriveScript(p), -1)
		if len(matches) != 1 {
			t.Fatalf("packs/%s/derive.lua carries %d copies of the Bedrock model-list helper, want 1", pack, len(matches))
		}
		if first == "" {
			first, firstPack = matches[0], pack
			continue
		}
		if matches[0] != first {
			t.Errorf("packs/%s/derive.lua's Bedrock model-list helper differs from packs/%s/derive.lua's", pack, firstPack)
		}
	}
}

// shippedBedrockDeclaration is the `bedrock` provider contribution packs/bedrock ships.
func shippedBedrockDeclaration(t *testing.T) packdecl.ProviderContribution {
	t.Helper()
	p, err := embeddedPack("bedrock")
	if err != nil {
		t.Fatal(err)
	}
	for _, prov := range p.Decl.Providers() {
		if prov.Name == "bedrock" {
			return prov
		}
	}
	t.Fatal("packs/bedrock declares no bedrock provider")
	return packdecl.ProviderContribution{}
}

// THE DECLARATION'S SHAPE: every shipped entry is keyed by its own id (so a profile's `model`
// means the same read as an alias or an id, as packs/openai-auth's list does), declares its
// maker and a display name, and holds a distinct `order`, the only order a Lua `pairs` walk has.
// At least one entry per maker a binding agent filters on, so each agent's fallback, "the first
// model that agent can call", exists: anthropic for claude, openai for codex.
func TestTheShippedBedrockListDeclaresEachEntrysMaker(t *testing.T) {
	decl := shippedBedrockDeclaration(t)
	if len(decl.Models) == 0 {
		t.Fatal("packs/bedrock ships no models")
	}
	if _, has := decl.Models["default"]; has {
		t.Error("the shipped list declares a `default` alias; its default is its first entry per agent " +
			"(order), and a `default` would steer claude's own Bedrock client, which OQ-ML2 forbids")
	}
	orders := map[int]string{}
	makers := map[string]bool{}
	for alias, id := range decl.Models {
		if alias != id {
			t.Errorf("models.%s = %q: a shipped entry is keyed by its own id", alias, id)
		}
		facts := decl.ModelOptions[alias]
		vendor := facts["vendor"]
		if !packdecl.ValidModelVendor(vendor) {
			t.Errorf("%s declares vendor %q, want its maker as one lowercase token", id, vendor)
		}
		makers[vendor] = true
		if facts["name"] == "" {
			t.Errorf("%s declares no display name", id)
		}
		n, err := strconv.Atoi(facts["order"])
		if err != nil {
			t.Errorf("%s declares order %q, want an integer", id, facts["order"])
			continue
		}
		if prior, dup := orders[n]; dup {
			t.Errorf("%s and %s both declare order %d", id, prior, n)
		}
		orders[n] = id
	}
	for _, maker := range []string{"anthropic", "openai"} {
		if !makers[maker] {
			t.Errorf("the shipped list has no %s entry, so the agent that calls only that maker has no fallback", maker)
		}
	}
}
