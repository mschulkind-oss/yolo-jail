package packload

// credentialgrant_test.go pins the gate's half of THE GRANT (ScopeInput.Grants,
// docs/design/credential-sources-separation.md OQ-ES5, ruled for the host 2026-09-27): a granted
// process receives the named providers' claimed env_sources values and nothing else a
// selection would bring — no provider selected, so no derive, no pre-flight demand, and no
// widening of the derive's lookup. The host's call site is pinned in internal/cli.

import (
	"strings"
	"testing"
)

func TestScopeCredentialsGrantDeliversClaimedKeysOnly(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers: twoProviders(t),
		Profiles:  map[string]string{"pi": "zai-profile"},
		Resolved: map[string]ResolvedProfile{
			"zai-profile": {Provider: "zai"}, "routes-profile": {Provider: "routes"},
		},
		EnvSources: hydrated("ZAI_API_KEY", "z", "CEREBRAS_API_KEY", "c",
			"ROUTE_BEARER", "b", "GH_TOKEN", "g"),
		Grants: map[string][]string{"bash": {"cerebras", "routes", "cerebras"}, "pi": {"routes"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(scope.EnvSourcesFor("bash").Keys(), ","); got != "CEREBRAS_API_KEY,ROUTE_BEARER,GH_TOKEN" {
		t.Errorf("a grant-only process receives the granted claims and the shared values: %s", got)
	}
	d := scope.Agent("bash")
	if d == nil || d.Profile != "" || d.Provider != "" || len(d.Shape) != 0 {
		t.Fatalf("a grant selects no profile and composes no shape: %+v", d)
	}
	if got := strings.Join(d.Granted, ","); got != "cerebras,routes" {
		t.Errorf("Granted = %s, want the named providers sorted and deduplicated", got)
	}
	// pi keeps its own profile's key and additionally receives the granted one.
	if got := strings.Join(scope.EnvSourcesFor("pi").Keys(), ","); got != "ZAI_API_KEY,ROUTE_BEARER,GH_TOKEN" {
		t.Errorf("pi on zai granted routes: %s", got)
	}
	// Selection is unchanged: the pre-flight asks only pi's zai, never a granted provider.
	if got := strings.Join(scope.SelectedProviders(), ","); got != "zai" {
		t.Errorf("SelectedProviders = %s, want zai alone — a grant selects nothing", got)
	}
	// The derive's lookup is not widened: pi's derive cannot see a granted provider's key.
	if v, ok := scope.LookupFor("pi")("ROUTE_BEARER"); ok {
		t.Errorf("pi's derive lookup found the granted ROUTE_BEARER=%q", v)
	}
	// The disclosure names the grant's recipients as recipients.
	got := strings.Join(scope.Disclosure(), "\n")
	for _, want := range []string{"CEREBRAS_API_KEY (provider cerebras): bash only",
		"ROUTE_BEARER (provider routes): bash, pi only", "ZAI_API_KEY (provider zai): pi only"} {
		if !strings.Contains(got, want) {
			t.Errorf("the disclosure must say %q:\n%s", want, got)
		}
	}
	// GrantedTo reports each granted provider's delivery, and every name it claims.
	var report []string
	for _, g := range scope.GrantedTo("bash") {
		report = append(report, g.Provider+"="+strings.Join(g.Delivered, "+")+"/"+strings.Join(g.Claims, "+"))
	}
	if got := strings.Join(report, " "); got != "cerebras=CEREBRAS_API_KEY/CEREBRAS_API_KEY routes=ROUTE_BEARER/ROUTE_BEARER+ROUTE_PAIR_ID" {
		t.Errorf("GrantedTo(bash) = %s", got)
	}
	if scope.GrantedTo("nobody") != nil {
		t.Error("a process with no grant reports none")
	}
}

// With no grant the gate's answer is what it always was, which is every jail launch.
func TestScopeCredentialsNoGrantIsUnchanged(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers:  twoProviders(t),
		Resolved:   map[string]ResolvedProfile{},
		EnvSources: hydrated("ZAI_API_KEY", "z", "GH_TOKEN", "g"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(scope.Agents()) != 0 || strings.Join(scope.EnvSourcesFor("bash").Keys(), ",") != "GH_TOKEN" {
		t.Errorf("no grant, no profile: no delivery, the shared values only")
	}
}

// `all` names every provider claiming a name env_sources holds, from the gate's own claims.
func TestClaimingProvidersIsEveryClaimantOfAHeldName(t *testing.T) {
	got := ClaimingProviders(twoProviders(t), hydrated("ROUTE_PAIR_ID", "p", "ZAI_API_KEY", "z", "GH_TOKEN", "g"))
	if strings.Join(got, ",") != "routes,zai" {
		t.Errorf("ClaimingProviders = %v, want routes and zai (cerebras claims nothing held)", got)
	}
	if len(ClaimingProviders(nil, nil)) != 0 {
		t.Error("an empty table claims nothing")
	}
}

// The disclosure's rule line must be true of the launch it heads (ES-D22). Under a grant a key
// reaches a process whose profile selects nothing, so "reaches only the agents whose profile
// selects it" would be contradicted by the very next line. With no grant, as at every jail
// launch, the rule line is unchanged.
func TestDisclosureHeaderNamesTheGrantAsARecipientRule(t *testing.T) {
	in := ScopeInput{
		Providers:  twoProviders(t),
		Profiles:   map[string]string{"pi": "zai-profile"},
		Resolved:   map[string]ResolvedProfile{"zai-profile": {Provider: "zai"}},
		EnvSources: hydrated("ZAI_API_KEY", "z", "CEREBRAS_API_KEY", "c"),
	}
	plain, err := ScopeCredentials(in)
	if err != nil {
		t.Fatal(err)
	}
	const selectsOnly = "Credential scope: a provider's credential reaches only the agents whose profile selects it."
	if got := plain.Disclosure(); len(got) == 0 || got[0] != selectsOnly {
		t.Errorf("without a grant the rule line is unchanged: %q", got)
	}
	in.Grants = map[string][]string{"usage-bar": {"cerebras"}}
	granted, err := ScopeCredentials(in)
	if err != nil {
		t.Fatal(err)
	}
	got := granted.Disclosure()
	want := "Credential scope: a provider's credential reaches only the processes whose profile " +
		"selects it or whose --with-credentials grant names it."
	if len(got) == 0 || got[0] != want {
		t.Errorf("under a grant the rule line must name the grant as a recipient rule:\n got %q\nwant %q", got, want)
	}
}
