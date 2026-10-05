package run

// macosuserservices.go is the macos-user launch's LAUNCH-OWNED SERVICES
// (docs/design/host-notch-services.md §4.7; docs/plans/notch-convergence.md OQ-NC1, ruled A).
// That backend has no network namespace, so a pack service that serves an agent's pairing reaches
// it the way it reaches `yolo host`: its host half runs as this launch's child, outside Seatbelt,
// on a loopback port this launch picked, answering only this launch's caller token, supervised
// while the sandboxed command runs (restarted on the same port under its restart policy, HS-D28),
// and stopped when the command exits. The mechanism is internal/launchservice, the one the host
// uses: admission (the host half of a pack yolo ships or a local one, named `yolo`; HS-D27), the
// plan (ports and token), the start, the supervision and the stop.
//
// THE TRIGGER is the credential gate's own refusal: a profiled agent's pairing through a pack
// service's adaptation refuses while nothing serves it (packload.UnservedAdapterError), and the
// channel then plans that service and composes again (composePackChannel's retry). Every profiled
// agent counts here, not only the launched one, because this backend writes every profiled
// agent's env file (writeMacosUserAgentEnvFiles) for a shell that starts one later.
//
// AND A VIA OR A CARRIER (HS-D30): a profiled agent whose profile's `via`, or whose carrier
// (docs/design/wire-bridge-gateway.md WG-I44), routes it through a pack service is a pairing the
// gate refuses nothing for, since a via whose service is not served is only cleared. So the
// channel asks first whether serving the service would route some agent through it
// (packload.ViaRoutedServices, the what-if both notches ask), and plans it when it would, before
// the composition, which then serves the via at the port the plan picked. Every profiled agent
// counts, a file-carried via included (pi's models.json, opencode's opencode.json): this backend
// renders those files per launch, into the one account home every workspace's session shares, so
// a second concurrent session of the same agent rewrites the file with its own launch's address
// and the first session's next start reads it (INFERRED, not measured: HS-D31).
//
// AND A PURE WORKER WITH NO JAIL DAEMON (HS-D29; a held service no adaptation names): its host
// half starts here too, beside the command, since nothing else would run it, and the channel is
// composed against it, so a pack env pointer at it reaches the command. A worker that declares a
// jail daemon runs that, confined, in the guest instead (planMacosUserWorkers).

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/wirebridged"
)

// launchedService is a started launch-owned service, as the macos-user arm uses it.
type launchedService interface {
	Stop()
	PID() int
	// Supervise watches it while the sandboxed command runs (launchservice.Running.Supervise):
	// a death is named, and the service restarted on its address under its policy (HS-D28).
	Supervise(agent string, w io.Writer, prefix string)
}

// macosUserSupervisionPrefix leads every line a launch-owned service's supervision prints on
// macos-user: a death and a restart arrive while the sandboxed command owns the terminal, so the
// line says who is speaking.
const macosUserSupervisionPrefix = "yolo: "

// macosUserCommandName is what a supervision line calls the sandboxed command: the command after
// `--`, or, for a bare `yolo`, the login shell the sandbox opens.
func (o *Options) macosUserCommandName() string {
	if len(o.Args) > 0 && o.Args[0] != "" {
		return filepath.Base(o.Args[0])
	}
	return "the sandboxed shell"
}

// noteMacosUserLocalHostCode is the disclosure of host code from a local pack, printed BEFORE it
// starts outside the sandbox (launchservice.Declared.Local; HS-D27): a pack yolo does not ship,
// whose argv is the user's own code, run as them on the Mac. what names the declaration ("the
// \"x\" service's host half"). Never suppressible (docs/reference/report-tiers.md OQ-RO3: the exec
// banner is the trust boundary); silent for a pack yolo ships, whose start line names it.
func (o *Options) noteMacosUserLocalHostCode(plan *launchservice.Plan, what string) {
	if !plan.Local {
		return
	}
	o.pr(o.Stderr).print(fmt.Sprintf("[bold yellow]This launch runs pack code on your machine, outside "+
		"the sandbox:[/bold yellow] [yellow]%s from pack %q, a local pack yolo does not ship: %s[/yellow]",
		richtext.Escape(what), plan.Pack, richtext.Escape(shquote.Join(plan.Cmd))))
}

