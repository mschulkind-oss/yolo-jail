package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// deriveenv_test.go pins the env-derive runner (OQ-CS8/OQ-PT9): the agent's own pack
// states the provider→environment binding in its derive.lua, and AgentEnv is the one
// runner that turns it into agentenv.Vars. Its production call sites — the jail notch's
// packChannel and the host notch's composition — are pinned in their own packages
// (internal/cli/run, internal/cli); what belongs here is the runner's own contract.

// envZaiPack ships the zai provider facts and a profile NAMED SOMETHING ELSE that
// selects them — the mismatch is load-bearing, exactly as in providerfor_test.go: with
// the profile named "zai" the profile-name fallback would return the same string and a
// selected_provider assertion could not tell resolution from luck.
func envZaiPack(t *testing.T) *Pack {
	t.Helper()
	return &Pack{Name: "zai", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai",
	   "api_key_env_name":"ZAI_API_KEY",
	   "endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}}},
	  {"kind":"profile","name":"glm","provider":"zai"}]}`)}
}

// envClaudePack installs the claude bin and carries the given derive.lua at its root —
// the two things the runner needs from the agent's pack: installsBin finds it,
// DeriveScript reads it.
func envClaudePack(t *testing.T, deriveLua string) *Pack {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(deriveLua), 0o644); err != nil {
		t.Fatalf("writing derive.lua: %v", err)
	}
	return &Pack{Name: "claude", Root: root, Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"}]}`)}
}

// envLookup answers the one variable the fixtures hydrate.
func envLookup(name string) (string, bool) {
	if name == "ZAI_API_KEY" {
		return "tok-9", true
	}
	return "", false
}

// The whole delivery in one: the profile resolves to the zai provider, the producer
// reads the HYDRATED entry (api_key filled from the lookup), and the vars come back
// sorted. This is the shape packs/claude ships for real.
func TestAgentEnvRunsTheAgentPacksProducer(t *testing.T) {
	claude := envClaudePack(t, `
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local out = { PROFILE = ctx.use_profiles.claude }
  out.ANTHROPIC_BASE_URL = p.endpoints.anthropic.base_url
  out.ANTHROPIC_AUTH_TOKEN = p.api_key
  return out
end)`)
	zai := envZaiPack(t)
	providers, err := ComposeProviders(nil, []*Pack{claude, zai})
	if err != nil {
		t.Fatalf("composing providers: %v", err)
	}
	resolved, err := ResolveProfiles([]*Pack{claude, zai}, nil, providers)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	vars, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "glm"}, "claude", "glm", envLookup,
		WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	want := []agentenv.Var{
		{Key: "ANTHROPIC_AUTH_TOKEN", Value: "tok-9"},
		{Key: "ANTHROPIC_BASE_URL", Value: "https://api.z.ai/api/anthropic"},
		{Key: "PROFILE", Value: "glm"},
	}
	if len(vars) != len(want) {
		t.Fatalf("vars = %#v, want %#v", vars, want)
	}
	for i := range want {
		if vars[i] != want[i] {
			t.Errorf("var %d = %#v, want %#v", i, vars[i], want[i])
		}
	}
}

// THE SELECTION HALF of the ctx, pinned where it can only fail for real: the profile is
// declared by the USER alone — `zai-fast` is in no manifest, so a resolution that walked
// the pack manifests falls back to the bare name and indexes a provider that does not
// exist — and the producer still emits the provider's environment. The empty result a
// revert produces is the failure mode this test exists to catch: the launch composes no
// provider env for a profile it accepted, and says nothing about it.
func TestAgentEnvResolvesAUserDeclaredProfileToItsProvider(t *testing.T) {
	claude := envClaudePack(t, `
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  return { ANTHROPIC_BASE_URL = p.endpoints.anthropic.base_url }
end)`)
	zai := envZaiPack(t)
	providers, err := ComposeProviders(nil, []*Pack{claude, zai})
	if err != nil {
		t.Fatalf("composing providers: %v", err)
	}
	resolved, err := ResolveProfiles([]*Pack{claude, zai}, map[string]UserProfile{
		"zai-fast": {Provider: "zai"},
	}, providers)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	vars, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "zai-fast"}, "claude", "zai-fast", envLookup,
		WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	want := []agentenv.Var{{Key: "ANTHROPIC_BASE_URL", Value: "https://api.z.ai/api/anthropic"}}
	if len(vars) != len(want) {
		t.Fatalf("vars = %#v, want %#v — a user-declared profile must select the provider "+
			"it names, not fall back to its own name", vars, want)
	}
	for i := range want {
		if vars[i] != want[i] {
			t.Errorf("var %d = %#v, want %#v", i, vars[i], want[i])
		}
	}
}

