package packload

// via_test.go pins the host half of `via` (docs/design/wire-bridge-gateway.md
// OQ-WG6/WG7): a profile's via resolves with the user's value winning, its service's
// via_address becomes ViaBase only when that pack is in the launch, the pair crosses under
// reserved keys, and ResolveVias adds the named service pack the way a live need does.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
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
	selected := []*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "bridge")}

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
	if added, _, err := ResolveVias([]*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "")}, map[string]string{"pi": "plain"},
		map[string]UserProfile{"plain": {Via: "bridge"}}, universe); err != nil || addedNames(added) != "bridge" {
		t.Errorf("user via: added %q err %v", addedNames(added), err)
	}
}

func TestResolveViasRefusesAPackYoloDoesNotShip(t *testing.T) {
	_, _, err := ResolveVias([]*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "stranger")},
		map[string]string{"pi": "pz"}, nil, needsUniverse())
	if err == nil || !strings.Contains(err.Error(), "not an embedded official pack") {
		t.Errorf("err = %v, want the WB-D9 refusal", err)
	}
}

func TestResolveViasRefusesAPackThatServesNoViaRoute(t *testing.T) {
	plain := viaServicePack(t, "bridge", "")
	_, _, err := ResolveVias([]*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "bridge")},
		map[string]string{"pi": "pz"}, nil, needsUniverse(plain))
	if err == nil || !strings.Contains(err.Error(), "no service with a via_address") {
		t.Errorf("err = %v, want the no-via_address refusal", err)
	}
}

// TestAViaForAnAgentNoSelectedPackInstallsIsInactive pins WG-I10: a use_profiles key may
// name any CLI a resolvable pack installs, selected or not, so an entry for an agent this
// launch does not carry must neither add the via's pack nor refuse the launch — not even
// when the via names a pack that would be refused for an agent that is present.
func TestAViaForAnAgentNoSelectedPackInstallsIsInactive(t *testing.T) {
	bridge := viaServicePack(t, "bridge", "http://127.0.0.1:8216")
	added, causes, err := ResolveVias([]*Pack{viaProfilePack(t, "bridge")},
		map[string]string{"pi": "pz"}, nil, needsUniverse(bridge))
	if err != nil || len(added) != 0 || len(causes) != 0 {
		t.Errorf("no pack installs pi, so its via is not active: added %q causes %q err %v",
			addedNames(added), causes, err)
	}
	if _, _, err := ResolveVias([]*Pack{viaProfilePack(t, "stranger")},
		map[string]string{"pi": "pz"}, nil, needsUniverse()); err != nil {
		t.Errorf("a via for an absent agent refused the launch: %v", err)
	}
	if got := ActiveVias([]*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "bridge")},
		map[string]string{"pi": "pz", "codex": "pz", "omp": "plain"}, nil); len(got) != 1 ||
		got[0] != (ActiveVia{Agent: "pi", Profile: "pz", Via: "bridge"}) {
		t.Errorf("ActiveVias = %+v, want only pi (codex is not installed, omp's profile has no via)", got)
	}
}

// TestSelectionCloseRunsBothHalves pins WG-I11's resolver: the needs closure, the via
// closure over the needs-closed set, and the needs closure again over what via added, with
// every cause line in the order the packs joined.
func TestSelectionCloseRunsBothHalves(t *testing.T) {
	// The via's service pack needs a helper of its own, which only the second needs pass
	// can find.
	bridge := &Pack{Name: "bridge", Decl: viaServicePack(t, "bridge", "http://127.0.0.1:8216").Decl}
	bridge.Decl.Needs = []packdecl.PackNeed{need("helper")}
	helper := binPack("helper")
	needy := needsPack("needy", need("extra", "pi"))
	extra := binPack("extra")
	sel := Selection{
		Embedded:    needsUniverse(bridge, helper, extra),
		UseProfiles: func([]*Pack) map[string]string { return map[string]string{"pi": "pz"} },
	}
	added, causes, err := sel.Close([]*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "bridge"), needy})
	if err != nil {
		t.Fatal(err)
	}
	if got := addedNames(added); got != "extra,bridge,helper" {
		t.Errorf("added %q, want extra (needs), bridge (via), helper (the via pack's need)", got)
	}
	if len(causes) != 3 || !strings.Contains(causes[1], "via of profile pz, active for pi") {
		t.Errorf("causes = %q", causes)
	}
	// A refused closure returns no additions: every caller reads a refusal as "the launch
	// will not start", and a half-closed set would describe a launch that cannot happen.
	sel.Embedded = needsUniverse(extra)
	if added, _, err := sel.Close([]*Pack{binPack("pi-pack", "pi"), viaProfilePack(t, "bridge"), needy}); err == nil || added != nil {
		t.Errorf("a via naming a pack outside the universe: added %q err %v", addedNames(added), err)
	}
}

// TestSelectionCloseReadsNoProfilesWithNothingSelected: the user's declarations are read
// only when some profile is selected, so a closure with nothing to resolve reads no file.
func TestSelectionCloseReadsNoProfilesWithNothingSelected(t *testing.T) {
	read := false
	sel := Selection{
		Embedded:     needsUniverse(),
		UseProfiles:  func([]*Pack) map[string]string { return nil },
		UserProfiles: func() (map[string]UserProfile, error) { read = true; return nil, nil },
	}
	if _, _, err := sel.Close([]*Pack{binPack("pi-pack", "pi")}); err != nil || read {
		t.Errorf("err %v, read %v: want no read with no profile selected", err, read)
	}
}

