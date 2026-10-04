package run

// macosuserdoorways.go is the macos-user launch's LAUNCH-OWNED DOORWAYS
// (docs/design/host-notch-services.md HS-D15, the doorway rule, ruled 2026-09-29; OQ-OA6's route
// (b) in docs/reference/agent-credentials.md). A DOORWAY is that ruling's word for the thin adapter
// an agent's client talks to, which checks the launch's caller token and forwards to a
// credential service's host daemon: the Codex refresh adapter (openai-auth) and the AWS
// container-credentials adapter (aws-auth). The rule is that the host daemon is the same on every
// backend and the doorway opens on whichever loopback the agent sees. A container has a loopback
// of its own, so there the doorway is a jail daemon. This backend's sandbox shares the Mac's, so
// the launch opens the doorway itself, outside Seatbelt, as a launch-owned listener
// (internal/launchservice, the mechanism the wire bridge's host half uses here): on the port the
// launch picked for the loophole's `listen`, answering only the caller token the launch minted
// for it, and stopped when the sandboxed command exits. The guest's supervisor runs no copy.
//
// WHAT IS A DOORWAY is declared, never named here: a loophole whose `jail_daemon` declares
// `host_cmd`, the argv that opens it outside (loopholes.DoorwaysOutside). What may run is the
// launch-owned mechanism's admission rule (launchservice.AdmitDoorway: a pack yolo ships, an
// argv naming `yolo`), applied to the payload by launchservice.AdmitDoorways where it is composed
// (admitDoorways) and in `yolo check`'s prediction of it, so a refused one is judged as the jail
// daemon it also is, and the launch and the prediction agree on what is served.
//
// WHERE AND TO WHOM IT ANSWERS was settled before this file runs: the served address is the one
// jailDaemonsFor settled for the loophole (servedaddresses.go), and the token the one the channel
// composed its clients with (callertokens.go). So packs/codex's CODEX_REFRESH_TOKEN_URL_OVERRIDE,
// the refresh marker the Codex launcher binds $YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN into, and
// aws-auth's AWS_CONTAINER_CREDENTIALS_FULL_URI and scoped AWS_CONTAINER_AUTHORIZATION_TOKEN all
// name the listener this file starts. That listener is the port's own reservation, handed to the
// doorway at its start (launchservice's reserve.go), so the host-service fronts this launch binds
// first cannot be given the port.
//
// ⚠ NEVER EXECUTED ON A MAC. The arm is pinned by unit tests with the start stubbed, and the
// doorway processes by tests that run them on Linux; none of it has run under a real macos-user
// launch.

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// startMacosUserDoorway starts one launch-owned doorway and returns it and its log; a var so a
// test can observe the start without spawning one.
var startMacosUserDoorway = func(plan *launchservice.Plan, env map[string]string) (launchedService, string, error) {
	r, err := launchservice.Start(plan, env)
	if err != nil {
		return nil, "", err
	}
	return r, r.Log, nil
}

// admitDoorways clears the host argv of every doorway in specs that the launch-owned mechanism
// does not admit, recording the refusals for noteRefusedDoorways, and returns the payload
// (launchservice.AdmitDoorways, the one admission `yolo check`'s prediction applies too). A
// cleared one is an ordinary jail daemon again: a container runs it, and the macos-user guest
// runs it, confined, or declines it by name (OQ-DP8, OQ-DP9). Called by jailDaemonsFor on every
// runtime, since the answer is a fact about the pack and not the backend; only macos-user reads
// a host argv at all.
func (o *Options) admitDoorways(packs []*packload.Pack, specs []loopholes.JailDaemonSpec) []loopholes.JailDaemonSpec {
	specs, o.refusedDoorways = launchservice.AdmitDoorways(packs, specs)
	return specs
}

// planMacosUserDoorways is the plan of every doorway this launch opens outside its sandbox
// (loopholes.DoorwaysOutside over the payload Run composed, specs): each one's admitted host
// argv resolved to the address the launch settled for its `listen`, behind the caller token the
// channel composed its clients with. Recorded on o for the decline, which says the doorway runs.
func (o *Options) planMacosUserDoorways(rt string, specs []loopholes.JailDaemonSpec,
	packs []*packload.Pack, channel *packChannel) []*launchservice.Plan {
	o.launchDoorways = nil
	packOf := launchservice.LoopholePacks(packs)
	for _, s := range loopholes.DoorwaysOutside(rt, specs) {
		d, err := launchservice.AdmitDoorway(packs, packOf[s.Name], s.Name, s.ResolvedHostCmd())
		if err != nil {
			continue // admitDoorways cleared every host argv it refuses; nothing to open
		}
		tokenEnv := paths.ServiceCallerTokenEnv(s.Name)
		// The port's reservation goes with the plan, for the start to hand the doorway
		// (servedaddresses.go): nothing this launch binds before then can be given it.
		o.launchDoorways = append(o.launchDoorways,
			launchservice.PlanAt(d, s.Listen, tokenEnv, channel.callerTokens[tokenEnv], o.takeReservedPort(s.Listen)))
	}
	return o.launchDoorways
}

