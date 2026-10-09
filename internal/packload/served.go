package packload

// served.go is the ONE "served at this notch" predicate (docs/plans/notch-convergence.md §4 item
// 2, NC-D16) and the one provider-composition helper every notch calls with it.
//
// SERVED AT THIS NOTCH is a term this file coins. A jail daemon — a loophole's `jail_daemon` or a
// pack service's — is served at a notch when that notch runs it, in its jail or beside it: a
// container runtime runs every daemon in its launch's composed payload (internal/cli/run's
// jailDaemonsFor), in the jail; macos-user runs them too since OQ-DP8/OQ-DP9
// (docs/design/declaration-parity.md), confined in its Seatbelt guest, except the ones the guest
// declines by name (internal/loopholes' JailDaemonsRunIn), and it also serves the credential
// DOORWAYS its launch opens outside the guest, whose jail-daemon form the guest declines
// (docs/design/host-notch-services.md HS-D15; "doorway" is that ruling's word for the thin
// adapter an agent's client talks to, which checks the launch's caller token and forwards to the
// service's host daemon); and the host notch runs none but the doorways `yolo host --` opens for
// the one agent it runs (HS-D21, internal/cli/run's hostdoorways.go). internal/loopholes'
// ServedJailDaemons is the one answer to which of a payload's daemons a runtime serves. An address
// such a daemon serves is SERVED exactly when the daemon is. A loophole that declares no jail
// daemon but binds host sockets or devices into a jail (a BOUND LOOPHOLE, LP-D1's term) is served
// by name at a notch whose container argv carries its binds (internal/loopholes' JailBoundNames):
// never at the host, never on macos-user, and on a container runtime only while it is active
// (docs/design/loophole-packaging.md LP-D1).
//
// WHY ONE PREDICATE. Each notch used to answer "does anything listen there?" its own way. The
// host composed no adapter address (a WithoutServiceAdaptations option) and cleared every via
// (ViaInert). macos-user composed the container's table and launched claude against
// `127.0.0.1:8214`, which nothing on it serves. And pack env pointed Codex and the AWS SDKs at
// `:1460` and `:1461` at every notch. One description meant three things. Now the notch is an
// INPUT, the set below, and every composer asks the same question of it: an address nothing
// serves is left out, and a pairing that needed it refuses by name (ES-D18, generalized).

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// ServedDaemons is the set of jail daemons served at one notch, and WHERE each serves. The zero
// value serves nothing, which is the host's answer.
//
// A SERVED ADDRESS (coined here, docs/plans/notch-convergence.md NC-D41) is the loopback
// `host:port` a served daemon, adaptation or via route answers at in THIS launch. It is the
// declared one — a loophole's `jail_daemon.listen`, a service's adapter `address` or
// `via_address` — on a jail with a network namespace of its own. On a jail that shares the
// launcher's (`network.mode: host`, a nested podman forced onto `--net=host`) the launcher
// picks an ephemeral port for each instead, because two jails on one loopback would otherwise
// contend for one port. The launcher is the only writer; every composer reads it from here, so
// a daemon's argv, its clients' pack env pointer and the provider table cannot disagree.
type ServedDaemons struct {
	// runs is whether this notch runs jail daemons at all. It is what separates "this daemon
	// is not in the payload" (a jail launch that did not enable it) from "no daemon runs
	// here" (the host): only at a notch that runs them would selecting another service pack
	// serve its address.
	runs  bool
	names map[string]bool
	// listen is each served daemon's served address, keyed by daemon name: what TokenListen
	// resolves to in a pack env value `served_by` it. Absent for a daemon that declares none.
	listen map[string]string
	// rebind maps a declared loopback `host:port` a pack service serves at to its served
	// address, for the declared addresses this launch moved. Empty on a private namespace.
	rebind map[string]string
	// host marks the host notch's set (AtHost): a notch that runs a jail daemon only as a
	// credential doorway its launch opens (docs/design/host-notch-services.md HS-D15, HS-D21),
	// so a daemon it does not serve is worded as one this launch did not open, never as one
	// only a jail could run.
	host bool
	// notServed is why this launch leaves a named daemon unserved, keyed by daemon name, where
	// the launch knows a reason the notch alone does not say (WithNotServedWhy): the loophole
	// is disabled, or the front door owns no process lifetime to open a doorway for. A pointer
	// at a BOUND LOOPHOLE never reads it (notBoundWhy says why).
	notServed map[string]string
	// inherited is each daemon whose pointer this launch takes from the launching jail instead
	// of serving it (WithInherited), keyed by daemon name, then by variable: the value the
	// launching jail's environment holds. Such a daemon runs nowhere in this launch, and Serves
	// still answers true for it, since its pointer is delivered.
	inherited map[string]map[string]string
	// mountsNothing marks a jail notch that binds nothing into its jail (MountsNothing):
	// macos-user, whose Seatbelt sandbox is a process on the host's own filesystem. A pointer at
	// what a loophole binds (a BOUND LOOPHOLE, notBoundWhy) is worded as one this notch cannot
	// bind, never as one the launch left switched off.
	mountsNothing bool
}

