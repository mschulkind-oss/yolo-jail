package cli

// hostinputs.go composes the HOST notch's derive inputs (docs/reference/host-agent-environment.md,
// the computed layer at the host), once per invocation, for every host-target reader of that invocation (HC-D11): `yolo
// host apply`'s dry run, --assert and --format json, the launch gate a wrapped `yolo host --`
// runs, and `yolo config render --at host`. The render side is entrypoint.HostInputs.
//
// OQ-HC1 (2026-09-28): "host parity with the same handling". The host runs the jail's derives,
// so what differs between the notches is composed HERE, from user scope alone:
//
//	providers     composedHostProviders — the table `yolo host --` already composes
//	profiles      the user's `profile`, resolved over that table, via addresses cleared
//	              (nothing serves one here, WG-I12), and a selection the host launch refuses
//	              left out and named
//	mcp_servers   the user's own entries, less any that names a jail-only path (named)
//	lsp_servers   the same
//
// and never an MCP preset (its command is a wrapper only a jail's boot writes), a pack's `mcp`
// declaration (OQ-MP3), or a workspace's config (P2).

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostInputComposition is one invocation's host derive inputs and what composing them left out.
type hostInputComposition struct {
	inputs *entrypoint.HostInputs
	// omitted is one sentence per input the host does not carry, for the report: a preset, an
	// entry naming a jail-only path, a selection the host refuses.
	omitted []string
	// providers and selection are what the composition DOES carry, named for the report's
	// detail tier: the provider table's entry names, and "<cli> → <profile>" per selection.
	// A `provider` or `profile` contribution renders invisibly — into the files of the
	// surfaces its facts reach — so this line is where the run names them (the census rule:
	// nothing a pack declares is silently absent).
	providers, selection []string
	// shaped names each `models` contribution the composition applied, "<provider> (<pack>
	// add|only)": like a provider, it renders invisibly, into the lists the derives write.
	shaped []string
}

// summary is the detail line naming what the composition carries.
func (c hostInputComposition) summary() string {
	providers, selection := "none", "none"
	if len(c.providers) > 0 {
		providers = strings.Join(c.providers, ", ")
	}
	if len(c.selection) > 0 {
		selection = strings.Join(c.selection, ", ")
	}
	line := "provider table composed for the derives: " + providers +
		" · profile selection (the profile key): " + selection
	if len(c.shaped) > 0 {
		line += " · model lists shaped by `models`: " + strings.Join(c.shaped, ", ")
	}
	return line
}

