package packload

// providerfacts_test.go pins OQ-BR8 (docs/design/providers-and-profiles-redesign.md, ruled
// 2026-09-29) end to end, through the credential gate every vehicle reads (ScopeCredentials),
// which runs each agent's real env derive over the shipped packs: every provider fact that
// used to be gated on a profile NAME keys on the PROVIDER now, so a second profile over a
// shipped provider (trap D5's `bedrock-sso`) and a user's own provider that declares the
// platform (`bedrock-eu`) get what the shipped profile gets. The maintainer: "just because you
// have a differently named profile here doesn't mean that things should happen differently."
//
// Each fact is asserted where it is delivered — the agent's shape vars (its env derive's
// output) and its pack env fold — and each case would fail with the fact back on a name gate,
// because none of the profiles below is named what the shipped gate named.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// awsToken is the caller token a launch mints for the aws-auth adapter, as ServedInJail's
// payload would; the pointer names it ({caller_token}) and is withheld without one.
var awsToken = map[string]string{"YOLO_SERVICE_AWS_AUTH_TOKEN": strings.Repeat("ab", 32)}

// factsFor runs the gate over packs with profiles selected, the aws-auth adapter served, and
// returns the scope.
func factsFor(t *testing.T, packs []*Pack, user *jsonx.OrderedMap, userProfiles map[string]UserProfile,
	profiles map[string]string) *CredentialScope {
	t.Helper()
	providers, resolved, _ := launchSelection(t, packs, user, userProfiles, profiles)
	served := ServedInJail([]string{"aws-auth", "wire-bridge"}).WithListen(declaredListen)
	s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles,
		Resolved: resolved, Served: &served, CallerTokens: awsToken})
	if err != nil {
		t.Fatalf("the gate refused: %v", err)
	}
	return s
}

func shapeHas(d *AgentDelivery, key, want string) bool {
	if d == nil {
		return false
	}
	for _, v := range d.Shape {
		if v.Key == key && !v.Unset && (want == "" || v.Value == want) {
			return true
		}
	}
	return false
}

func foldHasKey(fold []EnvFoldEntry, key string) bool {
	for _, e := range fold {
		if e.Key == key {
			return true
		}
	}
	return false
}

func shapeKeys(d *AgentDelivery) []string {
	if d == nil {
		return nil
	}
	var out []string
	for _, v := range d.Shape {
		out = append(out, v.Key)
	}
	return out
}

// BEDROCK: claude's own switch and aws-auth's credentials pointer, for the shipped profile, a
// user profile over the shipped provider, and a user's own Bedrock provider. The profile-served
// adapter starts for each, since its gate is the pointer's.
func TestEveryBedrockSelectionGetsEveryBedrockFact(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	user := userProviders(t, `{"bedrock":{"region":"us-west-2"},
	  "bedrock-eu":{"platform":"aws-bedrock","region":"eu-west-1"}}`)
	userProfiles := map[string]UserProfile{
		"bedrock-sso": {Provider: "bedrock"},
		"eu":          {Provider: "bedrock-eu"},
	}
	for _, tc := range []struct{ name, profile, region string }{
		{"the shipped profile", "bedrock", "us-west-2"},
		{"a user profile over the shipped provider (D5)", "bedrock-sso", "us-west-2"},
		{"a user's own provider with the platform (D5)", "eu", "eu-west-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profiles := map[string]string{"claude": tc.profile}
			s := factsFor(t, packs, user, userProfiles, profiles)
			d := s.Agent("claude")
			if !shapeHas(d, "CLAUDE_CODE_USE_BEDROCK", "1") {
				t.Errorf("claude on %s must get CLAUDE_CODE_USE_BEDROCK=1, got shape %v", tc.profile, shapeKeys(d))
			}
			if !shapeHas(d, "AWS_REGION", tc.region) {
				t.Errorf("claude on %s must get AWS_REGION=%s", tc.profile, tc.region)
			}
			fold := s.FoldFor("claude")
			for _, k := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"} {
				if !foldHasKey(fold, k) {
					t.Errorf("claude on %s must get aws-auth's %s", tc.profile, k)
				}
			}
			if foldHasKey(s.FoldFor(""), "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
				t.Error("the pointer must reach claude alone, never the shared fold")
			}
			for _, d := range UnselectedProfileServedDaemons(packs, s.Selection()) {
				if d.Name == "aws-auth" {
					t.Errorf("the aws-auth adapter must start for %s", tc.profile)
				}
			}
		})
	}

	// THE CONTROL: claude on a provider that is not Bedrock gets none of it, and the adapter
	// stays down, naming the platform that would start it.
	s := factsFor(t, packs, user, userProfiles, map[string]string{"claude": "codex"})
	if shapeHas(s.Agent("claude"), "CLAUDE_CODE_USE_BEDROCK", "") ||
		foldHasKey(s.FoldFor("claude"), "AWS_CONTAINER_CREDENTIALS_FULL_URI") {
		t.Error("claude on openai-codex must get no Bedrock fact")
	}
	down := UnselectedProfileServedDaemons(packs, s.Selection())
	if len(down) != 1 || down[0].Name != "aws-auth" || strings.Join(down[0].Platforms, ",") != "aws-bedrock" {
		t.Errorf("the aws-auth adapter must stay down, keyed on aws-bedrock: %+v", down)
	}
}

