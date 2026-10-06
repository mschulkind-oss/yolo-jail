package packload

// profiledisclosure.go is the launch's profile disclosure, worded once for every notch
// (docs/reference/providers.md#what-the-launch-checks-and-prints; docs/plans/notch-convergence.md
// item 13, row A7): one line per DISTINCT profile name the launch selects, naming the packs that
// DECLARE it and, for each agent the profile table keys to that name, the provider its selection
// resolved to and HOW that agent reaches it at this notch; then one warning line per agent the
// selection reaches nothing for, or delivers no credential to, each naming why and the fix.
//
// WHY PER AGENT. The line used to name every selected pack as having RECEIVED the name, which
// was true (the table reaches every pack's derive whole) and told nobody anything: measured on
// 2026-09-29, `yolo host -- pi` on `use_profiles: {pi: bedrock}` (the key since renamed
// `profile`) printed "declared: claude; received: <fifteen packs>" and pi started with no model
// and no API key, since no selected pack had a Bedrock binding for pi and nothing reached it a
// credential. What the launch CAN answer is
// what it composed: the provider, the pairing protocol resolution settled, the platform binding
// an agent's pack declares, and the credential variables that reach the agent. It still never
// says a derive HONORED the name (providers.md#pv-oq-10): a binding is a declaration, and what a
// derive does with it is not observable from here.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ProfileDisclosureInput is what the disclosure reads, all of it the launch's own composition:
// its CLI-keyed profile table (ProfileTable), its selected packs, and the resolved profiles and
// composed provider table the credential gate composed it against.
type ProfileDisclosureInput struct {
	Table map[string]string
	// Sets is each agent's whole ACTIVE SET (ProfileSets; docs/design/active-provider-sets.md),
	// Table's values being their first entries. When given, every name ANY set lists gets its
	// line and each agent holding it is answered for it, so a later entry says where it landed
	// as the primary does (the design's §6: "the launch disclosure … print each agent's set in
	// order"). nil reads Table alone.
	Sets      map[string][]string
	Packs     []*Pack
	Resolved  map[string]ResolvedProfile
	Providers *jsonx.OrderedMap
	// Reaches answers whether the variable name reaches agent at this notch, non-empty: at the
	// host the environment the exec hands it (the invoking shell included), in a jail what
	// crosses for that agent. nil asks no credential question.
	Reaches func(agent, name string) bool
	// Scope is the credential gate's answer, for the pointers this notch withheld (WithheldBy);
	// nil for none.
	Scope *CredentialScope
	// Deselect is, for agent, where its selection of profile came from and the exact spelling
	// that selects no profile for it there, as one clause — for the warning that the selection
	// reaches nothing for agent, which otherwise names only a flag and repeats every launch.
	// The notch knows its selection's sources (the config key, a -p) and this package does
	// not: a jail launch passes config.ProfileDeselection, and `yolo host`
	// config.HostProfileDeselection. nil names the -p form alone.
	Deselect func(agent, profile string) string
}

// ProfileReach is one agent's answer: the provider its selection resolved to, how the agent
// reaches it here (Route, "" when it reaches nothing), and the warnings, each a whole line.
type ProfileReach struct {
	Agent, Provider, Route string
	Warnings               []string
}

// ProfileDisclosure is one selected profile name: the selected packs that declare it, whether
// only the user's config does, and each agent the table keys to it.
type ProfileDisclosure struct {
	Name     string
	Declared []string
	// User is set when no selected pack declares the name, so the user's `profiles` does (an
	// undeclared name refuses the launch before this line).
	User   bool
	Agents []ProfileReach
}

// Head is the line's label, "Profile <name>:".
func (d ProfileDisclosure) Head() string { return "Profile " + d.Name + ":" }