// composeHostInputs composes the derive inputs for a host render of packs into home, from the
// user-scope config cfg. An error is a composition the apply must refuse before writing
// anything (HC-D7): a provider table or a profile table that cannot be composed is an input
// every surface shares, and `yolo host --` refuses on it too.
func composeHostInputs(cfg *jsonx.OrderedMap, packs []*packload.Pack, home string) (hostInputComposition, error) {
	var c hostInputComposition
	vars := map[string]string{}

	// THE PROVIDER AND PROFILE SECTION OF VALIDATION first, as `yolo host --` runs it: every
	// input below reads those keys, and the selection reads `profile` alone, so a config every
	// launch refuses (the retired `use_profiles` among it) would otherwise compose as if it
	// selected nothing, and an --assert would deselect the home's profile. In-jail, where the
	// config is a snapshot and a retired key only warns, the warning is named with the rest.
	if err := hostProviderSectionRefusal(cfg, func(w string) { c.omitted = append(c.omitted, w) }); err != nil {
		return c, err
	}

	providers, unservable, err := composedHostProviders(cfg, packs, nil)
	if err != nil {
		return c, fmt.Errorf("your provider table cannot be composed: %w", err)
	}
	vars[entrypoint.ProvidersWireEnv] = wireJSON(providers)
	if providers != nil {
		c.providers = append(c.providers, providers.Keys()...)
	}
	for _, p := range packs {
		for _, mc := range p.Decl.ModelsContributions() {
			verb := "add"
			if len(mc.Only) > 0 {
				verb = "only"
			}
			c.shaped = append(c.shaped, mc.Provider+" ("+p.Name+" "+verb+")")
		}
	}

	userProfiles, err := config.LoadProfiles(nil)
	if err != nil {
		return c, fmt.Errorf("your `profiles` cannot be read: %w", err)
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, providers)
	if err != nil {
		return c, fmt.Errorf("your profiles cannot be resolved: %w", err)
	}
	// NO VIA ROUTE: a via address is a jail daemon's, and nothing at this notch serves one, so
	// every agent keeps its own client (WG-I12) — as `yolo host --` and the host footer read it.
	inert, _ := packload.ViaServedAt(resolved, packs, packload.NothingServed())
	vars[entrypoint.ProfilesWireEnv] = wireJSON(packload.ProfilesWireTable(inert))

	// THE SELECTION (OQ-HC3): the user-scope `profile`, never a `-p` (host apply has
	// none). A pairing the host launch refuses — a profile only a jail's service can serve —
	// is left out and named, since a file selecting it would name a provider no host process
	// of that agent can reach.
	use := jsonx.NewOrderedMap()
	fold := hostProfileFold(cfg, packs, "", "")
	selected := fold.Table
	// Each agent's ACTIVE SET (docs/design/active-provider-sets.md §4.9): the value is a string or
	// a list, and profile below is its primary. A set is rendered whole or not at all (AP-P2).
	sets := packload.ProfileSets(selected)
	// The key's BARE list (its list form, or a list under "*") reaches an agent whose pack
	// declares no provider_sets as its first entry alone (OQ-AP3), and the apply says so, as a
	// launch does: the entries that agent ignores are rendered into none of its files.
	if note := fold.BareListNote(true); note != "" {
		c.omitted = append(c.omitted, note)
	}
	for _, agent := range selected.Keys() {
		v, _ := selected.Get(agent)
		set := sets[agent]
		profile := ""
		if len(set) > 0 {
			profile = set[0]
		}
		// The set's own rules (AP-D3, OQ-AP2, AP-D9) and every later entry's pairing: either one
		// refused leaves the whole selection out, named.
		if why := hostSetOmission(packs, providers, resolved, agent, set, unservable); why != "" {
			c.omitted = append(c.omitted, fmt.Sprintf("profile %s → %s is not applied at "+
				"the host: %s", agent, strings.Join(set, ", "), why))
			continue
		}
		if refusal := packload.PairingRefusal(packs, providers, resolved, agent, profile,
			unservable); refusal != nil {
			// A BRIDGED SELECTION (docs/design/host-notch-services.md OQ-HS3, ruled per launch):
			// its address is a port one launch picks for a service that lives only as long as
			// that launch, so no file can hold it, and the apply says where the selection does
			// take effect instead of calling it refused.
			if note := bridgedSelectionNote(packs, agent, profile, refusal); note != "" {
				c.omitted = append(c.omitted, note)
				continue
			}
			c.omitted = append(c.omitted, fmt.Sprintf("profile %s → %s is not applied at "+
				"the host: %s", agent, profile, firstLine(refusal.Error())))
			continue
		}
		if len(set) > 0 {
			v = packload.ProfileSetWire(set)
		}
		use.Set(agent, v)
		// A set is named as -p spells it, "pi → zai,openrouter", since ", " separates agents here.
		c.selection = append(c.selection, agent+" → "+strings.Join(set, ","))
	}
	vars[entrypoint.UseProfilesWireEnv] = wireJSON(use)

	servers, omitted := hostServerTable(cfg, "mcp_servers", home)
	c.omitted = append(c.omitted, omitted...)
	vars[entrypoint.MCPServersWireEnv] = wireJSON(servers)
	lsp, omitted := hostServerTable(cfg, "lsp_servers", home)
	c.omitted = append(c.omitted, omitted...)
	vars[entrypoint.LSPServersWireEnv] = wireJSON(lsp)

	// THE PRESETS NEVER EXPAND HERE (HC-D6): each command names the node wrapper a jail's boot
	// writes and the jail's npm prefix, neither of which a real home has, and composing them
	// for the host instead is what mcp-presets-removal.md retires them rather than do.
	if v, ok := cfg.Get("mcp_presets"); ok {
		if list, isList := v.([]any); isList {
			for _, p := range list {
				if name, isStr := p.(string); isStr && name != "" {
					c.omitted = append(c.omitted, fmt.Sprintf("mcp_presets %s is not written at "+
						"the host: its command is a wrapper only a jail writes — declare the "+
						"server under `mcp_servers` with a command this machine has", name))
				}
			}
		}
	}

	c.inputs = &entrypoint.HostInputs{Vars: vars, Packs: packs,
		AgentLookup: hostAgentLookup(cfg)}
	return c, nil
}