// startMacosUserService starts one launch-owned service and returns it and its log; a var so a
// test can observe the start without spawning one.
var startMacosUserService = func(plan *launchservice.Plan, env map[string]string) (launchedService, string, error) {
	r, err := launchservice.Start(plan, env)
	if err != nil {
		return nil, "", err
	}
	return r, r.Log, nil
}

// composedProvidersFor is composedProviders for this launch: a planned launch-owned service's
// conversions are composed at the ports this launch picked, so a user's `adapters` override of
// them does not apply (OQ-HS4).
func (o *Options) composedProvidersFor(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	served packload.ServedDaemons) (*jsonx.OrderedMap, []packload.Adaptation, error) {
	if len(o.launchServices) == 0 {
		return composedProviders(cfg, packs, served)
	}
	addresses, _ := config.LoadAdapterAddresses(nil)
	addresses = launchservice.WithoutOverrides(addresses, packs, launchservice.Names(o.launchServices))
	return packload.ComposeProvidersAt(cfgMap(cfg, "providers"), packs, addresses, served)
}

// planMacosUserService answers a channel composition that refused: added reports that it planned
// the service a pairing needs, so the composition should run again. A refusal it cannot answer
// (a pack nothing selects, a provider no pack ships) is left as it is. One it can answer only
// with a reason (a service with no host half, or one whose pack yolo does not ship) is returned
// with that reason added, which is HS-D5's refusal on this backend.
func (o *Options) planMacosUserService(err error, packs []*packload.Pack) (bool, error) {
	var e *packload.UnservedAdapterError
	if !errors.As(err, &e) || !e.Selected || e.ProviderPack != "" {
		return false, nil
	}
	for _, p := range o.launchServices {
		if p.Service == e.Adaptation.Service {
			return false, nil
		}
	}
	d, aerr := launchservice.Admit(packs, e.Adaptation.Service)
	if aerr != nil {
		why := aerr.Error()
		var adm *launchservice.AdmissionError
		if errors.As(aerr, &adm) {
			why = adm.Why
		}
		return false, fmt.Errorf("%w — and this macos-user launch cannot start the %q service's "+
			"host half: %s", err, e.Adaptation.Service, why)
	}
	plan, perr := launchservice.NewPlan(packs, d)
	if perr != nil {
		return false, perr
	}
	o.launchServices = append(o.launchServices, plan)
	if o.launchServiceAgents == nil {
		o.launchServiceAgents = map[string][]string{}
	}
	o.launchServiceAgents[plan.Service] = append(o.launchServiceAgents[plan.Service], e.Agent)
	return true, nil
}

