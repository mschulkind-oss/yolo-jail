package run

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauth"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// loopholeDaemon is a host-side service this jail can reach.
//
// hostPath/jailPath are deliberately transport-NEUTRAL names: for a loopback-TLS
// service they are the published endpoint file (host side and as mounted in-jail),
// for a still-unix-socket one they are the socket. envVarName is chosen to match —
// see hostServiceEnvVar vs hostServiceSocketEnvVar. stop() tears the daemon down at
// container exit.
type loopholeDaemon struct {
	name       string
	hostPath   string
	jailPath   string
	envVarName string
	stop       func()
}

// resolveNetMode returns the container network mode this launch will use, resolved
// the same way the assembler resolves it (config `network.mode` overrides the flag).
func (o *Options) resolveNetMode(cfg *jsonx.OrderedMap) string {
	netMode := o.Network
	if netSec := cfgMap(cfg, "network"); netSec != nil {
		if m := mapStr(netSec, "mode"); m != "" {
			netMode = m
		}
	}
	return netMode
}

// advertiseHostFor returns the host name a loopback-TLS daemon should PUBLISH for
// this jail to dial, which is not always the container runtime's gateway.
//
// A loopback-TLS daemon binds the LAUNCHER's 127.0.0.1, so the answer depends
// entirely on whether the jail will share the launcher's network namespace:
//
//   - SHARED namespace — `--net=host`, forced for podman-in-podman (doubly-nested
//     netns can't be created without NET_ADMIN) and selectable as
//     `network.mode: "host"`. The jail's 127.0.0.1 IS the listener's, so 127.0.0.1
//     is not merely correct, it is the ONLY thing that works. The gateway name
//     resolves to the launcher's own upstream host, where nothing is listening —
//     measured in a nested jail: "connect: connection refused" at the gateway
//     address while the daemon was healthy on the shared loopback.
//   - SEPARATE namespace — the normal bridge case. The gateway name is what a jail
//     resolves to reach the machine its launcher runs on, and rootless podman's
//     network helper forwards that address to the host's loopback.
//
// Empty means "leave it to svcendpoint's default", which is the gateway name.
func (o *Options) advertiseHostFor(rt string, cfg *jsonx.OrderedMap) string {
	if sharesLauncherNetns(rt, o.resolveNetMode(cfg), o.inContainer()) {
		return "127.0.0.1"
	}
	return ""
}

// sharesLauncherNetns reports whether the jail being launched will share THIS
// process's network namespace. It has THREE readers and they must never disagree:
//
//   - advertiseHostFor, above, to decide what every loopback-TLS daemon PUBLISHES.
//   - assembleRunCmd, to tell the jail paths.HostLoopbackShared — the disposition
//     under which an unreachable service has no host-stack excuse, because with one
//     namespace there is no forwarding hop to have got wrong (OQ-R5).
//   - appliedNetMode (backendcaps.go), which reads it as a MODE: one namespace with
//     the launcher IS host networking, and that is what the agent's briefing says
//     `localhost` means.
//
// Which is exactly why it is one function and not three spellings of a predicate. The
// pair that drifts apart produces a jail told to escalate a failure at an address
// its daemons never published — a refused launch manufactured out of a healthy host,
// which is the one outcome the whole host-loopback path is built to avoid.
//
// Apple Container is excluded before the mode is read at all: it does its own networking and
// takes no network selector from the assembler, so its jail never shares this namespace
// whatever `network.mode` says. ⚠ THIS ALSO SAID IT "gets no host-service bind mount", which
// is FALSE and is retracted: hostServicesMountArgs emits that bind on every backend
// (TestAppleContainerMountsOnlyActiveOpenAIAuthEndpoint asserts it), and the endpoint file the
// one admitted loophole publishes really does cross it. What does not work there is the DIAL,
// measured — nothing crosses container→host on `container` 1.1.0 — which is the fact
// backendInertReason states and the launch now reports for every pack (packloopholes.go).
//
// MACOS-USER IS ALWAYS TRUE, and it is the only runtime here that is true by
// CONSTRUCTION rather than by configuration (DP-L2, docs/design/declaration-parity.md
// §5.1.1). Neither Seatbelt profile yolo emits contains a single `network*` operation —
// `(allow default)` covers it — and nothing in internal/macosuser touches a port, a bind
// or a listener. A sandboxed process is an ordinary child of the launcher on the
// launcher's own stack, so `host` is not a mode it can be put into, it is the only mode
// it has. The briefing said "Bridge mode … reach the host at host.containers.internal"
// to an agent whose `localhost` already WAS the host's.
//
// THE WIDENING DOES MOVE AN ADVERTISE ADDRESS ON THIS BACKEND, and it is supposed to.
// The paragraph above used to end by saying it could not, on the grounds that only
// appliedNetMode was live here; that stopped being true when the macos-user arm started
// host services of its own (run.Run's native arm → startLoopholesDisclosed →
// startLoopholesMatching, which calls advertiseHostFor). ⚠ TWO CLAUSES OF THIS PARAGRAPH
// WERE STALE and are retracted rather than reworded: it named `startOpenAIAuth`, which was
// DELETED on 2026-09-17 with the subset spawn path (openaiauthbackend.go records the
// inversion), and it called this "the subset path and not the full one", which was true of
// an arm that started one credential service by hand. The arm starts EVERY admitted
// loophole now, through the same wrapper every container launch uses — measured:
// `"packs": ["claude"]` publishes both `claude-oauth-broker.endpoint` and
// `openai-auth-broker.endpoint` there. So the predicate decides what the WHOLE set
// publishes. "Always true" is what makes that 127.0.0.1: the sandboxed process is an
// ordinary child of the launcher on the launcher's own stack, so the listener's loopback IS
// the sandbox's, and a gateway name would be an address nothing is listening on.
//
// A DISPOSITION IT STILL CANNOT MOVE. assembleRunCmd's paths.HostLoopbackShared is written
// in the assembler, several hundred lines below the macos-user return, so this backend
// emits no disposition at all — which is why the widening cannot manufacture the refused
// launch the pair-drift hazard above describes.
//
// WHAT THE ARM STILL PRINTS ITSELF is the inert report on its `--dry-run` path (a plan
// render crosses no spawn boundary, so it reaches no wrapper) and the JAIL-DAEMON DECLINE on
// both paths: every host daemon starts here and not one jail daemon does, because there is
// no in-jail supervisor on this backend at all (jaildaemondecline.go).
func sharesLauncherNetns(rt, netMode string, inContainer bool) bool {
	if rt == "container" {
		return false
	}
	if inStrSlice(paths.NativeRuntimes, rt) {
		return true
	}
	// `network.mode: "host"` is the explicit form; podman-in-podman is the forced
	// one — netavark cannot create a netns without NET_ADMIN, so the assembler emits
	// --net=host there whatever the config asked for.
	return netMode == "host" || (rt == "podman" && inContainer)
}

// inContainer reports whether THIS process is already inside a container — the same
// probe the assembler uses to decide `--net=host`. The two must agree: if the
// assembler shares the namespace and this says otherwise, every loopback-TLS daemon
// publishes an address the jail cannot reach.
func (o *Options) inContainer() bool {
	return !o.IsMacOS && (o.PathExists("/run/.containerenv") || o.PathExists("/.dockerenv"))
}

// startLoopholes starts all host services for this jail and returns handles.
// Apple Container starts only OpenAI authentication, whose endpoint directory it
// mounts explicitly. Otherwise: the in-process cgroup delegate (Linux + cgroup v2 only, and now only
// when its own loophole says so) and external services from config.loopholes +
// manifest host_daemon specs. The broker singleton is ensured but returns NO handle
// (host-wide, not per-jail).
//
// THERE IS NO "BUILTIN SERVICE" STEP LEFT, and that is the point of the whole
// activation sprint rather than a tidy-up of this function. Two services used to sit
// at the top of this list answering "why is it on?" differently from every other row
// of §1.3's table: the JOURNAL BRIDGE, started off a top-level `journal` config key,
// and the CGROUP DELEGATE, started because the platform allowed it and nothing asked.
// Both are manifest loopholes now, shipped by official packs of their own names. The
// journal bridge went all the way through the ordinary spawn loop below; the delegate
// keeps its in-process start (see startCgroupDelegate for the SO_PEERCRED reason it
// cannot be a spawned daemon at all) but is GATED on its record like everything else.
func (o *Options) startLoopholes(cname, rt string, cfg *jsonx.OrderedMap) []loopholeDaemon {
	allow := func(string) bool { return true }
	if rt == "container" { // parity: HonoredBy — Apple Container starts only OpenAI authentication
		allow = func(name string) bool { return name == openAIAuthBrokerName }
	}
	return o.startLoopholesMatching(cname, rt, cfg, allow)
}

