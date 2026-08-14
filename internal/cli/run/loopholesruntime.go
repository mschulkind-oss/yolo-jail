package run

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
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
	if rt == "container" {
		return ""
	}
	if o.resolveNetMode(cfg) == "host" || (rt == "podman" && o.inContainer()) {
		return "127.0.0.1"
	}
	return ""
}

// inContainer reports whether THIS process is already inside a container — the same
// probe the assembler uses to decide `--net=host`. The two must agree: if the
// assembler shares the namespace and this says otherwise, every loopback-TLS daemon
// publishes an address the jail cannot reach.
func (o *Options) inContainer() bool {
	return !o.IsMacOS && (o.PathExists("/run/.containerenv") || o.PathExists("/.dockerenv"))
}

// startLoopholes starts all host services for this jail and returns handles.
// Apple Container gets none (no socket bind-mount there).
// Otherwise: the builtin cgroup delegate (Linux + cgroup v2 only), the journal
// bridge (opt-in via the top-level `journal` key), and external services from
// config.loopholes. The broker singleton is ensured but returns NO handle
// (host-wide, not per-jail).
func (o *Options) startLoopholes(cname, rt string, cfg *jsonx.OrderedMap) []loopholeDaemon {
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	mkdirHostServicesDir(socketsDir)
	if rt == "container" {
		return nil
	}

	advertise := o.advertiseHostFor(rt, cfg)
	var handles []loopholeDaemon

	// 1. Built-in cgroup delegate (Linux only, cgroup v2 only).
	if h, ok := o.startCgroupDelegate(cname, rt, socketsDir); ok {
		handles = append(handles, h)
	}

	// 1.5. Built-in journal bridge (opt-in via top-level `journal` key).
	if h, ok := o.startJournal(socketsDir, cfg); ok {
		handles = append(handles, h)
	}

	// 2. External services from config.loopholes (+ manifest host_daemon specs).
	discovered := loopholes.Discover(loopholes.DiscoverOptions{
		IncludeBundled:  true,
		LoopholesConfig: cfgMap(cfg, "loopholes"),
	})
	manifestSpecs := loopholes.ManifestHostDaemonSpecs(discovered)
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
	for _, name := range order {
		if name == paths.BuiltinCgroupLoopholeName || name == paths.BuiltinJournalLoopholeName {
			continue
		}
		if name == broker.BrokerLoopholeName {
			// Host-wide singleton — ensure it, but no per-jail handle.
			o.brokerEnsure()
			continue
		}
		if h, ok := o.startExternalService(name, external[name], socketsDir, transportOf[name], advertise, daemonOf[name]); ok {
			handles = append(handles, h)
		}
	}
	return handles
}

// stopLoopholes tears down handles WITH THE FROZEN GUARD STACK (do not
// reorder): stop each handle, then — when cname/rt are given — take the
// per-workspace flock NON-BLOCKING; if busy, a relaunch is mid-flight → leave
// the relay + sockets dir alone. Else, if the container is STILL RUNNING, leave
// them alone. Else reap the per-jail relay BEFORE rmtree'ing the sockets dir (so
// the relay's SIGTERM socket cleanup targets a dir that still exists).
func (o *Options) stopLoopholes(handles []loopholeDaemon, socketsDir, cname, rt string) {
	out := o.pr(o.Stdout)
	for _, h := range handles {
		func() {
			defer func() { _ = recover() }()
			if h.stop != nil {
				h.stop()
			}
		}()
	}
	if socketsDir == "" {
		return
	}

	var lock *workspaceLock
	if cname != "" {
		lockDir := filepath.Join(paths.GlobalStorage(), "locks")
		_ = os.MkdirAll(lockDir, 0o755)
		f, err := os.OpenFile(filepath.Join(lockDir, cname+".lock"), os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			if ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); ferr != nil {
				_ = f.Close()
				out.printf("[dim]Another yolo invocation is launching %s; "+
					"leaving its relay and sockets dir alone.[/dim]", cname)
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
		if o.findRunningContainer(cname, rt) != "" {
			out.printf("[dim]Container %s is still running; leaving its "+
				"relay and sockets dir alone.[/dim]", cname)
			return
		}
	}
	// Reap the per-jail relay BEFORE the rmtree.
	base := filepath.Base(socketsDir)
	if strings.HasPrefix(base, hostServicesDirPrefix) {
		shortHash := strings.TrimPrefix(base, hostServicesDirPrefix)
		o.relayKill(relayPIDFile(shortHash))
		// The relay's socket is host-only now, so the rmtree below no longer covers
		// it. A SIGTERMed relay unlinks it itself (dev/ino-guarded); a SIGKILLed one
		// cannot, and the file would then litter /tmp forever.
		_ = os.Remove(relaySocketFile(shortHash))
		// Fronted daemons' upstream sockets are host-only for the same reason,
		// and their daemons never unlink after a SIGKILL either.
		retireFrontSockets(shortHash)
	}
	if fileExists(socketsDir) {
		_ = os.RemoveAll(socketsDir)
	}
}

// startCgroupDelegate starts the builtin cgroup delegate as an IN-PROCESS
// goroutine (no external binary), bound to <sockets_dir>/cgroup-delegate.sock.
// Skipped on macOS and non-cgroup-v2 Linux. The container cgroup is resolved
// lazily on the first request. See startCgroupDelegateInProc.
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
		// Still unix-socket: the in-image client hardcodes CGD_SOCKET and is
		// ported in its own change.
		envVarName: hostServiceSocketEnvVar(paths.BuiltinCgroupLoopholeName),
		stop:       stop,
	}, true
}