// planMacosUserViaServices adds to this launch's launch-owned services every pack service a via
// or a carrier routes a profiled agent through once served (packload.ViaRoutedServices,
// docs/design/host-notch-services.md HS-D30): the what-if composes the provider table and resolves
// the profiles with the service served, over the launch's own inputs and served set (served), and
// a service whose admitted host half would carry an agent is planned (launchservice.NewPlan, its
// adaptations' and its via address's ports reserved). Called before the channel composes, so the
// first composition already serves the via; a service this launch serves already, by an earlier
// plan or in its guest, is no candidate. Every profiled agent counts (the package doc says why,
// and what a concurrent session risks).
//
// A service whose via or carrier re-points none of the agents it names (ViaRouted.NoEffect only:
// agy on bedrock-bridge) is not planned, so no host process starts outside the sandbox that no
// agent reaches; notes names each such agent, for the arm to print where it reports what runs
// outside the sandbox (noteMacosUserWorkers).
func (o *Options) planMacosUserViaServices(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	profiles *jsonx.OrderedMap, userProfiles map[string]packload.UserProfile,
	served packload.ServedDaemons) (notes []string, err error) {
	addresses, _ := config.LoadAdapterAddresses(nil)
	active := packload.ProfileTable(profiles)
	routed, err := packload.ViaRoutedServices(packload.ViaWhatIf{User: cfgMap(cfg, "providers"), Packs: packs,
		Addresses: addresses, Served: served, Profiles: userProfiles, Active: active},
		func(service string) bool {
			_, aerr := launchservice.Admit(packs, service)
			return aerr == nil
		})
	if err != nil {
		return nil, err
	}
	for _, r := range routed {
		if o.launchServiceRunning(r.Service) {
			continue
		}
		if len(r.Agents) == 0 {
			for _, line := range r.NoEffectLines(active) {
				notes = append(notes, "[yellow]Warning: "+richtext.Escape(line)+"[/yellow]")
			}
			continue
		}
		d, aerr := launchservice.Admit(packs, r.Service)
		if aerr != nil {
			continue // ViaRoutedServices admitted it; nothing changed in between
		}
		plan, perr := launchservice.NewPlan(packs, d)
		if perr != nil {
			return nil, perr
		}
		o.launchServices = append(o.launchServices, plan)
		if o.launchServiceAgents == nil {
			o.launchServiceAgents = map[string][]string{}
		}
		o.launchServiceAgents[plan.Service] = append(o.launchServiceAgents[plan.Service], r.Agents...)
	}
	return notes, nil
}

// checkServedViaRoutes is the via gate (wirebridged.ViaRouteGate, WG-I13 to WG-I15) over the tables
// a macos-user channel composed once it planned its launch-owned services: a re-pointed via agent
// the service will serve no route for refuses the launch, and a route without the wire the agent
// prefers, or a via that re-points nothing, is a warning, in checkViaRoutes' words, which that
// pre-flight could not give on this backend because it runs before the channel plans a service
// (HS-D30). A container's pre-flight sees its jail daemon served and asks there.
func (o *Options) checkServedViaRoutes(packs []*packload.Pack, channel *packChannel) error {
	refusals, notices := wirebridged.ViaRouteGate(packs, channel.providers,
		packload.ProfileTable(channel.profiles), channel.resolvedProfiles)
	for _, n := range notices {
		o.pr(o.Stderr).print("[yellow]Warning: " + n + "[/yellow]")
	}
	if len(refusals) == 0 {
		return nil
	}
	msgs := make([]string, len(refusals))
	for i, r := range refusals {
		msgs[i] = r.Error()
	}
	return fmt.Errorf("packs: %s", strings.Join(msgs, "\npacks: "))
}

// serviceAgents is every profiled agent of channel that plan's service carries: the ones whose
// pairing planned it (launchServiceAgents), every one whose delivery points it at an address the
// plan moved (an adapter pairing the service serves, whichever trigger planned it), and every one
// whose via or carrier routes it through the service (viaURLsThrough). The agents whose credentials
// the service is handed, as a jail's bridge reads each served agent's own env file.
func (o *Options) serviceAgents(plan *launchservice.Plan, channel *packChannel) []string {
	agents := append([]string(nil), o.launchServiceAgents[plan.Service]...)
	if channel == nil || channel.scope == nil {
		return agents
	}
	via := viaURLsThrough(channel, plan.Service)
	for _, agent := range channel.scope.Agents() {
		if slices.Contains(agents, agent) {
			continue
		}
		if _, routed := via[agent]; routed || plan.RoutesAny(nil, channel.scope.Agent(agent)) {
			agents = append(agents, agent)
		}
	}
	slices.Sort(agents)
	return agents
}

