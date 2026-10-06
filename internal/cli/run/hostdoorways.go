package run

// hostdoorways.go is the `yolo host` launch's LAUNCH-OWNED DOORWAYS
// (docs/design/host-notch-services.md HS-D15, the doorway rule, ruled 2026-09-29; HS-D21 records
// this build). A DOORWAY is that ruling's word for the thin adapter an agent's client talks to,
// which checks the launch's caller token and forwards to a credential service's host daemon.
// The rule: the host daemon is the same on every backend, and the doorway opens on whichever
// loopback the agent sees. A container has a loopback of its own, so there the doorway is a jail
// daemon; a macos-user sandbox shares the Mac's, so that launch opens it outside Seatbelt
// (macosuserdoorways.go); and an agent `yolo host` runs is this machine's own process, so the
// host launch opens it the same way, through the same mechanism: internal/launchservice's
// admission, its plan at a settled address and caller token, its start and its stop.
//
// WHICH DOORWAYS. The ones a selected pack's pointer serves for the agent this launch runs: a
// PROFILE-SERVED daemon (coined in internal/packload's profileserved.go: every pointer naming it
// is gated on a profile or a provider platform) whose gate the agent's selection satisfies, a
// platform gate counting only for an agent with a client of that platform (HS-D23), and whose
// loophole is enabled. That is aws-auth's AWS container-credentials adapter for an agent on a
// Bedrock provider (OQ-CN7 (b): the jail starts it on the same condition). A doorway whose
// pointer is ungated, which the Codex refresh adapter is, is not opened here: its pointer
// reaches every agent the host composes and serves none of them but codex, and `yolo host --
// codex` already serves that URL from the managed adapter (HS-D20).
//
// WHERE AND TO WHOM IT ANSWERS is settled here, before the composition: a loopback port this
// launch reserves (launchservice's reserve.go; never the declared 1461, which a jail sharing this
// loopback may hold) and hands the doorway at its start, so the host-service front Start binds
// before it cannot be given the port, and a caller token minted for this launch.
// The composition then serves the daemon at that address (HostDoorways.Served), so aws-auth's
// AWS_CONTAINER_CREDENTIALS_FULL_URI and its scoped AWS_CONTAINER_AUTHORIZATION_TOKEN name the
// listener this launch starts, for the one agent whose selection asked for it.
//
// WHAT IT FORWARDS TO is the route the host already has for this daemon (HS-D19): the aws-auth
// host service, reached through the front a session of this launch publishes over the host-wide
// singleton, exactly the front a macos-user session publishes (startLoopholesMatching, with this
// file's runtime name taking that arm's session dir and loopback advertise).
//
// ⚠ THE LOOPBACK IS THE MACHINE'S. There is no network namespace at the host notch, so the
// nested-jail blindness AGENTS.md describes does not apply here, and nothing here is a
// loopback-forwarding question: the doorway binds 127.0.0.1 on the one stack the agent uses.

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/openauthclient"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// hostNotchRuntime is the runtime name the loophole lifecycle is handed for a `yolo host`
// launch: not a backend (no jail is launched), the name the lifecycle's two per-runtime choices
// read, a session dir of the launch's own and a front advertising 127.0.0.1
// (startLoopholesMatching, sharesLauncherNetns).
const hostNotchRuntime = "host"

// HostDoorways is one `yolo host` launch's doorways: the ones it opens, where each answers,
// its caller tokens, and why each doorway-capable daemon it leaves closed is closed.
type HostDoorways struct {
	plans []*launchservice.Plan
	// listen maps each opened doorway's name to its served address.
	listen map[string]string
	// notOpened maps a profile-served daemon whose pointer this launch composes and withholds
	// to the clause saying why the launch did not open it (packload's WithNotServedWhy).
	notOpened map[string]string
	// set is the loophole set of the packs this launch selected, for the start, and packs the
	// selected packs that ship an opened doorway, for the disclosure before it.
	set   loopholes.Set
	packs []*packload.Pack
}

