package run

// macosuserservices.go is the macos-user launch's LAUNCH-OWNED SERVICES
// (docs/design/host-notch-services.md §4.7; docs/plans/notch-convergence.md OQ-NC1, ruled A).
// That backend starts no jail daemon and has no network namespace, so a pack service reaches it
// the way it reaches `yolo host`: its host half runs as this launch's child, outside Seatbelt, on
// a loopback port this launch picked, answering only this launch's caller token, and stops when
// the sandboxed command exits. The mechanism is internal/launchservice, the one the host uses:
// admission (an official pack's host half, named `yolo`), the plan (ports and token), the start
// and the stop.
//
// THE TRIGGER is the credential gate's own refusal: a profiled agent's pairing through a pack
// service's adaptation refuses while nothing serves it (packload.UnservedAdapterError), and the
// channel then plans that service and composes again (composePackChannel's retry). Every profiled
// agent counts here, not only the launched one, because this backend writes every profiled
// agent's env file (writeMacosUserAgentEnvFiles) for a shell that starts one later.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// launchedService is a started launch-owned service, as the macos-user arm uses it.
type launchedService interface {
	Stop()
	PID() int
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

// launchServiceInput is what a macos-user launch hands each launch-owned service: the channel's
// three wire tables, the host broker's private socket, and the env_sources the credential gate
// delivers to the agents whose pairing needed it, for their providers only
// (AgentDelivery.EnvSources). The caller token is added by launchservice.Start.
func (c *packChannel) launchServiceInput(agents []string) map[string]string {
	env := map[string]string{
		entrypoint.ProvidersWireEnv:   jsonDumpsOrEmptyObj(c.providers),
		entrypoint.ProfilesWireEnv:    jsonDumpsOrEmptyObj(packload.ProfilesWireTable(c.resolvedProfiles)),
		entrypoint.UseProfilesWireEnv: jsonDumpsOrEmptyObj(c.profiles),
		openauthclient.HostSocketEnv:  openaiauthhost.HostSocketPath(),
	}
	for _, agent := range agents {
		d := c.scope.Agent(agent)
		if d == nil || d.EnvSources == nil {
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
func (o *Options) startMacosUserServices(channel *packChannel) (func(), error) {
	var running []launchedService
	stop := func() {
		for _, r := range running {
			r.Stop()
		}
	}
	for _, plan := range o.launchServices {
		r, log, err := startMacosUserService(plan, channel.launchServiceInput(o.launchServiceAgents[plan.Service]))
		if err != nil {
			stop()
			return func() {}, err
		}
		running = append(running, r)
		o.pr(o.Stderr).print(fmt.Sprintf("Started the %q service (pack %q, pid %d) on %s for this "+
			"launch, outside the sandbox: it answers only this launch's caller token and stops "+
			"when the command exits. Its log: %s", plan.Service, plan.Pack, r.PID(),
			strings.Join(o.servicePointedAt(plan, channel), ", "), log))
	}
	return stop, nil
}

// servicePointedAt is the addresses of plan its agents were pointed at, the only routes it opens:
// launchservice.Plan.PointedAt over the channel's delivery to each agent whose pairing needed it,
// the one reading the start line, the dry run's line and `yolo host --` share
// (docs/design/host-notch-services.md HS-D24).
func (o *Options) servicePointedAt(plan *launchservice.Plan, channel *packChannel) []string {
	var deliveries []*packload.AgentDelivery
	for _, agent := range o.launchServiceAgents[plan.Service] {
		deliveries = append(deliveries, channel.scope.Agent(agent))
	}
	return plan.PointedAt(deliveries...)
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
