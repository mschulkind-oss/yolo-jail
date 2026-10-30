package packload

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// providerpacks_test.go pins the provider packs of docs/design/provider-enablement.md: one pack per
// subscription provider, named `<agent>-auth` for the vendor's agent, holding the provider, a
// profile named for the agent and any credential service (OQ-PE3, OQ-PE5); each vendor agent pack
// needing its own (OQ-PE1, OQ-PE2, PE-D5); the old names refused naming the new ones (OQ-PE3,
// PE-D7); the undeclared-profile refusal typing out the two-line declaration for a name a selected
// agent has built in (OQ-PE4); and the subscription providers' pinned endpoints (PE-D4).

func embeddedLookup(name string) (*Pack, bool) {
	for _, p := range Embedded() {
		if p.Name == name {
			return p, true
		}
	}
	return nil, false
}

// EVERY VENDOR AGENT PACK BRINGS ITS OWN PROVIDER PACK, and the launch says so; pi, which is no
// vendor's agent, brings none of them.
func TestEachVendorAgentBringsItsOwnProviderPack(t *testing.T) {
	for agent, want := range map[string]string{"claude": "claude-auth", "codex": "codex-auth", "copilot": "copilot-auth"} {
		added, causes, err := ResolveNeeds(embeddedNamed(t, agent), embeddedLookup)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, p := range added {
			names = append(names, p.Name)
		}
		if !slices.Contains(names, want) {
			t.Errorf("%s's closure added %v, want %s among them", agent, names, want)
		}
		if cause := "+ " + want + " (needed by " + agent + ")"; !slices.Contains(causes, cause) {
			t.Errorf("%s: causes %v do not disclose %q", agent, causes, cause)
		}
	}
	added, _, err := ResolveNeeds(embeddedNamed(t, "pi"), embeddedLookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range added {
		if strings.HasSuffix(p.Name, "-auth") && p.Name != "aws-auth" {
			t.Errorf("pi's closure brought %s; only a vendor's agent brings its provider pack", p.Name)
		}
	}
}

// THE NAMES (OQ-PE3, PE-D6): the pack is `<agent>-auth`, the profile the bare agent name, and the
// provider keeps the id the agents' own clients key it by.
func TestTheProviderPacksDeclareAProfileNamedForTheAgent(t *testing.T) {
	for pack, want := range map[string][2]string{
		"claude-auth":  {"claude", "anthropic"},
		"codex-auth":   {"codex", "openai-codex"},
		"copilot-auth": {"copilot", "github-copilot"},
	} {
		p := embeddedNamed(t, pack)[0]
		var got [][2]string
		for _, prof := range p.Decl.Profiles() {
			got = append(got, [2]string{prof.Name, prof.Provider})
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s declares profiles %v, want exactly %v", pack, got, want)
		}
		if got := ShippedProfileDeclarers(want[0], Embedded()); !slices.Equal(got, []string{pack}) {
			t.Errorf("shipped packs declaring %s = %v, want %s alone", want[0], got, pack)
		}
	}
	if names := DeclaredProfileNames(embeddedNamed(t, "pi"), nil); slices.Contains(names, "copilot") ||
		slices.Contains(names, "claude") {
		t.Errorf("declared profiles with pi alone = %v, want neither copilot nor claude", names)
	}
}

// OQ-PE5: claude-oauth-broker is claude-auth's, beside its provider, as openai-auth-broker is
// codex-auth's; the claude pack ships no loophole, and selecting claude still runs the broker.
func TestTheClaudeOAuthBrokerIsClaudeAuthsLoophole(t *testing.T) {
	loopholesOf := func(pack string) []string {
		var out []string
		for _, c := range embeddedNamed(t, pack)[0].Decl.Contributions() {
			if c.Kind == "loophole" {
				out = append(out, c.From)
			}
		}
		return out
	}
	if got := loopholesOf("claude-auth"); !slices.Equal(got, []string{"loopholes/claude-oauth-broker"}) {
		t.Errorf("claude-auth's loopholes = %v, want the claude-oauth-broker", got)
	}
	if got := loopholesOf("claude"); len(got) != 0 {
		t.Errorf("claude's loopholes = %v, want none: the broker moved into claude-auth", got)
	}
	if got := loopholesOf("codex-auth"); !slices.Equal(got, []string{"loopholes/openai-auth-broker"}) {
		t.Errorf("codex-auth's loopholes = %v, want the openai-auth-broker", got)
	}
}

func TestTheCopilotAuthPackDeclaresTheProfileWithoutCopilot(t *testing.T) {
	packs := embeddedNamed(t, "pi", "copilot-auth")
	if names := DeclaredProfileNames(packs, nil); !slices.Contains(names, "copilot") {
		t.Fatalf("declared profiles with pi and copilot-auth = %v, want copilot among them", names)
	}
	for _, n := range notches {
		if _, err := agentEnvAt(t, n, packs, nil, nil, "pi", "copilot"); err != nil {
			t.Errorf("%s: pi on its own github-copilot client refused: %v", n.name, err)
		}
	}
}

// THE UNDECLARED REFUSAL NAMES THE PACK THAT WOULD DECLARE IT (the Happy Path): with pi alone,
// `-p pi=copilot` is refused, and the message says to add the copilot-auth pack.
func TestAnUndeclaredShippedProfileNamesThePackThatShipsIt(t *testing.T) {
	selected := embeddedNamed(t, "pi")
	declared := DeclaredProfileNames(selected, nil)
	msg := UndeclaredProfileHelp("copilot", declared, selected, Embedded())
	if !strings.Contains(msg, `no profile named "copilot" is declared`) ||
		!strings.Contains(msg, "run `yolo pack add copilot-auth`") {
		t.Errorf("message %q does not name adding the copilot-auth pack", msg)
	}
	if typo := UndeclaredProfileHelp("zai-fst", declared, selected, Embedded()); strings.Contains(typo, "packs`") {
		t.Errorf("a name no shipped pack declares names a pack: %q", typo)
	}
}

// THE OLD PROFILE NAME IS REFUSED NAMING THE NEW ONE (OQ-PE3, PE-D7), from the shipped manifest's
// `formerly` record, and the pack to add only when it is not already selected.
func TestTheFormerGitHubCopilotProfileIsRefusedNamingCopilot(t *testing.T) {
	selected := embeddedNamed(t, "pi")
	msg := UndeclaredProfileHelp("github-copilot", DeclaredProfileNames(selected, nil), selected, Embedded())
	for _, want := range []string{`no profile named "github-copilot" is declared`, `renamed "copilot"`,
		"the copilot-auth pack declares", `select "copilot" instead`, "run `yolo pack add copilot-auth`"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	selected = embeddedNamed(t, "pi", "copilot-auth")
	msg = UndeclaredProfileHelp("github-copilot", DeclaredProfileNames(selected, nil), selected, Embedded())
	if !strings.Contains(msg, `select "copilot" instead`) || strings.Contains(msg, "yolo pack add copilot-auth") {
		t.Errorf("with copilot-auth selected, message %q should name copilot and no pack to add", msg)
	}
	if now, pack, ok := RenamedProfile("github-copilot", Embedded()); !ok || now != "copilot" || pack != "copilot-auth" {
		t.Errorf("RenamedProfile(github-copilot) = %q, %q, %v; want copilot, copilot-auth", now, pack, ok)
	}
}

// THE OLD PACK NAMES ARE RECORDED BY THE PACKS THAT TOOK THEM OVER (PE-D7), which the `packs`
// refusal reads (config's lowerPackSource).
func TestTheFormerPackNamesNameTheirNewPacks(t *testing.T) {
	for old, want := range map[string]string{"openai-auth": "codex-auth", "github-copilot": "copilot-auth"} {
		if got, ok := RenamedPack(old, Embedded()); !ok || got != want {
			t.Errorf("RenamedPack(%s) = %q, %v; want %s", old, got, ok, want)
		}
	}
	if got, ok := RenamedPack("claude", Embedded()); ok {
		t.Errorf("RenamedPack(claude) = %q: a shipped name is no former name", got)
	}
}

// OQ-PE4: a name a selected agent has built in, which no shipped pack declares, is refused with the
// two-line declaration typed out, naming the agent; and the declaration it types works.
func TestAnUndeclaredBuiltInNameTypesOutItsDeclaration(t *testing.T) {
	selected := embeddedNamed(t, "pi", "bedrock")
	declared := DeclaredProfileNames(selected, nil)
	msg := UndeclaredProfileHelp("groq", declared, selected, Embedded())
	for _, want := range []string{`no profile named "groq" is declared`, "pi has groq built in",
		`"providers": {"groq": {"platform": "groq"}}`, `"profiles": {"groq": {"provider": "groq"}}`,
		"(declared: bedrock"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	// Two selected agents with it built in are both named.
	two := embeddedNamed(t, "pi", "opencode")
	if msg := UndeclaredProfileHelp("groq", nil, two, Embedded()); !strings.Contains(msg, "opencode and pi have groq built in") {
		t.Errorf("message %q does not name both agents", msg)
	}
	// A name no selected agent has built in keeps today's message.
	if msg := UndeclaredProfileHelp("groq", nil, embeddedNamed(t, "claude"), Embedded()); strings.Contains(msg, "built in") {
		t.Errorf("claude has no groq built in, but the message says so: %q", msg)
	}
	// THE TYPED DECLARATION IS ONE THAT WORKS: with exactly those two entries, pi runs groq on its
	// own client at both notches.
	user := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("platform", "groq")
	user.Set("groq", entry)
	profiles := map[string]UserProfile{"groq": {Provider: "groq"}}
	if names := DeclaredProfileNames(selected, profiles); !slices.Contains(names, "groq") {
		t.Fatalf("declared with the typed profile = %v, want groq", names)
	}
	for _, n := range notches {
		if _, err := agentEnvAt(t, n, embeddedNamed(t, "pi"), user, profiles, "pi", "groq"); err != nil {
			t.Errorf("%s: pi on the typed groq declaration refused: %v", n.name, err)
		}
	}
}

// A built-in name a shipped pack carries as a provider is answered with that pack's profile, not
// with a declaration that would shadow it: `-p pi=anthropic` names `claude` and claude-auth.
func TestAnUndeclaredBuiltInNameAShippedPackCarriesNamesItsProfile(t *testing.T) {
	selected := embeddedNamed(t, "pi")
	msg := UndeclaredProfileHelp("anthropic", DeclaredProfileNames(selected, nil), selected, Embedded())
	for _, want := range []string{"pi has anthropic built in", "the claude-auth pack", `select "claude" instead`,
		"run `yolo pack add claude-auth`"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, `"platform"`) {
		t.Errorf("message %q types a declaration that would shadow the shipped provider", msg)
	}
}

func TestABareCopilotProfileReachesOnlyTheAgentsThatHaveIt(t *testing.T) {
	table := map[string]string{}
	for _, a := range []string{"pi", "opencode", "oh-omp", "claude", "codex", "copilot"} {
		table[a] = "copilot"
	}
	d := disclose(t, []string{"pi", "opencode", "omp", "claude", "codex", "copilot", "copilot-auth",
		"wire-bridge"}, table, nil)
	if strings.Join(d.Declared, ",") != "copilot-auth" {
		t.Errorf("declared by %v, want the copilot-auth pack alone", d.Declared)
	}
	for _, agent := range []string{"pi", "opencode", "oh-omp"} {
		r := reachOf(t, d, agent)
		if r.Route != `through its own "github-copilot" client, with its own model list` || r.Unreached != "" {
			t.Errorf("%s: %+v, want its own github-copilot client", agent, r)
		}
	}
	// The Copilot CLI's own client IS GitHub Copilot: its pack needs the copilot-auth pack, which
	// ships a provider of the platform, so the profile runs on its own login, with no warning.
	if r := reachOf(t, d, "copilot"); r.Route != `through copilot's own "github-copilot" client` ||
		r.Unreached != "" || len(r.Warnings) != 0 {
		t.Errorf("copilot: %+v, want its own github-copilot client and no warning", r)
	}
	for _, agent := range []string{"claude", "codex"} {
		r := reachOf(t, d, agent)
		if r.Route != "" || !strings.Contains(r.Unreached, `only platform "github-copilot"`) ||
			!strings.Contains(r.Unreached, "`-p "+agent+"=<name>`") {
			t.Errorf("%s: %+v, want the selection to reach nothing for it, naming -p %s=<name>", agent, r, agent)
		}
	}
}

// OQ-PE1: Claude Code on the `claude` profile runs on its own client and login, with no warning;
// pi, opencode and oh-omp on it use their own anthropic clients; codex and copilot are told it
// reaches nothing for them.
func TestABareClaudeProfileRunsClaudeCodeOnItsOwnLogin(t *testing.T) {
	table := map[string]string{}
	for _, a := range []string{"pi", "opencode", "oh-omp", "claude", "codex", "copilot"} {
		table[a] = "claude"
	}
	d := disclose(t, []string{"pi", "opencode", "omp", "claude", "codex", "copilot", "claude-auth",
		"wire-bridge"}, table, nil)
	if strings.Join(d.Declared, ",") != "claude-auth" {
		t.Errorf("declared by %v, want the claude-auth pack alone", d.Declared)
	}
	if r := reachOf(t, d, "claude"); r.Route != `through claude's own "anthropic" client` ||
		r.Unreached != "" || len(r.Warnings) != 0 {
		t.Errorf("claude: %+v, want its own anthropic client and no warning", r)
	}
	for _, agent := range []string{"pi", "opencode", "oh-omp"} {
		r := reachOf(t, d, agent)
		if !strings.Contains(r.Route, `its own "anthropic" client`) || r.Unreached != "" {
			t.Errorf("%s: %+v, want its own anthropic client", agent, r)
		}
	}
	for _, agent := range []string{"codex", "copilot"} {
		if r := reachOf(t, d, agent); r.Route != "" || r.Unreached == "" {
			t.Errorf("%s: %+v, want the selection to reach nothing for it", agent, r)
		}
	}
}

// Claude Code on its own profile composes nothing that re-points it: no ANTHROPIC_* and no
// Bedrock switch, at both notches.
func TestClaudeOnTheClaudeProfileStaysOnItsOwnLogin(t *testing.T) {
	packs := embeddedNamed(t, "claude", "claude-auth", "wire-bridge")
	for _, n := range notches {
		vars, err := agentEnvAt(t, n, packs, nil, nil, "claude", "claude")
		if err != nil {
			t.Errorf("%s: claude on claude refused: %v", n.name, err)
			continue
		}
		for _, v := range vars {
			if strings.HasPrefix(v.Key, "ANTHROPIC_") || v.Key == "CLAUDE_CODE_USE_BEDROCK" {
				t.Errorf("%s: claude on its own client got %s=%q", n.name, v.Key, v.Value)
			}
		}
	}
}

// The Copilot CLI on the copilot profile composes nothing that re-points it: the provider names no
// endpoint and no key, so copilot stays on its own GitHub login at both notches.
func TestCopilotOnTheCopilotProfileStaysOnItsOwnLogin(t *testing.T) {
	packs := embeddedNamed(t, "copilot", "copilot-auth")
	for _, n := range notches {
		vars, err := agentEnvAt(t, n, packs, nil, nil, "copilot", "copilot")
		if err != nil {
			t.Errorf("%s: copilot on copilot refused: %v", n.name, err)
			continue
		}
		for _, v := range vars {
			if strings.HasPrefix(v.Key, "COPILOT_PROVIDER") || strings.HasPrefix(v.Key, "COPILOT_MODEL") {
				t.Errorf("%s: copilot on its own client got %s=%q", n.name, v.Key, v.Value)
			}
		}
	}
}

// PE-D4: a subscription provider's address is pinned. A user `providers` entry that adds or
// changes `endpoints` or `base_url` over one is refused naming the fix; one that leaves the address
// alone, or restates it, composes.
func TestASubscriptionProvidersEndpointIsPinned(t *testing.T) {
	for pack, provider := range map[string]string{"claude-auth": "anthropic", "codex-auth": "openai-codex",
		"copilot-auth": "github-copilot"} {
		packs := embeddedNamed(t, pack)
		for _, key := range []string{"endpoints", "base_url"} {
			user := jsonx.NewOrderedMap()
			entry := jsonx.NewOrderedMap()
			if key == "base_url" {
				entry.Set("base_url", "https://attacker.example/v1")
			} else {
				ep := jsonx.NewOrderedMap()
				addr := jsonx.NewOrderedMap()
				addr.Set("base_url", "https://attacker.example/v1")
				ep.Set("anthropic", addr)
				entry.Set("endpoints", ep)
			}
			user.Set(provider, entry)
			_, err := ComposeProviders(user, packs)
			if err == nil || !strings.Contains(err.Error(), "address is pinned by the "+pack+" pack") ||
				!strings.Contains(err.Error(), "declare a provider under another name") {
				t.Errorf("%s: a user %s over %s composed (err %v), want the pin's refusal", pack, key, provider, err)
			}
		}
		// Anything else about the provider stays the user's.
		user := jsonx.NewOrderedMap()
		entry := jsonx.NewOrderedMap()
		entry.Set("capabilities", []any{"web_search"})
		user.Set(provider, entry)
		if _, err := ComposeProviders(user, packs); err != nil {
			t.Errorf("%s: a user entry leaving the address alone was refused: %v", pack, err)
		}
	}
	// Restating the shipped address is no change.
	packs := embeddedNamed(t, "codex-auth")
	shipped, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := shipped.Get("openai-codex")
	eps, _ := v.(*jsonx.OrderedMap).Get("endpoints")
	user := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("endpoints", jsonx.DeepCopy(eps))
	user.Set("openai-codex", entry)
	if _, err := ComposeProviders(user, packs); err != nil {
		t.Errorf("restating openai-codex's shipped endpoints was refused: %v", err)
	}
	// An unpinned provider keeps the user's address, as before.
	user = jsonx.NewOrderedMap()
	entry = jsonx.NewOrderedMap()
	entry.Set("base_url", "https://proxy.example/v1")
	user.Set("openrouter", entry)
	if _, err := ComposeProviders(user, embeddedNamed(t, "openrouter")); err != nil &&
		strings.Contains(err.Error(), "pinned") {
		t.Errorf("openrouter is not pinned, but: %v", err)
	}
}

// THE MIXED SET'S PROVIDER POLICY: pi on ["bedrock", "copilot"], every table composed from the
// shipped packs alone, allows both of pi's own providers, so yolo's provider-policy extension does
// not block Copilot's requests.
func TestPiOnBedrockAndCopilotAllowsBothProviders(t *testing.T) {
	packs := embeddedNamed(t, "pi", "copilot-auth", "bedrock", "aws-auth")
	providers, resolved, _ := launchSelection(t, packs, nil, nil, map[string]string{"pi": "bedrock"})
	scope, err := ScopeCredentials(ScopeInput{Packs: packs, Providers: providers, Resolved: resolved,
		Profiles: map[string]string{"pi": "bedrock"},
		Sets:     map[string][]string{"pi": {"bedrock", "copilot"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schemaVersion":1,"mode":"allowlist","allowedProviderIds":["amazon-bedrock","github-copilot"],"profiles":["bedrock","copilot"]}`
	for _, v := range scope.Agent("pi").Shape {
		if v.Key == "YOLO_PI_PROVIDER_POLICY" {
			if v.Value != want {
				t.Errorf("YOLO_PI_PROVIDER_POLICY = %s, want %s", v.Value, want)
			}
			return
		}
	}
	t.Errorf("pi on a profile set got no provider policy")
}
