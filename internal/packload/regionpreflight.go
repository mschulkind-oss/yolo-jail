package packload

// regionpreflight.go is the REGION PRE-FLIGHT (docs/design/bedrock-plumbing.md §8, OQ-BR6,
// ruled 2026-09-25): a launch whose profile selects a provider that is reached through a
// region, and in which yolo can see no region for that provider, is refused before anything
// starts. Without it codex fails at its first request, and opencode and pi silently use
// us-east-1, a region nobody chose (the ruling's premise, read from their shipped clients and
// never run).
//
// WHICH PROVIDERS. One whose pack declares `region_env_name` (packdecl.Contribution), and only
// one: that field is the whole requirement. Core names no provider and no variable here —
// matching "bedrock" by name is the candidate providers-and-profiles-redesign.md §4 rejects, the
// provider marker is OQ-BR2's open question, and which AWS variable carries a region is the
// pack's fact for the reason envoverride.go gives for the credential variables. packs/claude
// declares AWS_REGION and AWS_DEFAULT_REGION on its `bedrock` provider.
//
// WHAT COUNTS AS A REGION is exactly what the ruling names, both of which yolo can see at
// launch: the composed entry's `region` (the pack's fact under the user's `providers` entry,
// which may set it from either config scope), and one of the declared variables set in what
// the launch delivers to the agent. Nothing else — an `~/.aws/config` region in particular is
// not counted, because whether an agent reads it is unproven, and "unproven emits nothing":
// the refusal says it was not counted rather than guessing that it would be.
//
// SCOPED LIKE THE CREDENTIAL PRE-FLIGHT: a provider no agent's profile selects is no
// requirement, and an entry the composed table does not hold (the user's `null`) is nobody's.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// regionRequirement is one provider's region requirement: the pack whose declaration the
// composed entry carries, and the variables that declaration names.
type regionRequirement struct {
	pack string
	vars []string
}

// regionRequirements maps each provider the selected packs ship to its region requirement,
// attributed to the LAST shipper as requiredProviders attributes a credential (a provider name
// is sole-owned across packs, so in a launch that loaded there is one). A provider whose
// shipper declares no `region_env_name` has no entry.
func regionRequirements(packs []*Pack) map[string]regionRequirement {
	out := map[string]regionRequirement{}
	for _, p := range packs {
		for _, prov := range p.Decl.Providers() {
			if len(prov.RegionEnvName) == 0 {
				delete(out, prov.Name)
				continue
			}
			out[prov.Name] = regionRequirement{pack: p.Name, vars: prov.RegionEnvName}
		}
	}
	return out
}

// ProviderRegionGaps returns the FACT lines of the region pre-flight, empty when every selected
// provider that needs a region has one. No verdict and no remedy: ProviderRegionRefusal wraps
// them in both, the same at every notch.
//
// selected is the providers some agent's profile selects (CredentialScope.SelectedProviders).
// lookup answers "does the environment this launch delivers to the agent hold this variable",
// which each notch answers for itself: the jail notch from what crosses into the jail, which
// excludes the environment yolo was launched from, and the host notch from the environment it
// is about to exec, which includes it. stranded reports whether a variable lookup did not find
// is nonetheless set where yolo was launched — the one mistake worth naming, since the user can
// see AWS_REGION in their own shell — and is nil where the lookup already covers that
// environment. consulted names the channels the notch looked in, quoted verbatim.
func ProviderRegionGaps(packs []*Pack, providers *jsonx.OrderedMap, selected []string,
	lookup func(string) (string, bool), stranded func(string) bool, consulted []string) []string {
	if providers == nil || len(selected) == 0 {
		return nil
	}
	isSelected := make(map[string]bool, len(selected))
	for _, name := range selected {
		isSelected[name] = true
	}
	reqs := regionRequirements(packs)
	var facts []string
	for _, name := range providers.Keys() {
		req, ok := reqs[name]
		if !ok || !isSelected[name] {
			continue
		}
		entry := providerEntry(providers, name)
		if entry == nil {
			continue // the user's null dropped it: nobody's requirement
		}
		if r, _ := entry.Get("region"); r != nil {
			if s, _ := r.(string); s != "" {
				continue
			}
		}
		if anySet(req.vars, lookup) {
			continue
		}
		facts = append(facts, "  • pack "+req.pack+" requires a region for provider "+quoted(name)+
			": its composed entry sets no \"region\", and "+noneSetPhrase(req.vars)+
			" in this launch's environment")
		for _, v := range req.vars {
			if stranded != nil && stranded(v) {
				facts = append(facts, "    "+v+" is set in the environment yolo was launched from, "+
					"which this launch does not deliver to the agent")
			}
		}
		facts = append(facts, fmt.Sprintf("    set one: \"providers\": {%q: {\"region\": \"<region>\"}} "+
			"in your yolo config, or %s=<region> in an env_sources entry", name, req.vars[0]))
	}
	if len(facts) == 0 {
		return nil
	}
	where := "each selected provider's composed entry"
	if len(consulted) > 0 {
		where += ", then " + strings.Join(consulted, ", ")
	}
	return append(facts, "  consulted for a region: "+where)
}

