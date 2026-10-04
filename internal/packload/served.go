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
// such a daemon serves is SERVED exactly when the daemon is.
//
// WHY ONE PREDICATE. Each notch used to answer "does anything listen there?" its own way. The
// host composed no adapter address (a WithoutServiceAdaptations option) and cleared every via
// (ViaInert). macos-user composed the container's table and launched claude against
// `127.0.0.1:8214`, which nothing on it serves. And pack env pointed Codex and the AWS SDKs at
// `:1460` and `:1461` at every notch. One description meant three things. Now the notch is an
// INPUT, the set below, and every composer asks the same question of it: an address nothing
// serves is left out, and a pairing that needed it refuses by name (ES-D18, generalized).

import (
	"net/url"
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
	// is disabled, or the front door owns no process lifetime to open a doorway for.
	notServed map[string]string
}

// AtHost returns s marked as the host notch's set (the host field says what that changes).
func (s ServedDaemons) AtHost() ServedDaemons {
	s.host = true
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
// reads loopholes.ServedJailDaemons). The caller passes that split's names, so the launch and
// `yolo check`'s prediction of it build the set from the one selection.
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
	out := ServedDaemons{runs: s.runs || o.runs, host: s.host || o.host, names: map[string]bool{}}
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
	served ServedDaemons) (*jsonx.OrderedMap, []Adaptation, error) {
	table, err := ComposeProviders(user, packs, WithAdapterAddresses(addresses), WithServed(served))
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
	details := scope.UnservedEnvLines(byLaunch)
	for _, profile := range unservedVias {
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
