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
// packs/bedrock declares AWS_REGION and AWS_DEFAULT_REGION on its `bedrock`, whose platform is
// "aws-bedrock", so a provider a USER declares with "platform": "aws-bedrock" carries the same
// requirement, naming the same variables — the ruling's "a provider a user defines gets the same
// behavior as the shipped one". A user provider whose platform no selected pack declares
// variables for carries none: core names no provider, no platform and no variable here, since
// which AWS variable carries a region is the pack's fact, for the reason envoverride.go gives
// for the credential variables.
//
// WHAT COUNTS AS A REGION is what the ruling names, both of which yolo can see at launch — the
// composed entry's `region` (the pack's fact under the user's `providers` entry, which may set
// it from either config scope), and one of the declared variables set in what the launch
// delivers to the agent — and, since the maintainer's direction of 2026-09-29
// (bedrock-plumbing.md BR-DIR1, revising BR-D5's "yolo does not read it"), the region the
// platform's REGION FILE holds for the profile the agent's credential comes from: ~/.aws/config
// for Bedrock. That one is not counted here but DELIVERED, by the credential gate's region fill
// (regionfill.go), in the first variable the agent reads, so each notch's lookup finds it like
// any delivered variable, and at `yolo host` and in a jail alike. When the file gives none, the
// ask carries what the fill read (RegionAsk.File) and the refusal names the file, the profile
// and why.
//
// PER AGENT, because the credential gate made delivery per agent (OQ-CN6): a region one agent
// receives through its own gated env or its own shape vars is not a region another agent on
// the same provider receives, and "delivered to some process of the launch" let exactly that
// through (the review's two-agent case: codex's own AWS_REGION satisfied claude's bedrock). So
// each notch hands one RegionAsk per agent whose profile selects a provider, with a lookup
// answering for that agent alone, and the refusal names the agents that receive no region.
//
// AND IN THE VARIABLES THAT AGENT READS. A program may read fewer of the platform's variables
// than its providers list, and its pack says so under `platform_regions` (agentRegionVars):
// opencode 1.18.32 reads AWS_REGION and never AWS_DEFAULT_REGION, falling back to us-east-1, so
// counting AWS_DEFAULT_REGION for it let exactly the launch this pre-flight exists to stop go
// through. Such an agent is asked about its own list, and a platform variable that reached it
// unread is named in the refusal (docs/design/bedrock-plumbing.md BR-D18).
//
// SCOPED LIKE THE CREDENTIAL PRE-FLIGHT: a provider no agent's profile selects is no
// requirement, and an entry the composed table does not hold (the user's `null`) is nobody's.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// regionRequirement is one PLATFORM's region requirement: the pack whose provider declared the
// platform's region variables, those variables, and where the platform's agents keep a region
// the environment does not carry (nil when no provider declares a `region_file`).
type regionRequirement struct {
	pack string
	vars []string
	file *packdecl.RegionFile
}

