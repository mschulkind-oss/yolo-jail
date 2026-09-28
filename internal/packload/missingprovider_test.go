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
	{name: "host", opts: []ComposeOption{WithoutServiceAdaptations()},
		unserved: func(p []*Pack) []Adaptation { return UnservableAdaptations(p, nil) }},
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

// THE EXCUSE: codex and pi on their own codex profile, with openai-auth unselected, as the host
// composes them. Neither registers a `yolo.env` producer, so the row has no reader in their
// launch environment and the launch composes what it would with the row present. Refusing
// them would stop `yolo host -p codex -- pi` (and a `use_profiles` of it), which reaches the
// subscription through the host's managed OpenAI launch.
func TestCodexAndPiWithoutTheirProviderStillCompose(t *testing.T) {
	for _, agent := range []string{"codex", "pi"} {
		for _, n := range notches {
			if _, err := agentEnvAt(t, n, embeddedNamed(t, agent), nil, nil, agent, "codex"); err != nil {
				t.Errorf("%s: %s on codex without openai-auth must compose as before: %v", n.name, agent, err)
			}
		}
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
	errs := PairingRefusals(claude, providers, resolved, map[string]string{"claude": "codex"})
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