// viaURLsThrough is, keyed by agent, the via URL the channel composed for each profiled agent
// whose via or carrier routes it through service (packload.ResolvedProfile.ViaFor, ViaURLFor) and
// whose own derived config carries that URL (packload.DerivedViaPointers: the agent is RE-POINTED,
// wire-bridge-gateway.md WG-I15). An agent its carrier routes by an adapter address instead
// (copilot, whose derive reads the via URL only as a gate) is not one: its route is the address
// its delivery's Shape names.
func viaURLsThrough(channel *packChannel, service string) map[string]string {
	out := map[string]string{}
	if channel == nil {
		return out
	}
	use := packload.ProfileTable(channel.profiles)
	for agent, profile := range use {
		r := channel.resolvedProfiles[profile]
		if via, _ := r.ViaFor(agent); via == "" || packload.ViaService(channel.packs, via) != service {
			continue
		}
		u := packload.ViaURLFor(r, agent)
		if u == "" {
			continue
		}
		if pointers, err := packload.DerivedViaPointers(channel.packs, channel.providers, use,
			channel.resolvedProfiles, agent); err == nil && len(pointers) > 0 {
			out[agent] = u
		}
	}
	return out
}

// launchServiceInput is what a macos-user launch hands each launch-owned service: the channel's
// three wire tables, the host broker's private socket, and, for each agent the service carries
// (serviceAgents), the env_sources the credential gate delivers to it for its providers only
// (AgentDelivery.EnvSources) and the doorway pointers and region variables the gate composed for it
// (packload.ServiceCredentialVars, HS-D32): what a jail's bridge reads from that agent's own env
// file. Into the service's input alone; the agent's own environment is the gate's, unchanged. The
// caller token is added by launchservice.Start.
func (c *packChannel) launchServiceInput(agents []string) map[string]string {
	env := map[string]string{
		entrypoint.ProvidersWireEnv:   jsonDumpsOrEmptyObj(c.providers),
		entrypoint.ProfilesWireEnv:    jsonDumpsOrEmptyObj(packload.ProfilesWireTable(c.resolvedProfiles)),
		entrypoint.UseProfilesWireEnv: jsonDumpsOrEmptyObj(c.profiles),
		openauthclient.HostSocketEnv:  openaiauthhost.HostSocketPath(),
	}
	for _, agent := range agents {
		d := c.scope.Agent(agent)
		if d == nil {
			continue
		}
		for k, v := range packload.ServiceCredentialVars(c.scope, c.providers, agent) {
			env[k] = v
		}
		if d.EnvSources == nil {
			continue
		}
		for _, k := range d.EnvSources.Keys() {
			if v, _ := d.EnvSources.Get(k); v != nil {
				if s, ok := v.(string); ok {
					env[k] = s
				}
			}
		}
	}
	return env
}

// startMacosUserServices starts every planned launch-owned service and says so, one line each,
// on every launch (a launch has no quiet mode, and this is host code running outside Seatbelt).
// It returns the stop for the arm to defer, or the refusal naming the service that did not start.
//
// EACH ONE IS SUPERVISED FROM ITS START (launchservice.Running.Supervise, HS-D28), on this arm's
// stderr, so a service that dies while the sandboxed command runs is named and restarted on the
// address the command was pointed at; the arm's deferred stop is its own teardown and says
// nothing. A local pack's host half is named, argv and all, before it starts.
func (o *Options) startMacosUserServices(channel *packChannel) (func(), error) {
	var running []launchedService
	stop := func() {
		for _, r := range running {
			r.Stop()
		}
	}
	for _, plan := range o.launchServices {
		o.noteMacosUserLocalHostCode(plan, fmt.Sprintf("the %q service's host half", plan.Service))
		r, log, err := startMacosUserService(plan, channel.launchServiceInput(o.serviceAgents(plan, channel)))
		if err != nil {
			stop()
			return func() {}, err
		}
		running = append(running, r)
		if isWorkerPlan(plan) {
			o.pr(o.Stderr).print(fmt.Sprintf("Started the %q service (pack %q, pid %d) for this launch, "+
				"outside the sandbox: %s, handed only this launch's caller token, and stopped when the "+
				"command exits. Its log: %s", plan.Service, plan.Pack, r.PID(),
				workerPointedAt(channel, plan.Service), log))
		} else {
			o.pr(o.Stderr).print(fmt.Sprintf("Started the %q service (pack %q, pid %d) on %s for this "+
				"launch, outside the sandbox: it answers only this launch's caller token and stops "+
				"when the command exits. Its log: %s", plan.Service, plan.Pack, r.PID(),
				strings.Join(o.servicePointedAt(plan, channel), ", "), log))
		}
		r.Supervise(o.macosUserCommandName(), o.Stderr, macosUserSupervisionPrefix)
	}
	return stop, nil
}

