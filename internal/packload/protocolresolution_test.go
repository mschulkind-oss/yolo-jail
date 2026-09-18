package packload

// protocolresolution_test.go pins the resolver (docs/design/protocol-resolution.md §3-§4)
// through its PRODUCTION CALL SITE. Every case below goes in at AgentEnv — the runner both
// notches reduce through — rather than at ResolveProtocol, because a test that exercises the
// helper and passes when the call site is deleted is not a test of the gate. The last test
// in this file is exactly that mutation, written down.
//
// §4.1's degenerate table is the spine: each row is a way there is NOTHING to resolve, and
// each is a launch that works today and has to keep working. A resolver is only as good as
// the pairings it declines to have an opinion about.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// protocolAgentPack installs `claude` with the given protocol list (raw JSON, or "" for a
// pack that declares none) and carries a producer that emits the address it was given. The
// producer is a fixture on purpose here: what these tests measure is CORE's pairing rule,
// and the shipped derive is measured against its own declaration in agentprotocols_test.go.
func protocolAgentPack(t *testing.T, protocolsJSON string) *Pack {
	t.Helper()
	root := t.TempDir()
	producer := `
yolo.env("claude", function(ctx)
  local p = ctx.providers[ctx.selected_provider]
  if not p then return {} end
  local ep = p.endpoints and p.endpoints.anthropic
  return { ANTHROPIC_BASE_URL = ep and ep.base_url or "" }
end)`
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(producer), 0o644); err != nil {
		t.Fatalf("writing derive.lua: %v", err)
	}
	return &Pack{Name: "claude", Root: root, Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"claude","via":"npm","package":"c"`+protocolsJSON+`}]}`)}
}

// composedEntry composes one provider pack alone and returns its table entry — the shape
// the resolver is asked about, produced by the composer rather than hand-built, so a test
// cannot assert against a table shape the launch never makes.
func composedEntry(t *testing.T, vendor *Pack) *jsonx.OrderedMap {
	t.Helper()
	providers, err := ComposeProviders(nil, []*Pack{vendor})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := providers.Get("p")
	return asOrdered(t, v)
}

// asOrdered narrows a composed value to the map the resolver reads.
func asOrdered(t *testing.T, v any) *jsonx.OrderedMap {
	t.Helper()
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("composed entry is a %T, want an ordered map", v)
	}
	return m
}

// runAgentEnv composes, resolves and delivers — the whole path a launch takes — and returns
// the vars or the refusal.
func runAgentEnv(t *testing.T, agent *Pack, vendor *Pack) ([]agentenv.Var, error) {
	t.Helper()
	packs := []*Pack{agent, vendor}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	return AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel",
		func(string) (string, bool) { return "", false }, WithResolvedProfiles(resolved))
}

// OUTCOME 1 — DIRECT. The provider declares the protocol the agent speaks, so the address is
// the provider's own and nothing else is consulted.
func TestResolutionIsDirectWhenTheProviderSpeaksTheAgentsProtocol(t *testing.T) {
	vars, err := runAgentEnv(t,
		protocolAgentPack(t, `,"protocols":["anthropic"]`),
		providerPack(t, `,"endpoints":{"anthropic":{"base_url":"https://vendor.example/a"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0].Value != "https://vendor.example/a" {
		t.Errorf("vars = %#v, want the provider's own anthropic address", vars)
	}
}