// D8, pinned where it could break: the credential crosses into the derive invocation
// ONLY. The composed table the launch relays as YOLO_PROVIDERS must carry no api_key
// before or after the run — hydrating it would put the secret on every container's argv
// and in every process's environment.
func TestAgentEnvHydratesOnlyTheDerivedCopy(t *testing.T) {
	claude := envClaudePack(t, `
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if p and p.api_key then return { ANTHROPIC_AUTH_TOKEN = p.api_key } end
  return {}
end)`)
	zai := envZaiPack(t)
	providers, err := ComposeProviders(nil, []*Pack{claude, zai})
	if err != nil {
		t.Fatalf("composing providers: %v", err)
	}
	resolved, err := ResolveProfiles([]*Pack{claude, zai}, nil, providers)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	if _, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "glm"}, "claude", "glm", envLookup,
		WithResolvedProfiles(resolved)); err != nil {
		t.Fatal(err)
	}
	dumped := jsonDumps(providers)
	if strings.Contains(dumped, "tok-9") || strings.Contains(dumped, `"api_key"`) {
		t.Fatalf("the composed table must stay secret-free (D8); after the env derive it "+
			"reads: %s", dumped)
	}
}

// jsonDumps renders an OrderedMap for an assertion that reads what the launch would
// relay.
func jsonDumps(m *jsonx.OrderedMap) string {
	data, err := jsonx.DumpsCompact(m)
	if err != nil {
		return "<marshal error: " + err.Error() + ">"
	}
	return string(data)
}

// A lookup that finds nothing composes no api_key at all — an empty credential is the
// pre-flight's refusal to make, and the producer's own absent-input rule then drops the
// variable (an empty token is a credential that gets SENT).
func TestAgentEnvComposesNoKeyWhenTheLookupMisses(t *testing.T) {
	claude := envClaudePack(t, `
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  local out = { ANTHROPIC_BASE_URL = p.endpoints.anthropic.base_url }
  if p.api_key then out.ANTHROPIC_AUTH_TOKEN = p.api_key end
  return out
end)`)
	zai := envZaiPack(t)
	providers, _ := ComposeProviders(nil, []*Pack{claude, zai})
	resolved, err := ResolveProfiles([]*Pack{claude, zai}, nil, providers)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}
	miss := func(string) (string, bool) { return "", false }
	vars, err := AgentEnv([]*Pack{claude, zai}, providers,
		map[string]string{"claude": "glm"}, "claude", "glm", miss,
		WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vars {
		if v.Key == "ANTHROPIC_AUTH_TOKEN" {
			t.Errorf("a missing lookup must compose no token, got %#v", v)
		}
	}
}

// The inert identities: no profile at this CLI name, no pack installing the bin, a pack
// with no derive.lua, and a derive.lua that registers no yolo.env for the agent all
// compose NOTHING — the launch proceeds with no provider env, never an error.
func TestAgentEnvInertInputsComposeNothing(t *testing.T) {
	claude := envClaudePack(t, `yolo.env("claude", function() return { X = "1" } end)`)
	noScript := &Pack{Name: "claude", Decl: claude.Decl}
	otherProducer := envClaudePack(t, `yolo.env("pi", function() return { X = "1" } end)`)
	providers, _ := ComposeProviders(nil, []*Pack{claude, envZaiPack(t)})

	cases := []struct {
		name    string
		packs   []*Pack
		profile string
	}{
		{"no profile", []*Pack{claude}, ""},
		{"no agent pack", []*Pack{envZaiPack(t)}, "glm"},
		{"agent pack without derive.lua", []*Pack{noScript}, "glm"},
		{"no yolo.env for this agent", []*Pack{otherProducer}, "glm"},
	}
	for _, tc := range cases {
		vars, err := AgentEnv(tc.packs, providers, map[string]string{"claude": tc.profile},
			"claude", tc.profile, envLookup)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if len(vars) != 0 {
			t.Errorf("%s: composed %#v, want nothing", tc.name, vars)
		}
	}
}

// A tombstoned value is a REMOVAL (agentenv.Var{Unset: true}); a bare empty string
// composes nothing, the rule the placeholder vocabulary enforced — an empty value is an
// absent input, not a variable to set.
func TestAgentEnvTombstoneRemovesAndEmptyDrops(t *testing.T) {
	claude := envClaudePack(t, `
yolo.env("claude", function(ctx)
  return { AWS_PROFILE = ctx.tombstone, ANTHROPIC_BASE_URL = "" , ZONE = "us" }
end)`)
	vars, err := AgentEnv([]*Pack{claude, envZaiPack(t)}, nil,
		map[string]string{"claude": "glm"}, "claude", "glm", envLookup)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentenv.Var{{Key: "AWS_PROFILE", Unset: true}, {Key: "ZONE", Value: "us"}}
	if len(vars) != len(want) {
		t.Fatalf("vars = %#v, want %#v", vars, want)
	}
	for i := range want {
		if vars[i] != want[i] {
			t.Errorf("var %d = %#v, want %#v", i, vars[i], want[i])
		}
	}
}