// AtHost returns s marked as the host notch's set (the host field says what that changes).
func (s ServedDaemons) AtHost() ServedDaemons {
	s.host = true
	return s
}

// RunsLaunchOwnedServices reports whether s is the served set of a notch whose launch runs a pack
// service as a LAUNCH-OWNED host half outside every jail (internal/launchservice;
// docs/design/host-notch-services.md §4.7, §4.8): the host's set (AtHost) and macos-user's
// (MountsNothing), the two notches with no network namespace of their own, where a service's jail
// daemon never runs and its host half does. A container's set answers false: its jail runs the
// service's jail daemon. `yolo check` asks it to predict the via trigger those launches apply
// (ViaRoutedServices, HS-D30).
func (s ServedDaemons) RunsLaunchOwnedServices() bool { return s.host || s.mountsNothing }

// MountsNothing returns s marked as the set of a jail notch that binds nothing into its jail (the
// mountsNothing field says what that changes).
func (s ServedDaemons) MountsNothing() ServedDaemons {
	s.mountsNothing = true
	return s
}

// WithNotServedWhy returns s with why, keyed by daemon name: the clause UnservedEnvLines puts
// after the daemon's name for a pointer this launch withheld. A daemon with no entry gets the
// notch's own clause. nil adds nothing.
func (s ServedDaemons) WithNotServedWhy(why map[string]string) ServedDaemons {
	if len(why) == 0 {
		return s
	}
	out := make(map[string]string, len(s.notServed)+len(why))
	for k, v := range s.notServed {
		out[k] = v
	}
	for k, v := range why {
		if v != "" {
			out[k] = v
		}
	}
	s.notServed = out
	return s
}

// notServedWhy is the clause naming why daemon is not served here: the launch's own reason
// when it gave one (WithNotServedWhy), else the notch's.
func (s ServedDaemons) notServedWhy(daemon string) string {
	if why := s.notServed[daemon]; why != "" {
		return why
	}
	switch {
	case s.host:
		// Every daemon a host launch's selection asks for carries the launch's own reason
		// (run.PlanHostDoorways), so what reaches this clause is a daemon whose pointer is not
		// gated on the agent's selection, the Codex refresh adapter's: no selection opens it.
		// A managed launch that serves that pointer itself and did not start gives its own
		// reason instead (LaunchServes).
		return "which `yolo host --` opens for no selection: at the host a jail daemon runs " +
			"only as a doorway opened for the one agent a launch runs, and only when every " +
			"pointer to it is gated on that agent's selected profile or provider, which this " +
			"one's is not (docs/design/host-notch-services.md HS-D15, HS-D22)"
	case s.runs:
		return "which this launch does not run (its loophole is disabled, or its pack is not selected)"
	default:
		return "which does not run here: jail daemons run only in a jail (a container, or the " +
			"macos-user sandbox), never at the host"
	}
}

// notBoundWhy is notServedWhy for a BOUND LOOPHOLE (a term docs/design/loophole-packaging.md
// LP-D1 coins): a loophole that serves its clients by binding host sockets or devices into a
// jail, declaring no `jail_daemon` (loopholes' JailBoundNames applies it). A pointer `served_by`
// one names a path that exists only in a jail whose argv carried the binds, so the clause says
// why this launch has none, ending in what a client does without the pointer: at the host it
// reaches the host's own server at its default path, which a pointer at a jail path would only
// defeat.
//
// THE NOTCH DECIDES, never the launch's reason (notServed). That map says why a launch did not
// run a jail daemon or open a doorway, and a bound loophole has neither: at `yolo host --` it
// holds a doorway's reason for every profile-served name (internal/cli/run's PlanHostDoorways),
// a gated pointer at a bound loophole included, and its next step ("`yolo host --` opens its
// doorway ... once ... enabled") is one no switch takes for a bind. A set that runs nothing is
// the host's answer (NothingServed), so it gets the host's clause.
func (s ServedDaemons) notBoundWhy(loophole string) string {
	switch {
	case s.host || !s.runs:
		return "and the host has no jail to bind it into, so a client here reaches the host's own " +
			"server at its default path instead"
	case s.mountsNothing:
		return "which the macos-user sandbox does not have: it binds nothing into the jail, so " +
			"nothing would answer it"
	default:
		return "which this launch did not bind: the loophole is off (`\"loopholes\": {" +
			strconv.Quote(loophole) + ": {\"enabled\": true}}` in your config turns it on) or " +
			"inactive on this machine (`yolo loopholes list` says why), so nothing would answer it"
	}
}

