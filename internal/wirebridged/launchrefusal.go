package wirebridged

// launchrefusal.go is the via half of the serve decision asked by the LAUNCHER, before a
// container exists (docs/design/wire-bridge-gateway.md WG-I13, WG-I14, WG-I15). An agent
// whose active profile routes it through this service is handed <via_address>/agent/<agent>
// whether or not this daemon serves what it sends there, so a via the daemon cannot serve
// used to start a jail whose agent failed at its first request, recorded only in the
// daemon's log. The launch now says so first, naming the profile, the agent and the reason.
//
// It first asks whether the agent's config POINTS it at that URL at all
// (packload.DerivedViaPointers, WG-I15): a derive decides which provider rows ride
// ctx.via_url, and several ride none for some providers (pi, oh-omp and codex keep
// `openai-codex` on their own subscription client; codex writes no row for a provider it
// cannot reach). A via that re-points nothing cannot fail a request, so it is DISCLOSED as
// having no effect and never refused. For an agent that is re-pointed, three findings of
// two severities:
//
//   - NO ROUTE AT ALL (a refusal). The daemon's own plan serves nothing under the agent's
//     prefix: its provider declares neither wire the via route passes through, or is one
//     a via route never carries. Every request the agent sends there fails, so the launch
//     refuses.
//   - A ROUTE WITHOUT THE AGENT'S WIRE (a notice). The route serves, but only the other
//     wire (WG-I20: one route, chat-completions and Responses, each request sent to the
//     upstream its path names). The wire is read off the agent's declared preference
//     (preferredViaWire); the wire its derive actually writes is spelled in the agent's own
//     config vocabulary, which core does not read, so the launch DISCLOSES a mismatch
//     rather than refusing on a declaration that is a preference (WG-I14). For every
//     shipped via agent the two agree (TestShippedViaRowsSpeakTheWireTheAgentPrefers), so
//     there the mismatch fails every request: the notice is a notice because the launcher's
//     evidence is an inference, not because the outcome is in doubt.
//   - NO EFFECT (a notice), above.
//
// It asks viaRoutesFor, the pure core the daemon's boot reads, and nothing else: WillServe's
// rule (one decision, two call sites), with the per-agent answer a message has to name.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// ViaRouteGate returns what the launch must say about every via agent that a pack in packs
// installs and that the daemon, booted from these same three tables, will not fully serve:
// one refusal per re-pointed agent with no route at all, one notice per re-pointed agent
// whose route lacks the wire it prefers, and one notice per agent whose config the via
// does not re-point. The inputs are what the launch relays as YOLO_PROVIDERS,
// YOLO_USE_PROFILES and YOLO_PROFILES.
//
// Skipped, and so never refused or disclosed:
//
//   - an agent no pack installs (packload.ActiveVias' reason, WG-I10: a user-scope
//     `profile` entry may name an agent this launch does not carry);
//   - an agent that is not a via agent (preferredViaWire: claude and copilot prefer
//     `anthropic` and reach the bridge through its adapter routes, and an agent that
//     declares no protocols, such as agy, names no wire the via route carries). It is never
//     refused, and it is DISCLOSED (unroutedViaNotice) when it declares a protocol and its
//     config does not point it at its via URL, since then the via carries none of its
//     requests while its profile says it does;
//   - an agent whose via URL is empty (packload.ViaURLFor): no via, or its service is not
//     in this launch, which the host notch's inert table always gives. An agent that is not
//     re-pointed cannot be refused at a prefix.
//
// One refusal reaches an agent that is not a via agent: adapterTakenRefusal's, for an agent the
// bridge would serve on the adapter route alone while this launch gives that one route to
// another agent's provider.
func ViaRouteGate(packs []*packload.Pack, providers *jsonx.OrderedMap,
	useProfiles map[string]string, resolved map[string]packload.ResolvedProfile) (refusals []error, notices []string) {
	plan := viaRoutesFor(providers, useProfiles, resolved)
	served := map[string]viaRoute{}
	for _, r := range plan.Routes {
		served[r.Agent] = r
	}
	agents := make([]string, 0, len(useProfiles))
	for agent := range useProfiles {
		agents = append(agents, agent)
	}
	sort.Strings(agents)

	for _, agent := range agents {
		wire, protocol, viaAgent := preferredViaWire(packs, agent)
		if !viaAgent {
			if err := adapterTakenRefusal(providers, useProfiles, resolved, agent); err != nil {
				refusals = append(refusals, err)
				continue
			}
			if n := unroutedViaNotice(packs, providers, useProfiles, resolved, agent, protocol); n != "" {
				notices = append(notices, n)
			}
			continue
		}
		profile := useProfiles[agent]
		r := resolved[profile]
		url := packload.ViaURLFor(r, agent)
		if url == "" {
			continue
		}
		alone := viaRoutesFor(providers, map[string]string{agent: profile}, resolved)
		if len(alone.Routes) == 0 && len(alone.Skipped) == 0 {
			continue // not a via route of this service at all
		}
		pointers, err := packload.DerivedViaPointers(packs, providers, useProfiles, resolved, agent)
		if err != nil {
			notices = append(notices, fmt.Sprintf("profile %q (active for %s) routes %s through "+
				"%s, but whether %s's config points it at its via URL, %s, could not be checked: "+
				"%v. The jail's boot runs the same derive", profile, agent, agent, ServiceName,
				agent, url, err))
			continue
		}
		if len(pointers) == 0 {
			why := ""
			// A provider the agent has built in gets no via row by the ruling, so say which rule
			// it is and the one way to route it anyway (docs/design/pi-codex-provider-shadowing.md
			// OQ-3: yolo writes no model entry over a provider an agent has built in).
			if own, builtIn := packload.BuiltInProviderFor(packs, agent, r.Provider); builtIn {
				why = fmt.Sprintf(". %q is one of %s's own providers, and yolo writes no model "+
					"entry over a provider an agent has built in, a via row included, so %s runs it "+
					"on its own client", r.Provider, agent, agent)
				if own.ID != "" && own.ID != r.Provider {
					why = fmt.Sprintf(". %q is %s's own %q provider, and yolo writes no model "+
						"entry over a provider an agent has built in, a via row included, so %s runs it "+
						"on its own client", r.Provider, agent, own.ID, agent)
				}
				why += fmt.Sprintf(". To route it through %s, declare the provider under `providers` "+
					"with a name %s has no provider of", ServiceName, agent)
			}
			if len(alone.Skipped) > 0 {
				why += ". The bridge would serve it no route either: " + strings.Join(alone.Skipped, "; ")
			}
			notices = append(notices, fmt.Sprintf("profile %q (active for %s) routes %s through "+
				"%s %s, but %s's config does not point it at its via URL, %s, so the via "+
				"has no effect on %s: its config is what it would be without via%s",
				profile, agent, agent, ServiceName, viaClause(r, agent), agent, url, agent, why))
			continue
		}
		route, ok := served[agent]
		if !ok {
			if len(alone.Skipped) == 0 {
				continue
			}
			fails := fmt.Sprintf("With no via route to serve, the bridge binds no via listener, "+
				"so nothing listens at that URL and every request %s sends there is refused a "+
				"connection", agent)
			if len(plan.Routes) > 0 {
				fails = fmt.Sprintf("That URL is on the bridge's via listener, which answers a "+
					"prefix it serves no route for with a 404, so every request %s sends there "+
					"fails", agent)
			}
			refusals = append(refusals, fmt.Errorf("profile %q (active for %s) routes %s through "+
				"%s %s, and %s's config points it at its via URL, %s, but the bridge will "+
				"serve no route for %s: %s. %s. Select a profile whose provider the via route can "+
				"pass %s's requests to, or %s",
				profile, agent, agent, ServiceName, viaClause(r, agent), agent, url, agent,
				strings.Join(alone.Skipped, "; "), fails, agent, viaWayOut(r, profile, agent)))
			continue
		}
		missing := ""
		switch wire {
		case wireAPIChatCompletions:
			if !route.Chat.served() {
				missing = "chat-completions"
			}
		case wireAPIResponses:
			if !route.Responses.served() {
				missing = "Responses"
			}
		}
		if missing == "" {
			continue
		}
		notices = append(notices, fmt.Sprintf("profile %q (active for %s) routes %s through %s, "+
			"but provider %s declares no %s endpoint, so the bridge will refuse %s's %s requests "+
			"at %s with a 404 (%s prefers %s: its first declared protocol is %q). Select a "+
			"profile whose provider offers %s, or %s",
			profile, agent, agent, ServiceName, route.ProviderName, missing, agent, missing, url,
			agent, missing, protocol, missing, viaWayOut(r, profile, agent)))
	}
	return refusals, notices
}