// regionRequirements maps each platform a selected pack says is reached through a region to its
// requirement. Every provider declaration carrying `region_env_name` (which packdecl refuses
// without a `platform` beside it) adds its variables to its platform's list, in declaration
// order and without repeats, and the requirement is attributed to the last pack that declared
// one; its `region_file` (which packdecl refuses without `region_env_name`), likewise the last
// declared. A platform no pack declares variables for has no entry, and neither has a provider
// with no platform at all.
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
			if prov.RegionFile != nil {
				req.file = prov.RegionFile
			}
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
	// File is what the region fill read for this agent (AgentDelivery.RegionFile), nil when it
	// read nothing: the refusal names the file and profile it consulted and why they gave no
	// region, and offers the file as a third way to set one.
	File *RegionFileLookup
	// ThroughVia says the agent reaches Provider through a via service its profile names and this
	// notch serves (ViaURLFor), so the agent's own client never reads the region: the service
	// reads it from what reaches the agent (the wire bridge reads AWS_REGION then
	// AWS_DEFAULT_REGION, docs/design/wire-bridge-gateway.md WG-I38). Such an agent is asked about
	// every variable the platform lists, not the fewer its program's pack says the program reads
	// (agentRegionVars): opencode on `bedrock-bridge` given AWS_DEFAULT_REGION alone has a region
	// the bridge uses.
	ThroughVia bool
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
	// missing is, per provider, the gaps on it: the agents that receive no region, grouped by
	// the variables they read, since a program may read fewer than its platform lists.
	missing := map[string][]*regionGap{}
	for _, ask := range asks {
		entry := providerEntry(providers, ask.Provider)
		if entry == nil {
			continue // not in the table, or the user's null dropped it: nobody's requirement
		}
		// THE PLATFORM, off the composed entry: pack default under user override, so a user
		// provider that says it is "aws-bedrock" meets the requirement the shipped one does.
		platform := entryString(entry, "platform")
		req, ok := reqs[platform]
		if !ok || entryString(entry, "region") != "" {
			continue
		}
		lookup := ask.Lookup
		if lookup == nil {
			lookup = func(string) (string, bool) { return "", false }
		}
		vars, narrowedBy := agentRegionVars(packs, ask.Agent, platform, req.vars)
		if ask.ThroughVia {
			vars, narrowedBy = req.vars, ""
		}
		if anySet(vars, lookup) {
			continue
		}
		gap := gapFor(missing, ask.Provider, vars)
		if slices.Contains(gap.agents, ask.Agent) {
			continue
		}
		gap.agents = append(gap.agents, ask.Agent)
		// A variable the platform lists and this agent does not read, delivered to it anyway:
		// the one form of this mistake the user can see, since the value is in their config.
		for _, v := range req.vars {
			if slices.Contains(vars, v) {
				continue
			}
			if val, ok := lookup(v); ok && val != "" {
				gap.unread = append(gap.unread, "    "+v+" reaches "+ask.Agent+", which does not read it: "+
					"pack "+narrowedBy+" says "+ask.Agent+" reads its region on "+quoted(platform)+
					" from "+orList(vars)+" alone")
			}
		}
		// THE REGION FILE the fill consulted for this agent (BR-DIR1), and why it gave none:
		// one line per distinct file and profile, since agents on one provider share both.
		if ask.File != nil {
			if fact := ask.File.refusalFact(); !slices.Contains(gap.files, fact) {
				gap.files = append(gap.files, fact)
				if r := ask.File.remedy(); r != "" {
					gap.remedies = append(gap.remedies, r)
				}
				if ask.File.stranded == "" {
					gap.consulted = append(gap.consulted, ask.File.fileLabel()+" ["+ask.File.Section+"]")
				}
			}
		}
	}
	var facts []string
	var files []string
	for _, name := range providers.Keys() {
		platform := entryString(providerEntry(providers, name), "platform")
		req := reqs[platform]
		for _, gap := range missing[name] {
			agents := slices.Clone(gap.agents)
			sort.Strings(agents)
			facts = append(facts, "  • pack "+req.pack+" requires a region for provider "+quoted(name)+
				" (platform "+quoted(platform)+"), selected for "+andList(agents)+": its composed "+
				"entry sets no \"region\", and "+noneSetPhrase(gap.vars)+" in what this launch "+
				"delivers to "+andList(agents))
			facts = append(facts, gap.unread...)
			for _, v := range gap.vars {
				if stranded != nil && stranded(v) {
					facts = append(facts, "    "+v+" is set in the environment yolo was launched from, "+
						"which this launch does not deliver to the agent")
				}
			}
			facts = append(facts, gap.files...)
			remedy := fmt.Sprintf("    set one: \"providers\": {%q: {\"region\": \"<region>\"}} "+
				"in your yolo config, or %s=<region> in an env_sources entry", name, gap.vars[0])
			for _, r := range gap.remedies {
				remedy += ", or " + r
			}
			facts = append(facts, remedy)
			for _, c := range gap.consulted {
				if !slices.Contains(files, c) {
					files = append(files, c)
				}
			}
		}
	}
	if len(facts) == 0 {
		return nil
	}
	where := "each selected provider's composed entry"
	if len(consulted) > 0 {
		where += ", then " + strings.Join(consulted, ", ")
	}
	if len(files) > 0 {
		where += ", then " + strings.Join(files, ", ")
	}
	return append(facts, "  consulted for a region: "+where)
}

// regionGap is one refusal fact of the region pre-flight: the agents on one provider that
// receive none of the variables they read, those variables, the lines naming a variable the
// platform lists that reached one of them unread, and the region files the fill read for them
// (the fact, the remedy, and the consulted entry of each).
type regionGap struct {
	vars      []string
	agents    []string
	unread    []string
	files     []string
	remedies  []string
	consulted []string
}

// gapFor returns provider's gap for agents reading vars, adding one in first-seen order.
func gapFor(missing map[string][]*regionGap, provider string, vars []string) *regionGap {
	for _, g := range missing[provider] {
		if slices.Equal(g.vars, vars) {
			return g
		}
	}
	g := &regionGap{vars: vars}
	missing[provider] = append(missing[provider], g)
	return g
}

// agentRegionVars is the region variables agent reads on platform: the list the program's own
// pack declares under `platform_regions` for it (packdecl.Contribution.PlatformRegions), and the
// pack that declared it, or else the platform's own list and "". Found by BIN OWNERSHIP, the rule
// AgentEnv finds an agent's env producer by: the selected pack that installs the agent's CLI is
// the one that can say which variables that CLI reads. opencode's is the shipped case: its
// Bedrock loader reads AWS_REGION and never AWS_DEFAULT_REGION, falling back to us-east-1, so
// counting the latter for it let a launch through to a region nobody chose (BR-D18).
func agentRegionVars(packs []*Pack, agent, platform string, platformVars []string) ([]string, string) {
	owner := binOwner(packs, agent)
	if owner == nil || owner.Decl == nil {
		return platformVars, ""
	}
	if own := owner.Decl.RegionEnvNamesFor(agent, platform); len(own) > 0 {
		return own, owner.Name
	}
	return platformVars, ""
}

// orList joins names as alternatives in English: "a", "a or b", "a, b or c".
func orList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	}
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
// overrules; the one launch a user may need it for is an agent that reads a region from a place
// neither the refusal nor the fill counts, which is a fact about the user's setup rather than a
// yolo fault. A region in the platform's region file IS counted since BR-DIR1: the fill delivers
// it (regionfill.go), and the facts name the file and profile it read when it gave none.
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
