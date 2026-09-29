package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// launchEnvFor is one agent's launch environment as the credential gate composes it over packs
// (packload.ScopeCredentials, the function every vehicle reads): its pack env fold and its env
// derive's shape vars, under the selection a launch resolves — the provider table composed and
// the profiles resolved, so a `platform` gate and a derive keyed on the provider both answer.
// No served set is passed, so a pointer comes back with its declared value unresolved
// ({listen}, {caller_token}). Since OQ-BR8 moved the provider facts out of name-gated `env`
// into the agents' derives (docs/design/providers-and-profiles-redesign.md), the fold alone is
// not what an agent is launched with; this is.
func launchEnvFor(t *testing.T, packs []*packload.Pack, profiles map[string]string, agent string) map[string]string {
	t.Helper()
	env, err := launchEnvOf(packs, profiles, agent)
	if err != nil {
		t.Fatalf("composing %s's launch environment: %v", agent, err)
	}
	return env
}

// launchEnvOf is launchEnvFor's body, returning the gate's refusal instead of failing.
func launchEnvOf(packs []*packload.Pack, profiles map[string]string, agent string) (map[string]string, error) {
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		return nil, err
	}
	resolved, err := packload.ResolveProfiles(packs, nil, providers)
	if err != nil {
		return nil, err
	}
	scope, err := packload.ScopeCredentials(packload.ScopeInput{Packs: packs, Providers: providers,
		Profiles: profiles, Resolved: resolved})
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, e := range scope.FoldFor(agent) {
		env[e.Key] = e.Value
	}
	if d := scope.Agent(agent); d != nil {
		for _, v := range d.Shape {
			if v.Unset {
				delete(env, v.Key)
				continue
			}
			env[v.Key] = v.Value
		}
	}
	return env, nil
}
