package packload

import "testing"

// pi's env derive hands the launch's active set to yolo's pi extension as its provider policy
// (docs/design/simultaneous-auth-and-pack-isolation.md §3.1): the set's pi provider IDs,
// deduplicated and sorted, a built-in mapped to pi's own ID, and nothing at all without a
// profile, so pi keeps its native behavior. The shipped pi pack, through the gate that runs it.
func TestPiHandsItsActiveSetAsAProviderRequestPolicy(t *testing.T) {
	packs := embeddedNamed(t, "pi", "bedrock", "openai-auth")
	providers := userProviders(t, `{
	  "zai":{"endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  "openai-codex":{"endpoints":{"openai-responses":{"base_url":"https://chatgpt.example/codex"}}},
	  "bedrock":{"platform":"aws-bedrock","region":"us-west-2"}}`)
	resolved := map[string]ResolvedProfile{
		"zai":     {Provider: "zai"},
		"zai-two": {Provider: "zai"},
		"codex":   {Provider: "openai-codex"},
		"bedrock": {Provider: "bedrock"},
	}
	policy := func(profile string, sets map[string][]string) (string, bool) {
		t.Helper()
		profiles := map[string]string{}
		if profile != "" {
			profiles["pi"] = profile
		}
		scope, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers,
			Resolved: resolved, Profiles: profiles, Sets: sets})
		if err != nil {
			t.Fatal(err)
		}
		d := scope.Agent("pi")
		if d == nil {
			return "", false
		}
		for _, v := range d.Shape {
			if v.Key == "YOLO_PI_PROVIDER_POLICY" {
				return v.Value, true
			}
		}
		return "", false
	}
	doc := func(ids, profiles string) string {
		return `{"schemaVersion":1,"mode":"allowlist","allowedProviderIds":[` + ids + `],"profiles":[` + profiles + `]}`
	}
	for _, tc := range []struct {
		name, profile string
		sets          map[string][]string
		want          string
	}{
		{"one profile", "zai", nil, doc(`"zai"`, `"zai"`)},
		{"a set of two, sorted; profiles in set order", "zai", map[string][]string{"pi": {"zai", "codex"}}, doc(`"openai-codex","zai"`, `"zai","codex"`)},
		{"two profiles on one provider deduplicate", "zai", map[string][]string{"pi": {"zai", "zai-two"}}, doc(`"zai"`, `"zai","zai-two"`)},
		{"native Bedrock is pi's own amazon-bedrock", "bedrock", nil, doc(`"amazon-bedrock"`, `"bedrock"`)},
		{"Bedrock later in the set", "zai", map[string][]string{"pi": {"zai", "bedrock"}}, doc(`"amazon-bedrock","zai"`, `"zai","bedrock"`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := policy(tc.profile, tc.sets)
			if !ok || got != tc.want {
				t.Errorf("YOLO_PI_PROVIDER_POLICY = %q (set %v), want %q", got, ok, tc.want)
			}
		})
	}
	if got, ok := policy("", nil); ok {
		t.Errorf("pi with no profile must keep its native behavior and get no policy, got %q", got)
	}
}