// adapterTakenRefusal is the refusal for agent when its active profile routes it through this
// service (the profile's own `via`, or its carrier: packload.ResolvedProfile.ViaFor,
// docs/design/wire-bridge-gateway.md WG-I44), the daemon would serve agent the adapter route were
// agent the launch's only candidate, and this launch gives that one route to another agent's
// provider instead. The daemon serves ONE adapter route, the first candidate in agent order
// (routeFor), and agent's derive points its client at its own provider's adapter address, so its
// requests would reach the other agent's provider with that agent's credential, or, where that
// route listens elsewhere, nothing at all. Copilot carried on `-p bedrock` beside claude on
// cerebras is the shape: both providers' anthropic address is the adapter's. nil for an agent
// routed through no service, one the daemon would serve no adapter route even alone (the
// notices say what that means), and one the launch's route serves on its own provider.
func adapterTakenRefusal(providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile, agent string) error {
	profile := useProfiles[agent]
	r := resolved[profile]
	if via, _ := r.ViaFor(agent); via != ServiceName || packload.ViaURLFor(r, agent) == "" {
		return nil
	}
	own, why := routeFor(providers, map[string]string{agent: profile}, resolved)
	if why != "" {
		return nil
	}
	taken, why := routeFor(providers, useProfiles, resolved)
	if why != "" || taken.Agent == agent ||
		(taken.ProviderName == own.ProviderName && taken.ListenAddr == own.ListenAddr) {
		return nil
	}
	fails := fmt.Sprintf("so every request %s sends there would reach provider %s, with the "+
		"credential that reaches %s, and not provider %s", agent, taken.ProviderName, taken.Agent,
		own.ProviderName)
	if taken.ListenAddr != own.ListenAddr {
		fails = fmt.Sprintf("which listens at %s, so nothing listens at %s and every request %s "+
			"sends there is refused a connection", taken.ListenAddr, own.ListenAddr, agent)
	}
	return fmt.Errorf("profile %q (active for %s) routes %s through %s %s, and %s's config points "+
		"it at the bridge's adapter address for provider %s, %s, but the bridge serves one adapter "+
		"route, and this launch gives it to %s on provider %s (profile %q), %s. Select profiles "+
		"over one provider for %s and %s, or another profile for %s (`-p %s=<name>`)",
		profile, agent, agent, ServiceName, viaClause(r, agent), agent, own.ProviderName,
		own.ListenAddr, taken.Agent, taken.ProviderName, useProfiles[taken.Agent], fails,
		agent, taken.Agent, agent, agent)
}