// startLoopholesMatching is the shared lifecycle for backends that can carry only a
// subset of host services. Apple Container admits the OpenAI credential endpoint file,
// while macos-user starts that same one service without activating unrelated loopholes.
func (o *Options) startLoopholesMatching(cname, rt string, cfg *jsonx.OrderedMap, allow func(string) bool) []loopholeDaemon {
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	mkdirHostServicesDir(socketsDir)

	advertise := o.advertiseHostFor(rt, cfg)
	var handles []loopholeDaemon

	// 1. External services from config.loopholes (+ manifest host_daemon specs).
	//    Census site 4 — the host daemon SPAWN — through the converged set.
	//
	// THE SET's ManifestHostDaemonSpecs, not the package-level one. This is the list the
	// spawn loop below walks, so it is where §4.3 G3's origin gate has to bite: an
	// UNAPPROVED fetched pack's daemon used to enter this map and get started, because the
	// gate had one reader (RunDoctorChecks) and this was not it. The package-level function
	// now admits no pack record at all, and going through the Set is how this call site
	// says it evaluated the gate.
	//
	// BUILT FIRST NOW, before the cgroup delegate rather than after it, because the
	// delegate's own switch is a record in this set. The delegate used to start before
	// any discovery happened at all — which is exactly what "presence activates" looked
	// like in code.
	set := loopholes.NewHostSet(cfgMap(cfg, "loopholes"))
	discovered := set.Enabled()
	kept := discovered[:0]
	for _, lp := range discovered {
		if allow(lp.Name) {
			kept = append(kept, lp)
		}
	}
	discovered = kept

	// 2. The in-process cgroup delegate, gated on its loophole (Linux + cgroup v2 still
	//    checked inside, because that is a fact about this kernel rather than a
	//    declaration anyone can make).
	if allow(paths.BuiltinCgroupLoopholeName) && o.cgroupDelegateHonored(set) {
		if h, ok := o.startCgroupDelegate(cname, rt, socketsDir); ok {
			handles = append(handles, h)
		}
	}
	// BEFORE the spawn, because a daemon's argv already names the file: {settings}
	// resolved to a real path at record-load time, so by the time this loop reaches
	// exec.Command the file has to hold this launch's values (loopholesettings.go).
	o.writeLoopholeSettings(discovered, cfg)
	manifestSpecs := set.ManifestHostDaemonSpecs(discovered)
	// The TRANSPORT comes from the Loophole record, not from the config-shaped spec
	// map, because it is the framework's decision and not a user-supplied key. A name
	// absent here (a config loophole with only a `command`) takes the default in
	// startExternalService. The parsed HostDaemon rides along for its
	// publishes/request_end — the config-synthesized records carry one too, which
	// is what puts a config entry's daemon behind the loopback-TLS front.
	transportOf := map[string]string{}
	daemonOf := map[string]*loopholes.HostDaemon{}
	for _, lp := range discovered {
		transportOf[lp.Name] = lp.Transport
		if lp.HostDaemon != nil {
			daemonOf[lp.Name] = lp.HostDaemon
		}
	}
	external := map[string]*jsonx.OrderedMap{}
	var order []string
	if manifestSpecs != nil {
		for _, name := range manifestSpecs.Keys() {
			if v, _ := manifestSpecs.Get(name); v != nil {
				if m, ok := v.(*jsonx.OrderedMap); ok {
					external[name] = m
					order = append(order, name)
				}
			}
		}
	}
	if loopCfg := cfgMap(cfg, "loopholes"); loopCfg != nil {
		for _, name := range loopCfg.Keys() {
			if !allow(name) {
				continue
			}
			if _, seen := external[name]; seen {
				continue
			}
			spec := cfgMap(loopCfg, name)
			if spec != nil {
				if _, hasCmd := spec.Get("command"); hasCmd {
					external[name] = spec
					order = append(order, name)
				}
			}
		}
	}
	// placementRefused: a loophole whose MANIFEST names host code living where an agent
	// can rewrite it (the placement rule, landing item 1a's manifest faces). The
	// config faces are refused earlier, at validation; a manifest's own host_daemon.cmd
	// and doctor_cmd could not be, because two of the three targets are RUNTIME
	// resolutions — the module dir after symlinks, the argv after {loophole_dir}
	// substitution — and a resolved record is the first place they exist.
	//
	// Refused HERE rather than at discovery for the same reason discovery cannot refuse
	// a name collision: Discover has no error channel by contract, and the spawn is the
	// last moment before the code actually runs. A refused loophole keeps its non-exec
	// declarations (they were already emitted into the argv) — this gate covers the one
	// face that executes.
	placementRefused := map[string]bool{}
	for _, lp := range discovered {
		for _, problem := range lp.PlacementProblems(o.Workspace) {
			placementRefused[lp.Name] = true
			o.pr(o.Stdout).print("[red]Refusing to start loophole " + lp.Name + ": " + problem + "[/red]")
		}
	}
	for _, name := range order {
		// THE BUILTIN-NAME SKIP IS GONE, and its absence is the last piece of the
		// activation sprint rather than a simplification.
		//
		// It used to drop the host daemon of any loophole whose name matched
		// paths.BuiltinLoopholeNames — `journal` or `cgroup-delegate` — with a warning
		// saying the declarations had crossed anyway. There is no builtin name left for
		// it to fire on: both are pack-shipped manifests now, `journal` declares a
		// host_daemon that this loop is SUPPOSED to spawn, and `cgroup-delegate` declares
		// none at all so it never enters `order`. Keeping the branch would have inverted
		// the original defect — refusing to start the very daemon the manifest exists to
		// declare — which is what "do not recreate that shape in reverse" means.
		if placementRefused[name] {
			continue
		}
		// THE NAME TEST IS GONE, and the declaration that replaced it is the point
		// of this branch rather than a tidier spelling of it.
		//
		// It used to read `if name == broker.BrokerLoopholeName { o.brokerEnsure();
		// continue }` — core knowing one loophole by name, ensuring yolo's own
		// singleton, and returning NO handle, so the front over it did not exist and
		// a whole per-jail relay had to be supervised beside this loop instead. The
		// question that test was really asking is "is this daemon shared across
		// jails?", and `host_daemon.scope` is now the manifest's way to answer it
		// (loopholedecl.ScopeHost). Compared against ScopeHost rather than ScopeJail
		// deliberately: a record with no scope at all is per-jail, which is the
		// direction where a dropped field costs a spawn instead of silently
		// declining to start a daemon somebody asked for.
		if hd := daemonOf[name]; hd != nil && hd.Scope == loopholes.ScopeHost {
			if h, ok := o.startHostSingleton(name, external[name], socketsDir, advertise, hd); ok {
				handles = append(handles, h)
			}
			continue
		}
		if h, ok := o.startExternalService(name, external[name], socketsDir, transportOf[name], advertise, daemonOf[name]); ok {
			handles = append(handles, h)
		}
	}
	return handles
}

// cgroupDelegateHonored is the cgroup delegate's SWITCH — the thing that replaced
// "the platform allows it, so it is running".
//
// HONORED, NOT Active(), and it is now the SAME shape as brokerLoopholeActive rather
// than the contrast this comment used to draw. It said that predicate "may stop at
// Active() because the broker's record is BUNDLED — yolo's own manifest, in yolo's own
// tree, under a name no pack may claim"; all three clauses expired on 2026-08-19, when
// the manifest moved into `packs/claude` and the reserved namespace was deleted with
// it (docs/design/broker-as-a-pack.md §13). Both predicates ask `Active() &&
// MayRunHostCode` for one reason: the record comes from a PACK, so the origin gate is
// live, and starting a host-side listener on the strength of a pack's record is exactly
// the crossing that gate exists to govern.
//
// THE LOOKUP IS SHADOWABLE, and so is the broker's — the asymmetry this paragraph used
// to name is gone with the reservation (TestBrokerLookupIsUnshadowable was replaced by
// TestBrokerLookupIsPackExclusive, which asserts the surviving half: loophole names are
// sole-owned ACROSS PACKS, fatally, so a second claimant refuses the launch for everyone
// who selected the pack that already owns the name). A pack a user installs may ship a
// `cgroup-delegate` loophole and turn this on. That is precisely the case OQ-A3 already
// admits — "a fetched pack can declare itself on", bounded by the origin gate rather
// than by the declaration — and the bound here is unusually comfortable: installing a
// pack is a user-scope act, and the most this switch can buy is a capability the same
// user could grant with one config line. The delegate hands a jail control of ITS OWN
// cgroup and reads no host state (OQ-A4's own severity argument).
//
// It deliberately does NOT ask whether the record declares a host_daemon. A pack
// claiming this name with a daemon of its own gets that daemon spawned by the ordinary
// loop AND, if enabled, yolo's in-process delegate — which is two services rather than
// half of one, and is the shape the sprint prefers: nothing is silently dropped.
func (o *Options) cgroupDelegateHonored(set loopholes.Set) bool {
	lp, ok := set.Lookup(paths.BuiltinCgroupLoopholeName)
	if !ok {
		return false
	}
	return lp.Active() && set.MayRunHostCode(lp)
}

