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
// user's providers JSON (may be "") and user profiles.
func bedrockTables(t *testing.T, agent, userProvidersJSON string,
	userProfiles map[string]packload.UserProfile, extra ...string) (providersJSON, wire string) {
	t.Helper()
	packs := testPacksForAgent(t, agent, extra...)
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
