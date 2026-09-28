package packload

// served.go is the ONE "served at this notch" predicate (docs/plans/notch-convergence.md §4 item
// 2, NC-D16) and the one provider-composition helper every notch calls with it.
//
// SERVED AT THIS NOTCH is a term this file coins. A jail daemon — a loophole's `jail_daemon` or a
// pack service's — is served at a notch when that notch runs it: a container runtime runs every
// daemon in its launch's composed payload (internal/cli/run's jailDaemonsFor), while macos-user,
// which has no in-jail supervisor, and the host notch, which runs no jail daemon at all, run none.
// An address such a daemon serves is SERVED exactly when the daemon is.
//
// WHY ONE PREDICATE. Each notch used to answer "does anything listen there?" its own way. The
// host composed no adapter address (a WithoutServiceAdaptations option) and cleared every via
// (ViaInert). macos-user composed the container's table and launched claude against
// `127.0.0.1:8214`, which nothing on it serves. And pack env pointed Codex and the AWS SDKs at
// `:1460` and `:1461` at every notch. One description meant three things. Now the notch is an
// INPUT, the set below, and every composer asks the same question of it: an address nothing
// serves is left out, and a pairing that needed it refuses by name (ES-D18, generalized).

import (
	"sort"
	"strconv"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// ServedDaemons is the set of jail daemons served at one notch. The zero value serves nothing,
// which is the host's and macos-user's answer.
type ServedDaemons struct {
	// runs is whether this notch runs jail daemons at all. It is what separates "this daemon
	// is not in the payload" (a container launch that did not enable it) from "no daemon
	// runs here" (the host, macos-user): only at a notch that runs them would selecting
	// another service pack serve its address.
	runs  bool
	names map[string]bool
}

// ServedAtContainer is the set a container runtime serves: every jail daemon its composed
// payload names.
func ServedAtContainer(names []string) ServedDaemons {
	s := ServedDaemons{runs: true, names: map[string]bool{}}
	for _, n := range names {
		s.names[n] = true
	}
	return s
}

// NothingServed is the set a notch that runs no jail daemon serves: the host, and macos-user.
func NothingServed() ServedDaemons { return ServedDaemons{} }

// ServedAtRuntime is the served set of a jail launch on runtime rt whose composed payload names
// the daemons in names: every one of them on a container runtime, and none on macos-user,
// which has no in-jail supervisor and declines every one. The one place the jail notches'
// answer differs, so the launch (internal/cli/run) and `yolo check`'s prediction of it ask the
// same function.
func ServedAtRuntime(rt string, names []string) ServedDaemons {
	if rt == "macos-user" {
		return NothingServed()
	}
	return ServedAtContainer(names)
}

// ServiceJailDaemonNames is the name of every selected pack service that declares a jail
// daemon, sorted: the services a container launch's payload runs. A service with no jail
// daemon runs nowhere in this build.
func ServiceJailDaemonNames(packs []*Pack) []string {
	var out []string
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, s := range p.Decl.Services() {
			if s.JailDaemon != nil && len(s.JailDaemon.Cmd) > 0 {
				out = append(out, s.Name)
			}
		}
	}
	sort.Strings(out)
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
// service is not served at this notch, Via itself kept, and the names of the profiles it
// cleared, sorted. ViaURLFor, the one predicate both notches' derive paths ask "is this agent's
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
		out[name] = r
	}
	sort.Strings(cleared)
	return out, cleared
}

// UnservedLines is what a launch says it withheld because nothing at its notch serves it, in
// the one wording every notch prints: a header line, then one indented line per pack env
// variable group the gate withheld (CredentialScope.UnservedEnvLines) and per profile whose via
// ViaServedAt cleared. nil when nothing was withheld, which is every container launch whose
// selected loopholes are enabled.
//
// servedByLaunch reports a variable the launch sets itself from a server of its own, which is
// therefore not missing: `yolo host -- codex` runs its own refresh adapter and sets
// CODEX_REFRESH_TOKEN_URL_OVERRIDE to it (internal/openaiauthhost). nil for none.
func UnservedLines(scope *CredentialScope, unservedVias []string, servedByLaunch func(string) bool) []string {
	details := scope.UnservedEnvLines(servedByLaunch)
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