// stopLoopholes tears down handles WITH THE FROZEN GUARD STACK (do not
// reorder): stop each handle, then — when cname/rt are given — take the
// per-workspace flock NON-BLOCKING; if busy, a relaunch is mid-flight → leave the
// sockets dir alone. Else, if a container of this name STILL EXISTS, running or
// not, or the runtime cannot be asked whether one does, leave it alone. Else
// retire the fronted daemons' host-only upstream sockets and rmtree the sockets
// dir.
//
// EXISTS, not "is running". A relaunch drops the workspace lock once its container
// is seen running or once onStarted's bounded poll gives up, whichever comes first,
// so a relaunch whose container is still `created` can hold no lock and show no
// running container while its fronts are live and its endpoint files are published
// here. Only the existence probe (`ps -a`) sees it — the rule forgetGoneContainer
// already keeps in this same chain.
//
// Nothing else publishes into that dir, so "no container of this jail's name
// exists" is the whole liveness question. A host-wide singleton serving other
// jails never writes there: its rendezvous is keyed by the loophole name
// (paths.HostSingletonSocket), and each jail's endpoint file for it is published
// by that jail's own front, in the yolo process that launched the jail. The one exception is macos-user, whose
// caller passes no cname and so skips both guards; two sessions of one workspace
// share the dir there (docs/design/host-daemon-ownership.md, OQ-HD10).
//
// THE RELAY REAP THAT USED TO SIT HERE IS GONE with internal/brokerrelay: a
// SIGTERM-and-wait on a pid file, an unlink of the relay's own socket, and an
// ordering comment about doing both before the rmtree. Nothing replaced it,
// because the front that took the relay's place is a goroutine in THIS process
// whose stop() ran in the loop above — and the one daemon behind it is host-wide
// and must survive this jail (startHostSingleton).
func (o *Options) stopLoopholes(handles []loopholeDaemon, socketsDir, cname, rt string) {
	out := o.pr(o.Stdout)
	for _, h := range handles {
		// One span PER FRONT, not one for the loop: a fronted daemon's stop
		// is front-close (≤2s) plus a process-group SIGTERM→5s→SIGKILL, and
		// the question this exists to answer is WHICH daemon lingered
		// (design H3) — a single span around the loop answers only "one of
		// them did".
		sp := o.Perf.Span("shutdown.stop_front." + h.name)
		func() {
			// A PANICKING stop() IS REPORTED, not swallowed. The recover is here so
			// one wedged handle cannot abandon the rest of the teardown — but a bare
			// `_ = recover()` also erased the only evidence that a teardown step ran
			// and failed, leaving a daemon alive with nothing said about it. The
			// panic value is the whole diagnostic, so it goes in the line.
			defer func() {
				if r := recover(); r != nil {
					out.printf("[yellow]Warning: stopping host service '%s' panicked: %v"+
						" — its daemon or front may still be running.[/yellow]", h.name, r)
				}
			}()
			if h.stop != nil {
				h.stop()
			}
		}()
		sp.End()
	}
	if socketsDir == "" {
		return
	}

	var lock *workspaceLock
	if cname != "" {
		lockDir := filepath.Join(paths.GlobalStorage(), "locks")
		// Discarded deliberately: the only consequence of a failed MkdirAll is the
		// OpenFile below failing, which IS reported — reporting both would state one
		// fault twice.
		_ = os.MkdirAll(lockDir, 0o755)
		f, err := os.OpenFile(filepath.Join(lockDir, cname+".lock"), os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			// UNLOCKED TEARDOWN IS A DECISION, so it is said out loud. Without the
			// flock this teardown cannot tell a relaunch mid-flight from a jail that
			// ended, so the rmtree below may delete the endpoint files the NEXT launch
			// is publishing into — the failure mode the lock exists to prevent.
			out.printf("[yellow]Warning: could not take the relaunch lock for %s (%v); "+
				"tearing down its sockets dir without it.[/yellow]", cname, err)
		} else {
			if ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); ferr != nil {
				// Nothing was written to this fd, so a Close error cannot lose data;
				// the flock we failed to take is released by the close either way.
				_ = f.Close()
				out.printf("[dim]Another yolo invocation is launching %s; "+
					"leaving its sockets dir alone.[/dim]", cname)
				return
			}
			lock = &workspaceLock{f: f}
		}
	}
	defer func() {
		if lock != nil {
			lock.Close()
		}
	}()

	if cname != "" {
		// A MARK, not a span: this probe runs the runtime's ps with timeout 0
		// (lifecycle.go), so if the runtime hangs here this mark is the last
		// line in the timing file — the dangling record that names where the
		// prompt went to die (gap T2 in docs/reference/perf-logging.md, which
		// owns the bound).
		o.Perf.Mark("shutdown.container_check")
		// TRI-STATE, not findExistingContainer's collapsed answer, which reads "the
		// runtime could not be asked" as "no container", and the rmtree below would
		// then delete a possibly-live jail's endpoint files on no answer at all.
		//
		// BOTH early returns also keep the fronted daemons' upstream sockets, on
		// purpose: frontSocketFile keys them by this jail's hash, which a relaunch of
		// the workspace reuses, so while a container of this name may exist they may
		// be a live relaunch's. Declining costs one directory and a few socket files
		// in /tmp: the next launch of this workspace unlinks each stale endpoint and
		// upstream socket before its spawn (reportStaleRemoval) and removes both at
		// its own teardown.
		id, known := o.probeExistingContainer(cname, rt, 0)
		if id != "" {
			out.printf("[dim]Container %s still exists; leaving its "+
				"sockets dir alone.[/dim]", cname)
			return
		}
		if !known {
			out.printf("[yellow]Warning: could not ask %s whether container %s still "+
				"exists; leaving its host-services dir %s in place. The next launch of "+
				"this workspace reuses it and removes it when that launch ends.[/yellow]",
				rt, cname, socketsDir)
			return
		}
	}
	// Fronted daemons' upstream sockets are host-only, so the rmtree below does
	// not cover them: a SIGTERMed daemon may unlink its own, a SIGKILLed one
	// cannot, and the leftover file would litter /tmp forever. A HOST-SCOPED
	// daemon's socket is not in this set and must not be — it is keyed by loophole
	// name, not by this jail's hash, and other jails are still using it.
	base := filepath.Base(socketsDir)
	if strings.HasPrefix(base, hostServicesDirPrefix) {
		retireFrontSockets(strings.TrimPrefix(base, hostServicesDirPrefix))
	}
	if fileExists(socketsDir) {
		// A FAILED RMTREE IS THE STALE-ENDPOINT HAZARD this whole file keeps
		// defending against, one launch later: a surviving endpoint file names a port
		// nobody is on, and the next launch's readiness wait can be satisfied by it
		// instantly. The pre-spawn unlink is the other half of that defence, and it
		// reports too.
		if err := os.RemoveAll(socketsDir); err != nil {
			out.printf("[yellow]Warning: could not remove the host-services dir %s (%v); "+
				"stale endpoint files there can mislead the next launch.[/yellow]",
				socketsDir, err)
		}
	}
}