// AN ADAPTER IS NEVER PREFERRED OVER A NATIVE ENDPOINT (§4.1), and the preference is the
// AGENT'S DECLARATION ORDER: a provider offering both wires resolves on the one the agent
// named first. The fixture agent prefers the second-listed wire, so a resolver that walked
// the PROVIDER's keys instead would pick the other one.
func TestResolutionFollowsTheAgentsDeclarationOrder(t *testing.T) {
	both := `,"endpoints":{"anthropic":{"base_url":"https://vendor.example/a"},` +
		`"openai":{"base_url":"https://vendor.example/o"}}`
	res, err := ResolveProtocol("claude", []string{"openai", "anthropic"}, "p",
		composedEntry(t, providerPack(t, both)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Protocol != "openai" || !res.Direct {
		t.Errorf("resolution = %#v, want a direct resolution on the FIRST protocol the agent named", res)
	}
}

// §4.1 ROW 1 — a provider that declares NO endpoints and carries a key is the BYO-key
// launch, and it stays legal (OQ-PR2). Nothing was repointed, so there is no pairing to
// resolve; refusing it would break a case that works today.
func TestAProviderWithNoEndpointsIsNeverRefused(t *testing.T) {
	if _, err := runAgentEnv(t, protocolAgentPack(t, `,"protocols":["anthropic"]`),
		providerPack(t, ``)); err != nil {
		t.Errorf("a provider declaring no endpoints must resolve (the BYO-key launch): %v", err)
	}
}

// §4.1 LAST ROW — an agent pack that declares NO protocols constrains nothing, and every
// provider resolves directly for it. This is the compatibility shape: a pack that has not
// been updated must behave exactly as it did before the field existed, including against a
// provider whose endpoints it could not possibly read.
func TestAnAgentWithNoProtocolsIsNeverRefused(t *testing.T) {
	if _, err := runAgentEnv(t, protocolAgentPack(t, ``),
		providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/o"}}`)); err != nil {
		t.Errorf("an agent declaring no protocols must resolve against anything: %v", err)
	}
}

// THE SHORTHAND NAMES NO PROTOCOL, so it offers nothing to pair with and resolves as the
// nothing-to-settle case — never as a refusal. Reading it here would mean guessing which
// wire it points at, which is the ambiguity §5 deletes rather than resolves.
func TestTheShorthandIsNotReadAsAProtocol(t *testing.T) {
	vendor := &Pack{Name: "vendor", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"p"},
	  {"kind":"profile","name":"sel","provider":"p"}]}`)}
	providers, err := ComposeProviders(userProviders(t, `{"p":{"base_url":"http://localhost:8080"}}`),
		[]*Pack{protocolAgentPack(t, `,"protocols":["anthropic"]`), vendor})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := providers.Get("p")
	res, err := ResolveProtocol("claude", []string{"anthropic"}, "p", asOrdered(t, entry))
	if err != nil {
		t.Errorf("a bare base_url must not resolve as a protocol mismatch: %v", err)
	}
	if res.Protocol != "" {
		t.Errorf("resolution = %#v, want the zero value (nothing had to resolve)", res)
	}
}

// OUTCOME 4 — the refusal, and what it must contain. Both sides and where each was declared
// (R3), so a WRONG declaration is visible in the refusal rather than in a later request.
func TestRefusalNamesBothProtocolsAndWhereTheyWereDeclared(t *testing.T) {
	_, err := runAgentEnv(t,
		protocolAgentPack(t, `,"protocols":["anthropic"]`),
		providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/o"}}`))
	if err == nil {
		t.Fatal("a pairing with no common protocol must refuse the launch")
	}
	for _, want := range []string{
		`provider "p" speaks "openai"`,
		`agent "claude" speaks "anthropic"`,
		"keys of its `endpoints`",
		"`protocols` list on the pack that installs claude",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must contain %q:\n%v", want, err)
		}
	}
}

// NOTHING TO RESOLVE WITHOUT A SELECTION. A launch with no active profile, and a profile
// that resolves to a provider the composed table does not hold, are both ordinary launches
// — the derives' own "no selection" case — and neither is this gate's to report.
func TestNoSelectionIsNotARefusal(t *testing.T) {
	agent := protocolAgentPack(t, `,"protocols":["anthropic"]`)
	vendor := providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/o"}}`)
	packs := []*Pack{agent, vendor}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	// No profile table at all: AgentEnv's own identity case.
	if _, err := AgentEnv(packs, providers, nil, "claude", "sel",
		func(string) (string, bool) { return "", false }); err != nil {
		t.Errorf("a launch that resolves no provider must not be refused: %v", err)
	}
}

// THE GATE IS PINNED TO BEING CALLED, not merely to being right: deleting the call from
// AgentEnv must fail a test. This one names the mutation so the next person can run it —
// remove the refuseUnspeakableProvider call in deriveenv.go and this test, plus
// TestRefusalNamesBothProtocolsAndWhereTheyWereDeclared and the openai row of
// TestClaudeDeriveKeepsACredentialWithItsAddress, go red.
//
// Its own assertion is the one the helper cannot make: the refusal arrives from the runner
// that DELIVERS the environment, so nothing is composed for a pairing that does not work —
// a gate that returned the vars alongside an error would have leaked the credential it
// exists to withhold.
func TestTheGateRefusesBeforeAnythingIsComposed(t *testing.T) {
	vars, err := runAgentEnv(t,
		protocolAgentPack(t, `,"protocols":["anthropic"]`),
		providerPack(t, `,"endpoints":{"openai":{"base_url":"https://vendor.example/o"}}`))
	if err == nil {
		t.Fatal("AgentEnv must refuse a pairing nothing can serve — the call site is gone")
	}
	if vars != nil {
		t.Errorf("vars = %#v, want nil: a refused pairing composes NOTHING", vars)
	}
}