// A producer that sets a variable to something other than a string (or a tombstone) is
// a broken producer, and the error names the pack, the agent and the variable — this
// composition IS the delivery, so it refuses rather than guessing a coercion.
func TestAgentEnvRejectsNonStringValues(t *testing.T) {
	claude := envClaudePack(t, `yolo.env("claude", function() return { RETRY = 3 } end)`)
	_, err := AgentEnv([]*Pack{claude}, nil, map[string]string{"claude": "glm"},
		"claude", "glm", envLookup)
	if err == nil {
		t.Fatal("a non-string env value must be an error")
	}
	for _, want := range []string{"pack claude", "claude", "RETRY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
}

// A Lua error in the producer is an error out of the runner, attributed to the pack
// that shipped the producer.
func TestAgentEnvAttributesLuaErrors(t *testing.T) {
	claude := envClaudePack(t, `yolo.env("claude", function() error("boom") end)`)
	_, err := AgentEnv([]*Pack{claude}, nil, map[string]string{"claude": "glm"},
		"claude", "glm", envLookup)
	if err == nil {
		t.Fatal("a Lua error in the env producer must surface")
	}
	if !strings.Contains(err.Error(), "pack claude") {
		t.Errorf("error should name the shipping pack: %v", err)
	}
}

// realClaudePack is the SHIPPED claude pack, not a fixture of it — the point of the test
// below is that `packs/claude/derive.lua` composes what the ruling says, so a fixture
// derive would assert the test's own copy of the rule.
func realClaudePack(t *testing.T) *Pack {
	t.Helper()
	for _, p := range Embedded() {
		if p.Name == "claude" {
			return p
		}
	}
	t.Fatal("no embedded pack named claude")
	return nil
}

// providerPack ships one provider with the given endpoints block and a profile that
// selects it. The profile is named something else on purpose, the way envZaiPack's is:
// with both called "p" a selected_provider assertion cannot tell resolution from the
// profile-name fallback returning the same string.
func providerPack(t *testing.T, endpointsJSON string) *Pack {
	t.Helper()
	return &Pack{Name: "vendor", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"p","api_key_env_name":"P_KEY"`+endpointsJSON+`},
	  {"kind":"profile","name":"sel","provider":"p"}]}`)}
}

// TestClaudeDeriveKeepsACredentialWithItsAddress is OQ-2's ruling, run through the real
// packs/claude/derive.lua.
//
// Three provider shapes reach that producer and only the middle one was wrong. Until
// 2026-09-18 a provider that NAMED a protocol claude does not speak, and carried a key,
// had that key composed with no base URL beside it — so a third-party credential went to
// api.anthropic.com. The other two must keep working, and they are why the rule cannot be
// the simpler "only emit a key when routed": a provider naming NO endpoint has repointed
// nothing, and its key is the deliberate BYO-key launch against Anthropic's own API.
func TestClaudeDeriveKeepsACredentialWithItsAddress(t *testing.T) {
	for _, tc := range []struct {
		name      string
		endpoints string
		wantURL   string
		wantToken string
	}{
		{
			name:      "anthropic endpoint: routed, and the key travels with it",
			endpoints: `,"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}}`,
			wantURL:   "https://api.z.ai/api/anthropic",
			wantToken: "tok-9",
		},
		{
			// THE DEFECT: the provider said where it lives and it is not Anthropic.
			name:      "openai endpoint only: no address for claude, so no credential",
			endpoints: `,"endpoints":{"openai":{"base_url":"https://api.cerebras.ai/v1"}}`,
			wantURL:   "",
			wantToken: "",
		},
		{
			// NOT the defect, and the reason the rule reads the DECLARATION rather than
			// the absence of a URL: nothing was repointed, so the key is correct.
			name:      "no endpoints at all: the BYO-key launch against Anthropic's own API",
			endpoints: ``,
			wantURL:   "",
			wantToken: "tok-9",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claude, vendor := realClaudePack(t), providerPack(t, tc.endpoints)
			providers, err := ComposeProviders(nil, []*Pack{claude, vendor})
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := ResolveProfiles([]*Pack{claude, vendor}, nil, providers)
			if err != nil {
				t.Fatal(err)
			}
			vars, err := AgentEnv([]*Pack{claude, vendor}, providers,
				map[string]string{"claude": "sel"}, "claude", "sel",
				func(n string) (string, bool) { return "tok-9", n == "P_KEY" },
				WithResolvedProfiles(resolved))
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]string{}
			for _, v := range vars {
				got[v.Key] = v.Value
			}
			if got["ANTHROPIC_BASE_URL"] != tc.wantURL {
				t.Errorf("ANTHROPIC_BASE_URL = %q, want %q", got["ANTHROPIC_BASE_URL"], tc.wantURL)
			}
			if got["ANTHROPIC_AUTH_TOKEN"] != tc.wantToken {
				t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want %q.\n\nA credential travels with the "+
					"address it was minted for, or not at all: a provider that named a protocol "+
					"claude does not speak is one claude cannot reach, so its key must not be "+
					"composed — while a provider that named no protocol has repointed nothing, "+
					"and its key is correct.", got["ANTHROPIC_AUTH_TOKEN"], tc.wantToken)
			}
		})
	}
}
