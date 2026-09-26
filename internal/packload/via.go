package packload

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ResolveVias is the `via` half of the selection closure (OQ-WG6/WG7 (c),
// docs/design/wire-bridge-gateway.md): selecting a profile whose `via` names a service
// pack adds that pack to the launch the way a live `needs` entry does, so a pi-only jail
// with a via profile gets the service's daemon. Core knows only "the pack a via names";
// which pack that is, and that it runs a bridge, are the manifests' facts.
//
// active is the launch's CLI-name → profile-name table (the effective use_profiles);
// user is the user's profile declarations, whose `via` wins over a pack-shipped one's,
// as it does in ResolveProfiles. embedded looks a name up in the embedded official set.
// The vias it walks are ActiveVias', so an entry for an agent no selected pack installs
// adds nothing (WG-I10).
//
// Callers reach it through Selection.Close, the whole closure, rather than calling it
// beside ResolveNeeds by hand: the launch, `yolo check`, config validation and
// `config promote` each used to run the needs half alone, and one resolver is what keeps
// their pack lists the same (WG-I11).
//
// The rules, each a refusal with the pack and profile named:
//
//   - A via may name only an EMBEDDED official pack, for needs' reason (WB-D9): a
//     profile — which a user config can declare — must not pull unreviewed code into a
//     launch. A pack the user already selected is a join, not an addition.
//   - The named pack must declare a service with a `via_address`; a pack that serves no
//     via route would leave the agent pointed at an address nothing binds.
//
// It returns the packs added (not yet in selected), one cause line per addition in the
// needs closure's shape, and the error. Selection.Close re-runs ResolveNeeds over the
// grown set, since an added pack may declare needs of its own.
func ResolveVias(selected []*Pack, active map[string]string, user map[string]UserProfile,
	embedded func(name string) (*Pack, bool)) (added []*Pack, causes []string, err error) {
	set := make(map[string]*Pack, len(selected))
	for _, p := range selected {
		if p != nil {
			set[p.Name] = p
		}
	}
	for _, v := range ActiveVias(selected, active, user) {
		p, have := set[v.Via]
		if !have {
			target, ok := embedded(v.Via)
			if !ok {
				return nil, nil, fmt.Errorf("profile %q (active for %s) names via %q, which is "+
					"not an embedded official pack — a via may name only packs yolo ships, so a "+
					"profile cannot pull unreviewed code into a launch (the needs rule, WB-D9)",
					v.Profile, v.Agent, v.Via)
			}
			p = target
			set[v.Via] = p
			added = append(added, p)
			causes = append(causes, "+ "+v.Via+" (via of profile "+v.Profile+", active for "+v.Agent+")")
		}
		if addr, _ := ViaServiceAddress([]*Pack{p}, v.Via); addr == "" {
			return nil, nil, fmt.Errorf("profile %q (active for %s) names via %q, whose pack "+
				"declares no service with a via_address — nothing would serve %s's route, so "+
				"its agent would be pointed at an address nothing binds", v.Profile, v.Agent, v.Via, v.Agent)
		}
	}
	return added, causes, nil
}

// ActiveVia is one agent whose active profile names a via: the agent's CLI name, the
// profile active for it, and the service pack that profile's `via` names.
type ActiveVia struct {
	Agent, Profile, Via string
}

// ActiveVias lists, in agent order, every agent whose profile in active names a via, the
// user's `via` winning over a pack-shipped one as it does in ResolveProfiles. It is the one
// answer to "which vias does this selection turn on": the closure (ResolveVias) adds a pack
// for each, and the launch's via-route gate (wirebridged.ViaRouteGate) is asked only
// when there is one.
//
// AN AGENT NO PACK IN packs INSTALLS HAS NO ACTIVE VIA (WG-I10). A `use_profiles` key is
// checked against every CLI a resolvable pack installs, selected or not, so a user-scope
// entry for an agent this launch does not carry is legal and common. Its via has no agent
// to route: it must neither add a pack (the disclosure line would say "active for" an
// agent that is not there) nor refuse a launch over a route nobody would use. The rule is
// the protocol gate's (binOwner): an agent no selected pack installs pairs with nothing.
func ActiveVias(packs []*Pack, active map[string]string, user map[string]UserProfile) []ActiveVia {
	shipped := packShippedProfiles(packs)
	agents := make([]string, 0, len(active))
	for agent := range active {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	var out []ActiveVia
	for _, agent := range agents {
		name := active[agent]
		if name == "" || binOwner(packs, agent) == nil {
			continue
		}
		via := shipped[name].Via
		if u, ok := user[name]; ok && u.Via != "" {
			via = u.Via
		}
		if via == "" {
			continue
		}
		out = append(out, ActiveVia{Agent: agent, Profile: name, Via: via})
	}
	return out
}

// ViaInert returns resolved with every profile's ViaBase cleared and its Via kept: the
// table a notch that runs no jail daemon hands its derives (WG-I8, WG-I12). ViaURLFor, the
// one predicate both derive paths ask "is this agent's via live?", then answers "" for
// every agent, so each keeps its own client whatever the pack set holds. The pack set is
// the gap it closes: a user who lists wire-bridge in `packs` explicitly gives
// ResolveProfiles a via_address to resolve at `yolo host` too, and the host's env derive
// would otherwise receive a URL no daemon serves there. Via stays stated, so a reader of
// the table still sees which profiles route through a service.
func ViaInert(resolved map[string]ResolvedProfile) map[string]ResolvedProfile {
	if resolved == nil {
		return nil
	}
	out := make(map[string]ResolvedProfile, len(resolved))
	for name, r := range resolved {
		r.ViaBase = ""
		out[name] = r
	}
	return out
}

// ViaPointer is one place an agent's derived config carries its via URL: the surface whose
// derive wrote it ("" for the agent's env producer), the key path from that layer's root to
// the string, and the whole layer the path is in.
type ViaPointer struct {
	Surface string
	Path    []string
	Layer   map[string]any
}

// DerivedViaPointers answers "does this agent's config actually point it at its via URL?"
// by running the derives that write that config, over the launch's own tables and with
// ctx.via_url set as the boot sets it, and returning every place their output carries the
// URL (WG-I15, docs/design/wire-bridge-gateway.md). None means the via re-points nothing
// for this agent: its config is what it would be without via.
//
// It exists because a profile's `via` is an input to a derive, not an instruction to it.
// Each derive decides for itself which provider rows ride ctx.via_url: pi and oh-omp skip
// `openai-codex`, their built-in subscription client, and codex writes a row only for a
// provider it can reach at all, while opencode re-points whatever provider is selected.
// No declaration states those rules, so the launch's via-route gate
// (wirebridged.ViaRouteGate) asks the derives, the one place they live, instead of
// refusing on a guess from the manifests.
//
// The inputs are the boot's: every rendered surface a selected pack declares for this
// agent, with the ctx deriveComputedLayer builds (the resolved selection, the provider and
// profile tables), plus the agent's env producer, which packload.AgentEnv runs for the
// jail's environment. The providers carry no credential (hydrateProviders with no lookup),
// because only an address is looked for. An unknown `yolo.<name>` is tolerated, as the boot
// tolerates it, and the boot is where it is reported. A Lua error is returned: the boot
// runs the same derive and refuses the launch over it.
//
// A string counts when it is the URL or starts with the URL and a slash. A match is
// read off output values only, so a derive that names the URL in a key points nothing.
func DerivedViaPointers(packs []*Pack, providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]ResolvedProfile, agent string) ([]ViaPointer, error) {
	profile := useProfiles[agent]
	url := ViaURLFor(resolved[profile], agent)
	if url == "" {
		return nil, nil
	}
	options := map[string]string{}
	if r, ok := resolved[profile]; ok && r.Options != nil {
		options = r.Options
	}
	base := luahook.DeriveCtx{
		Agent:              agent,
		ProfileName:        profile,
		SelectedProvider:   ProviderFor(resolved, profile),
		Profile:            options,
		NativeCapabilities: NativeCapabilities(packs, agent),
		ViaURL:             url,
		UnknownAPI:         func(string) {},
	}
	tables := func() map[string]map[string]any {
		return map[string]map[string]any{
			manifest.SourceProviders:   hydrateProviders(providers, nil),
			manifest.SourceUseProfiles: plainProfiles(useProfiles),
		}
	}
	vm := luahook.GopherLuaVM{}
	var out []ViaPointer
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		script := DeriveScript(p)
		if script == "" {
			continue
		}
		surfaces, _ := p.Surfaces()
		for _, s := range surfaces {
			if s.Agent != agent || s.ResolvedMode() == manifest.ModeUnrendered {
				continue
			}
			ctx := base
			ctx.Surface = s.Name
			ctx.Tables = tables()
			layer, err := vm.Derive(script, &ctx)
			if err != nil {
				return nil, fmt.Errorf("pack %s: %s/%s's derive: %w", p.Name, agent, s.Name, err)
			}
			out = appendViaPointers(out, s.Name, layer, url)
		}
	}
	if owner := binOwner(packs, agent); owner != nil {
		if script := DeriveScript(owner); script != "" {
			ctx := base
			ctx.Env = true
			ctx.Tables = tables()
			env, err := vm.Derive(script, &ctx)
			if err != nil {
				return nil, fmt.Errorf("pack %s: %s's env derive: %w", owner.Name, agent, err)
			}
			out = appendViaPointers(out, "", env, url)
		}
	}
	return out, nil
}

// appendViaPointers walks one derived layer in key order and appends a ViaPointer for each
// string value that is url or lies under it.
func appendViaPointers(out []ViaPointer, surface string, layer map[string]any, url string) []ViaPointer {
	var walk func(v any, path []string)
	walk = func(v any, path []string) {
		switch t := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k], append(append([]string(nil), path...), k))
			}
		case []any:
			for i, e := range t {
				walk(e, append(append([]string(nil), path...), strconv.Itoa(i)))
			}
		case string:
			if t == url || strings.HasPrefix(t, url+"/") {
				out = append(out, ViaPointer{Surface: surface, Path: path, Layer: layer})
			}
		}
	}
	walk(layer, nil)
	return out
}