// viaClause is how agent's profile comes to route it through this service, for the gate's lines:
// the profile's own `via`, or, for an agent the profile's carrier carries
// (packload.ResolvedProfile.ViaFor, docs/design/wire-bridge-gateway.md WG-I44), that the agent has
// no client of the provider's platform.
func viaClause(r packload.ResolvedProfile, agent string) string {
	if r.Via != "" {
		return fmt.Sprintf("(via: %q)", r.Via)
	}
	return fmt.Sprintf("(it has no client of provider %q's platform, so %s carries it)", r.Provider, ServiceName)
}

// viaWayOut is the gate's other remedy, after "select a profile whose provider the route serves":
// drop the profile's own `via`, so the agent uses its own client, or, for a carried agent, which
// has no client of the platform to fall back on, select another profile for it.
func viaWayOut(r packload.ResolvedProfile, profile, agent string) string {
	if r.Via != "" {
		return fmt.Sprintf("remove \"via\" from profile %q so %s uses its own client", profile, agent)
	}
	return fmt.Sprintf("select another profile for %s (`-p %s=<name>`), since %s has no client of "+
		"provider %q's platform of its own", agent, agent, agent, r.Provider)
}

// unroutedViaNotice is the notice for an agent that is NOT a via agent, one whose first declared
// protocol is a wire the via route does not carry (claude and copilot prefer `anthropic`), when
// its active profile nonetheless routes it through this service and its config does not point it
// at its via URL. Such a via carries none of the agent's requests, and nothing said so: a profile
// over `bedrock` with "via": "wire-bridge" (the everything profile's shape, OQ-BR11) turns claude's
// own Bedrock client off (PP-D4) while no via route serves claude, so claude started on its own
// login with no word of it. When the installing pack declares a platform switch for the selected
// provider's platform, the notice names it: the agent reaches that platform through its own client
// alone. "" for anything else, which the gate stays silent on as before: an agent that declares no
// protocols (nothing in its pack reads a via), no live via URL, a via that is not this service's,
// a derive that cannot be run (the boot refuses over it), or a config the via does re-point.
func unroutedViaNotice(packs []*packload.Pack, providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]packload.ResolvedProfile, agent, protocol string) string {
	if protocol == "" {
		return ""
	}
	profile := useProfiles[agent]
	r := resolved[profile]
	url := packload.ViaURLFor(r, agent)
	if url == "" {
		return ""
	}
	if alone := viaRoutesFor(providers, map[string]string{agent: profile}, resolved); len(alone.Routes) == 0 &&
		len(alone.Skipped) == 0 {
		return "" // not a via route of this service at all
	}
	// THE ADAPTER ROUTE CARRIES IT: the provider's anthropic endpoint is this service's, and the
	// daemon, booted from these same tables, serves a route there for this agent's profile —
	// claude's everything profile over a Bedrock provider named by region alone (WG-I39), whose
	// anthropic address the adapter composed for a via profile. The via does send the agent's
	// requests through the service, so there is nothing to disclose.
	if _, why := routeFor(providers, map[string]string{agent: profile}, resolved); why == "" {
		return ""
	}
	pointers, err := packload.DerivedViaPointers(packs, providers, useProfiles, resolved, agent)
	if err != nil || len(pointers) > 0 {
		return ""
	}
	msg := fmt.Sprintf("profile %q (active for %s) routes %s through %s %s, but no via route "+
		"carries %s: it speaks %q, a wire the via route does not pass, and its config does not point "+
		"it at its via URL, %s, so the via sends none of %s's requests through %s.", profile, agent,
		agent, ServiceName, viaClause(r, agent), agent, protocol, url, agent, ServiceName)
	// A CARRIED AGENT has no client of the platform (packload.AgentBindsPlatform), so neither a
	// platform switch below nor dropping a `via` the profile does not name is its way out.
	if r.Via == "" {
		return msg + " " + strings.ToUpper(viaWayOut(r, profile, agent)[:1]) + viaWayOut(r, profile, agent)[1:]
	}
	var entry *jsonx.OrderedMap
	if providers != nil {
		if v, ok := providers.Get(r.Provider); ok {
			entry, _ = v.(*jsonx.OrderedMap)
		}
	}
	if platform := entryString(entry, "", "platform"); platform != "" {
		for _, p := range packs {
			if p == nil || p.Decl == nil {
				continue
			}
			for _, sw := range p.Decl.PlatformSwitches(agent) {
				if sw.Platform == platform {
					msg += fmt.Sprintf(" %s reaches a provider of platform %q, such as %q, only through its own "+
						"client, which %s in %s switches on.", agent, platform, r.Provider, sw.Key(), sw.Surface)
					return msg + fmt.Sprintf(" Remove \"via\" from profile %q so %s uses that client", profile, agent)
				}
			}
		}
	}
	return msg + fmt.Sprintf(" Remove \"via\" from profile %q", profile)
}