// startCgroupDelegate starts the builtin cgroup delegate as an IN-PROCESS
// goroutine (no external binary), bound to <sockets_dir>/cgroup-delegate.sock.
// Skipped on macOS and non-cgroup-v2 Linux. The container cgroup is resolved
// lazily on the first request. See startCgroupDelegateInProc.
//
// # THE LAST AF_UNIX SERVICE, and it is not waiting on a client
//
// Every other host service is on loopback-tls (docs/reference/loophole-transport.md
// §8.4). The obvious reading of why this one is not — "its in-image client is
// still generated Python" — was true for the journal bridge and is FALSE here:
// cmd/yolo-cglimit is a baked Go binary. What does not survive the hop is
// SO_PEERCRED.
//
// The delegate's whole security model is kernel-attested identity
// (docs/reference/security-shim.md §2, "we never trust the container to identify
// itself"). `create_and_join` writes the peer's HOST-NAMESPACE PID — read off
// the connection by the kernel, never sent by the caller — into the job
// cgroup's cgroup.procs, and that write is what moves the caller into the
// cgroup. A TCP connection carries no peer credential at all, and a
// loopback-TLS FRONT is worse than nothing: SO_PEERCRED on the upstream Unix
// socket would then attest YOLO'S OWN pid, so the delegate would move the yolo
// run process into the jail's job cgroup.
//
// A client-supplied PID is not a substitute twice over: it is caller-asserted
// where the current value is kernel-attested, and it is a PID in the
// container's namespace where the host needs one in its own. Crossing that gap
// (NSpid translation, or a credential the transport can carry) is a security
// decision with its own design, not a transport swap — so it is deliberately
// NOT bundled into the transport retirement. This service stays on AF_UNIX
// until that decision is made.
//
// ⚠ macOS IS UNSERVED FOR AN EARLIER REASON THAN THE TRANSPORT, and this comment
// used to name the wrong one ("still broken for the virtiofs reason the
// unification exists to fix"). The delegate is cgroup v2, so the in-process start
// is Linux-only — off Linux startCgroupDelegateInProc returns false
// (cgddaemon_other.go) and no socket is bound at all. The virtiofs limitation is
// therefore never reached there, and retiring AF_UNIX would not serve macOS: only
// a kernel with cgroups would. The AF_UNIX argument above is about what keeps this
// service off loopback-TLS on the platform where it DOES run.
func (o *Options) startCgroupDelegate(cname, rt, socketsDir string) (loopholeDaemon, bool) {
	sockPath := filepath.Join(socketsDir, paths.CgdSocketName)
	stop, ok := o.startCgroupDelegateInProc(cname, rt, sockPath)
	if !ok {
		return loopholeDaemon{}, false
	}
	return loopholeDaemon{
		name:     paths.BuiltinCgroupLoopholeName,
		hostPath: sockPath,
		jailPath: paths.JailHostServicesDir + "/" + paths.CgdSocketName,
		// The _SOCKET spelling, because the VALUE is a socket path. See the
		// SO_PEERCRED argument above the function.
		envVarName: hostServiceSocketEnvVar(paths.BuiltinCgroupLoopholeName),
		stop:       stop,
	}, true
}

// THE DELEGATE'S TWO REPORTERS ARE NOT HERE, and the build tag is the reason.
// `noteCgroupDelegateUnavailable` and `noteCgroupDelegateFailed` live beside their
// only callers in cgddaemon_linux.go, because the declines they describe are
// Linux-only facts — a kernel without cgroup v2, a bind, a chmod. This file has no
// build tag, so a reporter left here is unused on darwin and `GOOS=darwin
// staticcheck` says so (U1000). The alternative fix — a darwin call site — is the
// one cgddaemon_other.go rules out: off Linux the PLATFORM axis owns the message
// and a second line here would be the half-message loophole-system.md forbids.

// THE JOURNAL BRIDGE'S TWO FUNCTIONS USED TO LIVE HERE — `resolveJournalMode` and
// `startJournal` — and their absence is worth a paragraph, because it is the shape
// this sprint is deleting rather than two functions that happened to move.
//
// `resolveJournalMode` read the TOP-LEVEL `journal` config key and mapped
// off/user/full (plus the bool `true`) onto a `--mode` argv; `startJournal`
// hand-built a spec and called startExternalService with it, in a numbered step of
// its own beside the cgroup delegate. So the bridge was on because a key in CORE'S
// OWN CONFIG SCHEMA said so — one of exactly two loopholes core named by hand
// (docs/reference/loophole-system.md#current-values, the retired-top-level-keys row) —
// with no manifest, no `default_enabled`,
// no scope rule over the mode, and a reserved name enforced nowhere.
//
// It is now the official `journal` pack's manifest loophole, discovered and spawned
// by the same loop as everything else, with the mode a declared `settings` key
// (`full`, `scope: "user"`) delivered through the file `{settings}` names. Nothing
// replaced these two functions; the general path already did their job.