// PlanHostDoorways plans the doorways of a `yolo host` launch of packs, the packs its selection
// function chose, whose one agent's selection the gate reads as sel (packload.SelectionOf). cfg
// is the user-scope config the launch composes from. opens is whether the front door owns its
// command's lifetime (`yolo host --` does; `yolo host env` and `yolo host apply` run no process
// a doorway could live beside), and launch is the spelling that would open them, for a front
// door that does not.
func PlanHostDoorways(cfg *jsonx.OrderedMap, packs []*packload.Pack, sel packload.GateSelection,
	opens bool, launch string) (*HostDoorways, error) {
	d := &HostDoorways{listen: map[string]string{}, notOpened: map[string]string{}}
	// AN AGENT WITH NO CLIENT OF ITS SELECTION'S PLATFORM IS NOT A DOORWAY'S CLIENT (HS-D23).
	// A platform gate fires on the provider's platform alone, so copilot under `-p bedrock`,
	// which has no Bedrock client of its own (the profile line warns that the selection reaches
	// nothing for it), would get aws-auth's doorway, the host code behind it and a live
	// credential it has no model client to use. The doorways are planned over the selection less
	// such an agent's platform, and a doorway only its platform asked for stays closed, naming why.
	gateSel, clientless := withoutClientlessPlatforms(packs, sel)
	undelivered := map[string]bool{}
	for _, u := range packload.UnselectedProfileServedDaemons(packs, gateSel) {
		undelivered[u.Name] = true
	}
	if len(clientless) > 0 {
		askedByPlatform := map[string]bool{}
		for _, u := range packload.UnselectedProfileServedDaemons(packs, sel) {
			askedByPlatform[u.Name] = true
		}
		for name := range undelivered {
			if !askedByPlatform[name] {
				d.notOpened[name] = noClientWhy(clientless)
			}
		}
	}
	// The doorways this agent's selection asks for: every profile-served daemon, less the ones
	// no gate of the selection delivers (the jail's own filter, withoutUnselectedProfileDaemons).
	var wanted []string
	for _, name := range packload.ProfileServedDaemonNames(packs) {
		if !undelivered[name] {
			wanted = append(wanted, name)
		}
	}
	if len(wanted) == 0 {
		// No selection asks for a doorway, so nothing reads a loophole manifest: an unprofiled
		// host launch discovers nothing it did not before.
		return d, nil
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		LoopholesConfig:   cfgMap(cfg, "loopholes"),
		PackModules:       packLoopholeModules(packs),
		PackSupersessions: packSupersessions(packs),
	})
	d.set = set
	specs, refused := launchservice.AdmitDoorways(packs,
		set.JailDaemons(set.Enabled(), hostNotchRuntime, nil))
	composed := map[string]loopholes.JailDaemonSpec{}
	for _, s := range loopholes.Doorways(specs) {
		composed[s.Name] = s
	}
	for _, name := range wanted {
		if _, ok := composed[name]; ok && opens {
			continue
		}
		d.notOpened[name] = notOpenedWhy(set, refused, name, opens, launch)
	}
	if !opens {
		return d, nil
	}
	packOf := launchservice.LoopholePacks(packs)
	for _, name := range wanted {
		s, ok := composed[name]
		if !ok {
			continue
		}
		picked, err := launchservice.ReservePorts([]string{s.Listen})
		if err != nil {
			d.Release()
			return nil, fmt.Errorf("pick a loopback port for the %q doorway: %w", name, err)
		}
		held := picked[s.Listen]
		s.Listen = held.Addr()
		decl, err := launchservice.AdmitDoorway(packs, packOf[name], name, s.ResolvedHostCmd())
		if err != nil {
			// AdmitDoorways admitted this argv above; with only the address changed, a
			// refusal here is a yolo bug, and the launch refuses rather than serve a pointer
			// at nothing.
			held.Release()
			d.Release()
			return nil, fmt.Errorf("the %q doorway: %w", name, err)
		}
		token, err := svcendpoint.NewToken()
		if err != nil {
			held.Release()
			d.Release()
			return nil, fmt.Errorf("mint the %q doorway's caller token: %w", name, err)
		}
		// The reservation goes with the plan, for Start to hand the doorway: the fronts this
		// launch binds before it cannot be given the port (launchservice's reserve.go).
		d.plans = append(d.plans, launchservice.PlanAt(decl, s.Listen,
			paths.ServiceCallerTokenEnv(name), token, held))
		d.listen[name] = s.Listen
		for _, p := range packs {
			if p != nil && p.Name == decl.Pack && !slices.Contains(d.packs, p) {
				d.packs = append(d.packs, p)
			}
		}
	}
	return d, nil
}

