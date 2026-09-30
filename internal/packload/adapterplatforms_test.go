package packload

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// adapterplatforms_test.go pins the composition half of the region-composed Bedrock upstream
// (docs/design/wire-bridge-gateway.md WG-I39): an adaptation that fronts a platform gives a
// provider of that platform, which names no From address, the adaptation's address marked for a
// via profile (ForViaKey), and nothing else changes: a provider that names its own From address
// gets the ordinary, unmarked address, and a provider of another platform gets none.

func composedTableOf(t *testing.T, user string, names ...string) *jsonx.OrderedMap {
	t.Helper()
	var layer *jsonx.OrderedMap
	if user != "" {
		v, err := jsonx.Decode([]byte(user))
		if err != nil {
			t.Fatal(err)
		}
		layer = v.(*jsonx.OrderedMap)
	}
	table, err := ComposeProviders(layer, embeddedNamed(t, names...))
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func TestAnAdaptationFrontsOnlyAProviderOfItsPlatformThatNamesNoAddress(t *testing.T) {
	table := composedTableOf(t, `{
	  "corp-bedrock": {"platform": "aws-bedrock",
	    "endpoints": {"openai": {"base_url": "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"}}},
	  "vertex": {"platform": "gcp-vertex"}
	}`, "claude", "bedrock", "wire-bridge")
	for name, want := range map[string]string{"bedrock": "wire-bridge", "corp-bedrock": "", "vertex": "-"} {
		entry := providerEntry(table, name)
		got := forViaService(entry, "anthropic")
		if want == "-" {
			if ep, ok := entry.Get("endpoints"); ok {
				t.Errorf("%s: another platform got endpoints %v", name, ep)
			}
			continue
		}
		if got != want {
			t.Errorf("%s: anthropic endpoint marked for %q, want %q", name, got, want)
		}
		if name == "corp-bedrock" && !providerProtocols(entry)["anthropic"] {
			t.Errorf("a Bedrock provider naming its own openai address lost its ordinary adapter address")
		}
	}
	// As a profile with no via sees it, the marked address is none, and a profile with the via
	// sees it whole.
	bedrock := providerEntry(table, "bedrock")
	if eps := providerProtocols(EndpointsForProfile(bedrock, "")); len(eps) != 0 {
		t.Errorf("a profile with no via still sees %v", eps)
	}
	if eps := providerProtocols(EndpointsForProfile(bedrock, "wire-bridge")); !eps["anthropic"] {
		t.Errorf("the via profile lost the address composed for it")
	}
}
