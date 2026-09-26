package packload

// via_test.go pins the host half of `via` (docs/design/wire-bridge-gateway.md
// OQ-WG6/WG7): a profile's via resolves with the user's value winning, its service's
// via_address becomes ViaBase only when that pack is in the launch, the pair crosses under
// reserved keys, and ResolveVias adds the named service pack the way a live need does.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// viaServicePack declares a service with a via_address, the shape packs/wire-bridge ships.
func viaServicePack(t *testing.T, name, addr string) *Pack {
	body := `{"contributes":[{"kind":"service","name":"` + name + `","endpoint":"` + name + `.endpoint",` +
		`"jail_daemon":{"cmd":["yolo-jaild","x"]}`
	if addr != "" {
		body += `,"via_address":"` + addr + `"`
	}
	return &Pack{Name: name, Decl: declFrom(t, body+`}]}`)}
}

// viaProfilePack ships a provider and a profile whose via names the given pack.
func viaProfilePack(t *testing.T, via string) *Pack {
	return &Pack{Name: "vendor", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"zai","endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  {"kind":"profile","name":"pz","provider":"zai","via":"`+via+`"},
	  {"kind":"profile","name":"plain","provider":"zai"}]}`)}
}

func TestResolveProfilesCarriesTheViaPair(t *testing.T) {
	vendor := viaProfilePack(t, "bridge")
	with := resolve(t, []*Pack{vendor, viaServicePack(t, "bridge", "http://127.0.0.1:8216")}, nil)
	if p := with["pz"]; p.Via != "bridge" || p.ViaBase != "http://127.0.0.1:8216" {
		t.Errorf("pz = %+v, want via bridge at its declared address", p)
	}
	if p := with["plain"]; p.Via != "" || p.ViaBase != "" {
		t.Errorf("a profile with no via must carry none: %+v", p)
	}
	// The host notch's shape: the service pack is not in the launch, so the agent keeps its
	// own client — Via is still stated, ViaBase is empty, and ViaURLFor answers "".
	without := resolve(t, []*Pack{vendor}, nil)
	if p := without["pz"]; p.Via != "bridge" || p.ViaBase != "" || ViaURLFor(p, "pi") != "" {
		t.Errorf("without the service pack: %+v (url %q), want Via kept and no base", p, ViaURLFor(p, "pi"))
	}
}

func TestAUserViaWinsOverThePackShippedOne(t *testing.T) {
	packs := []*Pack{viaProfilePack(t, "bridge"),
		viaServicePack(t, "bridge", "http://127.0.0.1:8216"),
		viaServicePack(t, "other", "http://127.0.0.1:9000")}
	got := resolve(t, packs, map[string]UserProfile{"pz": {Provider: "zai", Via: "other"}})
	if p := got["pz"]; p.Via != "other" || p.ViaBase != "http://127.0.0.1:9000" {
		t.Errorf("pz = %+v, want the user's via", p)
	}
}

func TestViaURLForIsThePerAgentPrefix(t *testing.T) {
	r := ResolvedProfile{Via: "wire-bridge", ViaBase: "http://127.0.0.1:8216/"}
	if got := ViaURLFor(r, "pi"); got != "http://127.0.0.1:8216/agent/pi" {
		t.Errorf("ViaURLFor = %q", got)
	}
	if got := ViaURLFor(ResolvedProfile{ViaBase: "http://127.0.0.1:8216"}, "pi"); got != "" {
		t.Errorf("no via, no URL: %q", got)
	}
}

func TestProfilesWireTableCarriesViaUnderReservedKeys(t *testing.T) {
	wire := ProfilesWireTable(map[string]ResolvedProfile{
		"pz":    {Provider: "zai", Options: map[string]string{"via": "an option"}, Via: "wire-bridge", ViaBase: "http://127.0.0.1:8216"},
		"plain": {Provider: "zai"},
	})
	body, err := jsonx.DumpsCompact(wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"_via": "wire-bridge"`, `"_via_base": "http://127.0.0.1:8216"`, `"via": "an option"`} {
		if !strings.Contains(body, want) {
			t.Errorf("wire table lacks %s:\n%s", want, body)
		}
	}
	plain, _ := wire.Get("plain")
	if keys := plain.(*jsonx.OrderedMap).Keys(); len(keys) != 1 || keys[0] != "provider" {
		t.Errorf("a profile with no via must carry no via keys: %v", keys)
	}
}

