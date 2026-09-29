package packload

// providerplatform_test.go pins how a provider's `platform` (OQ-BR2,
// docs/design/providers-and-profiles-redesign.md) composes into the table every derive reads:
// under the key a user's own entry spells, pack default under user override, so a provider the
// user defines carries the same fact the shipped one does.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

func entryPlatform(t *testing.T, table *jsonx.OrderedMap, name string) string {
	t.Helper()
	e := providerEntry(table, name)
	if e == nil {
		t.Fatalf("the composed table has no %q entry", name)
	}
	v, _ := e.Get("platform")
	s, _ := v.(string)
	return s
}

func TestAProviderPlatformComposesPackUnderUser(t *testing.T) {
	pack := &Pack{Name: "cloud", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"shipped","platform":"aws-bedrock"}]}`)}
	packs := []*Pack{pack}

	if got := entryPlatform(t, compose(t, nil, packs), "shipped"); got != "aws-bedrock" {
		t.Errorf("a pack's platform must reach its composed entry, got %q", got)
	}
	over := userProviders(t, `{"shipped":{"platform":"elsewhere"},
	  "mine":{"platform":"aws-bedrock","region":"eu-west-1"}}`)
	table := compose(t, over, packs)
	if got := entryPlatform(t, table, "shipped"); got != "elsewhere" {
		t.Errorf("the user's platform must override the pack's, got %q", got)
	}
	if got := entryPlatform(t, table, "mine"); got != "aws-bedrock" {
		t.Errorf("a provider only the user declares carries its platform, got %q", got)
	}
}

// shippedPack is one embedded pack by name, as a launch loads it.
func shippedPack(t *testing.T, name string) *Pack {
	t.Helper()
	loaded, problems := MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no shipped %s pack", name)
	return nil
}

// THE SHIPPED DECLARATION: packs/claude's `bedrock` is the provider the ruling is about, so its
// composed entry says it is Bedrock. Read from the embedded packs, so deleting the field from
// packs/claude/pack.json fails here.
func TestTheShippedBedrockProviderDeclaresItsPlatform(t *testing.T) {
	claude := shippedPack(t, "claude")
	if got := entryPlatform(t, compose(t, nil, []*Pack{claude}), "bedrock"); got != "aws-bedrock" {
		t.Errorf(`packs/claude's bedrock provider must declare "platform": "aws-bedrock", got %q`, got)
	}
}