// resolveJournalMode maps the top-level `journal` config value to a bridge
// mode. Mirrors the pre-port Python _resolve_journal_mode: the canonical
// strings pass through; the bool `true` becomes "user" (the safer default for
// unprivileged agents — never "full", which needs host journal read access);
// absent / null / false / "off" / anything invalid collapse to "off" (a bad
// string is reported separately by validateJournal, so here it is inert).
func resolveJournalMode(cfg *jsonx.OrderedMap) string {
	v, ok := cfgGet(cfg, "journal")
	if !ok || v == nil {
		return "off"
	}
	if b, isBool := v.(bool); isBool {
		if b {
			return "user"
		}
		return "off"
	}
	if s, isStr := v.(string); isStr {
		switch s {
		case "off", "user", "full":
			return s
		}
	}
	return "off"
}

// startJournal starts the built-in journal bridge, parallel to
// startCgroupDelegate, keyed off the top-level `journal` config value. It
// re-execs THIS yolo binary as `yolo internal daemon journal --socket … --mode
// …` (the same self-exec + socket-wait + SIGTERM-teardown plumbing every other
// spawned host service uses, via startExternalService) so the socket is mounted
// and torn down like any other loophole. Off / absent → no handle, no spawn.
// Linux/podman only — the macOS unified-logging analog lives in
// internal/entrypoint/darwin.go and is out of scope here (rt=="container"
// already returned before startLoopholes reached this point).
func (o *Options) startJournal(socketsDir string, cfg *jsonx.OrderedMap) (loopholeDaemon, bool) {
	mode := resolveJournalMode(cfg)
	if mode == "off" {
		return loopholeDaemon{}, false
	}
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{
		"yolo", "internal", "daemon", paths.BuiltinJournalLoopholeName,
		"--socket", "{socket}", "--mode", mode,
	})
	// Still a plain socket: the in-image yolo-journalctl client is generated Python
	// speaking AF_UNIX and is ported in its own change.
	return o.startExternalService(paths.BuiltinJournalLoopholeName, spec, socketsDir,
		transportLegacySocket, "", nil)
}

// killServiceGroup tears down a spawned host service's whole PROCESS GROUP.
//
// The spawn set Setsid, so the daemon leads its own session and group and a
// negative pid reaches everything it forked. Signalling only the direct child —
// what this replaces — left forked grandchildren running after deselection, the
// lockfile entry, and `yolo loopholes list` all forgot the loophole
// (loophole-packaging.md §4.5 accepted exactly this fix).
//
// exited is the channel the spawn-side cmd.Wait() goroutine closes; waiting on
// it rather than calling Wait here keeps the child reaped in exactly one place.
// The straggler SIGKILL also goes to the group: a daemon that ignored SIGTERM
// usually shields its children the same way.
func killServiceGroup(cmd *exec.Cmd, exited <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid // Setsid: the child is its own group leader
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}