// isWorkerPlan reports whether plan is a pure worker's (planMacosUserWorkers): one no address
// moved for, since no adaptation names its service.
func isWorkerPlan(plan *launchservice.Plan) bool { return len(plan.Moved) == 0 }

// servicePointedAt is the addresses of plan its agents were pointed at, the only routes it opens:
// launchservice.Plan.RoutedAt over the channel's delivery to each agent the service carries
// (serviceAgents, whichever trigger planned it) and the via URLs of the agents whose config the
// via re-points, the one reading the start line, the dry run's line and `yolo host --` share
// (docs/design/host-notch-services.md HS-D24). A pure worker's plan has none of the launch's, which
// the dry run's line says in words rather than as an empty list (workerPointedAt).
func (o *Options) servicePointedAt(plan *launchservice.Plan, channel *packChannel) []string {
	if isWorkerPlan(plan) {
		return []string{"no address of the launch's: " + workerPointedAt(channel, plan.Service)}
	}
	// Every agent the service carries (serviceAgents, the set whose credentials it is handed), not
	// only the one whose pairing planned it: a bridge the via trigger planned for pi also serves
	// claude's adapter pairing, which then planned nothing of its own.
	var deliveries []*packload.AgentDelivery
	for _, agent := range o.serviceAgents(plan, channel) {
		deliveries = append(deliveries, channel.scope.Agent(agent))
	}
	// A via route the agent's config file carries is named by its via URL, which no Shape holds
	// (launchservice.Plan.RoutedAt, HS-D30).
	var viaURLs []string
	for _, u := range viaURLsThrough(channel, plan.Service) {
		viaURLs = append(viaURLs, u)
	}
	return plan.RoutedAt(viaURLs, deliveries...)
}

// workerPointedAt is how a pure worker's start line and dry-run line name who reaches it: the pack
// env variables the channel composed `served_by` it, in the shared fold or any profiled agent's
// (packload.PointersAt), or, with none, that no agent is pointed at it.
func workerPointedAt(channel *packChannel, service string) string {
	var vars []string
	if channel != nil {
		for _, agent := range append([]string{""}, channel.scope.Agents()...) {
			for _, v := range packload.PointersAt(channel.scope.FoldFor(agent), service) {
				if !slices.Contains(vars, v) {
					vars = append(vars, v)
				}
			}
		}
	}
	if len(vars) == 0 {
		return "a worker no agent is pointed at"
	}
	slices.Sort(vars)
	return "a worker this launch points its agents at through " + strings.Join(vars, ", ")
}

