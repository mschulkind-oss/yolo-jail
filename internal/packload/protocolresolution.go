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
// run pre-flight, where the capability gate sits, runs before the runtime is resolved, so it
// cannot compose the table this notch serves (ComposeProvidersAt's served adapters, which
// decide a pairing); its census resolves profiles for their capabilities alone
// (config/capabilities.go), so a copy there would refuse a different set of launches.

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
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
	// Service is the `service` the declaring pack contributes beside the adapter, "" when it
	// contributes none. It is the one fact that tells an adapter whose own pack runs the
	// daemon serving Address from one naming a remote gateway or a proxy the user runs
	// (protocol-resolution.md#the-three-declarations: "the presence of a sibling `service`
	// contribution is the only thing that tells the three apart"). A notch that runs no pack
	// service cannot serve the first kind (ServiceAdaptations).
	Service string
	// FromPlatforms are the provider platforms whose providers this adaptation fronts though
	// they name no From endpoint (packdecl.AdapterPair.FromPlatforms): Service reaches such a
	// provider itself, and the address it gets is marked ForViaKey (adaptEndpoints).
	FromPlatforms []string
}

// ForViaKey is the key adaptEndpoints writes on an endpoint it composes for a provider that
// names no From address of its own, through an adaptation's FromPlatforms, with the name of the
// adaptation's pack, the value a profile's `via` names (docs/design/wire-bridge-gateway.md
// WG-I39). Its meaning: an agent is sent to this address only when its profile routes it through
// that pack (ResolvedProfile.ViaFor): a profile whose `via` names the pack, or, on one naming no
// via, an agent with no client of the provider's platform, which the pack then carries (the
// profile's carrier, carrier.go, WG-I44), because an agent with its own client for the platform
// uses that client on every other profile. So the address never makes a pairing unspeakable
// (ResolveProtocol), it is no endpoint at all for an agent routed through no such pack
// (EndpointsForProfile), and the service serves it only for an agent routed through it. A
// derive reads it off the endpoint as `for_via` (packs/copilot/derive.lua).
const ForViaKey = "for_via"

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
	// served is the notch's served set (WithServed); servedSet says one was given at all.
	served    ServedDaemons
	servedSet bool
	// modelNote receives what the `models` pass could not do as written (WithModelNotes).
	modelNote func(string)
}

// ServiceAdaptations returns the conversions packs declare whose own pack serves them with a
// `service` (Adaptation.Service), each Address carrying the user's override when there is one
// (WithAdapterAddresses' map): the adaptations a notch that does not serve that service leaves
// out (WithServed), spelled for a refusal to name where the agent would have been pointed.
// UnservedAdaptationsAt picks the ones one notch cannot serve.
func ServiceAdaptations(packs []*Pack, addresses map[string]string) []Adaptation {
	var out []Adaptation
	for _, a := range Adaptations(packs) {
		if a.Service == "" {
			continue
		}
		if override, ok := addresses[AdapterKey(a.From, a.To)]; ok && override != "" {
			a.Address = override
		}
		out = append(out, a)
	}
	return out
}

// UnservedAdapterError is the gate's refusal for a pairing only an adaptation this notch cannot
// serve would resolve: a pack declares the conversion, and serves its address from a daemon of
// its own that does not run here. The pack may be selected or not, and selecting it would not
// change the answer. Typed, so the notch that left the adaptation out (the host) can name where
// the profile does work; its Error() is complete without that.
type UnservedAdapterError struct {
	// Agent and Provider are the pairing that refused.
	Agent, Provider string
	// Adaptation is the conversion that would have resolved it, with the address the agent
	// would have been pointed at.
	Adaptation Adaptation
	// Selected reports whether the adaptation's pack is selected at this launch. When it is
	// not, the pack is a shipped one outcome 3 would otherwise have offered.
	Selected bool
	// ProviderPack, when set, is a shipped pack this launch did not select that declares
	// Provider: the composed table did not hold the provider at all, and this refusal is the
	// gate's answer with that pack's declaration composed in (missingProvider). Selecting it
	// would therefore not change the answer. NeededBy is a selected pack whose `needs` names
	// ProviderPack, "" when none does.
	ProviderPack, NeededBy string
}