// Detail is the rest of the line: who declares the name, then each agent's provider and route.
func (d ProfileDisclosure) Detail() string {
	who := "declared by " + strings.Join(d.Declared, ", ")
	if d.User || len(d.Declared) == 0 {
		who = "declared by your config's `profiles`"
	}
	parts := []string{who}
	for _, a := range d.Agents {
		switch {
		case a.Provider == "":
			parts = append(parts, a.Agent+" → "+a.Route)
		case a.Route == "":
			parts = append(parts, fmt.Sprintf("%s → provider %q, which it cannot use here (below)",
				a.Agent, a.Provider))
		default:
			parts = append(parts, fmt.Sprintf("%s → provider %q, %s", a.Agent, a.Provider, a.Route))
		}
	}
	return strings.Join(parts, "; ")
}

// Line is the whole line, unstyled.
func (d ProfileDisclosure) Line() string { return d.Head() + " " + d.Detail() }

// Warnings is every agent's warning lines, in agent order.
func (d ProfileDisclosure) Warnings() []string {
	var out []string
	for _, a := range d.Agents {
		out = append(out, a.Warnings...)
	}
	return out
}

// ProfileDisclosures is the disclosure for a launch, one entry per distinct selected name,
// sorted by name, each agent under it sorted. Nil when nothing is selected: a launch with no
// profile is the common case, and restating its absence would be noise.
//
// Two packs declaring one name are both listed. The name is owned only within a pack, so they
// are unrelated declarations of one selector value, and saying so beats hiding the coincidence.
func ProfileDisclosures(in ProfileDisclosureInput) []ProfileDisclosure {
	byName := map[string][]string{}
	if in.Sets != nil {
		for agent, set := range in.Sets {
			for _, name := range set {
				if name != "" && !slices.Contains(byName[name], agent) {
					byName[name] = append(byName[name], agent)
				}
			}
		}
	} else {
		for agent, name := range in.Table {
			if name != "" {
				byName[name] = append(byName[name], agent)
			}
		}
	}
	if len(byName) == 0 {
		return nil
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ProfileDisclosure, 0, len(names))
	for _, name := range names {
		d := ProfileDisclosure{Name: name}
		for _, p := range in.Packs {
			if p != nil && p.Decl != nil && p.Decl.ProfileFor(name) != nil {
				d.Declared = append(d.Declared, p.Name)
			}
		}
		sort.Strings(d.Declared)
		d.User = len(d.Declared) == 0
		agents := byName[name]
		sort.Strings(agents)
		for _, agent := range agents {
			d.Agents = append(d.Agents, profileReach(in, agent, name))
		}
		out = append(out, d)
	}
	return out
}