// THE EVERYTHING PROFILE (OQ-BR11, OQ-BR1's `bedrock-bridge`), as OQ-MM6 amended it on 2026-10-05
// (docs/design/model-lists-and-pickers.md): a profile over the Bedrock provider that routes through
// the wire bridge (`via`) runs claude's own Bedrock client POINTED AT THE BRIDGE, with its own
// signing skipped, so the bridge alone signs; and aws-auth's pointer still reaches claude, for the
// bridge to sign with. Until then the switch was forbidden there (the old Part 2's "never
// CLAUDE_CODE_USE_BEDROCK"), and claude was routed at the Messages adapter instead.
func TestTheBridgedBedrockProfileRunsClaudesBedrockModeAtTheBridge(t *testing.T) {
	packs := embeddedNamed(t, "claude", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	user := userProviders(t, `{"bedrock":{"region":"us-west-2"}}`)
	userProfiles := map[string]UserProfile{"bedrock-bridge": {Provider: "bedrock", Via: "wire-bridge"}}
	s := factsFor(t, packs, user, userProfiles, map[string]string{"claude": "bedrock-bridge"})
	d := s.Agent("claude")
	for _, kv := range [][2]string{{"CLAUDE_CODE_USE_BEDROCK", "1"}, {"CLAUDE_CODE_SKIP_BEDROCK_AUTH", "1"},
		{"ANTHROPIC_BEDROCK_BASE_URL", "http://127.0.0.1:8214"}} {
		if !shapeHas(d, kv[0], kv[1]) {
			t.Errorf("a bridged Bedrock profile must run claude's Bedrock mode at the bridge (%s=%s): %v",
				kv[0], kv[1], shapeKeys(d))
		}
	}
	if shapeHas(d, "ANTHROPIC_BASE_URL", "") {
		t.Errorf("a bridged Bedrock profile must not route claude at the Messages adapter: %v", shapeKeys(d))
	}
	for _, k := range []string{"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"} {
		if !foldHasKey(s.FoldFor("claude"), k) {
			t.Errorf("a bridged Bedrock profile must still get aws-auth's %s", k)
		}
	}
	// The control, same launch shape without the via: claude's own client, signing itself.
	native := factsFor(t, packs, user, userProfiles, map[string]string{"claude": "bedrock"})
	if !shapeHas(native.Agent("claude"), "CLAUDE_CODE_USE_BEDROCK", "1") ||
		shapeHas(native.Agent("claude"), "ANTHROPIC_BEDROCK_BASE_URL", "") ||
		shapeHas(native.Agent("claude"), "CLAUDE_CODE_SKIP_BEDROCK_AUTH", "") {
		t.Error("control: the native profile over the same provider runs claude's own client, signing itself")
	}
}

// LLAMACPP'S ATTRIBUTION HEADER is the provider's option now: a user profile over llamacpp gets
// it, and only claude, the agent that reads the variable, is handed it.
func TestTheAttributionHeaderFollowsTheLlamacppProvider(t *testing.T) {
	packs := embeddedNamed(t, "claude", "pi", "llamacpp", "openai-auth", "aws-auth", "bedrock", "wire-bridge")
	userProfiles := map[string]UserProfile{"local": {Provider: "llamacpp"}}
	s := factsFor(t, packs, nil, userProfiles, map[string]string{"claude": "local", "pi": "local"})
	if !shapeHas(s.Agent("claude"), "CLAUDE_CODE_ATTRIBUTION_HEADER", "0") {
		t.Errorf("claude on a user profile over llamacpp must get CLAUDE_CODE_ATTRIBUTION_HEADER=0: %v",
			shapeKeys(s.Agent("claude")))
	}
	if shapeHas(s.Agent("pi"), "CLAUDE_CODE_ATTRIBUTION_HEADER", "") ||
		foldHasKey(s.FoldFor("pi"), "CLAUDE_CODE_ATTRIBUTION_HEADER") {
		t.Error("pi does not read claude's attribution header and must not be handed it")
	}
	// A profile may turn it back on: the option is the profile surface.
	on := map[string]UserProfile{"local": {Provider: "llamacpp", Options: map[string]string{"attribution_header": "true"}}}
	s = factsFor(t, packs, nil, on, map[string]string{"claude": "local"})
	if !shapeHas(s.Agent("claude"), "CLAUDE_CODE_ATTRIBUTION_HEADER", "1") {
		t.Error("a profile setting attribution_header true must send the header")
	}
}

// THE OPENAI LOGIN PRELAUNCH follows openai-codex, for pi and for claude, under a profile of any
// name. Its launcher reads the variables from the agent's own env file, so the shape vars are
// the delivery.
func TestTheOpenAIPrelaunchFollowsTheSubscriptionProvider(t *testing.T) {
	packs := embeddedNamed(t, "claude", "pi", "openai-auth", "aws-auth", "bedrock", "wire-bridge")
	userProfiles := map[string]UserProfile{"sub": {Provider: "openai-codex"}}
	for _, profile := range []string{"codex", "sub"} {
		s := factsFor(t, packs, nil, userProfiles, map[string]string{"claude": profile, "pi": profile})
		if !shapeHas(s.Agent("pi"), "YOLO_AUTH_PRELAUNCH_PI_FLAG", "--pi-auth") ||
			!shapeHas(s.Agent("pi"), "YOLO_AUTH_PRELAUNCH_PI_PATH", ".pi/agent/auth.json") {
			t.Errorf("pi on %s must get its OpenAI login prelaunch: %v", profile, shapeKeys(s.Agent("pi")))
		}
		if !shapeHas(s.Agent("claude"), "YOLO_AUTH_PRELAUNCH_CLAUDE_LOGIN", "1") {
			t.Errorf("claude on %s must get its OpenAI login prelaunch: %v", profile, shapeKeys(s.Agent("claude")))
		}
	}
	s := factsFor(t, packs, nil, nil, map[string]string{"pi": "bedrock"})
	if shapeHas(s.Agent("pi"), "YOLO_AUTH_PRELAUNCH_PI_FLAG", "") {
		t.Error("pi on another provider must get no OpenAI prelaunch")
	}
}

// THE CLAIMS FOLLOW THE PLATFORM (PP-D9): a Bedrock provider of the user's own, declaring the
// platform and no `api_key_env_name`, co-claims the AWS names the shipped `bedrock` beside it
// claims, so the agent on it receives the env_sources AWS credentials and every other process
// still does not. Before, the shipped provider was their only claimant, so the gate withheld
// them from claude on `bedrock-eu` and from the shared set alike: claude started in Bedrock
// mode with no AWS credential, while the docs said such a key reached every process.
func TestAUserBedrockProviderReceivesTheAWSCredentialsItsPlatformClaims(t *testing.T) {
	packs := embeddedNamed(t, "claude", "pi", "aws-auth", "bedrock", "openai-auth", "wire-bridge")
	awsNames := []string{"AWS_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_BEARER_TOKEN_BEDROCK"}
	sources := jsonx.NewOrderedMap()
	for _, k := range awsNames {
		sources.Set(k, "value-of-"+strings.ToLower(k))
	}
	sources.Set("UNRELATED", "shared")
	user := userProviders(t, `{"bedrock":{"region":"us-west-2"},
	  "bedrock-eu":{"platform":"aws-bedrock","region":"eu-west-1"},
	  "bedrock-own":{"platform":"aws-bedrock","region":"eu-west-2","api_key_env_name":"AWS_BEARER_TOKEN_BEDROCK"}}`)
	userProfiles := map[string]UserProfile{
		"eu":  {Provider: "bedrock-eu"},
		"own": {Provider: "bedrock-own"},
	}
	scope := func(profiles map[string]string) *CredentialScope {
		t.Helper()
		providers, resolved, _ := launchSelection(t, packs, user, userProfiles, profiles)
		s, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Profiles: profiles,
			Resolved: resolved, EnvSources: sources, NoDerives: true})
		if err != nil {
			t.Fatalf("the gate refused: %v", err)
		}
		return s
	}
	has := func(m *jsonx.OrderedMap, k string) bool { _, ok := m.Get(k); return ok }

	for _, profile := range []string{"eu", "bedrock"} {
		s := scope(map[string]string{"claude": profile, "pi": "codex"})
		for _, k := range awsNames {
			if !has(s.EnvSourcesFor("claude"), k) {
				t.Errorf("claude on %s must receive %s", profile, k)
			}
			if has(s.EnvSourcesFor("pi"), k) || has(s.EnvSourcesFor(""), k) {
				t.Errorf("claude on %s: %s must still reach no other process", profile, k)
			}
		}
		if !has(s.EnvSourcesFor(""), "UNRELATED") {
			t.Error("a variable no provider claims still reaches every process")
		}
	}

	// A provider that lists its own names keeps exactly those: a declaration is never widened.
	s := scope(map[string]string{"claude": "own"})
	if !has(s.EnvSourcesFor("claude"), "AWS_BEARER_TOKEN_BEDROCK") {
		t.Error("claude on a provider listing AWS_BEARER_TOKEN_BEDROCK must receive it")
	}
	if has(s.EnvSourcesFor("claude"), "AWS_PROFILE") {
		t.Error("a provider listing its own credential names must not inherit its platform's others")
	}
}