func TestResolveViasAddsTheNamedServicePack(t *testing.T) {
	bridge := viaServicePack(t, "bridge", "http://127.0.0.1:8216")
	universe := needsUniverse(bridge)
	selected := []*Pack{viaProfilePack(t, "bridge")}

	added, causes, err := ResolveVias(selected, map[string]string{"pi": "pz"}, nil, universe)
	if err != nil || addedNames(added) != "bridge" {
		t.Fatalf("added %q err %v, want bridge", addedNames(added), err)
	}
	if len(causes) != 1 || !strings.Contains(causes[0], "via of profile pz, active for pi") {
		t.Errorf("causes = %q", causes)
	}
	// Already selected: a join, not an addition.
	if added, _, err := ResolveVias(append(selected, bridge), map[string]string{"pi": "pz"}, nil, universe); err != nil || len(added) != 0 {
		t.Errorf("an already-selected via pack must join: added %q err %v", addedNames(added), err)
	}
	// No active via profile: nothing moves.
	if added, _, err := ResolveVias(selected, map[string]string{"pi": "plain"}, nil, universe); err != nil || len(added) != 0 {
		t.Errorf("no via profile active: added %q err %v", addedNames(added), err)
	}
	// A user via on the active profile is honored the same way.
	if added, _, err := ResolveVias([]*Pack{viaProfilePack(t, "")}, map[string]string{"pi": "plain"},
		map[string]UserProfile{"plain": {Via: "bridge"}}, universe); err != nil || addedNames(added) != "bridge" {
		t.Errorf("user via: added %q err %v", addedNames(added), err)
	}
}

func TestResolveViasRefusesAPackYoloDoesNotShip(t *testing.T) {
	_, _, err := ResolveVias([]*Pack{viaProfilePack(t, "stranger")}, map[string]string{"pi": "pz"}, nil, needsUniverse())
	if err == nil || !strings.Contains(err.Error(), "not an embedded official pack") {
		t.Errorf("err = %v, want the WB-D9 refusal", err)
	}
}

func TestResolveViasRefusesAPackThatServesNoViaRoute(t *testing.T) {
	plain := viaServicePack(t, "bridge", "")
	_, _, err := ResolveVias([]*Pack{viaProfilePack(t, "bridge")}, map[string]string{"pi": "pz"}, nil, needsUniverse(plain))
	if err == nil || !strings.Contains(err.Error(), "no service with a via_address") {
		t.Errorf("err = %v, want the no-via_address refusal", err)
	}
}

// TestTheShippedWireBridgeDeclaresTheViaAddress pins packs/wire-bridge's manifest: the
// port the daemon's via listener binds and every via agent's URL are this one value.
func TestTheShippedWireBridgeDeclaresTheViaAddress(t *testing.T) {
	for _, p := range Embedded() {
		if p.Name == "wire-bridge" {
			if addr, _ := ViaServiceAddress([]*Pack{p}, "wire-bridge"); addr != "http://127.0.0.1:8216" {
				t.Errorf("wire-bridge via_address = %q", addr)
			}
			return
		}
	}
	t.Fatal("no embedded wire-bridge pack")
}

// TestClaudeEnvIgnoresAVia pins WG7 (a) at the derive: claude's composed environment is
// byte-identical whether or not its active profile is a via profile — claude keeps its
// adapter route at the port's root, and the via URL is not an input its producer reads.
func TestClaudeEnvIgnoresAVia(t *testing.T) {
	vendor := providerPack(t, `,"endpoints":{"anthropic":{"base_url":"https://vendor.example/anthropic"}}`)
	packs := []*Pack{realClaudePack(t), vendor}
	providers, err := ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	run := func(r map[string]ResolvedProfile) string {
		vars, err := AgentEnv(packs, providers, map[string]string{"claude": "sel"}, "claude", "sel",
			func(string) (string, bool) { return "", false }, WithResolvedProfiles(r))
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, v := range vars {
			b.WriteString(v.Key + "=" + v.Value + "\n")
		}
		return b.String()
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	native := run(resolved)
	via := resolved["sel"]
	via.Via, via.ViaBase = "wire-bridge", "http://127.0.0.1:8216"
	resolved["sel"] = via
	if got := run(resolved); got != native || !strings.Contains(native, "ANTHROPIC_BASE_URL=https://vendor.example/anthropic") {
		t.Errorf("claude env moved under a via profile:\nnative:\n%s\nvia:\n%s", native, got)
	}
}