// profileReach answers, for agent on profile, whether the selection resolved to a provider that
// agent can use at this notch, from the launch's declarations alone:
//
//   - a provider with endpoints is reached by the pairing protocol resolution settles (the
//     agent's `protocols` against the endpoints the composed table holds, adapted ones
//     included), and a pairing that does not resolve refused the launch before this line;
//   - a provider with no endpoint and a `platform` is reached only by a client of the agent's
//     own for that platform, which its pack declares by binding the platform (bindsPlatform);
//     without one the selection reaches nothing for the agent, which is the one warning here;
//   - a provider with neither re-points nothing: the agent stays on its own client;
//   - a via that serves the agent here routes it through the via's service.
//
// And the credential half: a provider no endpoint names is no credential pre-flight's
// requirement (the ambient chain may serve it), so when none of the credential variables it
// claims reaches the agent at this notch, the line says so and names the fix, where the
// pre-flight would stay silent and the agent would start without one.
func profileReach(in ProfileDisclosureInput, agent, profile string) ProfileReach {
	r := ProfileReach{Agent: agent}
	owner := binOwner(in.Packs, agent)
	if owner == nil {
		r.Route = "not in this launch: no selected pack installs " + agent
		return r
	}
	r.Provider = ProviderFor(in.Resolved, profile)
	quoted := strconv.Quote(profile)
	if r.Provider == "" {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Warning: profile %s resolves to no provider "+
			"for %s, so it reaches nothing for %s: give the profile a `provider`", quoted, agent, agent))
		return r
	}
	// The provider as this profile sees it for this agent: an address composed for a via profile
	// (ForViaKey) is none for an agent the profile routes through no such via (ViaFor: the
	// profile's own via, or the carrier of an agent with no client of the platform).
	via, _ := in.Resolved[profile].ViaFor(agent)
	entry := EndpointsForProfile(providerEntry(in.Providers, r.Provider), via)
	if entry == nil {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Warning: profile %s selects provider %q for "+
			"%s, and this launch's provider table does not hold it, so it reaches nothing for %s: "+
			"select a pack that ships it, or declare it under `providers`", quoted, r.Provider,
			agent, agent))
		return r
	}
	// A PROVIDER THE AGENT HAS BUILT IN (BuiltInProviderFor; docs/design/pi-codex-provider-
	// shadowing.md OQ-3): its pack's derive writes it no model entry, so the agent reaches it
	// through its own client and list, whatever the provider's endpoints or the profile's via
	// say. Where the agent has the name built in for another plan and no provider of its own for
	// this one, nothing reaches it, and the line says so with the next step. A built-in provider
	// whose list the agent's pack renders from yolo's declaration (YoloList: pi and opencode on
	// openai-codex, ML-D1) runs yolo's list on the endpoint that list rides on, so it takes the
	// protocol resolution below like any catalogued provider.
	if own, builtIn := BuiltInProviderFor(in.Packs, agent, r.Provider); builtIn && !own.YoloList {
		if own.ID == "" {
			r.Warnings = append(r.Warnings, fmt.Sprintf("Warning: profile %s reaches nothing for %s: "+
				"%s has a provider of its own named %q for another plan and none for this "+
				"profile's, and yolo writes no model entry over a provider an agent has built in, "+
				"so nothing this profile configures reaches %s's own client. Select a profile whose "+
				"provider %s reaches (`-p %s=<name>`), or none for %s", quoted, agent, agent,
				r.Provider, agent, agent, agent, agent))
			return r
		}
		r.Route = fmt.Sprintf("through its own %q client, with its own model list", own.ID)
		if via != "" {
			r.Route += fmt.Sprintf(", which pack %q's route does not re-point", via)
		}
		if w := literalKeyWarning(in.Packs, agent, quoted, r.Provider, own, entry); w != "" {
			r.Warnings = append(r.Warnings, w)
		}
		return r
	}
	platform := entryString(entry, "platform")
	switch res, err := ResolveProtocol(agent, owner.Decl.SpokenProtocols(agent), r.Provider, entry, nil); {
	case ViaURLFor(in.Resolved[profile], agent) != "" && in.Resolved[profile].Via == "":
		// THE CARRIER (carrier.go): the profile names no via, and the agent has no client of the
		// provider's platform, so the service that fronts the platform carries it.
		r.Route = fmt.Sprintf("through pack %q, which carries an agent with no %q client of its own",
			via, platform)
	case ViaURLFor(in.Resolved[profile], agent) != "":
		r.Route = fmt.Sprintf("through pack %q's via route", via)
	case err != nil:
		first, _, _ := strings.Cut(err.Error(), "\n")
		r.Warnings = append(r.Warnings, fmt.Sprintf("Warning: profile %s reaches nothing for %s: %s",
			quoted, agent, first))
	case res.Protocol != "":
		r.Route = fmt.Sprintf("on its %q endpoint", res.Protocol)
	case hasEndpoint(entry):
		r.Route = "on the provider's endpoints (its pack declares no protocols to pair on)"
	case platform != "" && bindsPlatform(in.Packs, owner, agent, platform):
		r.Route = fmt.Sprintf("through %s's own %q client", agent, platform)
	case platform != "":
		none := "none for " + agent
		if in.Deselect != nil {
			none += ": " + in.Deselect(agent, profile)
		}
		r.Warnings = append(r.Warnings, fmt.Sprintf("Warning: profile %s reaches nothing for %s: "+
			"provider %q names no endpoint, only platform %q, and no selected pack gives %s a "+
			"client of that platform (a pack binds a platform by shipping a provider of it, "+
			"needing a pack that does, or declaring its program's switch or region for it), so "+
			"nothing this profile configures reaches %s's own client. Select a profile whose "+
			"provider %s reaches (`-p %s=<name>`), or %s", quoted, agent, r.Provider,
			platform, agent, agent, agent, agent, none))
	default:
		r.Route = "on its own client, which the provider re-points nowhere (it names no endpoint)"
	}
	// FOR A CARRIED AGENT THE PROVIDER'S OWN ENDPOINTS decide the credential half, not the address
	// the carrier composed for it: its requests go through the carrier (carrier.go), which sends
	// them on with the credential that reaches the agent, so a provider naming no endpoint of its
	// own still needs one delivered to it. An agent under a profile's own via keeps the rule it
	// had, which reads the address composed for that via as an endpoint.
	carrier := ""
	if in.Resolved[profile].Via == "" {
		carrier = via
	}
	ownless := !hasEndpoint(entry) || carrier != "" && !hasEndpoint(EndpointsForProfile(entry, ""))
	if r.Route != "" && ownless && in.Reaches != nil {
		if w := credentialWarning(in, agent, profile, r.Provider, carrier); w != "" {
			r.Warnings = append(r.Warnings, w)
		}
	}
	return r
}

