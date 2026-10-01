package packload

// carrier.go answers, for a profile that names no via, which service carries the agents that have
// no client of its provider's platform, and which agents those are
// (docs/design/bedrock-plumbing.md OQ-BR1, ruled 2026-09-29: `-p bedrock` puts an agent on Bedrock
// "through its own Bedrock client where it has one, and through the wire bridge where it has
// none"; docs/design/wire-bridge-gateway.md WG-I44).
//
// THE CARRIER, a term coined here: the service pack whose adaptation fronts the provider's
// platform (packdecl.AdapterPair.FromPlatforms), read off the composed entry as the pack its
// endpoint is marked for (ForViaKey). That mark exists exactly when composition gave a provider
// that names no address of its own the service's address (adaptEndpoints), so a carrier exists
// exactly when the service reaches the provider by what it is, as the wire bridge reaches the
// shipped `bedrock` by its region (WG-I39), and only at a notch that serves the service, since an
// unserved adaptation composes no address.
//
// THE CARRIED AGENTS are every agent a selected pack installs that declares a protocol and has no
// client of the provider's platform (AgentBindsPlatform, the test the profile line's warning and
// the host's AWS doorway already apply). An agent declaring no protocol (agy) can be pointed at no
// address, so nothing carries it; an agent with its own client keeps it (claude, codex, opencode
// and pi on `-p bedrock`). A carried agent is routed through the carrier exactly as a profile
// whose `via` names it would route the agent: ctx.via_url, the bridge's routes, the launch's lines.
//
// WHY THE PROFILE TABLE CARRIES IT. The wire bridge decides what to serve from the composed tables
// alone (wirebridged.WillServe, one decision with two call sites), and no table said which agents
// have a client of a platform: that is a fact of the pack manifests, which WG-I39 named as what
// left copilot and oh-omp on `-p bedrock` with nothing. The resolution reads the manifests and
// crosses to the jail as YOLO_PROFILES, so it answers once, here, and every reader asks
// ResolvedProfile.ViaFor.

import (
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// carrierFor is the carrier of a profile over provider entry (its composed entry, nil when the
// table does not hold it), the carrier's via address, and the agents of this launch it carries.
// All three are empty when the provider declares no platform, no endpoint of it is marked for a
// service, that service serves no via address in packs, or every agent has a client of the
// platform.
func carrierFor(packs []*Pack, entry *jsonx.OrderedMap) (carrier, base string, carried []string) {
	if entry == nil {
		return "", "", nil
	}
	platform := entryString(entry, "platform")
	if platform == "" {
		return "", "", nil
	}
	carrier = markedService(entry)
	if carrier == "" {
		return "", "", nil
	}
	if base, _ = ViaServiceAddress(packs, carrier); base == "" {
		return "", "", nil
	}
	seen := map[string]bool{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, bin := range p.InstallBins() {
			if seen[bin] {
				continue
			}
			seen[bin] = true
			owner := binOwner(packs, bin)
			if owner == nil || owner.Decl == nil || len(owner.Decl.SpokenProtocols(bin)) == 0 {
				continue
			}
			if bindsPlatform(packs, owner, bin, platform) {
				continue
			}
			carried = append(carried, bin)
		}
	}
	if len(carried) == 0 {
		return "", "", nil
	}
	sort.Strings(carried)
	return carrier, base, carried
}

// FrontsAPlatform reports whether some pack in packs declares a service-served adaptation that
// fronts a provider platform (packdecl.AdapterPair.FromPlatforms), the only way a profile naming
// no via gets a carrier. It reads the manifests alone, so a caller can skip composing the provider
// table for a launch where nothing could be carried.
func FrontsAPlatform(packs []*Pack) bool {
	for _, a := range Adaptations(packs) {
		if a.Service != "" && len(a.FromPlatforms) > 0 {
			return true
		}
	}
	return false
}

// markedService is the pack the first endpoint of entry marked for a service names (ForViaKey),
// "" when none is marked.
func markedService(entry *jsonx.OrderedMap) string {
	v, _ := entry.Get("endpoints")
	endpoints, _ := v.(*jsonx.OrderedMap)
	if endpoints == nil {
		return ""
	}
	for _, protocol := range endpoints.Keys() {
		if s := forViaService(entry, protocol); s != "" {
			return s
		}
	}
	return ""
}
