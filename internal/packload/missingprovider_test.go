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
	resolved, err := ResolveProfiles(packs, userProfiles, providers)
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
	for _, n := range notches {
		vars, err := agentEnvAt(t, n, claude, nil, nil, "claude", "codex")
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
			if ue.ProviderPack != "openai-auth" || ue.NeededBy != "claude" || ue.Adaptation.Pack != "wire-bridge" {
				t.Errorf("host: the refusal must name the provider's pack, who needs it and the adapter: %+v", ue)
			}
			for _, want := range []string{`provider "openai-codex" is not in this launch's provider table either`,
				`pack "openai-auth" ships it and is not selected`, `pack "claude"'s ` + "`needs`"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("host: Error() must say %q:\n%v", want, err)
				}
			}
		case "jail":
			var mp *MissingProviderError
			if !errors.As(err, &mp) {
				t.Fatalf("jail: err = %v, want *MissingProviderError", err)
			}
			if mp.Profile != "codex" || mp.Provider != "openai-codex" || mp.Shipper != "openai-auth" ||
				mp.NeededBy != "claude" || mp.Then == nil {
				t.Errorf("jail: %+v", mp)
			}
			for _, want := range []string{`profile "codex" selects provider "openai-codex" for agent "claude"`,
				"does not hold it", `pack "openai-auth" ships it and is not selected`,
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
	if _, err := agentEnvAt(t, notches[1], embeddedNamed(t, "codex"), nil, nil, "codex", "codex"); err != nil {
		t.Errorf("host: codex on codex without openai-auth must compose as before: %v", err)
	}
	var mp *MissingProviderError
	if _, err := agentEnvAt(t, notches[1], embeddedNamed(t, "pi"), nil, nil, "pi", "codex"); !errors.As(err, &mp) ||
		mp.Shipper != "openai-auth" {
		t.Errorf("host: pi on codex without openai-auth now has a reader (its prelaunch producer), "+
			"so it must be refused naming openai-auth, got %v", err)
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
		if !strings.Contains(err.Error(), "Add \"zai\" to `packs` and this profile resolves") {
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
	resolved, err := ResolveProfiles(pi, user, providers)
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
		if !errors.As(err, &mp) || mp.Shipper != "openai-auth" || mp.Then != nil {
			t.Fatalf("%s: err = %v, want the missing provider naming openai-auth to add", n.name, err)
		}
		if !strings.Contains(err.Error(), "Add \"openai-auth\" to `packs` and this profile resolves") {
			t.Errorf("%s: the refusal must name the pack whose addition resolves it:\n%v", n.name, err)
		}
	}
	// With the pack added the selection composes, so the refusal above was about the row alone.
	with := append([]*Pack{agent}, embeddedNamed(t, "openai-auth")...)
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
	if !errors.As(err, &mp) || mp.Shipper != "openai-auth" {
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
			if !strings.Contains(err.Error(), "no pack yolo ships declares it") ||
				!strings.Contains(err.Error(), "Declare it under `providers`") {
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
	_, err := agentEnvAt(t, notches[0], embeddedNamed(t, "codex", "openai-auth"), user, nil, "codex", "codex")
	var mp *MissingProviderError
	if !errors.As(err, &mp) || mp.Dropped != "openai-auth" {
		t.Fatalf("err = %v, want *MissingProviderError naming openai-auth as the dropped provider's pack", err)
	}
	if !strings.Contains(err.Error(), "a null `providers.openai-codex` entry in your config removes it") {
		t.Errorf("the refusal must name the null: %v", err)
	}
}

// `yolo check` predicts the launch through PairingRefusals, so it reports the same refusal for
// a `use_profiles` selection: a config check called clean that the launch then refuses is the
// defect PairingRefusals exists to prevent.
func TestPairingRefusalsReportsAMissingProvider(t *testing.T) {
	claude := embeddedNamed(t, "claude")
	providers, err := ComposeProviders(nil, claude)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(claude, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	errs := PairingRefusals(claude, providers, resolved, map[string]string{"claude": "codex"}, nil)
	var mp *MissingProviderError
	if len(errs) != 1 || !errors.As(errs[0], &mp) {
		t.Fatalf("PairingRefusals = %v, want the one missing-provider refusal", errs)
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
	vars, err := agentEnvAt(t, notches[0], embeddedNamed(t, "claude", "openai-auth"), user, nil, "claude", "codex")
	if err != nil {
		t.Fatalf("a row offering no protocol has nothing to settle, so the gate passes it: %v", err)
	}
	if len(vars) != 0 {
		t.Errorf("claude's codex branch with no address composed %v, want nothing", vars)
	}
	// And the same branch WITH the bridge's address still composes its route.
	vars, err = agentEnvAt(t, notches[0], embeddedNamed(t, "claude", "openai-auth", "wire-bridge"), nil, nil,
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