// hostSetOmission is why `yolo host apply` leaves one agent's ACTIVE SET out of the files it
// renders, "" to render it: the set's own rules (packload.ProfileSetProblems), then the pairing
// of every entry after the primary, whose own refusal the caller words with its bridged note.
// A set of one asks nothing here.
func hostSetOmission(packs []*packload.Pack, providers *jsonx.OrderedMap,
	resolved map[string]packload.ResolvedProfile, agent string, set []string,
	unservable []packload.Adaptation) string {
	if len(set) < 2 {
		return ""
	}
	if problems := packload.ProfileSetProblems(packs, providers, map[string][]string{agent: set}, resolved); len(problems) > 0 {
		return problems[0]
	}
	for i, name := range set {
		if i == 0 {
			continue
		}
		if r := packload.PairingRefusal(packs, providers, resolved, agent, name, unservable); r != nil {
			return fmt.Sprintf("profile %s, entry %d of the list, cannot be served here: %s",
				name, i+1, firstLine(r.Error()))
		}
	}
	return ""
}

// hostServerTable is the user-scope `key` table (mcp_servers or lsp_servers) as the host
// carries it: every entry but one that names a jail-only path (HC-D6), each such entry named.
// A null entry is a jail-side removal of a preset, and there are no presets here, so it is
// dropped. The predicate is the render's own output check (entrypoint.JailPathsIn, HC-D14).
func hostServerTable(cfg *jsonx.OrderedMap, key, home string) (*jsonx.OrderedMap, []string) {
	out := jsonx.NewOrderedMap()
	v, ok := cfg.Get(key)
	if !ok {
		return out, nil
	}
	table, isMap := v.(*jsonx.OrderedMap)
	if !isMap {
		return out, nil
	}
	var omitted []string
	for _, name := range table.Keys() {
		entry, _ := table.Get(name)
		if entry == nil {
			continue
		}
		if bad := entrypoint.JailPathsIn(entry, home); len(bad) > 0 {
			omitted = append(omitted, fmt.Sprintf("%s %s is not written at the host: it names "+
				"a path that exists only inside a jail (%s)", key, name, strings.Join(bad, "; ")))
			continue
		}
		out.Set(name, entry)
	}
	return out, omitted
}

// hostAgentLookup answers a server's requires_env for ONE agent, the way that agent's process
// will see its environment when `yolo host` launches it: the invoking environment with the
// agent's composition (composeHostVars, the body `yolo host env --agent` prints) applied over
// it — so a provider credential that reaches only the agent that selected its provider
// (OQ-CN6) gates the server for that agent alone (HC-D6). Composed lazily, once per agent, and
// only for an agent a requires_env server asks about.
func hostAgentLookup(cfg *jsonx.OrderedMap) func(agent string) func(string) (string, bool) {
	cache := map[string]map[string]*string{}
	workspace, err := os.Getwd()
	if err != nil {
		workspace = "."
	}
	return func(agent string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			composed, ok := cache[agent]
			if !ok {
				composed = map[string]*string{}
				c := composeHostVars(cfg, workspace, agent, "", nil)
				if c.err == nil {
					for _, v := range c.vars {
						if v.Unset {
							composed[v.Key] = nil
							continue
						}
						val := v.Value
						composed[v.Key] = &val
					}
				}
				cache[agent] = composed
			}
			if v, set := composed[key]; set {
				if v == nil {
					return "", false
				}
				return *v, true
			}
			return os.LookupEnv(key)
		}
	}
}

// wireJSON is a table's wire text, "{}" when it has none or will not encode.
func wireJSON(m *jsonx.OrderedMap) string {
	if m == nil {
		return "{}"
	}
	text, err := jsonx.DumpsCompact(m)
	if err != nil {
		return "{}"
	}
	return text
}

// firstLine is an error's first line, for a one-line report.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// sortedOmitted is the report order: stable across runs.
func sortedOmitted(lines []string) []string {
	out := append([]string(nil), lines...)
	sort.Strings(out)
	return out
}

// bridgedSelectionNote is what `yolo host apply` says of a profile selection whose pairing
// runs through a pack service a host launch starts for its command (launchservice.Admit admits
// its host half), "" for any other refusal: the apply renders no address for it, and the
// selection takes effect only through `yolo host --` or the host wrappers. An agent started any
// other way runs without it, which the maintainer's ruling accepts (OQ-HS3).
func bridgedSelectionNote(packs []*packload.Pack, agent, profile string, refusal error) string {
	var unserved *packload.UnservedAdapterError
	if !errors.As(refusal, &unserved) || !unserved.Selected || unserved.ProviderPack != "" {
		return ""
	}
	d, err := launchservice.Admit(packs, unserved.Adaptation.Service)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("profile %s → %s renders no address here: it runs through pack %q's "+
		"%q service, which a host launch starts for its own command and stops when that command "+
		"exits, so no file can name it. It takes effect through `yolo host -- %s` or the host "+
		"wrappers; %s started any other way runs without it (docs/design/host-notch-services.md "+
		"OQ-HS3)", agent, profile, d.Pack, d.Service, agent, agent)
}