// killServiceGroup tears down a spawned host service's whole PROCESS GROUP.
//
// The spawn set Setsid, so the daemon leads its own session and group and a
// negative pid reaches everything it forked. Signalling only the direct child —
// what this replaces — left forked grandchildren running after deselection, the
// lockfile entry, and `yolo loopholes list` all forgot the loophole
// (docs/reference/loophole-system.md#retirement-what-happens-when-a-pack-goes-away
// accepted exactly this fix).
//
// exited is the channel the spawn-side cmd.Wait() goroutine closes; waiting on
// it rather than calling Wait here keeps the child reaped in exactly one place.
// The straggler SIGKILL also goes to the group: a daemon that ignored SIGTERM
// usually shields its children the same way.
//
// THE ESCALATION IS REPORTED, with the name and the grace it burned. A daemon
// that ignores SIGTERM costs every teardown that grace in wall clock and is the
// shape that leaves forked grandchildren behind, and the branch used to be a
// bare `_ = syscall.Kill(…, SIGKILL)` — so the one observable was a quit that
// took five seconds longer than it should and said nothing about which of N
// daemons spent them.
func killServiceGroup(out printer, name string, grace time.Duration, cmd *exec.Cmd, exited <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid // Setsid: the child is its own group leader
	// Discarded deliberately: the only realistic error is ESRCH, meaning the group
	// is already gone — in which case `exited` is already closed and the select
	// below takes that branch immediately. A signal that failed for any other
	// reason shows up as the grace expiring, which is reported.
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(grace):
		out.printf("[yellow]Warning: host service '%s' ignored SIGTERM for %s; "+
			"killing its process group.[/yellow]", name, grace)
		// Discarded for the SIGTERM's reason, narrowed: SIGKILL cannot be blocked,
		// so the only way it fails is ESRCH — the group exited between the grace
		// expiring and this line, which is the outcome we wanted anyway.
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// serviceTermGraceDefault is how long a spawned host service's teardown waits
// after SIGTERM before SIGKILLing its process group. Tests shrink it via
// Options.ServiceTermGrace so the suite need not sleep for real; production
// always uses this value.
const serviceTermGraceDefault = 5 * time.Second

func (o *Options) serviceTermGrace() time.Duration {
	if o.ServiceTermGrace > 0 {
		return o.ServiceTermGrace
	}
	return serviceTermGraceDefault
}

// serviceReadyTimeoutDefault is the production readiness deadline for a spawned
// host service. Tests shrink it via Options.ServiceReadyTimeout to avoid real
// multi-second sleeps.
const serviceReadyTimeoutDefault = 5 * time.Second

// servicePollInterval is the tick every readiness poll in this file runs on —
// waitServiceReady's two loops and frontPublishFailure's.
//
// IT IS NOT A TIMEOUT, and the distinction is why it is named separately from
// the deadlines above and below it: a tick expiring means "ask again", so it
// decides nothing and reports nothing. The DEADLINE is the decision, and each
// deadline branch in this file names what it waited for and for how long.
const servicePollInterval = 50 * time.Millisecond

func (o *Options) serviceReadyTimeout() time.Duration {
	if o.ServiceReadyTimeout > 0 {
		return o.ServiceReadyTimeout
	}
	return serviceReadyTimeoutDefault
}

// waitServiceReady polls reachable() until it reports true, the readiness
// deadline passes, or the daemon crashes — whichever comes first. It returns ""
// on success and a human-readable failure clause otherwise.
//
// A daemon that CRASHES (non-zero exit) is reported immediately, with its exit
// status. A CLEAN exit keeps polling until the deadline instead: a daemonizing
// wrapper exits 0 while its detached child comes up shortly after, and failing
// on the wrapper's exit would break every daemon of that shape.
func (o *Options) waitServiceReady(reachable func() bool, exited <-chan struct{}, cmd *exec.Cmd) string {
	// REAL WALL CLOCK, deliberately NOT o.Now(), and this is the one place the reason
	// is written down — the two other readiness deadlines in this file used to point at
	// relayKill for it, and relayKill went with internal/brokerrelay.
	//
	// o.Now is an INJECTABLE LOGICAL CLOCK that tests freeze to make reap decisions
	// deterministic. A drain or a readiness poll measured against a frozen clock never
	// advances past its deadline, so the loop spins until the thing it is waiting for
	// happens on its own — a unit suite that hangs rather than fails. The timeout
	// MAGNITUDE stays a seam (o.ServiceReadyTimeout) so tests need not sleep for real;
	// only the clock SOURCE is pinned to the wall, which is what keeps the frozen-clock
	// regression catchable. internal/prune's own kill path takes time.Now() for the
	// identical reason.
	deadline := time.Now().Add(o.serviceReadyTimeout())
	for {
		if reachable() {
			return ""
		}
		if !time.Now().Before(deadline) {
			return "did not become reachable within " + o.serviceReadyTimeout().String()
		}
		select {
		case <-exited:
			// One more look before judging: the daemon may have published and
			// then exited deliberately.
			if reachable() {
				return ""
			}
			if st := cmd.ProcessState; st != nil && !st.Success() {
				return "exited at startup (" + st.String() + ")"
			}
			// Clean exit: poll WITHOUT the exited channel — closed, it would
			// win every select and turn this loop busy.
			for time.Now().Before(deadline) {
				if reachable() {
					return ""
				}
				time.Sleep(servicePollInterval)
			}
			return "exited (status 0) and its service never became reachable within " +
				o.serviceReadyTimeout().String()
		case <-time.After(servicePollInterval):
		}
	}
}

// transportLegacySocket is GONE, and its absence is the fact worth recording.
//
// It was this pipeline's private label for a host service still published as an
// AF_UNIX socket, needed because `unix-socket` was REMOVED from
// loopholes.validTransports rather than deprecated (loophole-transport.md §7.4)
// and no manifest can name it. Two built-ins used it. The journal bridge moved
// to loopback-tls; the cgroup delegate is IN-PROCESS and never reaches
// startExternalService at all (see startCgroupDelegate for why it stays on a
// socket, and it is not the client). Nothing passes a legacy transport to
// startExternalService any more, so the constant went unused — leaving it
// spelled would be an invitation.
//
// The socket branch in startExternalService below did NOT go with it: an empty
// transport still lands there, which is the live path for a `loopholes:` config
// entry that declares only a `command`.

// resolveDaemonArgv turns a host daemon's declared `command` into the argv that
// will actually run: ~ expanded, {endpoint}/{socket} substituted with daemonPath,
// the placement rule applied, and the bare `yolo` launcher token self-exec'd.
// Returns ok=false having already printed the refusal.
//
// EXTRACTED, not duplicated, when the host-wide singleton path arrived. The four
// steps below are a sequence whose ORDER carries two separate decisions — the
// placement check runs after substitution and before SelfExecArgv (see its
// comment) — and a second copy of that ordering is exactly the kind of drift that
// leaves one spawn path gated and the other not.
func (o *Options) resolveDaemonArgv(name string, spec *jsonx.OrderedMap, daemonPath string) ([]string, bool) {
	cmdTemplate := asAnyList(mapGet(spec, "command"))
	if len(cmdTemplate) == 0 {
		o.pr(o.Stdout).print("[red]Host service '" + name + "' has no command; skipping[/red]")
		return nil, false
	}
	var cmdArgs []string
	for _, a := range cmdTemplate {
		s := pyStrCoerce(a)
		if strings.HasPrefix(s, "~") {
			s = expandUser(s)
		}
		// {endpoint} is canonical; {socket} stays an accepted alias so a
		// third-party manifest written against the older name keeps working. Both
		// expand to the same host-side path — the framework decides what that path
		// IS, which is the whole point of owning the transport. (Under
		// publishes:"socket" that path is the upstream socket; a MANIFEST naming
		// {endpoint} there was already refused at load, so the alias only ever
		// fires for a config entry, whose daemon wants the socket path whichever
		// spelling it used.)
		s = strings.ReplaceAll(s, "{endpoint}", daemonPath)
		cmdArgs = append(cmdArgs, strings.ReplaceAll(s, "{socket}", daemonPath))
	}
	// The PLACEMENT rule, applied to what is about to be EXECUTED: a daemon
	// program living inside the workspace this launch mounts :rw (or inside the
	// jail-home tree) is rewritable by the agent between launches, so no earlier
	// gate — who declared it, what the lockfile recorded, what the banner printed —
	// says anything about the bytes that will run. Checked here rather than only in
	// config validation because a MANIFEST's host_daemon.cmd never passes through
	// that validator, and deliberately BEFORE SelfExecArgv: after the substitution
	// argv[0] is yolo's own path, which during nested-jail verification legitimately
	// lives in the workspace.
	if probs := config.LoopholePlacementProblems(
		"loopholes."+name+".command", cmdArgs, o.Workspace); len(probs) > 0 {
		p := o.pr(o.Stdout)
		for _, prob := range probs {
			p.print("[red]Refusing to start host service '" + name + "': " + prob + "[/red]")
		}
		return nil, false
	}
	// A manifest host_daemon.cmd of the form
	// ["yolo","internal","daemon",<name>,"--socket",…] re-execs the running yolo
	// binary as the daemon. Substituting os.Executable() for the bare "yolo"
	// token makes the spawn immune to PATH divergence — the jail agent's PATH
	// need not contain "yolo" (the old console-script name `yolo-host-processes`
	// wasn't on it, which broke the spawn). A config loophole's own command
	// (argv[0] != "yolo") is left untouched.
	return execx.SelfExecArgv(cmdArgs), true
}

// startHostSingleton is the `host_daemon.scope: "host"` path: ONE daemon per
// host, N fronts — one per jail — over its single socket.
//
// It is deliberately NOT startExternalService with a flag. Three of that
// function's four jobs are wrong here, and each one is wrong in a way that would
// be a real fault rather than a wasted step:
//
//   - it SPAWNS. This daemon may already be running for another jail, and a second
//     copy of the broker is not a second daemon, it is the concurrent single-use
//     refresh-token race the flock exists to prevent (agent-credentials.md §2.5).
//     So the daemon is ENSURED — liveness first, then a spawn under the host-wide
//     flock, with the loser of that race observing the winner (internal/broker).
//   - it binds the upstream at a PER-JAIL path (frontSocketFile). The whole point
//     of a singleton is one rendezvous every jail's front and every `yolo broker`
//     invocation agree on, so the path is derived from the loophole NAME
//     (paths.HostSingletonSocket) and from nothing else.
//   - its stop() KILLS the daemon's process group and unlinks the socket. Here
//     that would tear down a daemon other live jails are still using. stop() below
//     closes THIS JAIL'S FRONT and touches nothing else — which is what makes
//     "one singleton, N jails, one lock" survive a jail ending.
//
// What it does share is the half that is genuinely the same: the front. The
// endpoint file, its 0600 per-jail bearer token, the publication wait and the
// unlink-on-close are svcendpoint's exactly as they are for any other
// `publishes: "socket"` daemon.
//
// The returned handle carries NO env var name. The jail-facing variable for a
// host-scoped loophole is emitted at ARGV-ASSEMBLY time instead
// (hostServicesMountArgs), optimistically and before this function has run at all
// — deliberately, so that a launch whose front never publishes is REFUSED by the
// in-jail reachability witness rather than quietly becoming a jail that was never
// told the service exists (loopback-tls-reachability.md §7.3). Emitting it from
// here as well would put the same `-e` in the argv twice.
func (o *Options) startHostSingleton(
	name string, spec *jsonx.OrderedMap, socketsDir, advertiseHost string,
	hd *loopholes.HostDaemon,
) (loopholeDaemon, bool) {
	if spec == nil {
		return loopholeDaemon{}, false
	}
	hostPath := filepath.Join(socketsDir, name+paths.ServiceEndpointExt)
	// A dead predecessor's endpoint file names a port nobody is on; leaving it
	// would satisfy the publication wait below instantly. Same removal, same
	// reason, as the spawned path's.
	o.reportStaleRemoval("endpoint file", name, hostPath)
	daemonPath := paths.HostSingletonSocket(name)
	// NO pre-emptive unlink of daemonPath here, and the asymmetry with the spawned
	// path is the point: that socket may belong to a LIVE daemon serving another
	// jail. A stale one is the ensure's problem, and internal/broker already clears
	// it inside the flock, after deciding nothing is alive.
	cmdArgs, ok := o.resolveDaemonArgv(name, spec, daemonPath)
	if !ok {
		return loopholeDaemon{}, false
	}
	deps := broker.SingletonDeps(name, cmdArgs)
	deps.Out = o.Stdout
	if name == openaiauth.LoopholeName {
		deps.PrepareLocked = prepareLegacyOpenAIAuthState(o.Workspace, deps)
	}
	// Always enter BrokerSpawn: it owns the singleton flock and returns quickly
	// for a healthy daemon, while one-time state migrations must run under that
	// lock even when the old daemon is still alive.
	broker.BrokerSpawn(deps)
	if broker.BrokerIsAlive(deps) && !broker.SingletonSpeaksPreamble(deps) {
		// ALIVE BUT INCOMPATIBLE — the one state every other surface calls healthy.
		// A daemon started before this loophole moved behind a front is still
		// listening at the same path, and it will consume the front's preamble as
		// the client's request: every refresh fails while connect-based liveness,
		// including the in-jail witness, reports green. We do not kill it (two yolo
		// versions on one host would take turns restarting each other's daemon);
		// we say so, and name the one command that fixes it.
		//
		// THE COMMAND IS DERIVED FROM THE NAME, and that is the defect OQ-HD2 was
		// ruled to close. This sentence interpolated `name` into every clause but
		// the last, which read `Fix it with: yolo broker restart` — a wrong
		// instruction inside the sentence presenting itself as the fix, since for
		// any singleton but the Claude one that command cycles a DIFFERENT daemon
		// and leaves the broken one running. broker.CycleCommand is the one place
		// that spelling lives now.
		o.pr(o.Stdout).print("[yellow]Warning: the host-wide daemon for '" + name +
			"' predates this yolo and does not speak the connection preamble.\n" +
			"  It will accept connections and fail every request — for a credential daemon\n" +
			"  that means token refresh is broken on this host, silently.\n" +
			"  Fix it with: " + broker.CycleCommand(name) + "[/yellow]")
	}
	// The daemon's readiness is its socket ACCEPTING A CONNECT — never bare
	// existence, which a stale file satisfies instantly. BrokerSpawn has already
	// waited and already warned if it never bound; this re-asks because the ensure
	// may have been a no-op that observed a PID file whose process has since died.
	if !socketConnectable(daemonPath, time.Second) {
		o.pr(o.Stdout).print("[yellow]Warning: the host-wide daemon for '" + name +
			"' is not accepting connections at " + daemonPath +
			" — the jail cannot reach it. See " + deps.LogPath + "[/yellow]")
		return loopholeDaemon{}, false
	}
	frontStop := make(chan struct{})
	frontDone, frontFailed := frontRun(hostPath, advertiseHost, daemonPath, frontStop,
		svcendpoint.FrontOptions{
			HalfCloseUpstream: hd.RequestEnd == loopholes.RequestEndEOF,
			NoPreamble:        !hd.Preamble,
		})
	if failure := frontPublishFailure(hostPath, o.serviceReadyTimeout(), frontFailed); failure != "" {
		close(frontStop)
		// The DAEMON is not killed on this failure either. It was already running
		// (or was just ensured for everyone, not for us), and our front failing to
		// publish says nothing about whether another jail's is fine.
		o.pr(o.Stdout).print("[yellow]Warning: the front for host-wide service '" + name +
			"' " + failure + " — the jail cannot reach it. See " +
			deps.LogPath + "[/yellow]")
		return loopholeDaemon{}, false
	}
	return loopholeDaemon{
		name:     name,
		hostPath: hostPath,
		jailPath: hostServiceEndpointPath(name),
		stop: func() {
			// Close the front and WAIT for its listener's Close, which unlinks the
			// endpoint file and retires this jail's credential. Bounded, for the
			// spawned path's reason: a wedged front must not hold up teardown, and
			// the sockets-dir rmtree is the backstop — reported when it expires, which
			// is awaitFrontClosed's whole job.
			close(frontStop)
			o.awaitFrontClosed("host-wide service", name, hostPath, frontDone, frontStopGrace)
		},
	}, true
}

// reportStaleRemoval unlinks a dead predecessor's artifact before a spawn and
// reports the one outcome that changes what happens next: a file that is still
// there.
//
// ENOENT IS THE NORMAL CASE and says nothing, so this is silent on every healthy
// launch. Anything else is worth a line precisely because the consequence is
// invisible otherwise: a surviving endpoint file satisfies the readiness wait
// instantly and yolo then reports a service the jail can never dial, while a
// surviving upstream socket fails the fresh daemon's bind with EADDRINUSE. Both
// used to be `_ = os.Remove(path)`, which is the silent-decision shape this file
// is being cleaned of.
func (o *Options) reportStaleRemoval(what, name, path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		o.pr(o.Stdout).printf("[yellow]Warning: could not remove the stale %s for '%s' at %s "+
			"(%v); a surviving one can make this launch report a service the jail "+
			"cannot reach.[/yellow]", what, name, path, err)
	}
}

