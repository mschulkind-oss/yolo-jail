package config

// capabilities.go is THE CAPABILITY CENSUS behind `required_capabilities`
// (docs/design/agent-auth-modes.md §6.2, OQ-CAP2): which named jobs a launch can count as
// done, so that a config declaring it needs one that nothing does is refused before any backend
// starts. The launch's gate (`run.refuseUnmetCapabilities`) and `yolo check`'s prediction of it
// both call UnmetCapabilities, so the two answer from one census.
//
// FOUR SOURCES, and they are the whole census:
//
//   - the baseline every agent meets (CapabilityBaseline), which nothing has to declare;
//   - `providers.<name>.capabilities` in the COMPOSED providers table, read from
//     packload.ComposeProviders, the composition the launch delivers: a selected pack's
//     provider default under the user's override, so `providers.<name>: null` removes the
//     entry and a user's `capabilities` list replaces the pack's;
//   - the BUILT-IN LOGIN of every agent a selected pack installs — the `capabilities` on a
//     pack's `program` — found by bin ownership through packload.NativeCapabilities, the rule
//     capability-driven MCP delivery reads it by (§6.1 clause 1);
//   - an `mcp_servers.<name>` entry whose `provides` names the capability (§6.2's collision rule
//     already refuses two servers claiming one name). A null-removed server provides nothing:
//     the jail deletes it rather than running it.
//
// `mcp_presets` contributes nothing on purpose: a preset is a baked command list
// (internal/entrypoint/mcp.go), and neither shipped preset declares a `provides`.
//
// ONE DELIBERATE OVER-PERMISSION, which predates the pack half and now covers it: a source
// satisfies whether or not a profile makes it the ACTIVE one. A provider in the table counts
// although no profile selects it, and an agent's built-in login counts although a profile may
// point that agent at a provider without the job. Narrowing to the active source needs the
// launch's resolved profile table per agent, and the gate refuses a launch-wide config, not
// one agent's session; erring toward launching is the right direction for a fatal refusal. The
// gap the gate closes is the config where NOTHING could do the job (setup-support-gaps.md G9).