// TestViaInertClearsEveryAddressAndKeepsTheVia pins WG-I12's table: ViaURLFor answers ""
// for every agent over it, and the via each profile names is still stated.
func TestViaInertClearsEveryAddressAndKeepsTheVia(t *testing.T) {
	in := map[string]ResolvedProfile{
		"pz":    {Provider: "zai", Via: "wire-bridge", ViaBase: "http://127.0.0.1:8216"},
		"plain": {Provider: "zai"},
	}
	out := ViaInert(in)
	if p := out["pz"]; p.Via != "wire-bridge" || p.ViaBase != "" || ViaURLFor(p, "pi") != "" {
		t.Errorf("pz = %+v, want its via kept and no address", p)
	}
	if in["pz"].ViaBase == "" {
		t.Error("ViaInert modified its input")
	}
	if ViaInert(nil) != nil {
		t.Error("ViaInert(nil) must stay nil")
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

// pointerPack installs the agent `ag` with three surfaces, one of them unrendered, and
// carries deriveLua at its root.
func pointerPack(t *testing.T, deriveLua string) *Pack {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(deriveLua), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Pack{Name: "ag-pack", Root: root, Decl: declFrom(t, `{"contributes":[
	  {"kind":"program","bin":"ag","via":"npm","package":"@example/ag","protocols":["openai"]},
	  {"kind":"config","config":[
	    {"agent":"ag","name":"config","codec":"json","mode":"computed","path":"~/.ag/config.json"},
	    {"agent":"ag","name":"other","codec":"json","mode":"computed","path":"~/.ag/other.json"},
	    {"agent":"ag","name":"theirs","codec":"json","mode":"unrendered","path":"~/.ag/theirs.json"}]}]}`)}
}

// pointers runs DerivedViaPointers for ag over a via profile, and returns each pointer as
// "<surface>:<dotted path>" ("env" for the env producer's).
func pointers(t *testing.T, p *Pack, base string) ([]string, error) {
	t.Helper()
	resolved := map[string]ResolvedProfile{"v": {Provider: "zai", Via: "bridge", ViaBase: base}}
	providers := jsonx.NewOrderedMap()
	ptrs, err := DerivedViaPointers([]*Pack{p}, providers, map[string]string{"ag": "v"}, resolved, "ag")
	var out []string
	for _, ptr := range ptrs {
		surface := ptr.Surface
		if surface == "" {
			surface = "env"
		}
		out = append(out, surface+":"+strings.Join(ptr.Path, "."))
	}
	return out, err
}

// TestDerivedViaPointersReadsWhatTheBootRenders pins WG-I15's inputs: every rendered surface
// the agent's packs declare and the agent's env producer are run with ctx.via_url set, and
// each string that is the URL or lies under it is a pointer. An unrendered surface is never
// derived at boot, so its output is not a pointer. A value that only shares the URL's text,
// or a key naming it, points at nothing.
func TestDerivedViaPointersReadsWhatTheBootRenders(t *testing.T) {
	p := pointerPack(t, `
local function url(ctx) return ctx.via_url or "" end
yolo.derive("ag", "config", function(ctx)
  return { providers = { zai = { baseUrl = url(ctx) .. "/v1" } }, sibling = url(ctx) .. "x",
           [url(ctx)] = "a key, not a value" }
end)
yolo.derive("ag", "other", function(ctx) return { list = { "a", url(ctx) } } end)
yolo.derive("ag", "theirs", function(ctx) return { baseUrl = url(ctx) } end)
yolo.env("ag", function(ctx) return { AG_BASE_URL = url(ctx) } end)
`)
	got, err := pointers(t, p, "http://127.0.0.1:8216")
	if err != nil {
		t.Fatal(err)
	}
	want := "config:providers.zai.baseUrl other:list.1 env:AG_BASE_URL"
	if strings.Join(got, " ") != want {
		t.Errorf("pointers = %q, want %q", got, want)
	}
}

// TestDerivedViaPointersFindsNoneWhereNothingIsRepointed: a derive that keeps the selected
// provider on its own client points nothing at the URL, and an agent with no via URL is not
// derived at all.
func TestDerivedViaPointersFindsNoneWhereNothingIsRepointed(t *testing.T) {
	p := pointerPack(t, `yolo.derive("ag", "config", function(ctx)
  return { providers = { zai = { baseUrl = "https://api.z.ai/v4" } } }
end)
`)
	if got, err := pointers(t, p, "http://127.0.0.1:8216"); err != nil || len(got) != 0 {
		t.Errorf("pointers = %q err %v, want none", got, err)
	}
	if got, err := pointers(t, p, ""); err != nil || got != nil {
		t.Errorf("with no via URL: pointers = %q err %v, want nil", got, err)
	}
}

// TestDerivedViaPointersReturnsADeriveError: the boot runs the same derive and refuses the
// launch over its error, so the gate hands the error back instead of guessing an answer.
func TestDerivedViaPointersReturnsADeriveError(t *testing.T) {
	p := pointerPack(t, `yolo.derive("ag", "config", function(ctx) error("broken") end)`)
	if _, err := pointers(t, p, "http://127.0.0.1:8216"); err == nil ||
		!strings.Contains(err.Error(), "ag/config's derive") {
		t.Errorf("err = %v, want the surface's derive error", err)
	}
}
