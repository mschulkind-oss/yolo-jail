package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// fetchedmodels_test.go pins who wants a FETCHED LIST (docs/design/model-lists-and-pickers.md
// OQ-MM6, MM-D36) over the shipped packs: an agent whose pack declares `needs_model_list` and
// whose profile names no model, and one that speaks anthropic to the bridge's address composed for
// its via, where the bridge reads each model's maker. Nobody else costs the launch a fetch.

// listWantsFor composes packs with the user's own Bedrock provider `bare` (a platform, a region,
// no list) and returns what the launch should fetch for the agents' profiles.
func listWantsFor(t *testing.T, packs []*Pack, userProfiles map[string]UserProfile,
	profiles map[string]string) []ListWant {
	t.Helper()
	user := userProviders(t, `{"bare":{"platform":"aws-bedrock","region":"eu-west-1"}}`)
	providers, resolved, _ := launchSelection(t, packs, user, userProfiles, profiles)
	served := ServedInJail([]string{"aws-auth", "wire-bridge"}).WithListen(declaredListen)
	s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles,
		Resolved: resolved, Served: &served, CallerTokens: awsToken})
	if err != nil {
		t.Fatalf("the gate refused: %v", err)
	}
	return ListWants(packs, providers, resolved, s)
}

func TestTheLaunchWantsAListOnlyWhereSomethingReadsIt(t *testing.T) {
	packs := embeddedNamed(t, "claude", "copilot", "pi", "aws-auth", "openai-auth", "wire-bridge")
	bare := map[string]UserProfile{
		"bare":     {Provider: "bare"},
		"bare-via": {Provider: "bare", Via: "wire-bridge"},
		"bare-own": {Provider: "bare", Options: map[string]string{"model": "my.own-model-v1"}},
	}
	for _, tc := range []struct {
		name     string
		profiles map[string]string
		agents   string
		needs    string
	}{
		// copilot has nothing to start on, and the bridge reads makers for it.
		{"copilot", map[string]string{"copilot": "bare"}, "copilot", "copilot"},
		// a profile's own model is copilot's start, so nothing stops it; the bridge still reads makers.
		{"copilot with a model", map[string]string{"copilot": "bare-own"}, "copilot", ""},
		// claude in its own Bedrock mode at the bridge: the invoke route reads makers to refuse.
		{"claude through the bridge", map[string]string{"claude": "bare-via"}, "claude", ""},
		// claude's own client, and pi on the bridge's via route, read no list.
		{"claude native", map[string]string{"claude": "bare"}, "", ""},
		{"pi through the bridge", map[string]string{"pi": "bare-via"}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wants := listWantsFor(t, packs, bare, tc.profiles)
			if tc.agents == "" {
				if len(wants) != 0 {
					t.Fatalf("wants = %+v, want none", wants)
				}
				return
			}
			if len(wants) != 1 {
				t.Fatalf("wants = %+v, want one, for bare", wants)
			}
			w := wants[0]
			needs := []string{}
			for agent := range w.Needs {
				needs = append(needs, agent)
			}
			if w.Provider != "bare" || w.Platform != "aws-bedrock" || w.Region != "eu-west-1" ||
				w.Service != "aws-auth" || strings.Join(w.Agents, ",") != tc.agents || strings.Join(needs, ",") != tc.needs {
				t.Errorf("want = %+v, want agents %q needing %q, aws-auth in eu-west-1", w, tc.agents, tc.needs)
			}
		})
	}
}

// TestAProviderWithAListWantsNoFetch: a pack's or the user's own list is the list, so nothing is
// fetched for it, whoever reads it.
func TestAProviderWithAListWantsNoFetch(t *testing.T) {
	packs := embeddedNamed(t, "copilot", "aws-auth", "wire-bridge")
	user := userProviders(t, `{"bare":{"platform":"aws-bedrock","region":"eu-west-1","models":{"m":"us.anthropic.claude-test-v1"}}}`)
	profiles := map[string]string{"copilot": "bare"}
	providers, resolved, _ := launchSelection(t, packs, user, map[string]UserProfile{"bare": {Provider: "bare"}}, profiles)
	served := ServedInJail([]string{"aws-auth", "wire-bridge"}).WithListen(declaredListen)
	s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles,
		Resolved: resolved, Served: &served, CallerTokens: awsToken})
	if err != nil {
		t.Fatal(err)
	}
	if wants := ListWants(packs, providers, resolved, s); len(wants) != 0 {
		t.Errorf("wants = %+v for a provider with a list", wants)
	}
	v, _ := providers.Get("bare")
	if !HasModelList(v.(*jsonx.OrderedMap)) {
		t.Error("HasModelList is false for the user's own list")
	}
}