import (
	"errors"
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// CapabilityBaseline is the capability set every agent yolo can launch meets without anything
// declaring it: editing files and running commands are the floor of "a coding agent", so
// agent-auth-modes.md §6.2 makes them the default requirement. Named rather than left implicit
// because `"required_capabilities": ["code_editing"]` is the example the shipped config
// reference carries, and a gate that refused the floor would refuse the documented spelling.
var CapabilityBaseline = []string{"code_editing", "command_execution"}

// AllowUnmetCapabilitiesEnv is the escape hatch out of the capability gate: set, a launch whose
// requirement nothing declares continues, saying so. It is for a capability the environment
// really has that no declaration states — an agent whose pack does not declare what its login
// does, or a tool the agent reaches some way yolo does not deliver — never for a census defect.
const AllowUnmetCapabilitiesEnv = "YOLO_ALLOW_UNMET_CAPABILITIES"

// SelectedPacks resolves the pack selection the census counts, reporting complete=false when
// some of it could not be read. The launch passes its own selection (its narrowed entries and
// its `-p`); `yolo check` passes UserScopeSelectedPacks.
type SelectedPacks func() (packs []*packload.Pack, complete bool)

// errSelectionIncomplete is UnmetCapabilities' answer when a name is unmet and some selected
// pack could not be read: that pack may be the satisfier, so the census proves nothing.
var errSelectionIncomplete = errors.New("a selected pack could not be read, so whether it " +
	"satisfies them is unknown")

// CapabilitySatisfiers maps every capability a source of this launch declares to the phrase
// naming that source; packs is the launch's selection. See the file doc for the sources.
//
// The error is a provider composition that failed (an address conflict the launch refuses
// itself, below this census): the provider half then counts the user's own `providers` table,
// as the census did before packs counted, and the map is still complete in every other half.
func CapabilitySatisfiers(cfg *jsonx.OrderedMap, packs []*packload.Pack) (map[string]string, error) {
	out := map[string]string{}
	add := func(capName, source string) {
		if _, seen := out[capName]; !seen && capName != "" {
			out[capName] = source
		}
	}
	for _, name := range CapabilityBaseline {
		add(name, "the baseline every agent meets")
	}

	user := capabilityMap(cfg, "providers")
	providers, err := packload.ComposeProviders(user, packs)
	if err != nil {
		providers = user
		err = fmt.Errorf("the provider table did not compose: %w", err)
	}
	if providers != nil {
		for _, name := range providers.Keys() {
			pm := capabilityMap(providers, name)
			if pm == nil {
				continue // null drops the provider; it declares nothing
			}
			for _, capName := range capabilityStrings(pm, "capabilities") {
				add(capName, "provider '"+name+"'")
			}
		}
	}

	if servers := capabilityMap(cfg, "mcp_servers"); servers != nil {
		for _, name := range servers.Keys() {
			// A null-removed server is one this jail will not run: the null crosses in
			// YOLO_MCP_SERVERS and the entrypoint's LoadMCPServers deletes the entry. So the
			// merged VALUE is read rather than the key set, or a workspace `"tavily": null`
			// would keep satisfying `web_search` off the user-level entry it just deleted.
			sm := capabilityMap(servers, name)
			if sm == nil {
				continue
			}
			if provides, ok := sm.Get("provides"); ok {
				if s, ok := provides.(string); ok {
					add(s, "mcp_servers."+name)
				}
			}
		}
	}

	// THE BUILT-IN LOGIN of each agent the selection installs, by the bin-ownership rule the
	// launch's own delivery reads it with: the one selected pack that installs a bin speaks for
	// how that CLI authenticates on its own.
	seen := map[string]bool{}
	for _, p := range packs {
		for _, bin := range p.InstallBins() {
			if seen[bin] {
				continue
			}
			seen[bin] = true
			for _, capName := range packload.NativeCapabilities(packs, bin) {
				add(capName, "agent '"+bin+"' (its built-in login)")
			}
		}
	}
	return out, err
}

// UnmetCapabilities returns cfg's `required_capabilities` entries nothing satisfies, in
// declaration order and de-duplicated, so a refusal names them the way the config does.
//
// selected is consulted ONLY when the config's own declarations leave a name unmet, so a launch
// that requires nothing, or only what it declares itself, resolves no pack for this census. That
// is equivalent rather than an approximation: composing pack providers under the user's table
// cannot remove a capability the user's table declares, and the agents' half only adds.
//
// A non-nil error means the census could not prove the names it returns unmet: some selected
// pack could not be read, or the provider table did not compose. Each is a fault the launch
// reports itself — pack staging refuses a pack it cannot stage, and the channel composition
// refuses the table — so a caller that refused here would name the second fault first. The
// names are returned beside the error for that caller to say what it could not check.
func UnmetCapabilities(cfg *jsonx.OrderedMap, selected SelectedPacks) ([]string, error) {
	have, _ := CapabilitySatisfiers(cfg, nil) // no pack, so no composition that can fail
	missing := unmetGiven(cfg, have)
	if len(missing) == 0 || selected == nil {
		return missing, nil
	}
	packs, complete := selected()
	have, err := CapabilitySatisfiers(cfg, packs)
	missing = unmetGiven(cfg, have)
	if len(missing) == 0 {
		return nil, nil
	}
	if err != nil {
		return missing, err
	}
	if !complete {
		return missing, errSelectionIncomplete
	}
	return missing, nil
}

// unmetGiven is the requirement list minus what have holds.
func unmetGiven(cfg *jsonx.OrderedMap, have map[string]string) []string {
	seen := map[string]bool{}
	var missing []string
	for _, name := range capabilityStrings(cfg, "required_capabilities") {
		if name == "" || have[name] != "" || seen[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	return missing
}

// capabilityMap is m[key] as an object, nil for an absent, null or non-object value.
func capabilityMap(m *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	om, _ := asMap(v)
	return om
}

// capabilityStrings is m[key]'s string members, nil for anything but a list; the validator
// reports a malformed list, and the census reads what it can.
func capabilityStrings(m *jsonx.OrderedMap, key string) []string {
	if m == nil {
		return nil
	}
	v, _ := m.Get(key)
	raw, ok := asList(v)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := asStr(e); ok {
			out = append(out, s)
		}
	}
	return out
}
