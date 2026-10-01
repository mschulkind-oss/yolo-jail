package entrypoint

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE WIRE TABLES — the three JSON objects the launch composes once and every boot reads:
// the composed providers, the resolved profile table, and the per-CLI selection. They cross
// together or not at all, because each reader resolves one through the others (a selection
// names a profile, a profile names a provider).
//
// WireTables is THE list, and every place that relays or checks the set ranges over it
// rather than spelling it: the launch's two writers (the run pipeline's packChannel, through
// its wireTableValues), the channel-section reader (ParseEntryChannel), and the macos-user
// plan's bootstrap relay and its invariant (macosuser.BuildRunPlan, macosuser.PlanInvariants). Two hand-spelled copies of that set are how YOLO_PROFILES came
// to be relayed on no macos-user launch while both copies agreed with each other: codex
// then rendered no model_provider there, because the profile its selection named was
// absent from a table nobody relayed (docs/plans/notch-convergence.md, row D2).
const (
	ProvidersWireEnv   = "YOLO_PROVIDERS"
	ProfilesWireEnv    = "YOLO_PROFILES"
	UseProfilesWireEnv = "YOLO_USE_PROFILES"
)

// WireTables returns the wire-table variable names, in the order the launch writes them.
// A fresh slice each call, so no caller can edit the list another reads.
func WireTables() []string {
	return []string{ProvidersWireEnv, ProfilesWireEnv, UseProfilesWireEnv}
}

// LoadProviders reads the YOLO_PROVIDERS JSON object passed into the jail environment.
// Returns an OrderedMap whose key order follows declaration order.
func (e *Env) LoadProviders() *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	raw := e.Getenv(ProvidersWireEnv)
	if raw == "" {
		return out
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return out
	}
	if m, ok := decoded.(*jsonx.OrderedMap); ok {
		return m
	}
	return out
}

// LoadUseProfiles reads the YOLO_USE_PROFILES JSON object passed into the jail environment.
// Returns an OrderedMap mapping agent names to active profile names.
func (e *Env) LoadUseProfiles() *jsonx.OrderedMap {
	out := jsonx.NewOrderedMap()
	raw := e.Getenv(UseProfilesWireEnv)
	if raw == "" {
		return out
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return out
	}
	if m, ok := decoded.(*jsonx.OrderedMap); ok {
		return m
	}
	return out
}

// LoadProfiles reads the YOLO_PROFILES JSON object passed into the jail environment —
// the resolved table for every profile name this launch could activate, keyed by profile
// NAME (not by CLI: the same name can be active for several agents, which is the point of
// it being a selection rather than a per-agent setting). The entry type is packload's
// ResolvedProfile, the shape ResolveProfiles produced host-side, because both consumers
// here read the pair out of it the same way: the provider a profile selects
// (packload.ProviderFor) and the option map it carries.
//
// Absent, empty, or undecodable is an EMPTY map, the same answer LoadProviders gives for
// its table: a launch that composed no profiles (or an older launcher that emitted no
// variable) has no profiles, and that is not an error to recover from here — the
// launcher's composition is the one this side reads, and an empty table is the "no
// selection" world every derive already handles (OQ-CS2).
func (e *Env) LoadProfiles() map[string]packload.ResolvedProfile {
	out := map[string]packload.ResolvedProfile{}
	raw := e.Getenv(ProfilesWireEnv)
	if raw == "" {
		return out
	}
	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return out
	}
	m, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		return out
	}
	for _, name := range m.Keys() {
		v, _ := m.Get(name)
		entry, isMap := v.(*jsonx.OrderedMap)
		if !isMap {
			continue
		}
		p := packload.ResolvedProfile{Options: map[string]string{}}
		for _, key := range entry.Keys() {
			val, _ := entry.Get(key)
			s, isStr := val.(string)
			if !isStr {
				continue
			}
			switch key {
			case "provider":
				p.Provider = s
				continue
			case packload.WireViaKey:
				p.Via = s
				continue
			case packload.WireViaBaseKey:
				p.ViaBase = s
				continue
			case packload.WireCarrierKey:
				p.Carrier = s
				continue
			case packload.WireCarrierBaseKey:
				p.CarrierBase = s
				continue
			case packload.WireCarriedKey:
				for _, agent := range strings.Split(s, ",") {
					if agent != "" {
						p.Carried = append(p.Carried, agent)
					}
				}
				continue
			case packload.WireEnforceModelsKey:
				// Anything but "false" is the default, on: a value this build cannot read must
				// not switch a refusal off.
				enforce := s != "false"
				p.EnforceModels = &enforce
				continue
			}
			p.Options[key] = s
		}
		out[name] = p
	}
	return out
}
