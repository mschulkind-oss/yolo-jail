package packload

// missingprovider_test.go pins the protocol gate's answer for a selected profile whose provider
// the composed table does not hold (docs/design/credential-sources-separation.md ES-D25): a
// refusal naming why, except where the provider's row has no reader in the agent's launch
// environment. Every case goes in at AgentEnv, the runner both notches reduce through, over
// the SHIPPED packs where the case is a shipped one. The host's call sites are pinned in
// internal/cli (hostmissingprovider_test.go).

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// embeddedNamed returns the shipped packs with the given names, in that order.
func embeddedNamed(t *testing.T, names ...string) []*Pack {
	t.Helper()
	byName := map[string]*Pack{}
	for _, p := range Embedded() {
		byName[p.Name] = p
	}
	out := make([]*Pack, 0, len(names))
	for _, n := range names {
		p, ok := byName[n]
		if !ok {
			t.Fatalf("no embedded pack named %s", n)
		}
		out = append(out, p)
	}
	return out
}

// notch is the two ways a launch composes its table and hands the gate what it cannot serve:
// the jail's (every adaptation composed, nothing unserved) and the host's (no address a pack's
// own service serves, and the unservable list), as composedHostProviders and hostComposition do.
type notch struct {
	name     string
	opts     []ComposeOption
	unserved func([]*Pack) []Adaptation
}

var notches = []notch{
	{name: "jail", unserved: func([]*Pack) []Adaptation { return nil }},
	{name: "host", opts: []ComposeOption{WithServed(NothingServed())},
		unserved: func(p []*Pack) []Adaptation { return UnservedAdaptationsAt(p, nil, NothingServed()) }},
}

// agentEnvAt composes the table the way n does and runs AgentEnv for agent on profile.
func agentEnvAt(t *testing.T, n notch, packs []*Pack, user *jsonx.OrderedMap,
	userProfiles map[string]UserProfile, agent, profile string) ([]agentenv.Var, error) {
	t.Helper()
	providers, err := ComposeProviders(user, packs, n.opts...)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, withShippedProviderProfiles(userProfiles, providers), providers)
	if err != nil {
		t.Fatal(err)
	}
	return AgentEnv(packs, providers, map[string]string{agent: profile}, agent, profile,
		func(string) (string, bool) { return "", false },
		WithResolvedProfiles(resolved), WithUnservedAdaptations(n.unserved(packs)))
}

// THE MEASURED CASE: `"packs": ["claude"]` with the codex profile selected. The host applies no
// `needs` (ES-D24), so openai-auth, the one pack declaring openai-codex, is not in the table,
// and before this refusal the derive composed three context-window constants and no address:
// claude on its own login, exit 0. Now both notches refuse and compose nothing. At the host the
// answer is the bridge refusal itself, naming the provider's pack as changing nothing; at a
// jail it names the pack and what adding it alone would still refuse on.
func TestAClaudeCodexSelectionWithoutItsProviderRefuses(t *testing.T) {
	claude := embeddedNamed(t, "claude")
	userProfiles := map[string]UserProfile{"codex": {Provider: "openai-codex"}}
	for _, n := range notches {
		vars, err := agentEnvAt(t, n, claude, nil, userProfiles, "claude", "codex")
		if err == nil {
			t.Fatalf("%s: claude on codex with no openai-codex provider composed %v — the silent no-op", n.name, vars)
		}
		if vars != nil {
			t.Errorf("%s: a refused selection composes nothing, got %v", n.name, vars)
		}
		switch n.name {
		case "host":
			var ue *UnservedAdapterError
			if !errors.As(err, &ue) {
				t.Fatalf("host: err = %v, want the bridge refusal (*UnservedAdapterError)", err)
			}
			if ue.ProviderPack != "codex-auth" || ue.Adaptation.Pack != "wire-bridge" {
				t.Errorf("host: the refusal must name the provider's pack and the adapter: %+v", ue)
			}
			for _, want := range []string{`do not add "codex-auth" to ` + "`packs`" + ` to fix this`,
				`that would not change this refusal`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("host: Error() must say %q:\n%v", want, err)
				}
			}
		case "jail":
			var mp *MissingProviderError
			if !errors.As(err, &mp) {
				t.Fatalf("jail: err = %v, want *MissingProviderError", err)
			}
			if mp.Profile != "codex" || mp.Provider != "openai-codex" || mp.Shipper != "codex-auth" ||
				mp.Then == nil {
				t.Errorf("jail: %+v", mp)
			}
			for _, want := range []string{`Profile "codex" uses provider "openai-codex" for claude`,
				"Run `yolo pack add codex-auth`",
				`Pack "wire-bridge" adapts "openai-responses" → "anthropic"`} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("jail: Error() must say %q:\n%v", want, err)
				}
			}
		}
	}
}

