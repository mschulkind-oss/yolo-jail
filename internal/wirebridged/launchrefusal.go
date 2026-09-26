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
//     `use_profiles` entry may name an agent this launch does not carry);
//   - an agent that is not a via agent (preferredViaWire: claude and copilot prefer
//     `anthropic` and reach the bridge through its adapter routes, and an agent that
//     declares no protocols, such as agy, names no wire the via route carries);
//   - an agent whose via URL is empty (packload.ViaURLFor): no via, or its service is not
//     in this launch, which the host notch's inert table always gives. An agent that is not
//     re-pointed cannot be refused at a prefix.
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
			if len(alone.Skipped) > 0 {
				why = ". The bridge would serve it no route either: " + strings.Join(alone.Skipped, "; ")
			}
			notices = append(notices, fmt.Sprintf("profile %q (active for %s) routes %s through "+
				"%s (via: %q), but %s's config does not point it at its via URL, %s, so the via "+
				"has no effect on %s: its config is what it would be without via%s",
				profile, agent, agent, ServiceName, r.Via, agent, url, agent, why))
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
				"%s (via: %q), and %s's config points it at its via URL, %s, but the bridge will "+
				"serve no route for %s: %s. %s. Select a profile whose provider the via route can "+
				"pass %s's requests to, or remove \"via\" from profile %q so %s uses its own client",
				profile, agent, agent, ServiceName, r.Via, agent, url, agent,
				strings.Join(alone.Skipped, "; "), fails, agent, profile, agent))
			continue
		}
		missing := ""
		switch wire {
		case wireAPIChatCompletions:
			if route.Chat.BaseURL == "" {
				missing = "chat-completions"
			}
		case wireAPIResponses:
			if route.Responses.BaseURL == "" {
				missing = "Responses"
			}
		}
		if missing == "" {
			continue
		}
		notices = append(notices, fmt.Sprintf("profile %q (active for %s) routes %s through %s, "+
			"but provider %s declares no %s endpoint, so the bridge will refuse %s's %s requests "+
			"at %s with a 404 (%s prefers %s: its first declared protocol is %q). Select a "+
			"profile whose provider offers %s, or remove \"via\" from profile %q",
			profile, agent, agent, ServiceName, route.ProviderName, missing, agent, missing, url,
			agent, missing, protocol, missing, profile))
	}
	return refusals, notices
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