// literalKeyWarning is the warning for an agent on a provider it has built in whose key is only a
// literal `api_key` or `options.api_key` in the provider's entry, "" otherwise. Such a key reached
// the agent only inside the catalog row its derive no longer writes over a built-in provider
// (OQ-3), and no variable carries it (BuiltInKeyVars relays a variable's value, never a literal:
// docs/reference/providers.md holds a literal key in the table to be the drift), so the agent's
// own client starts with no key. The next step is the supported spelling: the key in a variable,
// named under `api_key_env_name`.
func literalKeyWarning(packs []*Pack, agent, quoted, provider string, own luahook.BuiltInProvider,
	entry *jsonx.OrderedMap) string {
	if len(CredentialEnvNames(entry)) > 0 {
		return ""
	}
	spelling := ""
	switch {
	case entryString(entry, "api_key") != "":
		spelling = "api_key"
	case entryString(childMap(entry, "options"), "api_key") != "":
		spelling = "options.api_key"
	default:
		return ""
	}
	reads := "the variable its own provider reads"
	if v := builtInKeyVar(packs, own, provider); v != "" {
		reads = v
	}
	return fmt.Sprintf("Warning: profile %s gives %s no key: provider %q's key is a literal "+
		"`%s`, which reached %s only through the model entry yolo no longer writes over a "+
		"provider an agent has built in, so it reaches no client and %s's own %q client starts "+
		"with none. Put the key in a variable and name it under `providers.%s.api_key_env_name` "+
		"(%s reads %s)", quoted, agent, provider, spelling, agent, agent, own.ID, provider, agent, reads)
}

// AgentBindsPlatform reports whether agent has a client of platform in this launch: some
// selected pack installs agent, and that pack binds the platform for it (bindsPlatform). It is
// the test the profile line's "reaches nothing" warning applies, exported for the host launch,
// which opens a platform-gated doorway only for an agent that has a client for it
// (docs/design/host-notch-services.md HS-D23).
func AgentBindsPlatform(packs []*Pack, agent, platform string) bool {
	owner := binOwner(packs, agent)
	return owner != nil && owner.Decl != nil && bindsPlatform(packs, owner, agent, platform)
}