// RegionConsulted is the region pre-flight's consulted list for a notch: the env_sources
// entries it walked, under FromEnvSources' name (or a note that none are configured), then the
// notch's other channels in the order it reads them.
func RegionConsulted(envSources []string, channels ...string) []string {
	first := FromEnvSources + ": none configured"
	if len(envSources) > 0 {
		first = FromEnvSources + ": " + strings.Join(envSources, ", ")
	}
	return append([]string{first}, channels...)
}

// ProviderRegionRefusal is the region pre-flight's whole message, at EVERY notch: the verdict,
// ProviderRegionGaps' facts under it, and the remedy — or, held (paths.AllowMissingProvidersEnv
// set), the override notice over the same facts. It reports whether the launch must stop, for
// the reason ProviderCredentialRefusal does: the hatch turns a refusal into a loud continuation.
//
// THE CREDENTIAL PRE-FLIGHT'S HATCH, not one of its own. A selected provider with no region is
// a provider this launch cannot deliver, which is what YOLO_ALLOW_MISSING_PROVIDERS already
// overrules; and the one launch a user may need it for is an agent that does read a region the
// refusal cannot count (an ~/.aws/config one), which is a fact about the user's setup rather
// than a yolo fault.
func ProviderRegionRefusal(facts []string, held bool) (lines []string, refuse bool) {
	if len(facts) == 0 {
		return nil, false
	}
	if held {
		return append([]string{"Warning: " + paths.AllowMissingProvidersEnv +
			" is set — CONTINUING, with a selected provider's region still unset. Nothing was " +
			"repaired: the agent's first request against that provider will fail, or go to a " +
			"region nobody chose."}, facts...), false
	}
	lines = append([]string{
		"Refusing to launch: a selected provider is reached through a region, and this launch names none.",
	}, facts...)
	return append(lines,
		"  A region in ~/.aws/config is not counted: that an agent reads it is unproven.",
		"  Set a region as above, or launch anyway with "+paths.AllowMissingProvidersEnv+"=1."), true
}

// anySet reports whether lookup finds a non-empty value for any of names. An EMPTY value is
// unset, as it is to the credential pre-flight: `AWS_REGION=` names no region.
func anySet(names []string, lookup func(string) (string, bool)) bool {
	for _, n := range names {
		if v, ok := lookup(n); ok && v != "" {
			return true
		}
	}
	return false
}

// noneSetPhrase says that none of names is set, in English: "X is not set", "neither X nor Y
// is set", "none of X, Y, Z is set".
func noneSetPhrase(names []string) string {
	switch len(names) {
	case 1:
		return names[0] + " is not set"
	case 2:
		return "neither " + names[0] + " nor " + names[1] + " is set"
	default:
		return "none of " + strings.Join(names, ", ") + " is set"
	}
}