// startExternalService is the common host-service path: substitute the host-side
// path into the argv, expand ~, spawn, wait for the service to become REACHABLE.
// Returns the handle on success.
//
// transport (with hd's publishes) selects what the daemon brings up and how we
// wait for it:
//
//   - loopback-tls, publishes "endpoint" (the default) — an endpoint FILE, and
//     the wait predicate parses it (svcendpoint.Probe). Existence is not health
//     there: a truncated or older-format file would otherwise read as healthy
//     forever, so the daemon is never respawned and the jail can never reach it.
//   - loopback-tls, publishes "socket" — the daemon binds a plain AF_UNIX socket
//     at a host-only upstream path (frontSocketFile), waited for by CONNECT with
//     its own deadline; then yolo starts a svcendpoint front and publishes the
//     endpoint file itself. The jail sees exactly what the first mode gives it.
//   - anything else — a unix socket in the services dir, waited for by
//     existence, exactly as before.
//
// The third branch is not dead and is not a safety net for a typo: it is the
// live path for a `loopholes:` config entry that declares only a `command` and
// therefore carries no transport at all. (It used to also carry the two
// built-ins on the retired socket transport; the journal bridge has moved to
// loopback-tls and the cgroup delegate is in-process, so an EMPTY transport is
// all that reaches it now.) An empty transport landing here is the conservative
// direction — the fallback keeps the path that works rather than assuming a
// publication that never happens.
//
// hd is the loophole's parsed host_daemon (nil for the built-ins and for
// anything else with no manifest-shaped daemon); only its Publishes/RequestEnd
// are read here — the argv still arrives through spec's "command".
func (o *Options) startExternalService(
	name string, spec *jsonx.OrderedMap, socketsDir, transport, advertiseHost string,
	hd *loopholes.HostDaemon,
) (loopholeDaemon, bool) {
	if spec == nil {
		return loopholeDaemon{}, false
	}
	loopbackTLS := transport == loopholes.TransportLoopbackTLS
	fronted := loopbackTLS && hd != nil && hd.Publishes == loopholes.PublishesSocket
	leaf := name + ".sock"
	if loopbackTLS {
		leaf = name + paths.ServiceEndpointExt
	}
	hostPath := filepath.Join(socketsDir, leaf)
	// Remove a dead predecessor's artifact BEFORE the spawn. Without this the wait
	// below can succeed against a stale endpoint file naming a port nobody is on,
	// and every client then dials a dead address.
	o.reportStaleRemoval("endpoint file", name, hostPath)
	// daemonPath is what the DAEMON brings up. Under publishes:"endpoint" it is
	// hostPath itself; under publishes:"socket" it is a host-only upstream socket
	// OUTSIDE the :rw-mounted services dir (frontSocketFile says why), with
	// yolo's front publishing hostPath in front of it.
	daemonPath := hostPath
	if fronted {
		daemonPath = frontSocketFile(frontShortHash(socketsDir), name)
		// Retire a dead predecessor's upstream socket BEFORE the spawn, for
		// retireFrontSockets' reasons: a leftover file would fail the fresh
		// daemon's bind with EADDRINUSE, and would satisfy any existence-shaped
		// wait instantly (the wait below is a connect for exactly that reason).
		o.reportStaleRemoval("upstream socket", name, daemonPath)
	}
	cmdArgs, ok := o.resolveDaemonArgv(name, spec, daemonPath)
	if !ok {
		return loopholeDaemon{}, false
	}
	logDir := filepath.Join(paths.GlobalStorage(), "logs")
	// Discarded deliberately: a failed MkdirAll surfaces as the OpenFile below
	// failing, which IS reported — one fault, one line.
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "host-service-"+name+".log")
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	if lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
	} else {
		// THE DAEMON STILL STARTS — its log is diagnostics, not a dependency — but
		// every failure line below ends in "see <logPath>", and sending a reader to a
		// file that was never opened is worse than the silence it replaced. So the one
		// launch where that advice is false says so here, once, before it is given.
		o.pr(o.Stdout).printf("[yellow]Warning: host service '%s' will run with no log: "+
			"could not open %s (%v).[/yellow]", name, logPath, err)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// env overrides.
	env := os.Environ()
	envSet := false
	// The advertise host reaches the daemon as an env var on THIS HOST-SIDE CHILD —
	// never inside a jail, so it carries none of the inheritance objection that keeps
	// the token out of the environment. One variable rather than a per-daemon flag:
	// a flag would have to be added to every daemon's flag set (three today, every
	// future one) and would make the framework's contract with a daemon two
	// placeholders instead of one.
	if loopbackTLS && advertiseHost != "" {
		env = append(env, svcendpoint.AdvertiseHostEnv+"="+advertiseHost)
		envSet = true
	}
	if e := cfgMap(spec, "env"); e != nil {
		for _, k := range e.Keys() {
			if v, ok := mapGet(e, k).(string); ok {
				if strings.HasPrefix(v, "~") {
					v = expandUser(v)
				}
				env = append(env, k+"="+v)
			}
		}
		envSet = true
	}
	if envSet {
		cmd.Env = env
	}
	if err := cmd.Start(); err != nil {
		o.pr(o.Stdout).print("[red]Failed to launch host service '" + name + "': " + err.Error() + "[/red]")
		return loopholeDaemon{}, false
	}
	// Reap the child from a goroutine so the readiness wait can see an exit the
	// moment it happens. The check this replaces read cmd.ProcessState inline,
	// which only cmd.Wait() populates — and nothing called it — so the check was
	// dead code and an instantly-crashing daemon silently burned the whole
	// readiness deadline, serially, one per daemon.
	exited := make(chan struct{})
	// Wait's error is discarded because it is a RESTATEMENT of cmd.ProcessState,
	// which waitServiceReady reads and reports ("exited at startup (<status>)").
	// Reporting both would say one exit twice, in two wordings.
	go func() { _ = cmd.Wait(); close(exited) }()

	// Wait for the service to become reachable. Real wall clock inside,
	// deliberately NOT o.Now() — see waitServiceReady.
	reachable := func() bool { return fileExists(hostPath) }
	awaited := hostPath
	if loopbackTLS {
		reachable = func() bool { return svcendpoint.Probe(hostPath) }
	}
	if fronted {
		// The fronted daemon's own readiness is its socket ACCEPTING A CONNECT —
		// never bare existence, which a leftover file would satisfy instantly
		// (the same reason Probe rather than existence is the health predicate
		// everywhere else), and never the endpoint file, which yolo itself
		// publishes only after this wait succeeds.
		reachable = func() bool { return socketConnectable(daemonPath, time.Second) }
		awaited = daemonPath
	}
	if failure := o.waitServiceReady(reachable, exited, cmd); failure != "" {
		// SIGKILL the GROUP (Setsid at spawn), not just the direct child: a
		// daemon that failed readiness may still have forked something. The error is
		// discarded because SIGKILL cannot be blocked: the only way it fails is ESRCH,
		// meaning the group is already gone — which the failure clause below is about
		// to report in its own words.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// Not fatal — the jail still starts. But it IS the state in which every
		// in-jail client of this loophole fails, and the failure is otherwise
		// silent until the agent hits it, so say so here and name the log that
		// has the reason (the same shape startHostSingleton's two warnings take).
		o.pr(o.Stdout).print("[yellow]Warning: host service '" + name + "' " + failure +
			" — the jail cannot reach it. Expected " + awaited +
			"; see " + logPath + "[/yellow]")
		return loopholeDaemon{}, false
	}
	// jail_endpoint is canonical; jail_socket stays an accepted alias, for the same
	// reason {socket} does — silently ignoring a third-party loophole's override key
	// over a rename is worse than carrying two spellings.
	jailPath := paths.JailHostServicesDir + "/" + leaf
	if loopbackTLS {
		jailPath = hostServiceEndpointPath(name)
	}
	if v := mapStr(spec, "jail_endpoint"); v != "" {
		jailPath = v
	} else if v := mapStr(spec, "jail_socket"); v != "" {
		jailPath = v
	}
	envVar := hostServiceSocketEnvVar(name)
	if loopbackTLS {
		envVar = hostServiceEnvVar(name)
	}
	out := o.pr(o.Stdout)
	stop := func() { killServiceGroup(out, name, o.serviceTermGrace(), cmd, exited) }
	if fronted {
		// The daemon's socket accepts; now publish the jail-facing half. The
		// front is started only AFTER the upstream wait on purpose: ServeFront
		// publishes as soon as it binds, so starting it earlier would let the
		// endpoint probe succeed while the daemon never came up, and every
		// authenticated connection would then be silently dropped at the dial
		// (docs/reference/loophole-transport.md#two-server-shapes-and-how-a-manifest-selects-one).
		frontStop := make(chan struct{})
		// frontDone closes when the front's listener is actually closed. Without it
		// stop() only ASKS the front to stop, so "the endpoint file is gone once
		// stop() returns" was true by timing rather than by construction — the
		// listener's Close (which unlinks the file, retiring the jail's credential)
		// races the caller. frontFailed carries the front's BIND error; see frontRun.
		frontDone, frontFailed := frontRun(hostPath, advertiseHost, daemonPath, frontStop,
			svcendpoint.FrontOptions{
				HalfCloseUpstream: hd.RequestEnd == loopholes.RequestEndEOF,
				// The daemon's own declaration decides, and the two ways to
				// declare one default OPPOSITE ways on purpose. A MANIFEST is
				// written against yolo's transport, so its default is
				// preamble-on and a dumb pipe says `"preamble": false`
				// (loopholedecl.HostDaemon.Preamble). A yolo-jail.jsonc
				// `loopholes:` entry is an argv for a third-party program that
				// is already running for somebody, so discover.go defaults it
				// OFF and `"preamble": true` is the opt-in. Either way the
				// answer arrives here as one bool on the record — this call
				// site does not know which kind of declaration produced it,
				// and must not.
				NoPreamble: !hd.Preamble,
			})
		if failure := frontPublishFailure(hostPath, o.serviceReadyTimeout(), frontFailed); failure != "" {
			close(frontStop)
			killServiceGroup(out, name, o.serviceTermGrace(), cmd, exited)
			// Discarded: cleanup on a path that is already reporting a failure. A
			// socket left behind here is retired by the next launch's pre-spawn
			// unlink or by retireFrontSockets, and both of those report.
			_ = os.Remove(daemonPath)
			// Same loudness contract as the daemon wait above: this is the state
			// in which the daemon is healthy and the jail still cannot reach it,
			// and `failure` is what distinguishes a front that could not BIND from
			// one that simply never published inside the deadline.
			o.pr(o.Stdout).print("[yellow]Warning: the front for host service '" + name +
				"' " + failure + " — the jail cannot reach it. See " +
				logPath + "[/yellow]")
			return loopholeDaemon{}, false
		}
		stop = func() {
			// Close the front FIRST — its listener's Close unlinks the endpoint
			// file, retiring the jail's credential — then the daemon group, then
			// the upstream socket, which a SIGKILLed daemon cannot unlink itself.
			close(frontStop)
			// WAIT for that Close, bounded: an unbounded wait would let a wedged
			// front hold up every teardown, and the sockets-dir rmtree in
			// stopLoopholes is the backstop if this ever expires — REPORTED, because
			// past this point the endpoint file and the per-jail credential inside it
			// may outlive the jail, and the backstop is another function's job.
			o.awaitFrontClosed("host service", name, hostPath, frontDone, frontStopGrace)
			killServiceGroup(out, name, o.serviceTermGrace(), cmd, exited)
			// Discarded: teardown litter only, and self-limiting — the next launch's
			// pre-spawn unlink and retireFrontSockets both target this exact path and
			// both report what they cannot remove.
			_ = os.Remove(daemonPath)
		}
	}
	return loopholeDaemon{
		name:       name,
		hostPath:   hostPath,
		jailPath:   jailPath,
		envVarName: envVar,
		stop:       stop,
	}, true
}