// THE EXCUSE: codex on its own codex profile, with openai-auth unselected, as the host composes
// it. codex registers no `yolo.env` producer, so the row has no reader in its launch environment
// and the launch composes what it would with the row present. Refusing it would stop
// `yolo host -p codex -- codex` in a pack set that does not join openai-auth, which reaches the
// subscription through the host's managed OpenAI launch. The host is the only notch it holds
// at: see TestAJailRefusesAUserProfileWhoseProviderItLacks.
//
// pi LEFT THE EXCUSE with OQ-BR8: its OpenAI login prelaunch moved from a `profile: "codex"`
// gated env into pi's own `yolo.env` producer, keyed on the provider, so pi now has a reader
// and is refused like any agent whose producer reads the table, naming openai-auth. The
// shipped pi pack `needs` openai-auth, which every notch joins, so only a hand-built pack set
// without it meets this.
func TestCodexAndPiWithoutTheirProviderStillCompose(t *testing.T) {
	userProfiles := map[string]UserProfile{"codex": {Provider: "openai-codex"}}
	if _, err := agentEnvAt(t, notches[1], embeddedNamed(t, "codex"), nil, userProfiles, "codex", "codex"); err != nil {
		t.Errorf("host: codex on codex without openai-auth must compose as before: %v", err)
	}
	var mp *MissingProviderError
	_, err := agentEnvAt(t, notches[1], embeddedNamed(t, "pi"), nil, userProfiles, "pi", "codex")
	if !errors.As(err, &mp) || mp.Shipper != "codex-auth" {
		t.Fatalf("host: pi on codex without openai-auth must be refused naming openai-auth, got %v", err)
	}
	want := `Profile "codex" uses provider "openai-codex" for pi, but that provider is unavailable. Run ` + "`yolo pack add codex-auth`" + ` to use this provider`
	if got := err.Error(); got != want {
		t.Errorf("rendered refusal = %q, want %q", got, want)
	}
}

// THE EXCUSE STOPS AT THE HOST. A jail renders each agent's config surfaces, and those derives
// read the provider table: pi's settings derive, opencode's config derive and codex's config
// derive each write nothing for a provider the table lacks. So a jail whose profile selects a
// provider no selected pack ships would start the agent on its own default, the silent no-op
// ES-D25 ends, however the agent's env producers look. Measured before the fix: a user-declared
// `profiles.myz = {provider: "zai"}` selected for pi, codex or opencode with `"packs": ["<agent>"]`
// passed `yolo check` and the jail in silence, while claude and copilot refused.
func TestAJailRefusesAUserProfileWhoseProviderItLacks(t *testing.T) {
	user := map[string]UserProfile{"myz": {Provider: "zai"}}
	for _, agent := range []string{"pi", "codex", "opencode"} {
		_, err := agentEnvAt(t, notches[0], embeddedNamed(t, agent), nil, user, agent, "myz")
		var mp *MissingProviderError
		if !errors.As(err, &mp) || mp.Shipper != "zai" || mp.Then != nil {
			t.Fatalf("jail/%s: err = %v, want the missing provider naming zai to add", agent, err)
		}
		if !strings.Contains(err.Error(), "Run `yolo pack add zai`") {
			t.Errorf("jail/%s: the refusal must name the pack whose addition resolves it:\n%v", agent, err)
		}
		// With the pack selected the jail composes, so the refusal was about the row alone.
		if _, err := agentEnvAt(t, notches[0], embeddedNamed(t, agent, "zai"), nil, user, agent, "myz"); err != nil {
			t.Errorf("jail/%s: with zai selected the selection resolves: %v", agent, err)
		}
	}
	// `yolo check` predicts the jail through PairingRefusals, so it reports the same refusal.
	pi := embeddedNamed(t, "pi")
	providers, err := ComposeProviders(nil, pi)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(pi, withShippedProviderProfiles(user, providers), providers)
	if err != nil {
		t.Fatal(err)
	}
	var mp *MissingProviderError
	if errs := PairingRefusals(pi, providers, resolved, map[string]string{"pi": "myz"}, nil); len(errs) != 1 ||
		!errors.As(errs[0], &mp) {
		t.Fatalf("PairingRefusals = %v, want the one missing-provider refusal", errs)
	}
}

