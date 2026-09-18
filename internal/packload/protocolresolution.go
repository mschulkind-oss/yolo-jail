package packload

// Protocol resolution: docs/design/protocol-resolution.md

// protocolresolution.go pairs an AGENT with a PROVIDER by the wire protocol each of them
// declares, and produces an address or a refusal (design §3). It is the reader for the
// `protocols` list packdecl added in step 2, and it is what makes that list mean something.
//
// # What it may not know
//
// CORE MAY NOT NAME A PROTOCOL (§4.4). `anthropic` and `openai` appear in this file only
// inside strings QUOTED FROM DECLARATIONS — never in a switch, a constant or a comparison.
// The resolver compares two sets of names it was handed; which names those are is the
// packs' business, and a protocol nothing declares resolves to nothing, which is inert.
//
// The same rule holds one level up: nothing here may name an ADAPTER either. What resolves
// a pairing is "some selected pack declared this conversion", never "the wire bridge is
// selected" — which is the requirement P6 states and the reason the shipped adapter gains
// an ordinary declaration instead of a shortcut.
//
// # Where the refusal lives, and why not beside the capability gate
//
// AgentEnv, which is the DELIVERY of an agent's provider environment and the one runner
// both notches reduce through. The gate has to see a SELECTION — §4.1 refuses a pairing this
// launch actually asked for and says nothing about the providers it merely has on the shelf
// — and the selection is resolved into the profile table AgentEnv already receives. The
// run pre-flight, where the capability gate sits, reads the merged user config only and
// cannot see a pack-shipped provider's endpoints at all (preflight.go's own ⚠ records that
// boundary for its census), so a copy there would refuse a different set of launches.

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// Adaptation is one declared protocol conversion, with the pack that declared it: the
// adapter half of the three declarations (§3).
//
// The PACK is carried and the address is not enough on its own, for one reason: outcome 3's
// refusal has to name the pack to add. That is the only place a pack NAME appears anywhere
// in this resolution — read out of a declaration, never compared against one — which is
// what P6 means in code: `wire-bridge` can be the answer and can never be the question.
type Adaptation struct {
	// Pack is the pack that declared the conversion.
	Pack string
	// From is the protocol its upstream speaks; To is the protocol it serves at Address.
	From, To string
	// Address is where the converted wire is served.
	Address string
}

// Adaptations returns every conversion the given packs declare, in pack order then
// declaration order.
//
// A PAIR DECLARED TWICE IS DROPPED, not merged and not refused here: the pair is
// sole-owned, the claim target carries both halves, and packload.Collisions' generic
// exclusive loop is the cross-pack check that REPORTS it. This keeps the FIRST, so a caller
// that skipped the pre-flight degrades to a stable table rather than to whichever pack
// happened to sort last — exactly the rule ComposeProviders follows for a duplicated
// provider name, and for the same reason.
//
// An entry with an empty half is skipped: the schema refuses it at authoring time, so
// reaching here means a manifest a newer host staged and this build read tolerantly, where
// a half-declared conversion is nothing rather than a fault.
func Adaptations(packs []*Pack) []Adaptation {
	var out []Adaptation
	seen := map[string]bool{}
	for _, p := range packs {
		for _, a := range p.Decl.Adapters() {
			if a.From == "" || a.To == "" || a.Address == "" {
				continue
			}
			key := a.From + " -> " + a.To
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, Adaptation{Pack: p.Name, From: a.From, To: a.To, Address: a.Address})
		}
	}
	return out
}

// ProtocolResolution is the resolver's answer for one (agent, provider) pairing: the
// agent protocol that resolved, and whether the provider offered it itself.
//
// A ZERO VALUE MEANS "NOTHING HAD TO RESOLVE", which is a legal and common answer rather
// than a failure — §4.1's degenerate rows are all of this shape. No provider selected, a
// provider that declares no endpoint at all (the BYO-key launch), an agent that declares no
// protocols (the compatibility shape): in each, nothing was repointed, so there is no
// pairing to settle and the derive composes exactly what it composed before.
type ProtocolResolution struct {
	// Protocol is the agent protocol that resolved, "" when nothing had to.
	Protocol string
	// Direct reports that the provider's own entry carries an address for Protocol.
	Direct bool
}

