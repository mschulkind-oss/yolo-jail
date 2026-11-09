package packload

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"strings"
	"testing"
)

func TestAPrivateNameNothingClaimsReachesNobody(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers:  twoProviders(t),
		Profiles:   map[string]string{"pi": "zai-profile"},
		Resolved:   map[string]ResolvedProfile{"zai-profile": {Provider: "zai"}},
		EnvSources: hydrated("ZAI_API_KEY", "z", "STRIPE_KEY", "s", "GH_TOKEN", "g"),
		Private:    map[string]bool{"ZAI_API_KEY": true, "STRIPE_KEY": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scope.SharedEnvSources().Keys(), ","); got != "GH_TOKEN" {
		t.Errorf("shared = %s, want only the public GH_TOKEN", got)
	}
	if got := strings.Join(scope.Agent("pi").EnvSources.Keys(), ","); got != "ZAI_API_KEY" {
		t.Errorf("pi's own = %s, want its provider's private key", got)
	}
	if scope.DeliversEnvSource("STRIPE_KEY") {
		t.Error("a private name nothing claims reaches no process (row 4)")
	}
	lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n")
	want := "  STRIPE_KEY: withheld from every process — nothing in this launch claims it. " +
		"If no provider or MCP server needs it, it belongs in the public set: yolo config unsecret STRIPE_KEY"
	if !strings.Contains(lines, want) || strings.Contains(lines, "=s") {
		t.Errorf("row 4's line, names only:\n%s", lines)
	}
}

func TestAnMCPServersRequiresEnvClaimsAPrivateNameForItsAgents(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers:  twoProviders(t),
		EnvSources: hydrated("TAVILY_API_KEY", "t"),
		Private:    map[string]bool{"TAVILY_API_KEY": true},
		MCPClaims:  map[string][]string{"TAVILY_API_KEY": {"tavily"}},
		MCPAgents:  []string{"claude", "pi"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range []string{"claude", "pi"} {
		if _, ok := scope.EnvSourcesFor(agent).Get("TAVILY_API_KEY"); !ok {
			t.Errorf("%s names the server, so it receives the key", agent)
		}
	}
	if _, ok := scope.EnvSourcesFor("").Get("TAVILY_API_KEY"); ok {
		t.Error("a bare shell does not name the server and must not receive the key")
	}
	if lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n"); !strings.Contains(lines, "  TAVILY_API_KEY (mcp tavily): claude, pi only") {
		t.Errorf("the MCP claim's line:\n%s", lines)
	}
}

func TestAPublicNameOnlyAnMCPServerClaimsStaysShared(t *testing.T) {
	scope, _ := ScopeCredentials(ScopeInput{
		EnvSources: hydrated("TAVILY_API_KEY", "t"),
		MCPClaims:  map[string][]string{"TAVILY_API_KEY": {"tavily"}},
		MCPAgents:  []string{"claude"},
	})
	if _, ok := scope.SharedEnvSources().Get("TAVILY_API_KEY"); !ok {
		t.Error("SE-D15: an MCP claim never narrows a public value")
	}
	if len(scope.PublicClaims()) != 0 {
		t.Error("only a provider's claim makes a public value a refusal")
	}
}

func TestAPublicNameAProviderClaimsIsReportedAndNotWidened(t *testing.T) {
	scope, _ := ScopeCredentials(ScopeInput{
		Providers:  twoProviders(t),
		EnvSources: hydrated("ZAI_API_KEY", "z"),
	})
	pc := scope.PublicClaims()
	if len(pc) != 1 || pc[0].Name != "ZAI_API_KEY" || strings.Join(pc[0].Providers, ",") != "zai" {
		t.Errorf("PublicClaims = %+v, want ZAI_API_KEY claimed by zai", pc)
	}
	if _, ok := scope.SharedEnvSources().Get("ZAI_API_KEY"); ok {
		t.Error("SEP-D4: a public claimed name stays scoped while the caller refuses")
	}
}