// WithListen returns s with each daemon's served address, keyed by daemon name (internal/cli/run
// reads it off the composed payload's JailDaemonSpec.Listen). An empty address is skipped.
func (s ServedDaemons) WithListen(listen map[string]string) ServedDaemons {
	s.listen = nil
	for name, addr := range listen {
		if addr == "" {
			continue
		}
		if s.listen == nil {
			s.listen = map[string]string{}
		}
		s.listen[name] = addr
	}
	return s
}

// WithInherited returns s serving each daemon in pointers by INHERITANCE: a nested launch that
// shares the launching jail's loopback takes that daemon's pointer from the launching jail's
// environment (loopholedecl.ParentJailInheritance; docs/design/sso-backed-bedrock.md SSO-D2) and
// runs neither of its daemons. pointers maps a daemon's name to the value of each variable it
// carries. A pack `env` pointer `served_by` such a daemon is delivered with the inherited value in
// place of its declared one, and one naming a variable pointers does not carry is withheld. nil
// or empty adds nothing.
func (s ServedDaemons) WithInherited(pointers map[string]map[string]string) ServedDaemons {
	if len(pointers) == 0 {
		return s
	}
	names := make(map[string]bool, len(s.names)+len(pointers))
	for n, ok := range s.names {
		names[n] = ok
	}
	inherited := make(map[string]map[string]string, len(s.inherited)+len(pointers))
	for d, vals := range s.inherited {
		inherited[d] = vals
	}
	for d, vals := range pointers {
		if d == "" {
			continue
		}
		names[d] = true
		inherited[d] = vals
	}
	s.names, s.inherited = names, inherited
	return s
}

// Inherits reports whether s takes daemon's pointer from the launching jail (WithInherited).
func (s ServedDaemons) Inherits(daemon string) bool {
	_, ok := s.inherited[daemon]
	return ok && s.runs
}

// inheritedValue is the launching jail's value of key for daemon's inherited pointer, and ok false
// when daemon is not inherited or its pointer carries no such variable.
func (s ServedDaemons) inheritedValue(daemon, key string) (string, bool) {
	if !s.Inherits(daemon) {
		return "", false
	}
	v, ok := s.inherited[daemon][key]
	return v, ok && v != ""
}

// WithRebind returns s with the declared-to-served address map for a pack service's declared
// adapter and via addresses (served address, above). nil moves nothing.
func (s ServedDaemons) WithRebind(rebind map[string]string) ServedDaemons {
	s.rebind = rebind
	return s
}

// Listen is the served address of the daemon named name, "" when it is not served here or
// declares no listen address.
func (s ServedDaemons) Listen(name string) string {
	if !s.Serves(name) {
		return ""
	}
	return s.listen[name]
}

// ServedURL is declared, a pack service's declared base URL, with its `host:port` replaced by
// the served address this launch moved it to; declared unchanged when the launch moved nothing
// there, which is every private-namespace launch.
func (s ServedDaemons) ServedURL(declared string) string {
	if len(s.rebind) == 0 {
		return declared
	}
	u, err := url.Parse(declared)
	if err != nil || u.Host == "" {
		return declared
	}
	to, ok := s.rebind[u.Host]
	if !ok {
		return declared
	}
	u.Host = to
	return u.String()
}

// resolveListen is value with TokenListen resolved to the served address of daemon, and ok
// false when value names the token and daemon has no served address to give it.
func (s ServedDaemons) resolveListen(daemon, value string) (string, bool) {
	if !strings.Contains(value, loopholedecl.TokenListen) {
		return value, true
	}
	addr := s.Listen(daemon)
	if addr == "" {
		return "", false
	}
	return strings.ReplaceAll(value, loopholedecl.TokenListen, addr), true
}

// ServedInJail is the set a jail launch serves: the jail daemons it runs, by name — on a
// container runtime every one its composed payload names, and on macos-user the ones its guest
// runs plus the doorways the launch opens outside it (loopholes.ServedJailDaemonNames, which
// reads loopholes.ServedJailDaemons). The caller passes that split's names and the bound
// loopholes its container argv binds (loopholes.JailBoundNames, nil on macos-user), so the launch
// and `yolo check`'s prediction of it build the set from the one selection.
func ServedInJail(names []string) ServedDaemons {
	s := ServedDaemons{runs: true, names: map[string]bool{}}
	for _, n := range names {
		s.names[n] = true
	}
	return s
}