func (e *UnservedAdapterError) Error() string {
	a := e.Adaptation
	pointed := fmt.Sprintf("%s would be pointed at a dead address", e.Agent)
	if !e.Selected {
		pointed = fmt.Sprintf("selecting pack %q would point %s at a dead address", a.Pack, e.Agent)
	}
	msg := fmt.Sprintf("provider %q would reach agent %q only through pack %q's %s → %s adapter at "+
		"%s, and that address is served by the pack's own %q service, a daemon this launch does "+
		"not run — nothing serves it here, so %s",
		e.Provider, e.Agent, a.Pack, strconv.Quote(a.From), strconv.Quote(a.To), a.Address, a.Service, pointed)
	if e.ProviderPack != "" {
		msg += fmt.Sprintf("; provider %q is not in this launch's provider table either — pack %q "+
			"ships it and is not selected%s, and selecting it would not change this answer",
			e.Provider, e.ProviderPack, neededByClause(e.NeededBy))
	}
	return msg
}

// MissingProviderError is the gate's refusal for a selected profile whose provider the
// composed table does not hold. docs/design/declaration-parity.md P1 names the one state that
// is never legal, a declaration accepted and doing nothing, and this was it: the gate returned
// nil, the agent's derive ran over a provider it could not see, and the agent launched with
// nothing re-pointed. Measured at the host with `"packs": ["claude"]`: `yolo host -p codex --
// claude` exited 0 and claude received three context-window constants and no address, so it
// ran on its own Claude login (docs/design/credential-sources-separation.md ES-D25).
type MissingProviderError struct {
	// Agent, Profile and Provider are the selection that refused.
	Agent, Profile, Provider string
	// Dropped is the selected pack that ships Provider, when one does: the table lacks it
	// because a null `providers.<name>` entry in the user's config removes it.
	Dropped string
	// Shipper is a shipped pack this launch did not select that declares Provider, "" when
	// none does. NeededBy is a selected pack whose `needs` names Shipper, "" when none does.
	Shipper, NeededBy string
	// Then is the gate's refusal with Shipper's declaration composed in, when that refusal is
	// not an *UnservedAdapterError (which is returned in this error's place): adding Shipper
	// alone would not make the profile work.
	Then error
}

func (e *MissingProviderError) Error() string {
	head := fmt.Sprintf("profile %q selects provider %q for agent %q, and this launch's provider "+
		"table does not hold it", e.Profile, e.Provider, e.Agent)
	lost := fmt.Sprintf("so %s would launch with nothing re-pointed, on whatever login it already holds", e.Agent)
	switch {
	case e.Dropped != "":
		return fmt.Sprintf("%s: pack %q ships it, and a null `providers.%s` entry in your config "+
			"removes it — %s. Remove the null, or select a profile whose provider this launch holds",
			head, e.Dropped, e.Provider, lost)
	case e.Shipper != "" && e.Then != nil:
		return fmt.Sprintf("%s: pack %q ships it and is not selected%s — %s. With %q added the "+
			"profile still refuses: %v", head, e.Shipper, neededByClause(e.NeededBy), lost, e.Shipper, e.Then)
	case e.Shipper != "":
		return fmt.Sprintf("%s: pack %q ships it and is not selected%s — %s. Add %q to `packs` and "+
			"this profile resolves", head, e.Shipper, neededByClause(e.NeededBy), lost, e.Shipper)
	default:
		return fmt.Sprintf("%s: no selected pack ships it, no pack yolo ships declares it, and your "+
			"config's `providers` has no entry for it — %s. Declare it under `providers`, or select "+
			"a profile whose provider this launch holds", head, lost)
	}
}

// neededByClause is the clause both refusals add when a selected pack's `needs` names the
// provider's pack: why a pack the manifests ask for is still not selected.
func neededByClause(neededBy string) string {
	if neededBy == "" {
		return ""
	}
	return fmt.Sprintf(", though pack %q's `needs` names it (a launch adds a needed pack only "+
		"while the need's `when_bins` holds)", neededBy)
}