// withoutClientlessPlatforms is sel less the platform of each agent that has no client of it
// (packload.AgentBindsPlatform), and those agents' platforms by agent. Over each agent's whole
// ACTIVE SET when sel carries one (docs/design/active-provider-sets.md AP-P1): an entry whose
// platform the agent has no client of is blanked, and the others keep asking.
func withoutClientlessPlatforms(packs []*packload.Pack, sel packload.GateSelection) (packload.GateSelection,
	map[string]string) {
	clientless := map[string]string{}
	for agent, platform := range sel.Platforms {
		if !packload.AgentBindsPlatform(packs, agent, platform) {
			clientless[agent] = platform
		}
	}
	for agent, platforms := range sel.SetPlatforms {
		for _, platform := range platforms {
			if _, named := clientless[agent]; !named && platform != "" &&
				!packload.AgentBindsPlatform(packs, agent, platform) {
				clientless[agent] = platform
			}
		}
	}
	if len(clientless) == 0 {
		return sel, nil
	}
	out := packload.GateSelection{Profiles: sel.Profiles, Platforms: map[string]string{}, Sets: sel.Sets}
	for agent, platform := range sel.Platforms {
		if packload.AgentBindsPlatform(packs, agent, platform) {
			out.Platforms[agent] = platform
		}
	}
	if sel.SetPlatforms != nil {
		out.SetPlatforms = map[string][]string{}
		for agent, platforms := range sel.SetPlatforms {
			kept := make([]string, len(platforms))
			for i, platform := range platforms {
				if platform != "" && packload.AgentBindsPlatform(packs, agent, platform) {
					kept[i] = platform
				}
			}
			out.SetPlatforms[agent] = kept
		}
	}
	return out, clientless
}

// noClientWhy is the clause for a doorway only a clientless agent's platform asked for.
func noClientWhy(clientless map[string]string) string {
	agents := make([]string, 0, len(clientless))
	for agent := range clientless {
		agents = append(agents, agent)
	}
	slices.Sort(agents)
	var parts []string
	for _, agent := range agents {
		parts = append(parts, fmt.Sprintf("%s has no client of platform %q", agent, clientless[agent]))
	}
	return "which this launch does not open, because " + strings.Join(parts, " and ") +
		" (the profile line's warning above)"
}

// notOpenedWhy is the clause for a doorway this launch's agent asks for and the launch does not
// open, in the order a user would fix them: the loophole is off, its host argv is refused, or
// the front door runs no process for it to live beside.
func notOpenedWhy(set loopholes.Set, refused []launchservice.RefusedDoorway, name string,
	opens bool, launch string) string {
	lp, ok := set.Lookup(name)
	if !ok {
		return "which this launch does not open: no selected pack's loophole declares it"
	}
	if !lp.Enabled {
		return fmt.Sprintf("which this launch does not open, because loophole %q is disabled: "+
			"`yolo host --` opens its doorway for this agent once %s sets "+
			"`\"loopholes\": {%q: {\"enabled\": true}}` (and the settings it needs, "+
			"`yolo loopholes list` names them)", name, paths.UserConfigPath(), name)
	}
	if !lp.Active() {
		why, _ := lp.InactiveReason()
		return fmt.Sprintf("which this launch does not open, because loophole %q is not "+
			"active here: %s", name, why)
	}
	if lp.JailDaemon == nil || len(lp.JailDaemon.HostCmd) == 0 {
		return "which declares no doorway for a notch with no jail (`jail_daemon.host_cmd`), " +
			"so only a jail runs it"
	}
	for _, r := range refused {
		if r.Name == name {
			return fmt.Sprintf("which this launch does not open: its doorway's host argv "+
				"(pack %q) is refused, %s", r.Pack, r.Why)
		}
	}
	if !opens {
		return "which only a launch that runs the agent opens, for as long as that agent " +
			"runs, and this command runs none: " + launch + " opens it for that command"
	}
	return "which this launch does not open: its loophole composes no doorway at the host"
}

// Release releases the reserved port of every planned doorway this launch has not started.
func (d *HostDoorways) Release() {
	if d == nil {
		return
	}
	for _, p := range d.plans {
		p.Release()
	}
}

// Plans is every doorway this launch opens, in order.
func (d *HostDoorways) Plans() []*launchservice.Plan {
	if d == nil {
		return nil
	}
	return d.plans
}

// Served is the host notch's served set with this launch's doorways in it, each at its
// settled address, and every doorway it leaves closed carrying its reason.
func (d *HostDoorways) Served() packload.ServedDaemons {
	served := packload.NothingServed()
	if d != nil && len(d.plans) > 0 {
		served = packload.ServedByLaunch(launchservice.Names(d.plans)).WithListen(d.listen)
	}
	served = served.AtHost()
	if d != nil {
		served = served.WithNotServedWhy(d.notOpened)
	}
	return served
}