// NothingServed is the set a notch that runs no jail daemon serves: the host, before it opens a
// doorway or starts a launch-owned service (marked AtHost there).
func NothingServed() ServedDaemons { return ServedDaemons{} }

// ServedByLaunch is the set a host or macos-user launch serves once it has decided to start the
// named pack services' host halves as launch-owned children (internal/launchservice,
// docs/design/host-notch-services.md). Their addresses come from WithRebind, the ports that
// launch picked. A macos-user launch serves them beside the jail daemons its guest runs (Plus).
func ServedByLaunch(services []string) ServedDaemons { return ServedInJail(services) }

// Plus is s and o together: every name either serves, each one's listen address, and both
// rebind maps (o's wins a collision, which no two sets built for one launch produce). It runs
// daemons when either does. macos-user's served set is its guest's jail daemons Plus the
// launch-owned services it planned.
func (s ServedDaemons) Plus(o ServedDaemons) ServedDaemons {
	out := ServedDaemons{runs: s.runs || o.runs, host: s.host || o.host,
		mountsNothing: s.mountsNothing || o.mountsNothing, names: map[string]bool{}}
	out = out.WithNotServedWhy(s.notServed).WithNotServedWhy(o.notServed)
	for _, part := range []ServedDaemons{s, o} {
		for n, ok := range part.names {
			if ok {
				out.names[n] = true
			}
		}
		for n, a := range part.listen {
			if out.listen == nil {
				out.listen = map[string]string{}
			}
			out.listen[n] = a
		}
		for k, v := range part.rebind {
			if out.rebind == nil {
				out.rebind = map[string]string{}
			}
			out.rebind[k] = v
		}
		for d, vals := range part.inherited {
			if out.inherited == nil {
				out.inherited = map[string]map[string]string{}
			}
			out.inherited[d] = vals
		}
	}
	return out
}

// Serves reports whether the jail daemon named name is served at this notch.
func (s ServedDaemons) Serves(name string) bool { return s.runs && name != "" && s.names[name] }

// RunsDaemons reports whether this notch runs jail daemons at all.
func (s ServedDaemons) RunsDaemons() bool { return s.runs }

