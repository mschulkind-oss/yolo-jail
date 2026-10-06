package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrock_native_test_support_test.go composes the tables a `-p bedrock` launch hands an
// agent's boot render: the provider table from the agent's real needs closure (so the
// `bedrock` entry is packs/bedrock's own declaration, the pack the agent needs) with the
// user's `providers` over it, and the resolved profiles, lowered to the wire the entrypoint
// reads. What differs per test is the user half.

// bedrockTables returns YOLO_PROVIDERS and YOLO_PROFILES for agent (plus extra packs) with the
// user's providers JSON (may be "") and user profiles. packs/bedrock ships no model list
// (docs/design/model-lists-and-pickers.md MM-D32), so the list is the user's alone.
func bedrockTables(t *testing.T, agent, userProvidersJSON string,
	userProfiles map[string]packload.UserProfile, extra ...string) (providersJSON, wire string) {
	t.Helper()
	return bedrockTablesFor(t, testPacksForAgent(t, agent, extra...), userProvidersJSON, userProfiles)
}

// bedrockListTables is bedrockTables with a company pack supplying the Bedrock list
// (bedrockListAdd), the way MM-D32 leaves a list to a pack.
func bedrockListTables(t *testing.T, agent, userProvidersJSON string,
	userProfiles map[string]packload.UserProfile, extra ...string) (providersJSON, wire string) {
	t.Helper()
	packs := append(testPacksForAgent(t, agent, extra...), companyModelsPack(t, bedrockListAdd))
	return bedrockTablesFor(t, packs, userProvidersJSON, userProfiles)
}

// bedrockListAdd is a company pack's `models` contribution adding a Bedrock list of two makers,
// in this order: the three entries packs/bedrock shipped until MM-D32, each with its maker, name
// and limits, so a test of how an agent renders a supplied list reads the list the old tests read.
const bedrockListAdd = `{"kind":"models","provider":"bedrock","add":[
  {"id":"global.anthropic.claude-opus-5-5","vendor":"anthropic","name":"Claude Opus 5.5 (Global)",
   "context_window":1000000,"max_tokens":128000,"input":["text","image"],"reasoning":true},
  {"id":"us.openai.gpt-6.1-sol","vendor":"openai","name":"GPT-6.1 Sol (US)",
   "context_window":1000000,"max_tokens":131072,"input":["text","image"]},
  {"id":"global.openai.gpt-6-astra","vendor":"openai","name":"GPT-6 Astra (Global)",
   "context_window":1050000,"max_tokens":128000,"input":["text","image"]}]}`

// bedrockTablesFor is bedrockTables over an explicit pack set.
func bedrockTablesFor(t *testing.T, packs []*packload.Pack, userProvidersJSON string,
	userProfiles map[string]packload.UserProfile) (providersJSON, wire string) {
	t.Helper()
	var user *jsonx.OrderedMap
	if userProvidersJSON != "" {
		v, err := jsonx.Decode([]byte(userProvidersJSON))
		if err != nil {
			t.Fatal(err)
		}
		m, ok := v.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("user providers %s is not an object", userProvidersJSON)
		}
		user = m
	}
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		t.Fatal(err)
	}
	return mustCompactJSON(t, providers), mustCompactJSON(t, packload.ProfilesWireTable(resolved))
}

// bedrockViaProfile is a user profile over the shipped provider that forces the wire bridge,
// the shape the shipped `bedrock-bridge` profile has.
var bedrockViaProfile = map[string]packload.UserProfile{
	"over-bridge": {Provider: "bedrock", Via: "wire-bridge"},
}