// doorwayInput is what a macos-user launch hands each doorway (launchservice.Input): the two
// routes a doorway outside reaches its host daemon by — the endpoint file of every host service
// this session published (the front the guest's copy would have dialed, which is how aws-auth's
// adapter forwards) and the OpenAI service's private socket (HS-D3's route, which the Codex
// adapter `yolo host -- codex` serves takes). The caller token is added by launchservice.Start.
func doorwayInput(launchEnv *jsonx.OrderedMap) map[string]string {
	env := map[string]string{openauthclient.HostSocketEnv: openaiauthhost.HostSocketPath()}
	if launchEnv != nil {
		for _, k := range launchEnv.Keys() {
			if isServiceEndpointEnv(k) {
				if v, _ := launchEnv.Get(k); v != nil {
					if s, ok := v.(string); ok {
						env[k] = s
					}
				}
			}
		}
	}
	return env
}

// startMacosUserDoorways opens every planned doorway and says so, one line each, on every
// launch: this is host code running outside Seatbelt, and a launch has no quiet mode. It returns
// the stop for the arm to defer, or the refusal naming the doorway that did not start, which the
// launch-owned mechanism's contract makes a refusal before the command runs (§4.5).
func (o *Options) startMacosUserDoorways(plans []*launchservice.Plan, launchEnv *jsonx.OrderedMap) (func(), error) {
	var running []launchedService
	stop := func() {
		for _, r := range running {
			r.Stop()
		}
	}
	for _, plan := range plans {
		r, log, err := startMacosUserDoorway(plan, doorwayInput(launchEnv))
		if err != nil {
			stop()
			return func() {}, err
		}
		running = append(running, r)
		o.pr(o.Stderr).print(fmt.Sprintf("Opened the %q doorway (pack %q, pid %d) on %s for this "+
			"launch, outside the sandbox: it answers only this launch's caller token, forwards to "+
			"the host's %q service, and stops when the command exits. Its log: %s", plan.Service,
			plan.Pack, r.PID(), strings.Join(plan.Addresses(), ", "), plan.Service, log))
	}
	return stop, nil
}

// launchDoorwayPlanned reports whether this launch opens the named loophole's doorway outside the
// sandbox, for the jail-daemon decline, which must not read as the doorway running nowhere.
func (o *Options) launchDoorwayPlanned(name string) bool {
	for _, p := range o.launchDoorways {
		if p.Service == name {
			return true
		}
	}
	return false
}

// noteRefusedDoorways is the disclosure for admitDoorways: one line per doorway whose host argv
// this launch will not run, naming it, its pack and why, and saying where its jail daemon goes
// instead. declined is the guest's own split of the payload (loopholes.JailDaemonsRunIn, which
// the arm printed just above), because a cleared doorway is judged there like any other jail
// daemon: the guest runs it, or declines it for a reason of its own, such as an argv naming the
// container's loophole mount, and then nothing serves it this launch. A disclosure, so no quiet
// switch (docs/reference/report-tiers.md, OQ-RO3). Silent when none.
//
// It ends with noteRefusedServiceHosts, the same disclosure for a pack service's host half, from
// the one call site the macos-user arm makes for both (below its decline report).
func (o *Options) noteRefusedDoorways(declined []loopholes.DeclinedJailDaemon) {
	guestDeclines := guestDeclinedNames(declined)
	for _, r := range o.refusedDoorways {
		o.pr(o.Stderr).print(fmt.Sprintf("[yellow]Not opened outside the sandbox: the %q doorway's "+
			"host argv (pack %q): %s. %s[/yellow]", r.Name, r.Pack, r.Why, guestPlacement(guestDeclines[r.Name])))
	}
	o.noteRefusedServiceHosts(guestDeclines)
}

// noteRefusedServiceHosts is the disclosure for launchservice.AdmitServiceHosts on macos-user:
// one line per pack service whose host half this launch will not run (a pack yolo does not
// ship, or an argv not naming `yolo`; OQ-HS4), naming it, its pack and why, and saying where its
// jail daemon goes instead, read off the guest's own split as the doorway's line is: the sandbox
// runs it (OQ-DP8), or declines it for a reason of its own, such as an endpoint file at a
// container path, and then nothing serves it this launch. A disclosure, so no quiet switch.
// Silent when none.
func (o *Options) noteRefusedServiceHosts(guestDeclines map[string]bool) {
	for _, r := range o.refusedServiceHosts {
		o.pr(o.Stderr).print(fmt.Sprintf("[yellow]Not started outside the sandbox: the %q service's "+
			"host half (pack %q): %s. %s[/yellow]", r.Name, r.Pack, r.Why, guestPlacement(guestDeclines[r.Name])))
	}
}

// guestDeclinedNames is the set of daemon names the guest's split declined.
func guestDeclinedNames(declined []loopholes.DeclinedJailDaemon) map[string]bool {
	out := map[string]bool{}
	for _, d := range declined {
		out[d.Spec.Name] = true
	}
	return out
}

// guestPlacement is where a refused host argv's jail daemon goes on macos-user, for the two
// disclosures above. When the guest declines it too, a daemon a selected pack declared runs
// nowhere this launch, so the line names the next step: a container backend runs every daemon its
// payload names (loopholes.ContainerRuntimeRunsIt; docs/reference/happy-path-principle.md).
func guestPlacement(declinedInGuest bool) string {
	if declinedInGuest {
		return "Its jail daemon is declined in the sandbox too (its Declined: line says why), " +
			"so nothing serves it this launch; " + loopholes.ContainerRuntimeRunsIt + "."
	}
	return "Its jail daemon runs in the sandbox instead."
}
