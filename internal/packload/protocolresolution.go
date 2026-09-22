package packload

// Protocol resolution: docs/reference/protocol-resolution.md

// protocolresolution.go pairs an AGENT with a PROVIDER by the wire protocol each of them
// declares, and produces an address or a refusal
// (docs/reference/protocol-resolution.md#the-four-outcomes). It is the reader for the
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

// AdapterKey is the SOLE-OWNED IDENTITY of an adaptation, spelled as one string: the pair,
// and nothing about the pack that declared it.
//
// It is the key a user's `adapters` config entry is written under, and the key the
// collision rule groups on — one spelling, because a config that named a conversion
// differently from the way core identifies one would be a second vocabulary for one fact.
func AdapterKey(from, to string) string { return from + "->" + to }

// ComposeOption carries the optional inputs ComposeProviders grew after its call sites were
// written. An OPTION and not a parameter for AgentEnv's reason: most callers have no
// override table to hand over, and demanding it positionally would make every one of them
// pass a nil it does not have.
type ComposeOption func(*composeOpts)

type composeOpts struct {
	adapterAddresses map[string]string
}

// WithAdapterAddresses supplies the user's adapter address overrides, keyed by AdapterKey
// (config.LoadAdapterAddresses reads them). An entry replaces the address the declaring
// pack shipped and changes nothing else: the PAIR is the pack's claim, and a user who
// wanted a different conversion would be declaring an adapter rather than moving one
// (protocol-resolution.md).
func WithAdapterAddresses(m map[string]string) ComposeOption {
	return func(o *composeOpts) { o.adapterAddresses = m }
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
			key := AdapterKey(a.From, a.To)
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
// elsewhere is what an UNSELECTED pack declares (UnselectedAdaptations), and it is read
// only on the failure path: it is the difference between outcome 3, which names the pack to
// add, and outcome 4, which cannot. It never affects whether the pairing resolves — a
// declaration in a pack this launch did not select is not in this launch, and the resolver
// may not select one on the user's behalf (OQ-PR3).
//
// providerName and agent are carried for the MESSAGE only. A refusal that says which two
// declarations disagreed is the whole discoverability argument (R3): the wrong one has to
// be visible in the refusal rather than in a later request.
func ResolveProtocol(agent string, spoken []string, providerName string,
	entry *jsonx.OrderedMap, elsewhere []Adaptation) (ProtocolResolution, error) {
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
	return ProtocolResolution{}, unspeakableProvider(agent, spoken, providerName, offered, elsewhere)
}

// UnselectedAdaptations returns the conversions declared by packs yolo SHIPS and this
// launch did NOT select — outcome 3's whole input, and nothing else's.
//
// IT READS THE EMBEDDED SET, which is deliberately not selection-gated (embedded.go states
// the rule for the reservation lists, and this is the same shape of question: what is true
// of everything yolo ships, regardless of what a particular jail loaded). The tree is
// materialized once per process and already exists before argv is parsed, so this is a walk
// over manifests in memory rather than a filesystem cost on a launch.
//
// WHAT IT BOUNDS, honestly: only a pack yolo ships can be named. A third-party pack the
// user has not selected is invisible here, so its pairing gets outcome 4's message instead
// of outcome 3's. That is a limit on the REMEDY, never on the rule — the pairing resolves
// identically once either pack is selected (P6), and core can only name what it can see.
func UnselectedAdaptations(selected []*Pack) []Adaptation {
	chosen := map[string]bool{}
	for _, p := range selected {
		chosen[p.Name] = true
	}
	var rest []*Pack
	for _, p := range Embedded() {
		if !chosen[p.Name] {
			rest = append(rest, p)
		}
	}
	return Adaptations(rest)
}

// missingAdapterFor picks the conversion an unselected pack declares that WOULD resolve
// this pairing: one whose `from` the provider offers and whose `to` the agent speaks.
//
// THE AGENT'S PREFERENCE ORDER DECIDES, the same order a direct resolution follows, so the
// remedy names the adapter for the wire the agent would rather have been given rather than
// whichever declaration happened to sort first.
func missingAdapterFor(spoken []string, offered map[string]bool, elsewhere []Adaptation) *Adaptation {
	for _, p := range spoken {
		for i := range elsewhere {
			a := elsewhere[i]
			if a.To == p && offered[a.From] {
				return &a
			}
		}
	}
	return nil
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
func refuseUnspeakableProvider(packs []*Pack, owner *Pack, agent, selected string,
	providers *jsonx.OrderedMap) error {
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
	_, err := ResolveProtocol(agent, owner.Decl.SpokenProtocols(agent), selected, entry,
		UnselectedAdaptations(packs))
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

// unspeakableProvider is the refusal for a pairing nothing this launch has can serve —
// §3's outcomes 3 and 4, which differ only in whether a remedy can be named.
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
//
// OUTCOME 3 NAMES THE PACK AND STOPS THERE. The resolver does not join it, does not offer
// to, and does not fall back to it — choosing a provider must not decide what runs in your
// jail (OQ-PR3). One line of remedy is what the ruling bought instead, and it teaches the
// mechanism: the user learns that an adapter exists, which pack has it, and that adding it
// is the whole fix.
func unspeakableProvider(agent string, spoken []string, providerName string,
	offered map[string]bool, elsewhere []Adaptation) error {
	var b strings.Builder
	fmt.Fprintf(&b, "provider %q speaks %s; agent %q speaks %s — this launch cannot point %s at it.",
		providerName, quotedProtocols(sortedSet(offered)), agent, quotedProtocols(spoken), agent)
	fmt.Fprintf(&b, "\n  The provider's protocols are the keys of its `endpoints`; the agent's are the "+
		"`protocols` list on the pack that installs %s.", agent)
	if m := missingAdapterFor(spoken, offered, elsewhere); m != nil {
		fmt.Fprintf(&b, "\n  Pack %q adapts %s → %s. Add it to `packs` and this pairing resolves, "+
			"with nothing else to configure.", m.Pack, strconv.Quote(m.From), strconv.Quote(m.To))
		return errors.New(b.String())
	}
	b.WriteString("\n  Nothing this launch can see declares an adapter between them, so there " +
		"is no address to give: choose a provider this agent speaks to, or select a pack that " +
		"adapts one of the provider's protocols into one of the agent's.")
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

// binOwner returns the selected pack that installs bin, or nil.
//
// EXTRACTED so AgentEnv and PairingRefusals cannot grow two answers to "whose agent is
// this". The gate below predicts the gate AgentEnv applies, and a prediction that found a
// different owner would predict a different launch — which is the failure mode the
// capability gate's own second copy documents and deliberately accepts. Here it is
// avoidable, so it is avoided.
func binOwner(packs []*Pack, bin string) *Pack {
	for _, p := range packs {
		if p.installsBin(bin) {
			return p
		}
	}
	return nil
}

// PairingRefusals reports every protocol-pairing refusal the launch will produce for this
// configuration — one per profiled agent, in table order, skipping the agents that resolve.
//
// # Why this exists, and why it is not a second copy
//
// The gate ships at AgentEnv and only there, so a config `yolo check` called clean was
// still refused at launch — the one thing `check` exists to prevent. That is the exact
// defect capabilities.go was written to fix for the capability gate, one feature later.
//
// ⚠ IT IS THE SAME FUNCTION, NOT A MIRROR OF IT. capabilities.go's copy is a copy because
// reaching the launch's gate would mean exporting a method on run.Options and dragging its
// printer along; that cost is real and its ⚠ records what the copy buys and what it risks.
// Nothing of the kind applies here: the gate is already a free function in this package
// over inputs a caller can assemble, so exporting an entry point is strictly cheaper than
// restating twelve lines of pairing rules somewhere they can drift.
//
// # What the caller still has to get right, and what it therefore cannot promise
//
// The INPUTS are assembled twice — a checker composes providers and resolves profiles the
// way the launch does — and that is the whole residue of duplication. Two consequences the
// caller must state rather than hide:
//
//   - A `-p <name>` at the command line is NOT here. `check` reads configuration; the flag
//     is an argument to a launch that has not happened. So a prediction covers the
//     `use_profiles` selection and nothing else, and a flag can still produce a refusal
//     nobody was warned about. Narrowing that needs the flag, not a wider census.
//   - An agent with no profile is not pairing with anything. AgentEnv returns early on an
//     empty profile and so does this, because the gate it predicts is reached through a
//     SELECTION (§4.1) — a provider merely present in the table repoints nothing.
//
// profiles maps an agent's CLI name to its selected profile name (ProfileTable's shape).
func PairingRefusals(packs []*Pack, providers *jsonx.OrderedMap,
	resolved map[string]ResolvedProfile, profiles map[string]string) []error {
	names := make([]string, 0, len(profiles))
	for agent := range profiles {
		names = append(names, agent)
	}
	sort.Strings(names)

	var out []error
	for _, agent := range names {
		profile := profiles[agent]
		if agent == "" || profile == "" {
			continue
		}
		owner := binOwner(packs, agent)
		if owner == nil {
			continue
		}
		if err := refuseUnspeakableProvider(packs, owner, agent,
			ProviderFor(resolved, profile), providers); err != nil {
			out = append(out, err)
		}
	}
	return out
}