// CallerTokens is each opened doorway's caller token under its variable, for the gate
// (packload.ScopeInput.CallerTokens), which scopes it to the agents its pointer reaches.
func (d *HostDoorways) CallerTokens() map[string]string {
	if d == nil {
		return nil
	}
	return launchservice.CallerTokens(d.plans)
}

// HostDoorwayStart starts one planned doorway: internal/cli's startLaunchService, so a test
// can observe it, and launchservice.Start otherwise.
type HostDoorwayStart func(*launchservice.Plan, map[string]string) (*launchservice.Running, error)

// Start opens every planned doorway for the command agent, after the host services they
// forward to: each doorway's loophole's host service, ensured as the host-wide singleton it is
// and fronted for this launch in a session dir of the launch's own (startLoopholesMatching). It
// returns the running doorways for the agent's parent to stop when the agent exits, and stop,
// which closes the fronts and removes the session dir after them; lines is what the launch
// says it opened, one line each. A doorway that does not start refuses the launch before the
// agent runs (§4.5), with the ones already open stopped. Output of the host-service start
// itself (a daemon that refused at spawn, a front that did not publish) goes to stderr as it
// happens, and the doorway still opens: with no endpoint it answers each request with the
// reason, as the jail's copy does (HS-D19).
func (d *HostDoorways) Start(cfg *jsonx.OrderedMap, workspace, agent string, stderr io.Writer,
	start HostDoorwayStart) (running []*launchservice.Running, stop func(), lines []string, err error) {
	stop = func() {}
	if d == nil || len(d.plans) == 0 {
		return nil, stop, nil, nil
	}
	o := &Options{Stdout: stderr, Stderr: stderr, Workspace: workspace, IsMacOS: paths.IsMacOS,
		reachSubject: agent}
	fillDefaults(o)
	// THE EXEC DISCLOSURE BEFORE THE SPAWN (§4.3 G4: the read/exec banners are the trust
	// boundary, and a launch has no quiet mode), for the packs whose host code this start runs:
	// the ones shipping the doorways it opens, whose claims name both the host service and the
	// doorway's host argv.
	//
	// Narrowed to the loopholes this start runs, the doorways' own services (the allow the spawn
	// below is handed), so another loophole those packs ship is not announced.
	names := launchservice.Names(d.plans)
	o.notePackHostExec(d.packs, func(name string) bool { return slices.Contains(names, name) })
	handles := o.startLoopholesMatching(d.set, runtime.FromWorkspace(workspace), hostNotchRuntime, cfg,
		func(name string) bool { return slices.Contains(names, name) })
	stopServices := func() { o.endServicesSession(handles) }
	// A HOST-WIDE DAEMON THAT PREDATES THE PREAMBLE REFUSES THIS LAUNCH TOO (HD-D5 (3)): its doorway
	// would forward every request through a front it misreads, so the agent would start without the
	// service. This notch asks no launch check, so the preamble is the only half of the jail
	// launch's refusal it owes (olderDaemonRefusal).
	if refused := preambleRefusal(handles); refused != nil {
		stopServices()
		return nil, func() {}, nil, errors.New(refused.text())
	}
	// The two routes a doorway reaches its host daemon by (doorwayInput's, for macos-user): the
	// endpoint file of each host service this session published, and the OpenAI service's
	// private socket.
	env := map[string]string{openauthclient.HostSocketEnv: openaiauthhost.HostSocketPath()}
	for _, h := range handles {
		env[hostServiceLaunchEnvVar(h)] = h.hostPath
		lines = append(lines, fmt.Sprintf("using the host-wide %q service for this launch, "+
			"through a front of its own that closes when %s exits: %s", h.name, agent, h.hostPath))
	}
	for _, plan := range d.plans {
		r, serr := start(plan, env)
		if serr != nil {
			for _, started := range running {
				started.Stop()
			}
			stopServices()
			return nil, func() {}, lines, serr
		}
		running = append(running, r)
		lines = append(lines, fmt.Sprintf("opened the %q doorway (pack %q, pid %d) on %s for %s: "+
			"it answers only this launch's caller token, forwards to the host's %q service, and "+
			"stops when %s exits. Its log: %s", plan.Service, plan.Pack, r.PID(),
			strings.Join(plan.Addresses(), ", "), agent, plan.Service, agent, r.Log))
	}
	return running, stopServices, lines, nil
}