func TestAnUnselectedShippedProviderWordsAPublicNamesLine(t *testing.T) {
	scope, _ := ScopeCredentials(ScopeInput{
		EnvSources:       hydrated("ZAI_API_KEY", "z"),
		UnselectedClaims: map[string][]ShippedClaim{"ZAI_API_KEY": {{Provider: "zai", Pack: "zai"}}},
	})
	if _, ok := scope.SharedEnvSources().Get("ZAI_API_KEY"); !ok {
		t.Error("row 1: the public declaration stands when nothing selected claims the name")
	}
	want := "  ZAI_API_KEY reaches every process from your public environment; the zai provider, " +
		"which this launch does not select, claims that name. If it is zai's key: yolo config secret ZAI_API_KEY"
	if lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n"); !strings.Contains(lines, want) {
		t.Errorf("row 1's unselected line:\n%s", lines)
	}
}

func TestAPrivateNameAnUnselectedPackClaimsNamesThePack(t *testing.T) {
	scope, _ := ScopeCredentials(ScopeInput{
		EnvSources:       hydrated("ZAI_API_KEY", "z"),
		Private:          map[string]bool{"ZAI_API_KEY": true},
		UnselectedClaims: map[string][]ShippedClaim{"ZAI_API_KEY": {{Provider: "zai", Pack: "zai"}}},
	})
	want := "  ZAI_API_KEY: withheld from every process — nothing in this launch claims it. To use it as zai's key: yolo pack add zai."
	if lines := strings.Join(scope.DisclosureWith(DisclosureNotes{}), "\n"); !strings.Contains(lines, want) {
		t.Errorf("row 4's pack remedy:\n%s", lines)
	}
}

func TestTheFallbackStillScopesAClaimedNameNobodyHydrated(t *testing.T) {
	scope, _ := ScopeCredentials(ScopeInput{
		Providers: twoProviders(t),
		Profiles:  map[string]string{"pi": "routes-profile"},
		Resolved:  map[string]ResolvedProfile{"routes-profile": {Provider: "routes"}},
		NoDerives: true,
		Fallback:  func(string) (string, bool) { return "from-shell", true },
	})
	if v, ok := scope.LookupFor("pi")("ZAI_API_KEY"); ok {
		t.Errorf("a claimed name only the launching shell holds reaches its claimants only, got %q", v)
	}
}

// The shipped set words the lines (SE-D8): with nothing selected, zai's pack claims ZAI_API_KEY;
// once zai is selected, it is no longer "unselected".
func TestUnselectedProviderClaimsReadsTheShippedSetMinusTheSelection(t *testing.T) {
	claims := UnselectedProviderClaims(nil)
	if got := claims["ZAI_API_KEY"]; len(got) == 0 || got[0].Provider != "zai" || got[0].Pack != "zai" {
		t.Fatalf("ZAI_API_KEY's shipped claim = %+v, want zai from pack zai", got)
	}
	var zai *Pack
	for _, p := range Embedded() {
		if p.Name == "zai" {
			zai = p
		}
	}
	if zai == nil {
		t.Fatal("the zai pack is not embedded")
	}
	if got := UnselectedProviderClaims([]*Pack{zai})["ZAI_API_KEY"]; len(got) != 0 {
		t.Errorf("a selected pack's claim is not an unselected one: %+v", got)
	}
}

// SE-D13 / SEP-D5: requires_env claims, a removed entry claims nothing, and only a pack with a
// derive.lua names agents.
func TestMCPEnvClaimsAndAgents(t *testing.T) {
	servers := jsonx.NewOrderedMap()
	tavily := jsonx.NewOrderedMap()
	tavily.Set("requires_env", []any{"TAVILY_API_KEY"})
	servers.Set("tavily", tavily)
	servers.Set("gone", nil)
	claims := MCPEnvClaims(servers)
	if got := strings.Join(claims["TAVILY_API_KEY"], ","); got != "tavily" || len(claims) != 1 {
		t.Errorf("claims = %v", claims)
	}
	var withDerive, without []*Pack
	for _, p := range Embedded() {
		switch {
		case p.Name == "claude":
			withDerive = append(withDerive, p)
		case p.Name == "guardrails":
			without = append(without, p)
		}
	}
	if got := strings.Join(MCPAgents(withDerive), ","); got != "claude" {
		t.Errorf("MCPAgents(claude) = %q", got)
	}
	if got := MCPAgents(without); len(got) != 0 {
		t.Errorf("a pack with no derive.lua renders no MCP table: %v", got)
	}
}