// --- the broker singleton, ensured before the argv is built ---

// brokerEnsure is a no-op if the host-wide broker singleton is alive; else it
// spawns it under a flock. The spawn/liveness/launcher/path-constant
// implementation lives ONCE in internal/broker — run just drives it via
// RealDeps (BrokerSpawn re-checks liveness inside its own flock). Best-effort;
// never fails the caller.
//
// IT IS NOT REDUNDANT WITH startHostSingleton, which ensures the same daemon from
// the loophole record a few phases later. The second ensure is idempotent by
// construction (liveness, then a flock whose loser observes the winner), so the
// cost of both is one Lstat.
//
// WHY IT RUNS BEFORE assembleRunCmd. The stated reason used to be that the
// assembler READ THIS SOCKET to decide whether the argv could promise the jail an
// endpoint. It does not: the jail-facing variable is emitted OPTIMISTICALLY, from
// the record alone, precisely so that a launch whose front never publishes is
// REFUSED by the in-jail reachability witness instead of quietly becoming a jail
// that was never told the service exists (hostScopedEndpoints,
// loopback-tls-reachability.md §7.3). Nothing about the argv is a function of
// whether this ensure succeeded.
//
// What makes the position right is that the SPAWN GATE AND THE ARGV WIRING ARE ONE
// PREDICATE: brokerLoopholeActive is a membership test on hostScopedEndpoints, the
// same derived list the assembler emits from (assemble_parts.go). Ensuring here
// puts the daemon's bind window before the container starts, under the gate that
// decides the wiring — so a jail that is told the address has had the daemon
// ensured for it, and a jail that is not leaves no daemon running on the host.
// Both halves used to be decided separately and disagreed in both directions
// (run.go, at the call).
//
// This is also the one caller with NO record IN HAND, which is why RealDeps still
// carries the broker's own argv: the gate above it consults the derived list, but
// the ensure itself is handed nothing but yolo's own constants — the same position
// `yolo host-daemon restart <name>` and its `broker` alias are in, with no launch
// at all.
func (o *Options) brokerEnsure() {
	deps := broker.RealDeps()
	if broker.BrokerIsAlive(deps) {
		return
	}
	broker.BrokerSpawn(deps)
}

