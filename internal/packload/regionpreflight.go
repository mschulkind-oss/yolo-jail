package packload

// regionpreflight.go is the REGION PRE-FLIGHT (docs/design/bedrock-plumbing.md §8, OQ-BR6,
// ruled 2026-09-25): a launch whose profile selects a provider that is reached through a
// region, and in which yolo can see no region for that provider, is refused before anything
// starts. Without it codex fails at its first request, and claude, opencode and pi silently
// use us-east-1, a region nobody chose (the ruling's premise, read from their shipped clients
// and never run; claude's was added from Claude Code 2.1.285's binary on 2026-09-29, whose
// resolver reads AWS_REGION, then AWS_DEFAULT_REGION, then the shared-config region, then
// falls back to "us-east-1").
//
// WHICH PROVIDERS: every one whose composed entry declares a PLATFORM some selected pack says is
// reached through a region — OQ-BR2's marker (docs/design/providers-and-profiles-redesign.md,
// ruled 2026-09-29): a provider is recognized by what it says it is, never by its name. A pack
// says so by declaring `region_env_name` beside `platform` on a provider it ships
// (packdecl.Contribution): the variables an agent on that platform reads its region from.
// packs/claude declares AWS_REGION and AWS_DEFAULT_REGION on its `bedrock`, whose platform is
// "aws-bedrock", so a provider a USER declares with "platform": "aws-bedrock" carries the same
// requirement, naming the same variables — the ruling's "a provider a user defines gets the same
// behavior as the shipped one". A user provider whose platform no selected pack declares
// variables for carries none: core names no provider, no platform and no variable here, since
// which AWS variable carries a region is the pack's fact, for the reason envoverride.go gives
// for the credential variables.
//
// WHAT COUNTS AS A REGION is exactly what the ruling names, both of which yolo can see at
// launch: the composed entry's `region` (the pack's fact under the user's `providers` entry,
// which may set it from either config scope), and one of the declared variables set in what
// the launch delivers to the agent. Nothing else — an `~/.aws/config` region in particular is
// not counted, because yolo does not read that file, and the refusal says exactly that. It
// used to say that an agent reading it was "unproven", which is false of claude: Claude Code's
// resolver does read the shared-config region for the active AWS_PROFILE (read statically from
// 2.1.285). So at `yolo host`, where the user's ~/.aws is claude's own, a user whose profile
// names a region is refused although claude would find one; whether yolo should read that file
// there is a question back to the maintainer (bedrock-plumbing.md, OQ-BR6), and until it is
// answered the hatch is the way through.
//
// PER AGENT, because the credential gate made delivery per agent (OQ-CN6): a region one agent
// receives through its own gated env or its own shape vars is not a region another agent on
// the same provider receives, and "delivered to some process of the launch" let exactly that
// through (the review's two-agent case: codex's own AWS_REGION satisfied claude's bedrock). So
// each notch hands one RegionAsk per agent whose profile selects a provider, with a lookup
// answering for that agent alone, and the refusal names the agents that receive no region.
//
// SCOPED LIKE THE CREDENTIAL PRE-FLIGHT: a provider no agent's profile selects is no
// requirement, and an entry the composed table does not hold (the user's `null`) is nobody's.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// regionRequirement is one PLATFORM's region requirement: the pack whose provider declared the
// platform's region variables, and those variables.
type regionRequirement struct {
	pack string
	vars []string
}

// regionRequirements maps each platform a selected pack says is reached through a region to its
// requirement. Every provider declaration carrying `region_env_name` (which packdecl refuses
// without a `platform` beside it) adds its variables to its platform's list, in declaration
// order and without repeats, and the requirement is attributed to the last pack that declared
// one. A platform no pack declares variables for has no entry, and neither has a provider with
// no platform at all.
func regionRequirements(packs []*Pack) map[string]regionRequirement {
	out := map[string]regionRequirement{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, prov := range p.Decl.Providers() {
			if len(prov.RegionEnvName) == 0 || prov.Platform == "" {
				continue
			}
			req := out[prov.Platform]
			for _, v := range prov.RegionEnvName {
				if !slices.Contains(req.vars, v) {
					req.vars = append(req.vars, v)
				}
			}
			req.pack = p.Name
			out[prov.Platform] = req
		}
	}
	return out
}

// entryString is one string field of a composed provider entry, "" when it is absent or not a
// string (the config validator refuses a non-string `region` or `platform`).
func entryString(entry *jsonx.OrderedMap, key string) string {
	if entry == nil {
		return ""
	}
	v, _ := entry.Get(key)
	s, _ := v.(string)
	return s
}

// RegionAsk is one agent the region pre-flight asks about: the provider its profile selects,
// and a lookup answering whether the launch delivers a variable, non-empty, to THAT agent.
type RegionAsk struct {
	Agent    string
	Provider string
	Lookup   func(string) (string, bool)
}

// ProviderRegionGaps returns the FACT lines of the region pre-flight, empty when every agent on
// a provider that needs a region receives one. No verdict and no remedy: ProviderRegionRefusal
// wraps them in both, the same at every notch.
//
// asks is one entry per agent whose profile selects a provider, each carrying that agent's own
// lookup, which each notch answers for itself: the jail notch from what crosses into the jail
// for that agent, which excludes the environment yolo was launched from, and the host notch
// from the environment it is about to exec, which includes it. stranded reports whether a
// variable no lookup found is nonetheless set where yolo was launched — the one mistake worth
// naming, since the user can see AWS_REGION in their own shell — and is nil where the lookup
// already covers that environment. consulted names the channels the notch looked in, quoted
// verbatim.
func ProviderRegionGaps(packs []*Pack, providers *jsonx.OrderedMap, asks []RegionAsk,
	stranded func(string) bool, consulted []string) []string {
	if providers == nil || len(asks) == 0 {
		return nil
	}
	reqs := regionRequirements(packs)
	// missing is, per provider, the agents on it that receive no region.
	missing := map[string][]string{}
	for _, ask := range asks {
		entry := providerEntry(providers, ask.Provider)
		if entry == nil {
			continue // not in the table, or the user's null dropped it: nobody's requirement
		}
		// THE PLATFORM, off the composed entry: pack default under user override, so a user
		// provider that says it is "aws-bedrock" meets the requirement the shipped one does.
		req, ok := reqs[entryString(entry, "platform")]
		if !ok || entryString(entry, "region") != "" {
			continue
		}
		lookup := ask.Lookup
		if lookup == nil {
			lookup = func(string) (string, bool) { return "", false }
		}
		if anySet(req.vars, lookup) {
			continue
		}
		if !slices.Contains(missing[ask.Provider], ask.Agent) {
			missing[ask.Provider] = append(missing[ask.Provider], ask.Agent)
		}
	}
	var facts []string
	for _, name := range providers.Keys() {
		agents := missing[name]
		if len(agents) == 0 {
			continue
		}
		sort.Strings(agents)
		platform := entryString(providerEntry(providers, name), "platform")
		req := reqs[platform]
		facts = append(facts, "  • pack "+req.pack+" requires a region for provider "+quoted(name)+
			" (platform "+quoted(platform)+"), selected for "+andList(agents)+": its composed "+
			"entry sets no \"region\", and "+noneSetPhrase(req.vars)+" in what this launch "+
			"delivers to "+andList(agents))
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
		"  A region in ~/.aws/config is not counted: yolo does not read it.",
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

// andList joins names in English: "a", "a and b", "a, b and c".
func andList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
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