// The excuse is the agent's having no env producer, and nothing else: the same selection for an
// agent whose pack DOES register one refuses, naming the pack to add. The fixture speaks the
// provider's wire, so the pairing itself would resolve.
func TestTheExcuseIsOnlyForAnAgentWithNoEnvProducer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(`
yolo.env("respy", function(ctx) return { RESPY_URL = "x" } end)`), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := &Pack{Name: "respy", Root: root, Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"respy","via":"npm","package":"r","protocols":["openai-responses"]}]}`)}
	user := map[string]UserProfile{"sub": {Provider: "openai-codex"}}
	for _, n := range notches {
		_, err := agentEnvAt(t, n, []*Pack{agent}, nil, user, "respy", "sub")
		var mp *MissingProviderError
		if !errors.As(err, &mp) || mp.Shipper != "codex-auth" || mp.Then != nil {
			t.Fatalf("%s: err = %v, want the missing provider naming openai-auth to add", n.name, err)
		}
		if !strings.Contains(err.Error(), "Run `yolo pack add codex-auth`") {
			t.Errorf("%s: the refusal must name the pack whose addition resolves it:\n%v", n.name, err)
		}
	}
	// With the pack added the selection composes, so the refusal above was about the row alone.
	with := append([]*Pack{agent}, embeddedNamed(t, "codex-auth")...)
	if _, err := agentEnvAt(t, notches[1], with, nil, user, "respy", "sub"); err != nil {
		t.Errorf("with openai-auth selected the selection resolves: %v", err)
	}
}

// What reads the table is the notch's question. A third-party agent whose pack only DERIVES a
// config surface for it (no `yolo.env`) refuses at a jail, which renders that surface, and is
// excused at the host, which renders none. One its pack derives nothing for at all is excused
// at both: no surface and no environment reads the row, so the launch is the same without it.
func TestAJailCountsASurfaceDeriveAsAReader(t *testing.T) {
	user := map[string]UserProfile{"sub": {Provider: "openai-codex"}}
	mk := func(script string) *Pack {
		root := t.TempDir()
		if script != "" {
			if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(script), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return &Pack{Name: "respy", Root: root, Decl: declFrom(t, `{"contributes":[
		  {"kind":"program","bin":"respy","via":"npm","package":"r","protocols":["openai-responses"]}]}`)}
	}
	deriving := mk(`yolo.derive("respy", "config", function(ctx) return {} end)`)
	_, err := agentEnvAt(t, notches[0], []*Pack{deriving}, nil, user, "respy", "sub")
	var mp *MissingProviderError
	if !errors.As(err, &mp) || mp.Shipper != "codex-auth" {
		t.Fatalf("jail: a surface derive reads the table, so err = %v must be the missing provider", err)
	}
	if _, err := agentEnvAt(t, notches[1], []*Pack{deriving}, nil, user, "respy", "sub"); err != nil {
		t.Errorf("host: no surface is rendered here, so nothing reads the row: %v", err)
	}
	bare := mk("")
	for _, n := range notches {
		if _, err := agentEnvAt(t, n, []*Pack{bare}, nil, user, "respy", "sub"); err != nil {
			t.Errorf("%s: nothing derives for respy, so nothing reads the row: %v", n.name, err)
		}
	}
}

// A provider nothing declares refuses, at both notches, for an agent with or without a
// producer: there is no pack to ask the pairing of, so the refusal says to declare it.
func TestAProviderNothingDeclaresRefuses(t *testing.T) {
	user := map[string]UserProfile{"offroad": {Provider: "nowhere"}}
	for _, agent := range []string{"claude", "codex"} {
		for _, n := range notches {
			_, err := agentEnvAt(t, n, embeddedNamed(t, agent), nil, user, agent, "offroad")
			var mp *MissingProviderError
			if !errors.As(err, &mp) || mp.Shipper != "" || mp.Dropped != "" {
				t.Fatalf("%s/%s: err = %v, want *MissingProviderError naming no pack", n.name, agent, err)
			}
			if !strings.Contains(err.Error(), "uses a provider supplied by your selected packs") ||
				!strings.Contains(err.Error(), "declare this provider in `providers` in your user config") {
				t.Errorf("%s/%s: %v", n.name, agent, err)
			}
		}
	}
}

// A provider a SELECTED pack ships but a null `providers` entry removes refuses, naming the
// null: the user took the provider out, and the profile selecting it would compose nothing.
func TestAProviderTheUserDroppedRefuses(t *testing.T) {
	user := jsonx.NewOrderedMap()
	user.Set("openai-codex", nil)
	_, err := agentEnvAt(t, notches[0], embeddedNamed(t, "codex", "codex-auth"), user, nil, "codex", "codex")
	var mp *MissingProviderError
	if !errors.As(err, &mp) || mp.Dropped != "codex-auth" {
		t.Fatalf("err = %v, want *MissingProviderError naming openai-auth as the dropped provider's pack", err)
	}
	if !strings.Contains(err.Error(), "Remove the `providers.openai-codex` null entry") {
		t.Errorf("the refusal must name the null: %v", err)
	}
}

// `yolo check` predicts the launch through PairingRefusals, so it reports the same refusal for
// a `profile` selection: a config check called clean that the launch then refuses is the
// defect PairingRefusals exists to prevent.
func TestPairingRefusalsReportsAMissingProvider(t *testing.T) {
	claude := embeddedNamed(t, "claude")
	providers, err := ComposeProviders(nil, claude)
	if err != nil {
		t.Fatal(err)
	}
	userProfiles := map[string]UserProfile{"codex": {Provider: "openai-codex"}}
	resolved, err := ResolveProfiles(claude, withShippedProviderProfiles(userProfiles, providers), providers)
	if err != nil {
		t.Fatal(err)
	}
	errs := PairingRefusals(claude, providers, resolved, map[string]string{"claude": "codex"}, nil)
	var mp *MissingProviderError
	if len(errs) != 1 || !errors.As(errs[0], &mp) {
		t.Fatalf("PairingRefusals = %v, want the one missing-provider refusal", errs)
	}
	want := `Profile "codex" uses provider "openai-codex" for claude, but that provider is unavailable. Run ` + "`yolo pack add codex-auth`" + ` to make this provider available, but that alone will not fix this refusal:`
	if !strings.HasPrefix(errs[0].Error(), want) {
		t.Errorf("check refusal = %q, want prefix %q", errs[0], want)
	}
}

// THE DERIVE'S OWN HALF (packs/claude/derive.lua): an openai-codex row with NO address reaches
// the producer when the row offers no protocol at all, the one shape the gate has nothing to
// settle on (a user override removing its `endpoints`). The producer used to emit its three
// context-window constants there, pointing nothing; it now composes nothing.
func TestTheShippedClaudeCodexBranchComposesNothingWithoutAnAddress(t *testing.T) {
	user := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("endpoints", nil)
	user.Set("openai-codex", entry)
	vars, err := agentEnvAt(t, notches[0], embeddedNamed(t, "claude", "codex-auth"), user, nil, "claude", "codex")
	if err != nil {
		t.Fatalf("a row offering no protocol has nothing to settle, so the gate passes it: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("claude's codex branch with no address composed %v, want nothing", vars)
	}
	// And the same branch WITH the bridge's address still composes its route.
	vars, err = agentEnvAt(t, notches[0], embeddedNamed(t, "claude", "codex-auth", "wire-bridge"), nil, nil,
		"claude", "codex")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vars {
		if v.Key == "ANTHROPIC_BASE_URL" && v.Value == "http://127.0.0.1:8215" {
			found = true
		}
	}
	if !found {
		t.Errorf("the bridged codex route must still compose its address: %v", vars)
	}
}

// A BARE `-p codex` REACHES AGY, AND AGY CANNOT CONSUME openai-codex. config.FoldProfiles keys
// every selected pack's program bin, so a bare list (or `-p codex` with no `=`) lands
// `{"agy": "codex"}` in the profile table; openai-auth is not selected, so the composed table
// holds no openai-codex. agy declares no protocols and no built-in providers, and its one derive
// renders ctx.mcp_servers, so no provider's row reaches anything it composes: the binding is
// INAPPLICABLE. docs/design/providers-and-profiles-redesign.md OQ-PP3 rules that a bare `-p X`
// for an agent it does not reach is one launch line, never a refusal, so a multi-agent jail still
// launches — and the jail's line (packload.profileReach) discloses it, which is what keeps this
// from being the silent no-op P1 forbids. Before this build both notches at AgentEnv answered
// agy's binding with a MissingProviderError naming openai-auth, so agy's mere presence in the pack
// set made a profile aimed at another agent fatal.
func TestABareProfileOnAnAgentWithNoProviderSurfaceIsDisclosedNotRefused(t *testing.T) {
	packs := embeddedNamed(t, "agy")
	user := map[string]UserProfile{"codex": {Provider: "openai-codex"}}
	// The fold's row for agy — the whole input the gate sees; see config.FoldProfiles.
	table := map[string]string{"agy": "codex"}
	for _, n := range notches {
		if _, err := agentEnvAt(t, n, packs, nil, user, "agy", "codex"); err != nil {
			t.Fatalf("%s: agy cannot consume openai-codex, so its binding is disclosed, not refused: %v",
				n.name, err)
		}
	}
	// THE DISCLOSURE, at the jail that renders the launch line: the same table the jail would
	// print warns, naming agy and why.
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, withShippedProviderProfiles(user, providers), providers)
	if err != nil {
		t.Fatal(err)
	}
	ds := ProfileDisclosures(ProfileDisclosureInput{
		Table: table, Packs: packs, Resolved: resolved, Providers: providers,
	})
	if len(ds) != 1 || !strings.Contains(strings.Join(ds[0].Warnings(), "\n"), "reaches nothing for agy") {
		t.Fatalf("the launch line must disclose agy's inapplicable binding: %+v", ds)
	}
}

// The reader predicate is about whether the agent can CONSUME the provider, not whether its pack
// registers a derive. The same config-surface derive refuses at a jail for an agent that can be
// pointed at the provider (it declares the protocol the provider offers, or implements the
// provider itself) and composes for one that cannot, mirroring profileReach's reachability.
// Without the consumability guard the third case refuses, which is agy's bug behind a fixture.
func TestASurfaceDeriveReadsTheTableOnlyForAProviderItsAgentCanConsume(t *testing.T) {
	user := map[string]UserProfile{"sub": {Provider: "openai-codex"}}
	mk := func(program string) *Pack {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "derive.lua"),
			[]byte(`yolo.derive("respy", "config", function(ctx) return {} end)`), 0o644); err != nil {
			t.Fatal(err)
		}
		return &Pack{Name: "respy", Root: root, Decl: declFrom(t, program)}
	}
	const head = `{"contributes":[{"kind":"program","bin":"respy","via":"npm","package":"r"`
	cases := []struct {
		name    string
		program string
		refuses bool
	}{
		{"speaks the protocol the provider offers", head + `,"protocols":["openai-responses"]}]}`, true},
		{"implements the provider itself", head + `,"built_in_providers":{"names":["openai"],"plans":{"openai-codex":{"provider":"openai"}}}}]}`, true},
		{"neither, so the binding is inapplicable", head + `}]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mk(tc.program)
			_, err := agentEnvAt(t, notches[0], []*Pack{p}, nil, user, "respy", "sub")
			var mp *MissingProviderError
			if refused := errors.As(err, &mp); refused != tc.refuses {
				t.Fatalf("refused = %v, want %v: err = %v", refused, tc.refuses, err)
			}
		})
	}
}