// serviceReadyTimeoutDefault is the production readiness deadline for a spawned
// host service. Tests shrink it via Options.ServiceReadyTimeout to avoid real
// multi-second sleeps.
const serviceReadyTimeoutDefault = 5 * time.Second

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
	// Real wall clock, deliberately NOT o.Now() — see relayKill below.
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
				time.Sleep(50 * time.Millisecond)
			}
			return "exited (status 0) and its service never became reachable"
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// transportLegacySocket is this pipeline's label for a host service still
// published as an AF_UNIX socket.
//
// It is NOT a manifest transport. `unix-socket` was REMOVED from
// loopholes.validTransports rather than deprecated (loophole-transport.md §7.4),
// so no manifest can select this and loadManifest rejects the value by name. It
// is spelled here, in the run pipeline, because the pipeline is the only thing
// that still needs it, and it needs it for exactly two built-ins whose in-image
// clients are generated Python speaking AF_UNIX: the cgroup delegate (whose
// client hardcodes CGD_SOCKET) and this journal bridge. Porting those two clients
// to Go deletes this constant with them.
//
// Its VALUE is only ever compared against loopback-tls, never parsed, so nothing
// depends on it matching the string internal/loopholes uses for the same idea.
const transportLegacySocket = "unix-socket"

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
// live path for the two built-ins on transportLegacySocket. An empty transport
// lands there too, which is the conservative direction — the fallback keeps the
// path that works rather than assuming a publication that never happens.
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
	_ = os.Remove(hostPath)
	// daemonPath is what the DAEMON brings up. Under publishes:"endpoint" it is
	// hostPath itself; under publishes:"socket" it is a host-only upstream socket
	// OUTSIDE the :rw-mounted services dir (frontSocketFile says why), with
	// yolo's front publishing hostPath in front of it.
	daemonPath := hostPath
	if fronted {
		daemonPath = frontSocketFile(frontShortHash(socketsDir), name)
		// Retire a dead predecessor's upstream socket BEFORE the spawn, for
		// retireStaleRelayFiles' reasons: a leftover file would fail the fresh
		// daemon's bind with EADDRINUSE, and would satisfy any existence-shaped
		// wait instantly (the wait below is a connect for exactly that reason).
		_ = os.Remove(daemonPath)
	}
	cmdTemplate := asAnyList(mapGet(spec, "command"))
	if len(cmdTemplate) == 0 {
		o.pr(o.Stdout).print("[red]Host service '" + name + "' has no command; skipping[/red]")
		return loopholeDaemon{}, false
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
	// The §4.3a PLACEMENT rule, applied to what is about to be EXECUTED: a daemon
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
		return loopholeDaemon{}, false
	}
	// A manifest host_daemon.cmd of the form
	// ["yolo","internal","daemon",<name>,"--socket",…] re-execs the running yolo
	// binary as the daemon. Substituting os.Executable() for the bare "yolo"
	// token makes the spawn immune to PATH divergence — the jail agent's PATH
	// need not contain "yolo" (the old console-script name `yolo-host-processes`
	// wasn't on it, which broke the spawn). A config loophole's own command
	// (argv[0] != "yolo") is left untouched.
	cmdArgs = execx.SelfExecArgv(cmdArgs)
	logDir := filepath.Join(paths.GlobalStorage(), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "host-service-"+name+".log")
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	if lf, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = lf, lf
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
	go func() { _ = cmd.Wait(); close(exited) }()

	// Wait for the service to become reachable. Real wall clock inside,
	// deliberately NOT o.Now() — see relayKill below.
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
		// daemon that failed readiness may still have forked something.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		// Not fatal — the jail still starts. But it IS the state in which every
		// in-jail client of this loophole fails, and the failure is otherwise
		// silent until the agent hits it, so say so here and name the log that
		// has the reason (mirrors relayEnsure's unpublished-endpoint warning).
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
	stop := func() { killServiceGroup(cmd, exited) }
	if fronted {
		// The daemon's socket accepts; now publish the jail-facing half. The
		// front is started only AFTER the upstream wait on purpose: ServeFront
		// publishes as soon as it binds, so starting it earlier would let the
		// endpoint probe succeed while the daemon never came up, and every
		// authenticated connection would then be silently dropped at the dial
		// (loophole-packaging.md §2.1b hazard 1).
		frontStop := make(chan struct{})
		// frontDone closes when the front's listener is actually closed. Without it
		// stop() only ASKS the front to stop, so "the endpoint file is gone once
		// stop() returns" was true by timing rather than by construction — the
		// listener's Close (which unlinks the file, retiring the jail's credential)
		// races the caller.
		frontDone := make(chan struct{})
		go func() {
			defer close(frontDone)
			_ = svcendpoint.ServeFrontWithOptions(hostPath, advertiseHost, daemonPath, frontStop,
				svcendpoint.FrontOptions{
					HalfCloseUpstream: hd.RequestEnd == loopholes.RequestEndEOF,
				})
		}()
		if !waitForEndpoint(hostPath, o.serviceReadyTimeout()) {
			close(frontStop)
			killServiceGroup(cmd, exited)
			_ = os.Remove(daemonPath)
			// Same loudness contract as the daemon wait above: this is the state
			// in which the daemon is healthy and the jail still cannot reach it.
			o.pr(o.Stdout).print("[yellow]Warning: the front for host service '" + name +
				"' did not publish " + hostPath + " — the jail cannot reach it. See " +
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
			// stopLoopholes is the backstop if this ever expires.
			select {
			case <-frontDone:
			case <-time.After(frontStopGrace):
			}
			killServiceGroup(cmd, exited)
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

// --- broker singleton + relay (minimal ensure; supervision keyed per jail) ---

// brokerEnsure is a no-op if the host-wide broker singleton is alive; else it
// spawns it under a flock. The spawn/liveness/launcher/path-constant
// implementation lives ONCE in internal/broker — run just drives it via
// RealDeps (BrokerSpawn re-checks liveness inside its own flock). Best-effort;
// never fails the caller.
func (o *Options) brokerEnsure() {
	deps := broker.RealDeps()
	if broker.BrokerIsAlive(deps) {
		return
	}
	broker.BrokerSpawn(deps)
}

// ensureBrokerRelay heals the per-jail relay on every path that targets the
// jail. Skipped for Apple Container and when the singleton
// socket is absent.
//
// cfg is read only to resolve the advertise host — what the relay's front should
// PUBLISH depends on whether the jail will share this process's network namespace.
func (o *Options) ensureBrokerRelay(cname, rt string, cfg *jsonx.OrderedMap) {
	if rt == "container" || !o.PathExists(broker.BrokerSingletonSocket) {
		return
	}
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	o.relayEnsure(cname, socketsDir, o.advertiseHostFor(rt, cfg))
}

// relayEnsure is idempotent per-jail relay supervision under a
// flock. Spawns the self-exec'd `yolo internal daemon broker-relay` (see
// relaySpawnArgv).
func (o *Options) relayEnsure(cname, socketsDir, advertiseHost string) {
	shortHash := relayShortHash(cname)
	pidFile := relayPIDFile(shortHash)
	sockPath := relaySocketFile(shortHash)
	endpointPath := relayEndpointFile(socketsDir)
	if o.relayHealthy(pidFile, sockPath, endpointPath) {
		return
	}
	lf, err := os.OpenFile(relayLockFile(shortHash), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer lf.Close()
	if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX) != nil {
		return
	}
	// The post-lock re-check must use the SAME predicate as the pre-lock one.
	// Checking only the cheap half here would let two racing invocations disagree
	// about what "healthy" means and respawn a relay the other just fixed.
	if o.relayHealthy(pidFile, sockPath, endpointPath) {
		return
	}
	// A jail launched by a PRE-UPGRADE yolo has a relay that works and no endpoint
	// file, so relayHealthy says unhealthy and we would kill it — and the container
	// could never reach the replacement, because its environment was frozen at
	// launch naming YOLO_SERVICE_..._SOCKET at the legacy path. Killing it converts
	// a working jail into a broken one MID-SESSION, recoverable only by relaunching.
	//
	// So: if the legacy relay is alive, leave it alone and say why. The upgrade
	// completes when the jail is next relaunched, which is the only moment the
	// container's environment can pick up the new variable.
	if legacy := legacyRelaySocketFile(socketsDir); o.relayIsAlive(pidFile, legacy) {
		o.pr(o.Stdout).print("[yellow]This jail predates the loopback-TLS relay transport; " +
			"leaving its existing relay running. Claude auth keeps working, and the jail " +
			"picks up the new transport when you next relaunch it.[/yellow]")
		return
	}

	o.relayKill(pidFile)
	mkdirHostServicesDir(socketsDir)
	retireStaleRelayFiles(sockPath, endpointPath)
	// Also retire the LEGACY socket. relayKill closes the listener but
	// SetUnlinkOnClose(false) leaves the file, so a pre-upgrade container whose
	// frozen env still names it would dial a dead file and get ECONNREFUSED —
	// indistinguishable from "the relay crashed". Removing it turns that into
	// ENOENT, which the terminator already reports as the clean "not wired up in
	// this jail" case.
	_ = os.Remove(legacyRelaySocketFile(socketsDir))
	argv := o.relaySpawnArgv(sockPath, broker.BrokerSingletonSocket, cname, endpointPath)
	if argv == nil {
		return
	}
	logDir := filepath.Join(paths.GlobalStorage(), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	cmd := exec.Command(argv[0], argv[1:]...)
	if l, err := os.OpenFile(filepath.Join(logDir, "broker-relay-"+shortHash+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = l, l
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The advertise host reaches the front as an env var on THIS HOST-SIDE CHILD —
	// never inside a jail, so it carries none of the inheritance objection that
	// keeps the token out of any environment.
	if advertiseHost != "" {
		cmd.Env = append(os.Environ(), svcendpoint.AdvertiseHostEnv+"="+advertiseHost)
	}
	if err := cmd.Start(); err != nil {
		return
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	o.waitForSocket(sockPath, broker.BrokerSpawnTimeout)
	if !waitForEndpoint(endpointPath, broker.BrokerSpawnTimeout) {
		// Not fatal — the jail still starts, and any later `yolo` invocation
		// re-ensures. But it IS the state in which every in-jail Claude auth request
		// 502s, and the failure is otherwise silent until the agent hits it, so say
		// so here and name the log that has the reason.
		o.pr(o.Stdout).print("[yellow]Warning: the broker relay for this jail did not publish " +
			endpointPath + " — in-jail Claude auth will fail until it does. See " +
			filepath.Join(logDir, "broker-relay-"+shortHash+".log") + "[/yellow]")
	}
}

// relayHealthy reports whether the per-jail relay is usable BY THE JAIL, which is
// strictly more than "the process is alive and its socket accepts".
//
// The Unix socket is host-only now; the jail's only route in is the published
// endpoint file. A relay that is alive and accepting on that socket but has
// published nothing — or a truncated, or an older-format, file — is unreachable
// from inside the jail, and existence-based health would call it fine forever:
// never respawned, and the jail permanently unable to authenticate. That is
// exactly the state a PRE-UPGRADE relay is in after a yolo upgrade, so it is not
// hypothetical, and the check is unconditional rather than gated on a platform.
//
// Probe first: it reads one small file, where relayIsAlive may spend up to two
// seconds on a connect probe.
func (o *Options) relayHealthy(pidFile, sockPath, endpointPath string) bool {
	if !svcendpoint.Probe(endpointPath) {
		return false
	}
	return o.relayIsAlive(pidFile, sockPath)
}

// retireStaleRelayFiles removes BOTH of a dead predecessor's artifacts before a
// respawn. Named rather than inlined because forgetting either one is silent:
//
//   - the socket, or the fresh relay's bind fails with EADDRINUSE;
//   - the endpoint file, or the publication wait is satisfied INSTANTLY by a file
//     naming a port nobody is on. The warning that says "this jail cannot reach its
//     relay" then never fires, and until the new front happens to republish, every
//     dial from inside the jail hits a dead address.
func retireStaleRelayFiles(sockPath, endpointPath string) {
	_ = os.Remove(sockPath)
	_ = os.Remove(endpointPath)
}

// hostServicesDirPrefix names the per-jail host-services dir:
// <prefix><8hex-of-cname>. See paths.HostServicesDirName.
const hostServicesDirPrefix = "yolo-host-services-"

// frontSocketFile is the upstream AF_UNIX socket a publishes:"socket" daemon
// binds — HOST-ONLY, in /tmp beside the relay's socket and for the relay's
// reasons (see relaySocketFile): leaving it in the :rw-mounted services dir
// would keep the retired socket transport reachable from inside the jail —
// which is what retiring it forbids — and would let the jail unlink the
// daemon's own socket. That directory holds endpoint files and nothing else.
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

// retireFrontSockets removes every fronted daemon's upstream socket for one
// jail, beside the relay-socket removal it mirrors: a SIGTERMed daemon may
// unlink its own socket, a SIGKILLed one cannot, and the leftover file would
// make the next launch's fresh daemon fail its bind with EADDRINUSE (the
// pre-spawn unlink in startExternalService covers relaunch; this covers a jail
// that simply ends).
func retireFrontSockets(shortHash string) {
	matches, _ := filepath.Glob(frontSocketFile(shortHash, "*"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// relaySocketFile is the relay's own Unix socket — HOST-ONLY, beside its pid and
// lock files.
//
// It deliberately no longer lives in the jail's :rw-mounted host-services dir.
// Leaving it there would keep the retired transport reachable from inside the jail
// (which is what retiring it forbids) and would let the jail unlink the relay's
// own socket. That directory now holds endpoint files and nothing else.
func relaySocketFile(shortHash string) string {
	return "/tmp/yolo-broker-relay-" + shortHash + ".sock"
}

// legacyRelaySocketFile is where a PRE-loopback-TLS relay bound its socket: inside
// the per-jail host-services dir, so the jail could dial it directly. The new relay
// binds host-only in /tmp and publishes an endpoint file here instead.
//
// Kept because a running container launched by an older yolo still names this path
// in its frozen environment, and both halves of relayEnsure's upgrade handling need
// it — the "do not kill a working legacy relay" check, and the retirement that
// turns a dead legacy file into an honest ENOENT.
func legacyRelaySocketFile(socketsDir string) string {
	return filepath.Join(socketsDir, broker.BrokerLoopholeName+".sock")
}

// relayEndpointFile is where the relay's front publishes, inside the per-jail
// host-services dir — the ONE thing in that dir the jail needs, and a credential.
func relayEndpointFile(socketsDir string) string {
	return filepath.Join(socketsDir, broker.BrokerLoopholeName+paths.ServiceEndpointExt)
}

// relaySpawnArgv builds the per-jail broker-relay spawn argv: the running yolo
// re-exec'd as `yolo internal daemon broker-relay --socket … --broker … --jail
// …`. SelfExecArgv substitutes os.Executable() for the bare "yolo" launcher
// token so the relay is tied to THIS binary regardless of PATH.
//
// The former YOLO_BROKER_RELAY_BIN gate and its script fallback are gone. That
// path was effectively dead — neither YOLO_BROKER_RELAY_BIN
// nor YOLO_REPO_ROOT was set in production, so relaySpawnArgv returned nil and
// the relay never started. Now it always yields a runnable argv, so relayEnsure
// actually spawns the relay whenever a broker loophole is active.
func (o *Options) relaySpawnArgv(sockPath, brokerSocket, cname, endpointPath string) []string {
	argv := []string{
		"yolo", "internal", "daemon", "broker-relay",
		"--socket", sockPath, "--broker", brokerSocket, "--jail", cname,
	}
	if endpointPath != "" {
		// A PATH, never a token: the front mints its own credential and writes it
		// inside this 0600 file. Nothing secret crosses this argv, so nothing here
		// needs redacting before YOLO_DEBUG prints it.
		argv = append(argv, "--endpoint", endpointPath)
	}
	return execx.SelfExecArgv(argv)
}

func (o *Options) relayIsAlive(pidFile, sockPath string) bool {
	pid, ok := readPIDFile(pidFile)
	if !ok {
		return socketConnectable(sockPath, 2*time.Second)
	}
	if !o.PIDAlive(pid) {
		return false
	}
	if !o.PathExists(sockPath) {
		return false
	}
	return socketConnectable(sockPath, 2*time.Second)
}

// relayKill SIGTERMs the relay PID (SIGKILL straggler), then removes the PID
// file. Identity/pgrep-fallback guards are omitted — the PID file is the common
// case; a recycled-PID misfire is bounded by the pidAlive check.
//
// It takes the PID FILE ONLY. It used to take a socket path as well and ignore it,
// which made every caller responsible for passing a value that could not matter —
// and left a site that a socket-path change would have to chase for no effect.
// Unlinking the socket is the relay's own SIGTERM job (under its dev/ino guard, so
// a successor that healed over the same path is never disturbed); a caller that
// wants the file gone after a SIGKILL removes it itself.
//
// relayKillGraceDefault is the production SIGTERM→SIGKILL drain window. Tests
// override it via Options.RelayKillGrace to avoid a real 3s sleep.
const relayKillGraceDefault = 3 * time.Second

// frontStopGrace bounds how long a fronted service's stop() waits for the front
// goroutine to close its listener (which unlinks the endpoint file). Short because
// the wait is for a Close, not for I/O: past it, stopLoopholes' sockets-dir rmtree
// is the backstop.
const frontStopGrace = 2 * time.Second

func (o *Options) relayKill(pidFile string) {
	pid, ok := readPIDFile(pidFile)
	if ok && o.PIDAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		// Real wall clock, deliberately NOT o.Now(). o.Now is an injectable
		// logical clock that tests freeze to make reap decisions
		// deterministic; draining against a frozen clock never advances past
		// the deadline, so this loop would spin until the target happens to
		// die. internal/prune.realRelayKill uses time.Now() for the same
		// reason. The grace MAGNITUDE is a separate seam (o.RelayKillGrace,
		// default relayKillGraceDefault): tests shrink it so this isn't 3s of
		// real sleep in the unit suite, but the clock SOURCE stays the wall
		// clock so the frozen-clock regression is still caught.
		deadline := time.Now().Add(o.RelayKillGrace)
		for time.Now().Before(deadline) {
			if !o.PIDAlive(pid) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if o.PIDAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	_ = os.Remove(pidFile)
}

// grace floor: a relay whose PID file's mtime is younger than this is spared, so
// one spawned for a jail mid-startup (ensured before its container is visible) is
// never reaped.
const relayOrphanGraceSeconds = 3600.0

// relayReapOrphans runs the backstop reap, piggybacking on the store-prune
// gate's live-container enumeration.
// A per-jail relay outlives the yolo process that spawned it by design, and
// stopLoopholes only reaps the current jail's relay in that original process's
// graceful tail — jails ended from attach sessions would leak their relay
// forever otherwise. The current jail's relay (just ensured, container not yet
// started) is excluded by folding cname into the live set. liveKnown==false
// (liveness unenumerable) declines the sweep (unknown never reads as "nothing
// live"). Best-effort: reuses the byte-verified prune engine and the run path's
// own relayKill machinery, called with no socket path.
func (o *Options) relayReapOrphans(liveKnown bool, liveCnames map[string]struct{}, cname string) {
	o.relayReapOrphansIn("/tmp", liveKnown, liveCnames, cname)
}

// relayReapOrphansIn is relayReapOrphans with an injectable scan base (the pid-
// file dir). Production always passes "/tmp" (the hardcoded default); tests
// pass a temp dir. Returns the pid files reaped, so the cname-fold decision is
// assertable without touching /tmp.
func (o *Options) relayReapOrphansIn(base string, liveKnown bool, liveCnames map[string]struct{}, cname string) []string {
	// Fold in the current jail's cname so its freshly-ensured relay is never
	// reaped (the live set is `live_jails | {cname}`).
	live := map[string]struct{}{cname: {}}
	for c := range liveCnames {
		live[c] = struct{}{}
	}
	return prune.ReapRelayOrphans(
		base, liveKnown, live, relayOrphanGraceSeconds, true, o.Now(),
		func(pidFile string) { o.relayKill(pidFile) },
	)
}

func (o *Options) waitForSocket(sockPath string, timeout time.Duration) {
	// Real wall clock, deliberately NOT o.Now() — see relayKill above.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if o.PathExists(sockPath) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForEndpoint polls until the relay's front has published a COMPLETE endpoint
// file, returning whether it landed.
//
// Content, not existence (svcendpoint.Probe): the file is written temp+rename so a
// reader cannot see a torn line, but an older or crashed publisher can still leave
// a file that parses as nothing usable — and treating that as "published" hands the
// jail an address it can never dial.
func waitForEndpoint(endpointPath string, timeout time.Duration) bool {
	// Real wall clock, deliberately NOT o.Now() — see relayKill above.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if svcendpoint.Probe(endpointPath) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return svcendpoint.Probe(endpointPath)
}

func relayShortHash(cname string) string { return sha1Hex8(cname) }
func relayPIDFile(shortHash string) string {
	return "/tmp/yolo-broker-relay-" + shortHash + ".pid"
}
func relayLockFile(shortHash string) string {
	return "/tmp/yolo-broker-relay-" + shortHash + ".lock"
}

func readPIDFile(p string) (int, bool) {
	data, err := os.ReadFile(p)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

// socketConnectable is a plain connect() probe.
func socketConnectable(sockPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", sockPath, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
