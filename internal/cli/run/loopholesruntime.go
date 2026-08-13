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

	out := o.pr(o.Stdout)
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
	// startExternalService.
	transportOf := map[string]string{}
	for _, lp := range discovered {
		transportOf[lp.Name] = lp.Transport
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
		if h, ok := o.startExternalService(name, external[name], socketsDir, transportOf[name], advertise); ok {
			handles = append(handles, h)
		} else {
			_ = out
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
	const prefix = "yolo-host-services-"
	base := filepath.Base(socketsDir)
	if strings.HasPrefix(base, prefix) {
		shortHash := strings.TrimPrefix(base, prefix)
		o.relayKill(relayPIDFile(shortHash))
		// The relay's socket is host-only now, so the rmtree below no longer covers
		// it. A SIGTERMed relay unlinks it itself (dev/ino-guarded); a SIGKILLed one
		// cannot, and the file would then litter /tmp forever.
		_ = os.Remove(relaySocketFile(shortHash))
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
	// Still unix-socket: the in-image yolo-journalctl client is generated Python
	// speaking AF_UNIX and is ported in its own change.
	return o.startExternalService(paths.BuiltinJournalLoopholeName, spec, socketsDir,
		loopholes.TransportUnixSocket, "")
}

// startExternalService is the common host-service path: substitute the host-side
// path into the argv, expand ~, spawn, wait for the service to become REACHABLE.
// Returns the handle on success.
//
// transport selects what the daemon publishes and how we wait for it:
//
//   - loopback-tls — an endpoint FILE, and the wait predicate parses it
//     (svcendpoint.Probe). Existence is not health there: a truncated or
//     older-format file would otherwise read as healthy forever, so the daemon is
//     never respawned and the jail can never reach it.
//   - anything else — a unix socket, waited for by existence, exactly as before.
//
// An empty transport is treated as unix-socket, which is what a config-declared
// loophole gets, and is the conservative direction: the fallback keeps the path
// that works today rather than assuming a publication that never happens.
func (o *Options) startExternalService(
	name string, spec *jsonx.OrderedMap, socketsDir, transport, advertiseHost string,
) (loopholeDaemon, bool) {
	if spec == nil {
		return loopholeDaemon{}, false
	}
	loopbackTLS := transport == loopholes.TransportLoopbackTLS
	leaf := name + ".sock"
	if loopbackTLS {
		leaf = name + paths.ServiceEndpointExt
	}
	hostPath := filepath.Join(socketsDir, leaf)
	// Remove a dead predecessor's artifact BEFORE the spawn. Without this the wait
	// below can succeed against a stale endpoint file naming a port nobody is on,
	// and every client then dials a dead address.
	_ = os.Remove(hostPath)
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
		// IS, which is the whole point of owning the transport.
		s = strings.ReplaceAll(s, "{endpoint}", hostPath)
		cmdArgs = append(cmdArgs, strings.ReplaceAll(s, "{socket}", hostPath))
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
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	if lf, err := os.OpenFile(filepath.Join(logDir, "host-service-"+name+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
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
	// Wait for the service to become reachable (5s). Real wall clock, deliberately
	// NOT o.Now() — see relayKill below.
	reachable := func() bool { return fileExists(hostPath) }
	if loopbackTLS {
		reachable = func() bool { return svcendpoint.Probe(hostPath) }
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if reachable() {
			break
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return loopholeDaemon{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !reachable() {
		_ = cmd.Process.Kill()
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
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan struct{})
			go func() { _, _ = cmd.Process.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
			}
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
	o.relayKill(pidFile)
	mkdirHostServicesDir(socketsDir)
	_ = os.Remove(sockPath)
	// The dead predecessor's endpoint file goes too. Left behind, the wait below
	// would be satisfied instantly by a file naming a port nobody is on, and every
	// dial from inside the jail would hit a dead address for the container's life.
	_ = os.Remove(endpointPath)
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