// preferredViaWire is the wire an agent's client sends through a via route, read off the
// installing pack's declared protocols (packdecl.Manifest.SpokenProtocols), which are in
// preference order: the FIRST one, where the `openai-responses` endpoint key is the
// Responses wire and the `openai` key the chat-completions wire (WG-I14). Every wired
// agent's derive agrees today: pi, oh-omp and opencode prefer `openai` and write a
// chat-completions via row, codex prefers `openai-responses` and writes a Responses one.
//
// viaAgent is false for an agent no pack in packs installs, and for one whose first
// preference is not a protocol the via route carries — the gateway doc's definition of a
// via agent. claude and copilot prefer `anthropic` and reach the bridge through its adapter
// routes, and their derives read no via URL. An agent that declares no protocols (agy)
// names no wire at all, and nothing in its pack reads ctx.via_url either.
func preferredViaWire(packs []*packload.Pack, agent string) (wire, protocol string, viaAgent bool) {
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		installs := false
		for _, bin := range p.InstallBins() {
			if bin == agent {
				installs = true
			}
		}
		if !installs {
			continue
		}
		protocols := p.Decl.SpokenProtocols(agent)
		if len(protocols) == 0 {
			return "", "", false
		}
		switch protocols[0] {
		case "openai":
			return wireAPIChatCompletions, protocols[0], true
		case wireAPIResponses:
			return wireAPIResponses, protocols[0], true
		}
		return "", protocols[0], false
	}
	return "", "", false
}