// ResolveProtocol answers whether agent can be pointed at the provider entry, given the
// protocols the agent's pack declares and the endpoints the provider's composed entry
// carries. It returns the resolution, or an error whose message is the launch refusal.
//
// THE ORDER IS THE AGENT'S PREFERENCE (§4.1): the list is walked in declaration order and
// the first protocol the provider offers wins. A provider offering two is resolved on the
// one the agent would rather have, which is the whole reason the field is a list and not a
// set.
//
// IT IS ASKED OF THE COMPOSED ENTRY, not of the manifests, and that is what keeps one rule
// in one place: whatever composition put in the table — a pack's own endpoints, a user's
// override, and later an adapter's address — is one set of addresses by the time the
// pairing is settled. An adapted pairing is therefore indistinguishable from a native one
// here, which is exactly what "the resolver picks an address that was declared" means in
// practice.
//
// providerName and agent are carried for the MESSAGE only. A refusal that says which two
// declarations disagreed is the whole discoverability argument (R3): the wrong one has to
// be visible in the refusal rather than in a later request.
func ResolveProtocol(agent string, spoken []string, providerName string,
	entry *jsonx.OrderedMap) (ProtocolResolution, error) {
	// An agent that states nothing constrains nothing (§4.1's last row). This is the
	// compatibility shape for a pack that has not been updated, and it must stay first: a
	// pack declaring no protocols has to resolve DIRECT for every provider, including one
	// whose endpoints it could not possibly read.
	if len(spoken) == 0 {
		return ProtocolResolution{}, nil
	}
	offered := providerProtocols(entry)
	// A provider that names no endpoint has REPOINTED NOTHING (§4.1's first two rows,
	// OQ-PR2): it means "use the agent's first-party API", with this key or with whatever
	// credential the agent already holds. There is no pairing to resolve, and refusing it
	// would break the plain BYO-key launch that works today.
	if len(offered) == 0 {
		return ProtocolResolution{}, nil
	}
	for _, p := range spoken {
		if offered[p] {
			return ProtocolResolution{Protocol: p, Direct: true}, nil
		}
	}
	return ProtocolResolution{}, unspeakableProvider(agent, spoken, providerName, offered)
}

// refuseUnspeakableProvider is the gate AgentEnv puts above every derive: the selected
// provider, this agent's declared protocols, and the composed table — resolved, and
// refused when nothing resolves.
//
// It is TOTAL over the ways there is nothing to ask: no provider selected (a launch with no
// profile, or a profile that resolves to none), or a provider name the composed table does
// not hold. Both are already the derives' own "no selection" case, and neither is this
// gate's to report — ProviderFor returning "" is an ordinary launch, not a fault.
//
// owner is the pack that installs agent's CLI, found by bin ownership: the same identity
// AgentEnv discovers a producer through, so the pack that speaks for an agent's environment
// is the pack that speaks for its wires.
func refuseUnspeakableProvider(owner *Pack, agent, selected string, providers *jsonx.OrderedMap) error {
	if selected == "" || providers == nil {
		return nil
	}
	v, ok := providers.Get(selected)
	if !ok {
		return nil
	}
	entry, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	_, err := ResolveProtocol(agent, owner.Decl.SpokenProtocols(agent), selected, entry)
	return err
}

// providerProtocols reports the protocols a composed provider entry offers — the KEY SET of
// its `endpoints` map, which is where an address is filed and therefore what the pairing
// compares.
//
// The `base_url` shorthand is deliberately NOT consulted, the same call
// internal/wirebridged's routeFor makes and for the same reason: a shorthand names no
// protocol, so reading it here would mean guessing which wire it points at — and a URL with
// no protocol is precisely the input the resolver cannot reason about (§5). An entry
// carrying only the shorthand therefore offers nothing, which resolves as the
// nothing-to-settle case above rather than as a refusal.
func providerProtocols(entry *jsonx.OrderedMap) map[string]bool {
	out := map[string]bool{}
	if entry == nil {
		return out
	}
	v, ok := entry.Get("endpoints")
	if !ok {
		return out
	}
	endpoints, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return out
	}
	for _, proto := range endpoints.Keys() {
		if sub, _ := endpoints.Get(proto); sub != nil && proto != "" {
			out[proto] = true
		}
	}
	return out
}

// unspeakableProvider is the refusal for a pairing nothing can serve — §3's outcome 4.
//
// A REFUSAL AND NOT A WARNING, for the capability gate's reason: the declarations' whole
// content is "this configuration does not work", and a launch that says so and proceeds has
// answered a declaration with a note. It stops the launch before any container starts,
// beside the capability gate and the notch gate.
//
// THE MESSAGE NAMES BOTH SIDES AND WHERE EACH WAS DECLARED (R3). A resolver that refuses
// without saying which declaration produced which half turns "an agent pack declares its
// protocols wrongly" into an unfixable launch failure; with both halves quoted, the wrong
// one is visible in the refusal itself.
func unspeakableProvider(agent string, spoken []string, providerName string,
	offered map[string]bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "provider %q speaks %s; agent %q speaks %s — this launch cannot point %s at it.",
		providerName, quotedProtocols(sortedSet(offered)), agent, quotedProtocols(spoken), agent)
	fmt.Fprintf(&b, "\n  The provider's protocols are the keys of its `endpoints`; the agent's are the "+
		"`protocols` list on the pack that installs %s.", agent)
	b.WriteString("\n  Nothing declares an adapter between them, so there is no address to " +
		"give: choose a provider this agent speaks to, or select a pack that adapts one of " +
		"the provider's protocols into one of the agent's.")
	return errors.New(b.String())
}

// sortedSet renders a set's members in a stable order, so a refusal reads the same twice.
func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// quotedProtocols renders protocol names the way the refusals quote them. They are values
// read out of declarations, never vocabulary this package knows, so they are quoted rather
// than spelled.
func quotedProtocols(names []string) string {
	if len(names) == 0 {
		return "nothing"
	}
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = strconv.Quote(n)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}