// Names is the served daemons' names, sorted.
func (s ServedDaemons) Names() []string {
	out := make([]string, 0, len(s.names))
	for n := range s.names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// WithServed composes the table for a notch serving served: an adaptation whose declaring pack
// serves it with a daemon of its own (Adaptation.Service) composes its address only when that
// daemon is served. Every other adaptation composes as usual, and so does every provider's own
// endpoint, so an agent that speaks the provider's wire resolves to it directly, as it would
// with the adapter's pack unselected. A pairing only the left-out adaptation would resolve
// refuses at the gate (UnservedAdapterError), naming why.
func WithServed(served ServedDaemons) ComposeOption {
	return func(o *composeOpts) {
		o.served = served
		o.servedSet = true
	}
}

// adaptationServed reports whether a's address is served under cfg. With no WithServed the
// table is composed as declared, every adaptation included: the shape a caller that is not
// composing for a notch (a lint, a footprint) reads.
func (cfg composeOpts) adaptationServed(a Adaptation) bool {
	return a.Service == "" || !cfg.servedSet || cfg.served.Serves(a.Service)
}

// ComposeProvidersAt is THE provider composition for a notch: the user's `providers` entries
// over every selected pack's shipped providers, the user's adapter address overrides, and only
// the adapter addresses served here. It returns the table and the adaptations this notch can
// never serve (UnservedAdaptationsAt), which the credential gate needs so a pairing only one of
// them would resolve refuses with the reason (ScopeInput.UnservedAdaptations); nil where every
// selected service runs.
func ComposeProvidersAt(user *jsonx.OrderedMap, packs []*Pack, addresses map[string]string,
	served ServedDaemons, opts ...ComposeOption) (*jsonx.OrderedMap, []Adaptation, error) {
	composeOptions := append([]ComposeOption(nil), opts...)
	composeOptions = append(composeOptions, WithAdapterAddresses(addresses), WithServed(served))
	table, err := ComposeProviders(user, packs, composeOptions...)
	if err != nil {
		return nil, nil, err
	}
	return table, UnservedAdaptationsAt(packs, addresses, served), nil
}

// UnservedAdaptationsAt is every conversion this notch cannot serve, each at the user's
// override: the selected packs' service-served adaptations whose daemon is not served, and, at
// a notch that runs no jail daemon, those of the shipped packs this launch did not select too,
// since selecting one there would compose no address either (outcome 3 must not offer it). nil
// when there is none, which is a container launch's ordinary answer.
func UnservedAdaptationsAt(selected []*Pack, addresses map[string]string, served ServedDaemons) []Adaptation {
	var out []Adaptation
	for _, a := range ServiceAdaptations(selected, addresses) {
		if !served.Serves(a.Service) {
			out = append(out, a)
		}
	}
	if !served.RunsDaemons() {
		out = append(out, ServiceAdaptations(unselectedEmbedded(selected), addresses)...)
	}
	return out
}

// ViaServedAt returns resolved with the via address cleared from every profile whose via
// service is not served at this notch, and every other via address at its served address, Via
// itself kept, and the names of the profiles it cleared, sorted. ViaURLFor, the one predicate both notches' derive paths ask "is this agent's
// via live?", then answers "" for those, so each agent keeps its own client. Via stays stated,
// so a reader of the table still sees which profiles route through a service, and the cleared
// names are what a launch discloses (P4: what a notch cannot do, it says).
func ViaServedAt(resolved map[string]ResolvedProfile, packs []*Pack,
	served ServedDaemons) (map[string]ResolvedProfile, []string) {
	if resolved == nil {
		return nil, nil
	}
	out := make(map[string]ResolvedProfile, len(resolved))
	var cleared []string
	for name, r := range resolved {
		if r.ViaBase != "" && !served.Serves(viaServiceName(packs, r.Via)) {
			r.ViaBase = ""
			cleared = append(cleared, name)
		}
		// A served via answers at its served address, which on a shared network namespace
		// is a port the launcher picked rather than the declared one.
		r.ViaBase = served.ServedURL(r.ViaBase)
		// A CARRIER this notch does not serve carries nobody here (carrier.go), and is not named
		// as cleared: the profile names no via to clear, and the profile line already says, per
		// agent, that the selection reaches nothing for an agent with no client of the platform.
		if r.Carrier != "" && !served.Serves(viaServiceName(packs, r.Carrier)) {
			r.Carrier, r.CarrierBase, r.Carried = "", "", nil
		}
		r.CarrierBase = served.ServedURL(r.CarrierBase)
		out[name] = r
	}
	sort.Strings(cleared)
	return out, cleared
}

// LaunchServes is a launch's own word on a pack env variable the gate withheld because no jail
// daemon at its notch serves it, from a launch that serves such a variable from a server of its
// own: `yolo host -- codex` runs its own refresh adapter and sets
// CODEX_REFRESH_TOKEN_URL_OVERRIDE to it (internal/openaiauthhost). served reports that the
// launch sets name itself, so name is not missing and goes unnamed. Otherwise why, when not "",
// is why the launch's own server did not start, which the line gives for name in place of the
// notch's clause (ServedDaemons.notServedWhy): that server, not the notch, decided it. A nil
// LaunchServes says nothing of any variable.
type LaunchServes func(name string) (served bool, why string)

// UnservedLines is what a launch says it withheld because nothing at its notch serves it, in
// the one wording every notch prints: a header line, then one indented line per pack env
// variable group the gate withheld (CredentialScope.UnservedEnvLines) and per profile whose via
// ViaServedAt cleared. nil when nothing was withheld, which is every container launch whose
// selected loopholes are enabled.
//
// byLaunch is the launch's own word on each withheld variable (LaunchServes), nil for none.
func UnservedLines(scope *CredentialScope, unservedVias []string, byLaunch LaunchServes) []string {
	return UnservedLinesWith(scope, unservedVias, nil, byLaunch)
}

// UnservedLinesWith is UnservedLines with the launch's own line for a via or carrier it cleared,
// keyed by profile, in place of the notch's: at `yolo host --` a via whose service the launch would
// start, and which the agent's own config files carry, is cleared because a host launch renders no
// per-launch file (docs/design/host-notch-services.md HS-D31), and the line, naming the profile and
// whether its via or its carrier was cleared, says so. A profile with no entry gets the notch's line.
func UnservedLinesWith(scope *CredentialScope, unservedVias []string, viaLines map[string]string,
	byLaunch LaunchServes) []string {
	details := scope.UnservedEnvLines(byLaunch)
	for _, profile := range unservedVias {
		if line := viaLines[profile]; line != "" {
			details = append(details, line)
			continue
		}
		details = append(details, "profile "+strconv.Quote(profile)+"'s via — its service does "+
			"not run here, so its agents keep their own clients rather than routing through it")
	}
	if len(details) == 0 {
		return nil
	}
	lines := []string{"Not set at this notch, because nothing here serves it " +
		"(docs/plans/notch-convergence.md §2.4):"}
	for _, d := range details {
		lines = append(lines, "  "+d)
	}
	return lines
}

// ViaService is the service of the pack named via that serves its via address, "" when that
// pack is not among packs or declares no service with a `via_address`: the service a profile
// naming via (or carried by via) routes through, and so the one whose launch-owned plan reserves
// the via address (internal/launchservice.NewPlan).
func ViaService(packs []*Pack, via string) string { return viaServiceName(packs, via) }

// viaServiceName is the service of pack via that serves a via address, "" when none does.
func viaServiceName(packs []*Pack, via string) string {
	for _, p := range packs {
		if p == nil || p.Name != via || p.Decl == nil {
			continue
		}
		for _, svc := range p.Decl.Services() {
			if svc.ViaAddress != "" {
				return svc.Name
			}
		}
	}
	return ""
}

// ViaWhatIf is what a launch that runs pack services as launch-owned host halves (a `yolo host
// --` launch, or a macos-user one) asks ViaRoutedServices with: the inputs its own provider
// composition and profile resolution read, and the agents it composes for.
type ViaWhatIf struct {
	// User is the user's `providers` entries, Addresses the user's adapter overrides, and Profiles
	// the user's profile declarations: the inputs ComposeProvidersAt and ResolveProfiles read.
	User      *jsonx.OrderedMap
	Packs     []*Pack
	Addresses map[string]string
	// Served is what the notch serves before it plans any service for a via.
	Served   ServedDaemons
	Profiles map[string]UserProfile
	// Active is each agent the launch composes for, keyed by CLI name, mapped to its selected
	// (primary) profile: the one ViaFor and ViaURLFor read.
	Active map[string]string
}

// ViaRouted is one pack service that would route agents through it once served, and the
// what-if tables that said so: the provider table and the resolved profiles composed with the
// service served, at its declared addresses.
type ViaRouted struct {
	Service string
	Pack    string
	// Agents are the agents serving the service would carry: each one's via or carrier names
	// the service and its config is RE-POINTED through it (ViaRePoints). A launch plans the
	// service only for these.
	Agents []string
	// NoEffect are the agents whose via or carrier names the service but whose config it
	// re-points nothing of (agy on bedrock-bridge, whose derive reads no via URL and whose
	// environment names no address of the bridge): serving the service would carry none of their
	// requests, so no launch starts it for them (ViaNoEffect says so), sorted.
	NoEffect  []string
	Providers *jsonx.OrderedMap
	Resolved  map[string]ResolvedProfile
}

// ViaRoutedServices is THE VIA TRIGGER of a launch that runs pack services as launch-owned host
// halves (docs/design/host-notch-services.md HS-D30; wire-bridge-gateway.md WG-I46): every held
// service that declares a `via_address`, that in.Served does not serve already, and that admit
// admits, for which a what-if composition with that service served names it as the route of some
// agent of in.Active, by its profile's own `via` or by its carrier (ResolvedProfile.ViaFor,
// WG-I44), in service order, each with the agents it routes and the ones it would not (Agents,
// NoEffect), sorted. A launch plans a service only when its Agents is non-empty.
//
// ROUTED MEANS RE-POINTED (docs/design/host-notch-services.md HS-D33; WG-I15's word, widened to
// the adapter address): an agent counts only when serving the service would carry its requests,
// its derived config carrying its via URL (DerivedViaPointers: pi, opencode, oh-omp, codex) or its
// environment naming an address of the service (ViaRePoints: claude and copilot, which ride the
// `for_via` adapter address). An agent whose profile names the via and whose config ignores it
// (agy) is NoEffect, so no launch starts a host process outside every sandbox that nothing
// reaches.
//
// BESIDE THE ADAPTER TRIGGER, NOT THROUGH IT. The adapter trigger is the credential gate's refusal
// (UnservedAdapterError), and a via refuses nothing: ViaServedAt clears a via whose service is
// not served, and an adaptation a via profile rides (the `for_via` address, WG-I39) composes no
// mark at all while the service is unserved, so the carrier itself exists only once the service
// is served. Hence the what-if: the one question "if this notch served the service, would this
// selection ride it?", answered by the composition and resolution every notch runs, so the
// launch and `yolo check`'s prediction of it ask the same thing.
//
// admit is the launch's admission (internal/launchservice.Admit, which this package cannot
// import): a service whose host half no launch may run is no candidate. An agent no selected
// pack installs routes nothing (ActiveVias' rule, WG-I10).
func ViaRoutedServices(in ViaWhatIf, admit func(service string) bool) ([]ViaRouted, error) {
	if len(in.Active) == 0 {
		return nil, nil
	}
	agents := make([]string, 0, len(in.Active))
	for agent, profile := range in.Active {
		if profile != "" && binOwner(in.Packs, agent) != nil {
			agents = append(agents, agent)
		}
	}
	sort.Strings(agents)
	if len(agents) == 0 {
		return nil, nil
	}
	held, _ := HeldServices(in.Packs)
	var out []ViaRouted
	for _, h := range held {
		name := h.Service.Name
		if h.Service.ViaAddress == "" || viaServiceName(in.Packs, h.Pack) != name ||
			in.Served.Serves(name) || (admit != nil && !admit(name)) {
			continue
		}
		served := in.Served.Plus(ServedByLaunch([]string{name}))
		providers, _, err := ComposeProvidersAt(in.User, in.Packs, in.Addresses, served)
		if err != nil {
			return nil, err
		}
		resolved, err := ResolveProfiles(in.Packs, in.Profiles, providers)
		if err != nil {
			return nil, err
		}
		addresses := serviceHostPorts(in.Packs, in.Addresses, served, h.Pack, name)
		var routed, inert []string
		for _, agent := range agents {
			r := resolved[in.Active[agent]]
			if via, _ := r.ViaFor(agent); via == "" || viaServiceName(in.Packs, via) != name ||
				ViaURLFor(r, agent) == "" {
				continue
			}
			// A derive that cannot run is counted as routed, as before this reading: the launch's
			// via gate names it, and the boot runs the same derive and refuses over it.
			if moved, err := ViaRePoints(in.Packs, providers, in.Active, resolved, agent, addresses); err != nil || moved {
				routed = append(routed, agent)
			} else {
				inert = append(inert, agent)
			}
		}
		if len(routed) > 0 || len(inert) > 0 {
			out = append(out, ViaRouted{Service: name, Pack: h.Pack, Agents: routed, NoEffect: inert,
				Providers: providers, Resolved: resolved})
		}
	}
	return out, nil
}

// ViaRePoints reports whether agent's config, under the tables a launch composed, is RE-POINTED
// through the service whose served addresses are addresses (`host:port`s) by its via or its
// carrier: some derive carries its via URL (DerivedViaPointers), or its env derive's output (the
// Shape a launch composes for it, AgentDelivery.Shape) names one of addresses, an occurrence no
// further digit follows. The second is how claude and copilot ride a via or a carrier: their
// derive reads ctx.via_url only as a gate and points them at the `for_via` adapter address
// (WG-I39). false for an agent with no via URL. A derive error is returned, the agent unread.
func ViaRePoints(packs []*Pack, providers *jsonx.OrderedMap, useProfiles map[string]string,
	resolved map[string]ResolvedProfile, agent string, addresses []string) (bool, error) {
	pointers, err := DerivedViaPointers(packs, providers, useProfiles, resolved, agent)
	if err != nil || len(pointers) > 0 {
		return len(pointers) > 0, err
	}
	if len(addresses) == 0 {
		return false, nil
	}
	named, err := derivedPointers(packs, providers, useProfiles, resolved, agent, func(_, v string) bool {
		return namesHostPort(v, addresses)
	})
	if err != nil {
		return false, err
	}
	for _, p := range named {
		if p.Surface == "" {
			return true, nil
		}
	}
	return false, nil
}

// serviceHostPorts is the `host:port` of every address service (of pack) serves under served:
// each adaptation of it at the user's override and at its declared address, and its via address,
// each at its served address (ServedDaemons.ServedURL), deduplicated.
func serviceHostPorts(packs []*Pack, addresses map[string]string, served ServedDaemons, pack, service string) []string {
	var raw []string
	for _, over := range []map[string]string{addresses, nil} {
		for _, a := range ServiceAdaptations(packs, over) {
			if a.Service == service {
				raw = append(raw, a.Address)
			}
		}
	}
	if viaServiceName(packs, pack) == service {
		if via, _ := ViaServiceAddress(packs, pack); via != "" {
			raw = append(raw, via)
		}
	}
	var out []string
	for _, r := range raw {
		u, err := url.Parse(served.ServedURL(r))
		if err != nil || u.Host == "" || slices.Contains(out, u.Host) {
			continue
		}
		out = append(out, u.Host)
	}
	return out
}

// namesHostPort reports whether value names one of hostPorts: an occurrence of it that no further
// digit follows, so 127.0.0.1:4313 is not named by a URL on 127.0.0.1:43137.
func namesHostPort(value string, hostPorts []string) bool {
	for _, hp := range hostPorts {
		for rest := value; ; {
			i := strings.Index(rest, hp)
			if i < 0 {
				break
			}
			rest = rest[i+len(hp):]
			if rest == "" || rest[0] < '0' || rest[0] > '9' {
				return true
			}
		}
	}
	return false
}

// Route is how profile, resolved in the what-if, reaches r's service: "via" for its own `via`, or
// `carrier "<pack>"` for the carrier of an agent with no client of the platform (WG-I44).
func (r ViaRouted) Route(profile string) string {
	if rp := r.Resolved[profile]; rp.Via == "" && rp.Carrier != "" {
		return "carrier " + strconv.Quote(rp.Carrier)
	}
	return "via"
}

// NoEffectLines is the line for each agent of r.NoEffect, on its profile in active (the
// what-if's agent-to-profile table): what a launch that plans r's service for none of its agents
// says instead, and what `yolo check` predicts it says (ViaNoEffect).
func (r ViaRouted) NoEffectLines(active map[string]string) []string {
	var out []string
	for _, agent := range r.NoEffect {
		route := r.Route(active[agent])
		out = append(out, fmt.Sprintf("profile %q (active for %s): its %s — %s", active[agent], agent,
			route, ViaNoEffect(route, agent, r.Service)))
	}
	return out
}

// ViaNoEffect is the clause a launch that runs pack services as launch-owned host halves gives an
// agent of ViaRouted.NoEffect: route is "via", or `carrier "<pack>"` for a profile's carrier. The
// service is not started for it, and nothing about the agent changes, so the clause names no next
// step: no notch routes such an agent through the service, a container's included.
func ViaNoEffect(route, agent, service string) string {
	return fmt.Sprintf("%s's config does not point it at the %q service, so the %s has no effect on "+
		"%s (its config is what it would be without it) and the service is not started for it "+
		"(docs/design/host-notch-services.md HS-D30)", agent, service, route, agent)
}

// FileCarriedVia is the config files through which agent's own derived config carries its via URL
// under the what-if tables of r (DerivedViaPointers, every pointer with a Surface), each the
// surface's declared path (its name when no selected pack declares it), sorted and deduplicated;
// nil when only its environment does, or nothing. A notch that renders no per-launch file cannot
// point such an agent at a per-launch address: `yolo host --` keeps its via cleared and says why
// (docs/design/host-notch-services.md HS-D31, OQ-HS3).
func FileCarriedVia(packs []*Pack, r ViaRouted, agent string, useProfiles map[string]string) ([]string, error) {
	pointers, err := DerivedViaPointers(packs, r.Providers, useProfiles, r.Resolved, agent)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		surfaces, _ := p.Surfaces()
		for _, sf := range surfaces {
			if sf.Agent == agent && sf.Path != "" {
				paths[sf.Name] = sf.Path
			}
		}
	}
	var out []string
	for _, ptr := range pointers {
		if ptr.Surface == "" {
			continue
		}
		file := ptr.Surface
		if path := paths[ptr.Surface]; path != "" {
			file = path
		}
		if !slices.Contains(out, file) {
			out = append(out, file)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ServiceCredentialVars is what a launch hands a launch-owned service it starts for agent beyond
// that agent's env_sources (AgentDelivery.EnvSources), each value as the credential gate composed
// it for agent (CredentialScope.EnvFor): every pack env pointer of agent's fold `served_by` a
// daemon this notch serves (a credential doorway's pointer and its scoped caller token, aws-auth's
// AWS_CONTAINER_CREDENTIALS_FULL_URI and AWS_CONTAINER_AUTHORIZATION_TOKEN for an agent on Bedrock),
// and every region variable a selected pack declares (`region_env_name`) for the platform of a
// provider of agent's active set. That is the part of an agent's own key channel that a jail's
// daemon reads from the agent's env file (internal/wirebridged's keyFor) and that env_sources does
// not carry, so the wire bridge's host half signs a via or carrier route as a jail's bridge does
// (docs/design/host-notch-services.md HS-D32). Never another agent's: the gate composed each value
// for agent alone. nil when none applies.
func ServiceCredentialVars(scope *CredentialScope, providers *jsonx.OrderedMap, agent string) map[string]string {
	d := scope.Agent(agent)
	if d == nil {
		return nil
	}
	var names []string
	for _, e := range scope.FoldFor(agent) {
		if e.ServedBy != "" && !slices.Contains(names, e.Key) {
			names = append(names, e.Key)
		}
	}
	reqs := regionRequirements(scope.packs)
	for _, provider := range SetProvidersOf(d) {
		for _, v := range reqs[entryString(providerEntry(providers, provider), "platform")].vars {
			if !slices.Contains(names, v) {
				names = append(names, v)
			}
		}
	}
	comp := scope.EnvFor(agent)
	var out map[string]string
	for _, name := range names {
		if e, ok := comp.Lookup(name); ok && !e.Unset && e.Value != "" {
			if out == nil {
				out = map[string]string{}
			}
			out[name] = e.Value
		}
	}
	return out
}