// bindsPlatform reports whether owner, the pack installing agent, binds platform for it: it
// ships a provider of that platform, needs a selected pack that does (every agent pack whose
// derive binds Bedrock needs packs/bedrock, providers.md#the-shipped-bedrock-provider, which
// awsauthneed_test.go pins against the derives), or declares its program's own switch or region
// variables for the platform.
func bindsPlatform(packs []*Pack, owner *Pack, agent, platform string) bool {
	ships := func(p *Pack) bool {
		for _, prov := range p.Decl.Providers() {
			if prov.Platform == platform {
				return true
			}
		}
		return false
	}
	if ships(owner) || owner.Decl.RegionEnvNamesFor(agent, platform) != nil {
		return true
	}
	for _, s := range owner.Decl.PlatformSwitches(agent) {
		if s.Platform == platform {
			return true
		}
	}
	for _, need := range owner.Decl.DeclaredNeeds() {
		for _, p := range packs {
			if p != nil && p.Decl != nil && p.Name == need.Pack && ships(p) {
				return true
			}
		}
	}
	return false
}

// credentialWarning is the warning for an agent none of whose provider's claimed credential
// variables reaches it at this notch, "" when one does or the provider claims none.
//
// carrier is the pack that carries agent's requests on this profile (carrier.go), "" when the
// agent's own client sends them: a carrier sends them on with the credential that reaches the
// agent, so with none it has nothing to send them with.
func credentialWarning(in ProfileDisclosureInput, agent, profile, provider, carrier string) string {
	var claims []string
	for name, providers := range credentialClaims(in.Providers) {
		for _, p := range providers {
			if p == provider {
				claims = append(claims, name)
			}
		}
	}
	if len(claims) == 0 {
		return ""
	}
	sort.Strings(claims)
	for _, name := range claims {
		if in.Reaches(agent, name) {
			return ""
		}
	}
	fix := "Deliver one through `env_sources`, which hands it only to agents on this provider"
	for _, name := range claims {
		if daemon := in.Scope.WithheldBy(name); daemon != "" {
			fix = fmt.Sprintf("The %q jail daemon's pointer would carry %s, and this notch does "+
				"not set it (the \"Not set at this notch\" line says why); or deliver one through "+
				"`env_sources`", daemon, name)
			break
		}
	}
	consequence := fmt.Sprintf("so %s starts on a credential it finds itself or on none", agent)
	if carrier != "" {
		consequence = fmt.Sprintf("so pack %q, which sends %s's requests on with the credential "+
			"that reaches %s, has none to send them with", carrier, agent, agent)
	}
	return fmt.Sprintf("Warning: profile %q delivers %s no credential for provider %q at this "+
		"notch: none of %s reaches it, %s. %s",
		profile, agent, provider, strings.Join(claims, ", "), consequence, fix)
}

// ActiveSetLines is one line per agent holding an ACTIVE SET of more than one profile
// (docs/design/active-provider-sets.md), agents in name order, naming the set in its order and
// which entry a fresh session starts on — the reading the risk table asks the launch to show
// before anything runs, since `pi=zai,codex` names a CLI and a profile in one value. Nil when
// every set has one entry, so a launch with none prints what it always printed.
//
// "UNLESS THE AGENT KEEPS A PICK OF ITS OWN" is the one wording true of every set-capable agent,
// and core names none of them: pi keeps a valid pick across sessions, so the first entry decides
// only when yolo has to pick, while opencode keeps none past its session whenever yolo writes the
// first entry's `model`, which its start order tries before its recent picks (AP-D15).
func ActiveSetLines(sets map[string][]string) []string {
	agents := make([]string, 0, len(sets))
	for agent, set := range sets {
		if len(set) > 1 {
			agents = append(agents, agent)
		}
	}
	sort.Strings(agents)
	out := make([]string, 0, len(agents))
	for _, agent := range agents {
		set := sets[agent]
		out = append(out, "Active set for "+agent+": "+strings.Join(set, ", ")+
			" — every entry's provider is live in one session, and a fresh session starts on "+
			set[0]+" unless the agent keeps a pick of its own inside the set")
	}
	return out
}