// missingProvider is refuseUnspeakableProvider's answer when the composed table does not hold
// the selected provider. It refuses, naming why, in every case but one.
//
// THE ONE EXCUSE: a shipped pack this launch did not select declares the provider, the
// pairing resolves with that declaration composed in, and nothing this notch runs for the
// agent reads the table (readsProviderTable). Then the launch composes the same agent with
// the row as without it, and there is nothing silent to refuse. What counts as a reader
// depends on the notch. The host (unserved non-nil) composes only the agent's environment,
// so the reader is its pack's `yolo.env` producer. That excuses codex on its `codex` profile at
// the host when nothing selects the provider's pack (a user pack named `codex` with no `needs`,
// say): it registers no `yolo.env`, and reaches the subscription through the host's own managed
// OpenAI launch. pi was excused too until OQ-BR8 moved its OpenAI login prelaunch into a
// `yolo.env` producer keyed on the provider; it has a reader now. The shipped codex and pi packs
// `need` openai-auth, which every notch now joins (notch-convergence item 6). A jail also renders each agent's config surfaces, whose derives read
// the table: pi's, codex's and opencode's each write nothing for a provider the table lacks,
// so a user-declared profile naming a provider no selected pack ships would start the agent
// on its own default. At a jail a `yolo.derive` for the agent in any selected pack is a
// reader too, and the excuse is left to an agent nothing derives for at all.
// Otherwise, with such a pack found, the gate is asked again with its declaration composed
// the way this notch composes (WithServed(NothingServed()) where unserved is non-nil, which is
// the notch that runs no pack service). An *UnservedAdapterError from that is returned
// itself, annotated with the pack: it is the final answer at this notch, and naming the
// provider's pack as the remedy would lead the user straight into it (ES-D19's two refusals
// in a row). Any other refusal is carried in MissingProviderError.Then.
func missingProvider(packs []*Pack, owner *Pack, agent, profile, selected string,
	unserved []Adaptation) error {
	e := &MissingProviderError{Agent: agent, Profile: profile, Provider: selected}
	if p := providerShipper(packs, selected); p != nil {
		e.Dropped = p.Name
		return e
	}
	shipper := providerShipper(unselectedEmbedded(packs), selected)
	if shipper == nil {
		return e
	}
	e.Shipper = shipper.Name
	e.NeededBy = packNeeding(packs, shipper.Name)
	with := append(append([]*Pack{}, packs...), shipper)
	var opts []ComposeOption
	if unserved != nil {
		opts = append(opts, WithServed(NothingServed()))
	}
	table, err := ComposeProviders(nil, with, opts...)
	if err != nil {
		return e
	}
	if _, held := table.Get(selected); !held {
		return e // unreachable: shipper declares it; kept so the question below cannot recurse
	}
	predicted := refuseUnspeakableProvider(with, owner, agent, profile, selected, table, unserved)
	if predicted == nil {
		if readsProviderTable(packs, owner, agent, unserved == nil) {
			return e
		}
		return nil
	}
	var ue *UnservedAdapterError
	if errors.As(predicted, &ue) {
		ue.ProviderPack, ue.NeededBy = e.Shipper, e.NeededBy
		return ue
	}
	e.Then = predicted
	return e
}

// providerShipper returns the pack whose entry for a provider named name the composed table
// holds, the LAST of packs to declare it (laterWins, as ComposeProviders keeps it), or nil.
func providerShipper(packs []*Pack, name string) *Pack {
	return lastDeclarer(packs, func(p *Pack) bool {
		for _, prov := range p.Decl.Providers() {
			if prov.Name == name {
				return true
			}
		}
		return false
	})
}

// packNeeding returns the first of packs whose `needs` names target, live or not, or "".
func packNeeding(packs []*Pack, target string) string {
	for _, p := range packs {
		for _, need := range p.Decl.DeclaredNeeds() {
			if need.Pack == target {
				return p.Name
			}
		}
	}
	return ""
}