// hostServicesDirPrefix names the per-jail host-services dir:
// <prefix><8hex-of-cname>. See paths.HostServicesDirName.
const hostServicesDirPrefix = "yolo-host-services-"

// frontSocketFile is the upstream AF_UNIX socket a PER-JAIL publishes:"socket"
// daemon binds — HOST-ONLY, in /tmp: leaving it in the :rw-mounted services dir
// would keep the retired socket transport reachable from inside the jail — which
// is what retiring it forbids — and would let the jail unlink the daemon's own
// socket. That directory holds endpoint files and nothing else.
//
// A HOST-SCOPED daemon's socket is NOT one of these. It is keyed by loophole name
// (paths.HostSingletonSocket) rather than by a jail's hash, precisely so every
// jail's front and every `yolo broker` invocation rendezvous on one file — which
// is also why retireFrontSockets below cannot reach it.
func frontSocketFile(shortHash, name string) string {
	return "/tmp/yolo-front-" + shortHash + "-" + name + ".sock"
}

// frontShortHash keys a fronted daemon's upstream socket to its jail. The
// sockets dir is normally /tmp/yolo-host-services-<8hex>; reusing that hash
// lets stopLoopholes find every front socket from the dir name alone. A dir
// without the prefix (tests) hashes the whole path instead.
func frontShortHash(socketsDir string) string {
	base := filepath.Base(socketsDir)
	if h := strings.TrimPrefix(base, hostServicesDirPrefix); h != base {
		return h
	}
	return sha1Hex8(socketsDir)
}

// retireFrontSockets removes every PER-JAIL fronted daemon's upstream socket for
// one jail: a SIGTERMed daemon may unlink its own socket, a SIGKILLed one cannot,
// and the leftover file would make the next launch's fresh daemon fail its bind
// with EADDRINUSE (the pre-spawn unlink in startExternalService covers relaunch;
// this covers a jail that simply ends).
//
// The glob is keyed by the jail's hash, so a host-scoped daemon's socket is
// unreachable from here BY CONSTRUCTION rather than by a check somebody has to
// remember — which is the property that keeps one jail ending from cutting off
// every other jail's credential path.
//
// BOTH ERRORS ARE DISCARDED, and both are safe. Glob's only error is
// ErrBadPattern, which a pattern built from frontSocketFile cannot be. A failed
// Remove leaves one file in /tmp whose every consequence is reported elsewhere: the
// only thing that reads this path again is the same jail's next launch, whose
// pre-spawn unlink reports what it cannot remove (reportStaleRemoval), and a fresh
// daemon that hits EADDRINUSE fails its readiness wait loudly.
func retireFrontSockets(shortHash string) {
	matches, _ := filepath.Glob(frontSocketFile(shortHash, "*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// frontStopGrace bounds how long a fronted service's stop() waits for the front
// goroutine to close its listener (which unlinks the endpoint file). Short
// because the wait is for a Close, not for I/O: past it, stopLoopholes'
// sockets-dir rmtree is the backstop — and the expiry is REPORTED at both stop()
// sites, because until that rmtree runs the endpoint file still carries a live
// per-jail credential.
const frontStopGrace = 2 * time.Second

// awaitFrontClosed waits for a front's listener to finish closing and REPORTS the
// one outcome that is not silence: the grace expiring.
//
// ONE reporter for both stop() paths, and the grace is a parameter, because the
// alternative is what was here — the same bare `select { case <-frontDone: case
// <-time.After(frontStopGrace): }` written twice, with the timeout branch empty in
// both. Past that branch the endpoint file still exists and the per-jail bearer token
// inside it is still valid, and the only thing that retires it is a *different*
// function's rmtree (stopLoopholes), which this teardown does not wait for and which
// declines outright if a container is still running. So it is exactly a decision with
// a security consequence and no observable, which is the shape this file is being
// cleaned of.
//
// kind is the service's scope word ("host service" / "host-wide service"), because
// which one lingered decides who is affected: a per-jail front outlives one jail, a
// host-wide one is shared.
func (o *Options) awaitFrontClosed(kind, name, endpointPath string, done <-chan struct{}, grace time.Duration) {
	select {
	case <-done:
	case <-time.After(grace):
		o.pr(o.Stdout).printf("[yellow]Warning: the front for %s '%s' did not close within "+
			"%s; its endpoint file %s and the credential in it may outlive this "+
			"jail.[/yellow]", kind, name, grace, endpointPath)
	}
}

// frontRun starts a svcendpoint front for one daemon and returns the two channels
// its caller needs: done, closed when the front's listener is actually closed (which
// unlinks the endpoint file and retires this jail's credential), and failed.
//
// FAILED IS THE POINT OF THIS HELPER. Both call sites used to spell the goroutine by
// hand as `_ = svcendpoint.ServeFrontWithOptions(…)`, so a front that could not come
// up produced exactly ONE observable — the publication wait timing out seconds later,
// saying "did not publish" and nothing about why. That is the shape a bind collision
// takes, and it is diagnosable only from the error this channel carries.
//
// It is silent on every healthy path and cannot cost a launch a line:
// ServeFrontWithOptions returns non-nil only when the LISTEN fails, while a listener
// closed on stop returns nil (svcendpoint/front.go's accept loop).
func frontRun(publishPath, advertiseHost, upstreamPath string, stop <-chan struct{},
	opts svcendpoint.FrontOptions) (done <-chan struct{}, failed <-chan error) {
	d := make(chan struct{})
	f := make(chan error, 1)
	go func() {
		defer close(d)
		if err := svcendpoint.ServeFrontWithOptions(
			publishPath, advertiseHost, upstreamPath, stop, opts); err != nil {
			f <- err
		}
	}()
	return d, f
}

// frontPublishFailure waits for a front to publish a COMPLETE endpoint file and
// returns "" when it does, else a clause naming WHY — its caller prefixes the
// service and appends the log. Both spawn paths use it: the per-jail one in
// startExternalService and the host-wide one in startHostSingleton.
//
// Content, not existence (svcendpoint.Probe): the file is written temp+rename so a
// reader cannot see a torn line, but an older or crashed publisher can still leave
// a file that parses as nothing usable — and treating that as "published" hands the
// jail an address it can never dial.
//
// TWO DISTINGUISHABLE FAILURES, deliberately, because they call for different next
// steps: the front's listen FAILED (its error, available at once — so the rest of the
// deadline is not burned reporting a symptom of it), or nothing published and the
// deadline passed, which names the path and the duration. A timeout that says neither
// converts "the port was taken" into "the capability is missing".
func frontPublishFailure(endpointPath string, timeout time.Duration, failed <-chan error) string {
	// Real wall clock, deliberately NOT o.Now() — see waitServiceReady.
	deadline := time.Now().Add(timeout)
	for {
		if svcendpoint.Probe(endpointPath) {
			return ""
		}
		select {
		case err := <-failed:
			if err != nil {
				return "could not bind its listener: " + err.Error()
			}
		default:
		}
		if !time.Now().Before(deadline) {
			if svcendpoint.Probe(endpointPath) {
				return ""
			}
			return "did not publish " + endpointPath + " within " + timeout.String()
		}
		time.Sleep(servicePollInterval)
	}
}

// socketConnectable is a plain connect() probe. The dial error is the ANSWER (false)
// rather than something to report: every caller turns it into a failure clause of its
// own naming the path and the deadline, and Close's error is discarded because nothing
// was written or read — a probe that connected has already learned everything it
// asked.
func socketConnectable(sockPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", sockPath, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