// planMacosUserWorkers adds to this launch's launch-owned services every PURE WORKER whose host
// half it runs outside the sandbox (docs/design/host-notch-services.md HS-D29; a held service no
// adaptation names, so no pairing plans it): one that declares a host half and NO jail daemon,
// that admission admits (launchservice.Admit: a pack yolo ships or a local one), and that the
// selection's gate asks for, read off the channel's own selection
// (packload.UnselectedProfileServedDaemons, the jail payload's filter). A worker that declares a
// jail daemon is not planned: the guest runs that half, confined
// (docs/design/jail-daemon-on-macos-user-plan.md JD-9, confinement preferred). added reports that
// it planned one this launch had not.
//
// One with no jail daemon this launch does not start is named in notes, with why and the next
// step, because nothing else runs it on this backend: a refused host half (the fetched refusal
// names a local checkout), or a gate no agent's selection delivers. Called by composePackChannel
// once a composition succeeds, which composes again when added, so a worker is in the served set
// and the caller tokens the channel composes its pointers with, as a container's jail daemon is;
// the notes ride the channel to planMacosUserDoorways, which prints them where the arm reports
// what runs outside the sandbox.
func (o *Options) planMacosUserWorkers(packs []*packload.Pack, channel *packChannel) (added bool, notes []string) {
	adapts := map[string]bool{}
	for _, a := range packload.ServiceAdaptations(packs, nil) {
		adapts[a.Service] = true
	}
	ungated := map[string]packload.ProfileServedDaemon{}
	if channel != nil {
		sel := packload.SelectionOfSets(packload.ProfileSets(channel.profiles), channel.resolvedProfiles, channel.providers)
		for _, u := range packload.UnselectedProfileServedDaemons(packs, sel) {
			ungated[u.Name] = u
		}
	}
	held, _ := packload.HeldServices(packs)
	for _, h := range held {
		s := h.Service
		if adapts[s.Name] || o.launchServiceRunning(s.Name) ||
			s.HostDaemon == nil || len(s.HostDaemon.Cmd) == 0 ||
			(s.JailDaemon != nil && len(s.JailDaemon.Cmd) > 0) {
			continue
		}
		if u, gated := ungated[s.Name]; gated {
			notes = append(notes, fmt.Sprintf("[dim]Not started: the %q service's host half (pack %q), "+
				"because %s, which it serves; select one to start it.[/dim]", s.Name, h.Pack,
				richtext.Escape(unselectedGates(u))))
			continue
		}
		d, err := launchservice.Admit(packs, s.Name)
		if err != nil {
			why := err.Error()
			var adm *launchservice.AdmissionError
			if errors.As(err, &adm) {
				why = adm.Why
			}
			notes = append(notes, fmt.Sprintf("[yellow]Not started outside the sandbox: the %q service's "+
				"host half (pack %q): %s. It declares no jail daemon, so nothing runs it this launch."+
				"[/yellow]", s.Name, h.Pack, richtext.Escape(why)))
			continue
		}
		plan, err := launchservice.NewPlan(packs, d)
		if err != nil {
			notes = append(notes, fmt.Sprintf("[yellow]Not started outside the sandbox: the %q service's "+
				"host half (pack %q): %s.[/yellow]", s.Name, h.Pack, richtext.Escape(err.Error())))
			continue
		}
		o.launchServices = append(o.launchServices, plan)
		added = true
	}
	return added, notes
}

// noteMacosUserWorkers prints the channel's line for each pure worker this launch does not start
// (packChannel.workerNotes, planMacosUserWorkers), after the line for each agent whose via starts
// nothing because it re-points nothing of the agent (packChannel.viaNotes,
// planMacosUserViaServices): a disclosure, so no quiet switch (docs/reference/report-tiers.md
// OQ-RO3). Silent when none.
func (o *Options) noteMacosUserWorkers(channel *packChannel) {
	if channel == nil {
		return
	}
	for _, line := range append(append([]string(nil), channel.viaNotes...), channel.workerNotes...) {
		o.pr(o.Stderr).print(line)
	}
}

// launchServiceRunning reports whether this launch runs the named service's host half, for the
// jail-daemon decline, which must not say a service runs nowhere when its host half runs here.
func (o *Options) launchServiceRunning(name string) bool {
	for _, p := range o.launchServices {
		if p.Service == name {
			return true
		}
	}
	return false
}

// unselectedGates is why a profile-served daemon is not started, in the words
// noteUnstartedProfileDaemons gives a jail daemon: the platforms no agent's provider is on, and
// the profiles no agent selected.
func unselectedGates(u packload.ProfileServedDaemon) string {
	quote := func(names []string) string {
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = fmt.Sprintf("%q", n)
		}
		return strings.Join(quoted, " or ")
	}
	var why []string
	if len(u.Platforms) > 0 {
		why = append(why, "no agent's selected provider is on platform "+quote(u.Platforms))
	}
	if len(u.Profiles) > 0 {
		why = append(why, "no agent's selected profile is "+quote(u.Profiles))
	}
	return strings.Join(why, ", and ")
}