// readsProviderTable reports whether anything the notch runs for agent reads the provider
// table: owner's `yolo.env` producer for it (AgentEnv), and, when surfaces is set (a jail,
// which renders config surfaces), a `yolo.derive` for it in any of packs. A script whose
// registrations cannot be read counts as a reader, so missingProvider refuses rather than
// excuses.
func readsProviderTable(packs []*Pack, owner *Pack, agent string, surfaces bool) bool {
	if script := DeriveScript(owner); script != "" {
		agents, err := (luahook.GopherLuaVM{}).EnvRegistrations(script)
		if err != nil || slices.Contains(agents, agent) {
			return true
		}
	}
	if !surfaces {
		return false
	}
	for _, p := range packs {
		script := DeriveScript(p)
		if script == "" {
			continue
		}
		regs, err := (luahook.GopherLuaVM{}).DeriveRegistrations(script)
		if err != nil {
			return true
		}
		for _, r := range regs {
			if r.Agent == agent {
				return true
			}
		}
	}
	return false
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
// A PAIR DECLARED TWICE IS HELD BY THE LATER DECLARATION, not merged and not refused here:
// the pair is sole-owned, the claim target carries both halves, and packload.Collisions'
// generic exclusive loop is the check that REPORTS it. The later one in packs' order holds it
// (laterWins, notch-convergence NC-D59), the rule ComposeProviders follows for a duplicated
// provider name, so the local pack's adapter beats one a pack pulled in through `needs`
// declares. The kept entry stands at the holder's position.
//
// An entry with an empty half is skipped: the schema refuses it at authoring time, so
// reaching here means a manifest a newer host staged and this build read tolerantly, where
// a half-declared conversion is nothing rather than a fault.
func Adaptations(packs []*Pack) []Adaptation {
	var all []Adaptation
	for _, p := range packs {
		service := ""
		if svcs := p.Decl.Services(); len(svcs) > 0 {
			service = svcs[0].Name
		}
		for _, a := range p.Decl.Adapters() {
			if a.From == "" || a.To == "" || a.Address == "" {
				continue
			}
			all = append(all, Adaptation{Pack: p.Name, From: a.From, To: a.To, Address: a.Address,
				Service: service, FromPlatforms: a.FromPlatforms})
		}
	}
	holds := laterWins(len(all), func(i int) string { return AdapterKey(all[i].From, all[i].To) })
	var out []Adaptation
	for i, a := range all {
		if holds[i] {
			out = append(out, a)
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
	// AN ADDRESS COMPOSED FOR A VIA PROFILE REPOINTS NOTHING for an agent that cannot speak it
	// (ForViaKey, docs/design/wire-bridge-gateway.md WG-I39): the provider names no endpoint of
	// its own, so an agent with its own client for the provider's platform is exactly as it
	// was with none, and codex on `-p bedrock` beside claude is not refused over an anthropic
	// address only claude's everything profile uses.
	own := false
	for p := range offered {
		if forViaService(entry, p) == "" {
			own = true
		}
	}
	if !own {
		return ProtocolResolution{}, nil
	}
	return ProtocolResolution{}, unspeakableProvider(agent, spoken, providerName, offered, elsewhere)
}

// UnselectedAdaptations returns the conversions declared by packs yolo SHIPS and this
// launch did NOT select — outcome 3's whole input, and nothing else's.
//
// IT READS THE EMBEDDED SET, which is deliberately not selection-gated here, because the
// question is what is true of everything yolo ships, regardless of what a particular jail
// loaded: outcome 3 names the pack to add. The packs are loaded once per process — from the
// build's shared content-addressed tree, adopted on first use — and a launch has already
// loaded them by the time this runs, so this is a walk over manifests in memory rather than
// a filesystem cost of its own.
//
// WHAT IT BOUNDS, honestly: only a pack yolo ships can be named. A third-party pack the
// user has not selected is invisible here, so its pairing gets outcome 4's message instead
// of outcome 3's. That is a limit on the REMEDY, never on the rule — the pairing resolves
// identically once either pack is selected (P6), and core can only name what it can see.
func UnselectedAdaptations(selected []*Pack) []Adaptation {
	return Adaptations(unselectedEmbedded(selected))
}

// unselectedEmbedded is every pack yolo ships that selected does not hold by name.
func unselectedEmbedded(selected []*Pack) []*Pack {
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
	return rest
}

// withoutAdaptations is list with every adaptation drop names (the same pack and pair) left
// out. The address is not compared, since an override moves it and not the identity.
func withoutAdaptations(list, drop []Adaptation) []Adaptation {
	if len(drop) == 0 {
		return list
	}
	var out []Adaptation
	for _, a := range list {
		if !slices.ContainsFunc(drop, func(d Adaptation) bool {
			return d.Pack == a.Pack && AdapterKey(d.From, d.To) == AdapterKey(a.From, a.To)
		}) {
			out = append(out, a)
		}
	}
	return out
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
// NO PROVIDER SELECTED is the one way there is nothing to ask (a launch with no profile, or
// a profile that resolves to none): ProviderFor returning "" is an ordinary launch, not a
// fault. A selected provider the composed table DOES NOT HOLD used to be treated the same
// way, as the derives' own "no selection" case, and that was the silent no-op P1 forbids: a
// profile the user named, accepted, and composed into nothing. It refuses now, naming why
// (missingProvider, ES-D25). profile is the selected profile's name, for that refusal.
//
// owner is the pack that installs agent's CLI, found by bin ownership: the same identity
// AgentEnv discovers a producer through, so the pack that speaks for an agent's environment
// is the pack that speaks for its wires.
//
// unserved is what this notch can never serve (UnservedAdaptationsAt: the adaptations the
// composition left out, and the unselected shipped packs' of the same kind), nil at a notch
// that runs its packs' services. It is taken out of outcome 3's candidates, because selecting
// such a pack there resolves nothing. A pairing one of them would have resolved then refuses
// as *UnservedAdapterError. That replaces outcome 4, whose "nothing declares an adapter" is
// false of it, and outcome 3, whose "Add it to `packs`" would lead to this same refusal.
func refuseUnspeakableProvider(packs []*Pack, owner *Pack, agent, profile, selected string,
	providers *jsonx.OrderedMap, unserved []Adaptation) error {
	if selected == "" {
		return nil
	}
	var v any
	held := false
	if providers != nil {
		v, held = providers.Get(selected)
	}
	if !held || v == nil {
		return missingProvider(packs, owner, agent, profile, selected, unserved)
	}
	// A malformed entry (not an object) is the config validator's to report, as it always
	// was: the question here is about a provider that is absent, not one that is misspelled.
	entry, ok := v.(*jsonx.OrderedMap)
	if !ok {
		return nil
	}
	spoken := owner.Decl.SpokenProtocols(agent)
	elsewhere := withoutAdaptations(UnselectedAdaptations(packs), unserved)
	_, err := ResolveProtocol(agent, spoken, selected, entry, elsewhere)
	if err == nil {
		return nil
	}
	// THE ORDER: a SELECTED pack's unservable adaptation first, since the user already chose the
	// pack declaring the conversion. Then outcome 3, when an unselected pack this notch CAN
	// serve would resolve the pairing, because adding that one works. Only then an unselected
	// pack's unservable one, which outcome 3 no longer offers.
	offered := providerProtocols(entry)
	var chosen, unchosen []Adaptation
	for _, a := range unserved {
		if hasPackNamed(packs, a.Pack) {
			chosen = append(chosen, a)
		} else {
			unchosen = append(unchosen, a)
		}
	}
	if a := missingAdapterFor(spoken, offered, chosen); a != nil {
		return &UnservedAdapterError{Agent: agent, Provider: selected, Adaptation: *a, Selected: true}
	}
	if missingAdapterFor(spoken, offered, elsewhere) != nil {
		return err
	}
	if a := missingAdapterFor(spoken, offered, unchosen); a != nil {
		return &UnservedAdapterError{Agent: agent, Provider: selected, Adaptation: *a}
	}
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

// hasPackNamed reports whether packs holds a pack called name.
func hasPackNamed(packs []*Pack, name string) bool {
	return slices.ContainsFunc(packs, func(p *Pack) bool { return p.Name == name })
}

// binOwner returns the selected pack that installs bin, or nil.
//
// EXTRACTED so AgentEnv and PairingRefusals cannot grow two answers to "whose agent is
// this". The gate below predicts the gate AgentEnv applies, and a prediction that found a
// different owner would predict a different launch — the failure mode the capability gate's
// second copy carried until its census moved to one function both callers share.
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
// ⚠ IT IS THE SAME FUNCTION, NOT A MIRROR OF IT. capabilities.go's census was a copy until
// 2026-09-30, because reaching the launch's gate meant exporting a method on run.Options and
// dragging its printer along; it is now one free function both call
// (config.UnmetCapabilities), which is the shape this gate had from the start: a free
// function in this package over inputs a caller can assemble, so exporting an entry point is
// strictly cheaper than restating twelve lines of pairing rules somewhere they can drift.
//
// # What the caller still has to get right, and what it therefore cannot promise
//
// The INPUTS are assembled twice — a checker composes providers and resolves profiles the
// way the launch does — and that is the whole residue of duplication. Two consequences the
// caller must state rather than hide:
//
//   - A `-p <name>` at the command line is NOT here. `check` reads configuration; the flag
//     is an argument to a launch that has not happened. So a prediction covers the
//     `profile` selection and nothing else, and a flag can still produce a refusal
//     nobody was warned about. Narrowing that needs the flag, not a wider census.
//   - An agent with no profile is not pairing with anything. AgentEnv returns early on an
//     empty profile and so does this, because the gate it predicts is reached through a
//     SELECTION (§4.1) — a provider merely present in the table repoints nothing.
//
// profiles maps an agent's CLI name to its selected profile name (ProfileTable's shape), and
// unserved is the predicted notch's UnservedAdaptationsAt, so `yolo check` predicts the refusal the
// configured runtime's launch makes.
func PairingRefusals(packs []*Pack, providers *jsonx.OrderedMap,
	resolved map[string]ResolvedProfile, profiles map[string]string, unserved []Adaptation) []error {
	names := make([]string, 0, len(profiles))
	for agent := range profiles {
		names = append(names, agent)
	}
	sort.Strings(names)

	var out []error
	for _, agent := range names {
		if err := PairingRefusal(packs, providers, resolved, agent, profiles[agent], unserved); err != nil {
			out = append(out, err)
		}
	}
	return out
}

// SetEntryPairingRefusals is PairingRefusals for the entries AFTER each agent's primary in an
// active-set table (ProfileSets; docs/design/active-provider-sets.md AP-P1), agents in name
// order, each refusal naming the entry's position: what AgentEnv refuses at launch for a later
// entry (refuseUnspeakableSetEntries), predicted from configuration. The primaries are
// PairingRefusals' to ask, so a caller asks both.
func SetEntryPairingRefusals(packs []*Pack, providers *jsonx.OrderedMap,
	resolved map[string]ResolvedProfile, sets map[string][]string, unserved []Adaptation) []error {
	agents := make([]string, 0, len(sets))
	for agent := range sets {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	var out []error
	for _, agent := range agents {
		set := sets[agent]
		if len(set) < 2 {
			continue
		}
		owner := binOwner(packs, agent)
		if owner == nil {
			continue
		}
		cfg := agentEnvOpts{resolved: resolved, unserved: unserved, set: set}
		if err := refuseUnspeakableSetEntries(packs, owner, agent, set[0], cfg, providers); err != nil {
			out = append(out, err)
		}
	}
	return out
}

// PairingRefusal is the gate's answer for ONE agent on one profile, or nil — PairingRefusals'
// body, with the notch's unservable adaptations (UnservedAdaptationsAt; nil where
// the packs' services run). An agent with no profile, or one no selected pack installs, pairs
// with nothing and gets nil, as AgentEnv composes nothing for it.
//
// The host footer asks it (internal/cli's hostFooterTables): a selection the host launch
// refuses is one no host process runs on, so the footer must not name it.
func PairingRefusal(packs []*Pack, providers *jsonx.OrderedMap, resolved map[string]ResolvedProfile,
	agent, profile string, unserved []Adaptation) error {
	if agent == "" || profile == "" {
		return nil
	}
	owner := binOwner(packs, agent)
	if owner == nil {
		return nil
	}
	return refuseUnspeakableProvider(packs, owner, agent, profile,
		ProviderFor(resolved, profile), providers, unserved)
}
