package runcmd

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// loopholeDaemon is a host-side service exposing a Unix socket inside the jail.
// index(image), and stop() tears it down at container exit.
type loopholeDaemon struct {
	name           string
	hostSocketPath string
	jailSocketPath string
	envVarName     string
	stop           func()
}

// startLoopholes ports start_loopholes: start all host services for this jail
// and return handles. Apple Container gets none (no socket bind-mount there).
// Otherwise: the builtin cgroup delegate (Linux + cgroup v2 only), the journal
// bridge (opt-in), and external services from config.loopholes. The broker
// singleton is ensured but returns NO handle (host-wide, not per-jail).
func (o *Options) startLoopholes(cname, rt string, cfg *jsonx.OrderedMap) []loopholeDaemon {
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	_ = os.MkdirAll(socketsDir, 0o755)
	if rt == "container" {
		return nil
	}

	out := o.pr(o.Stdout)
	var handles []loopholeDaemon

	// 1. Built-in cgroup delegate (Linux only, cgroup v2 only).
	if h, ok := o.startCgroupDelegate(cname, rt, socketsDir); ok {
		handles = append(handles, h)
	}

	// 2. External services from config.loopholes (+ manifest host_daemon specs).
	discovered := loopholes.Discover(loopholes.DiscoverOptions{
		IncludeBundled:  true,
		LoopholesConfig: cfgMap(cfg, "loopholes"),
	})
	manifestSpecs := loopholes.ManifestHostDaemonSpecs(discovered)
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
		if name == brokerLoopholeName {
			// Host-wide singleton — ensure it, but no per-jail handle.
			o.brokerEnsure()
			continue
		}
		if h, ok := o.startExternalService(name, external[name], socketsDir); ok {
			handles = append(handles, h)
		} else {
			_ = out
		}
	}
	return handles
}

// stopLoopholes ports stop_loopholes WITH THE FROZEN GUARD STACK (do not
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
		o.relayKill(relayPIDFile(shortHash), filepath.Join(socketsDir, brokerLoopholeName+".sock"))
	}
	if fileExists(socketsDir) {
		_ = os.RemoveAll(socketsDir)
	}
}

// startCgroupDelegate ports _start_host_service_builtin_cgroup: start the
// builtin cgroup delegate as an IN-PROCESS goroutine (matching Python's thread
// model — no external binary), bound to <sockets_dir>/cgroup-delegate.sock.
// Skipped on macOS and non-cgroup-v2 Linux. The container cgroup is resolved
// lazily on the first request. See startCgroupDelegateInProc.
func (o *Options) startCgroupDelegate(cname, rt, socketsDir string) (loopholeDaemon, bool) {
	sockPath := filepath.Join(socketsDir, paths.CgdSocketName)
	stop, ok := o.startCgroupDelegateInProc(cname, rt, sockPath)
	if !ok {
		return loopholeDaemon{}, false
	}
	return loopholeDaemon{
		name:           paths.BuiltinCgroupLoopholeName,
		hostSocketPath: sockPath,
		jailSocketPath: paths.JailHostServicesDir + "/" + paths.BuiltinCgroupLoopholeName + ".sock",
		envVarName:     hostServiceEnvVar(paths.BuiltinCgroupLoopholeName),
		stop:           stop,
	}, true
}

// startExternalService ports _start_host_service_external (the common path):
// substitute {socket}, expand ~, spawn, wait for the socket to bind. Returns the
// handle on success.
func (o *Options) startExternalService(name string, spec *jsonx.OrderedMap, socketsDir string) (loopholeDaemon, bool) {
	if spec == nil {
		return loopholeDaemon{}, false
	}
	hostSocket := filepath.Join(socketsDir, name+".sock")
	_ = os.Remove(hostSocket)
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
		cmdArgs = append(cmdArgs, strings.ReplaceAll(s, "{socket}", hostSocket))
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
	if e := cfgMap(spec, "env"); e != nil {
		for _, k := range e.Keys() {
			if v, ok := mapGet(e, k).(string); ok {
				if strings.HasPrefix(v, "~") {
					v = expandUser(v)
				}
				env = append(env, k+"="+v)
			}
		}
		cmd.Env = env
	}
	if err := cmd.Start(); err != nil {
		o.pr(o.Stdout).print("[red]Failed to launch host service '" + name + "': " + err.Error() + "[/red]")
		return loopholeDaemon{}, false
	}
	// Wait for the socket to bind (5s).
	deadline := o.Now().Add(5 * time.Second)
	for o.Now().Before(deadline) {
		if fileExists(hostSocket) {
			break
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return loopholeDaemon{}, false
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !fileExists(hostSocket) {
		_ = cmd.Process.Kill()
		return loopholeDaemon{}, false
	}
	jailSocket := mapStrOr(spec, "jail_socket", paths.JailHostServicesDir+"/"+name+".sock")
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
		name:           name,
		hostSocketPath: hostSocket,
		jailSocketPath: jailSocket,
		envVarName:     hostServiceEnvVar(name),
		stop:           stop,
	}, true
}

// resolveContainerCgroup ports _resolve_container_cgroup: the host-side cgroup v2
// path for a running container, or "" (macOS → always ""). Best-effort.
func (o *Options) resolveContainerCgroup(cname, rt string) string {
	if o.IsMacOS {
		return ""
	}
	if rt == "podman" {
		res := o.Exec([]string{"podman", "inspect", "--format", "{{.State.CgroupPath}}", cname}, "", nil, 5*time.Second)
		if res.Ran && !res.Timeout && res.RC == 0 {
			if cg := strings.TrimSpace(res.Stdout); cg != "" {
				cand := filepath.Join("/sys/fs/cgroup", cg)
				if o.PathExists(cand) {
					return cand
				}
			}
		}
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{.State.Pid}}", cname}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout || res.RC != 0 {
		return ""
	}
	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil || pid <= 0 {
		return ""
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" {
			cand := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(parts[2], "/"))
			if o.PathExists(cand) {
				return cand
			}
		}
	}
	return ""
}

// daemonLauncher resolves a console-script daemon name to its launch argv via a
// plain PATH lookup: the console script IS the Go binary on PATH now. Returns
// nil when the name isn't on PATH (that nil-vs-bare-name difference vs
// broker.DaemonLauncher is preserved pending their unification). The
// former YOLO_GO_DAEMONS/YOLO_GO_BIN_DIR migration seam (dead — nothing set
// those vars) was removed.
func (o *Options) daemonLauncher(consoleName string) []string {
	if _, ok := o.LookPath(consoleName); ok {
		return []string{consoleName}
	}
	return nil
}

// --- broker singleton + relay (minimal ensure; supervision keyed per jail) ---
const (
	brokerSingletonPIDFile = "/tmp/yolo-claude-oauth-broker.pid"
	brokerSingletonLock    = "/tmp/yolo-claude-oauth-broker.lock"
	brokerSpawnTimeout     = 5 * time.Second
)

// brokerEnsure ports _broker_ensure: if the singleton is alive, no-op; else
// spawn it under a flock. Best-effort; never fails the caller.
func (o *Options) brokerEnsure() {
	if o.brokerIsAlive() {
		return
	}
	o.brokerSpawn()
}

// brokerIsAlive ports _broker_is_alive: PID file live + socket present + ping.
func (o *Options) brokerIsAlive() bool {
	pid, ok := readPIDFile(brokerSingletonPIDFile)
	if !ok || !pidAlive(pid) {
		return false
	}
	if !o.PathExists(brokerSingletonSocket) {
		return false
	}
	return brokerPing(brokerSingletonSocket, 2*time.Second)
}

// brokerSpawn ports _broker_spawn: flock, re-check liveness, clear stale socket,
// spawn the broker host daemon, write the PID file, wait for the socket.
func (o *Options) brokerSpawn() {
	_ = os.MkdirAll(filepath.Dir(brokerSingletonLock), 0o755)
	lf, err := os.OpenFile(brokerSingletonLock, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer lf.Close()
	if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX) != nil {
		return
	}
	if o.brokerIsAlive() {
		return
	}
	_ = os.Remove(brokerSingletonSocket)
	logDir := filepath.Join(paths.GlobalStorage(), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	// Self-exec'd `yolo internal daemon claude-oauth-broker --socket <sock>`: the
	// running yolo re-execs itself as the broker host daemon (full dedup onto
	// internal/broker.BrokerSpawn is a later step).
	argv := execx.SelfExecArgv([]string{"yolo", "internal", "daemon", "claude-oauth-broker", "--socket", brokerSingletonSocket})
	cmd := exec.Command(argv[0], argv[1:]...)
	if l, err := os.OpenFile(filepath.Join(logDir, "host-service-claude-oauth-broker.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		cmd.Stdout, cmd.Stderr = l, l
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return
	}
	_ = os.WriteFile(brokerSingletonPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	o.waitForSocket(brokerSingletonSocket, brokerSpawnTimeout)
}

// ensureBrokerRelay ports _ensure_broker_relay: heal the per-jail relay on every
// path that targets the jail. Skipped for Apple Container and when the singleton
// socket is absent.
func (o *Options) ensureBrokerRelay(cname, rt string) {
	if rt == "container" || !o.PathExists(brokerSingletonSocket) {
		return
	}
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	o.relayEnsure(cname, socketsDir)
}

// relayEnsure ports _relay_ensure: idempotent per-jail relay supervision under a
// flock. Spawns the self-exec'd `yolo internal daemon broker-relay` (see
// relaySpawnArgv).
func (o *Options) relayEnsure(cname, socketsDir string) {
	shortHash := relayShortHash(cname)
	pidFile := relayPIDFile(shortHash)
	sockPath := filepath.Join(socketsDir, brokerLoopholeName+".sock")
	if o.relayIsAlive(pidFile, sockPath) {
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
	if o.relayIsAlive(pidFile, sockPath) {
		return
	}
	o.relayKill(pidFile, sockPath)
	_ = os.MkdirAll(socketsDir, 0o755)
	_ = os.Remove(sockPath)
	argv := o.relaySpawnArgv(sockPath, brokerSingletonSocket, cname)
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
	if err := cmd.Start(); err != nil {
		return
	}
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	o.waitForSocket(sockPath, brokerSpawnTimeout)
}

// relaySpawnArgv builds the per-jail broker-relay spawn argv: the running yolo
// re-exec'd as `yolo internal daemon broker-relay --socket … --broker … --jail
// …`. SelfExecArgv substitutes os.Executable() for the bare "yolo" launcher
// token so the relay is tied to THIS binary regardless of PATH.
//
// The former YOLO_BROKER_RELAY_BIN gate and (deleted) Python broker_relay.py
// fallback are gone. That path was effectively dead — neither YOLO_BROKER_RELAY_BIN
// nor YOLO_REPO_ROOT was set in production, so relaySpawnArgv returned nil and
// the relay never started. Now it always yields a runnable argv, so relayEnsure
// actually spawns the relay whenever a broker loophole is active.
func (o *Options) relaySpawnArgv(sockPath, brokerSocket, cname string) []string {
	return execx.SelfExecArgv([]string{
		"yolo", "internal", "daemon", "broker-relay",
		"--socket", sockPath, "--broker", brokerSocket, "--jail", cname,
	})
}

func (o *Options) relayIsAlive(pidFile, sockPath string) bool {
	pid, ok := readPIDFile(pidFile)
	if !ok {
		return socketConnectable(sockPath, 2*time.Second)
	}
	if !pidAlive(pid) {
		return false
	}
	if !o.PathExists(sockPath) {
		return false
	}
	return socketConnectable(sockPath, 2*time.Second)
}

// relayKill ports _relay_kill (simplified): SIGTERM the relay PID (SIGKILL
// straggler), then remove the PID file. Identity/pgrep-fallback guards are
// omitted in this port slice — the PID file is the common case; a recycled-PID
// misfire is bounded by the pidAlive check.
func (o *Options) relayKill(pidFile, sockPath string) {
	pid, ok := readPIDFile(pidFile)
	if ok && pidAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := o.Now().Add(3 * time.Second)
		for o.Now().Before(deadline) {
			if !pidAlive(pid) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if pidAlive(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	_ = os.Remove(pidFile)
	_ = sockPath
}

// grace floor: a relay whose PID file's mtime is younger than this is spared, so
// one spawned for a jail mid-startup (ensured before its container is visible) is
// never reaped.
const relayOrphanGraceSeconds = 3600.0

// relayReapOrphans runs the backstop reap of _relay_reap_orphans, piggybacking
// on the store-prune gate's live-container enumeration (run_cmd.py:2760-2771).
// A per-jail relay outlives the yolo process that spawned it by design, and
// stopLoopholes only reaps the current jail's relay in that original process's
// graceful tail — jails ended from attach sessions would leak their relay
// forever otherwise. The current jail's relay (just ensured, container not yet
// started) is excluded by folding cname into the live set. liveKnown==false
// (liveness unenumerable) declines the sweep (unknown never reads as "nothing
// live"). Best-effort: reuses the byte-verified prune engine and the run path's
// own relayKill machinery, matching Python's _relay_kill(pid_file) call with no
// socket_path.
func (o *Options) relayReapOrphans(liveKnown bool, liveCnames map[string]struct{}, cname string) {
	o.relayReapOrphansIn("/tmp", liveKnown, liveCnames, cname)
}

// relayReapOrphansIn is relayReapOrphans with an injectable scan base (the pid-
// file dir). Production always passes "/tmp" (Python's hardcoded default); tests
// pass a temp dir. Returns the pid files reaped, so the cname-fold decision is
// assertable without touching /tmp.
func (o *Options) relayReapOrphansIn(base string, liveKnown bool, liveCnames map[string]struct{}, cname string) []string {
	// Fold in the current jail's cname so its freshly-ensured relay is never
	// reaped (Python passes `live_jails | {cname}`).
	live := map[string]struct{}{cname: {}}
	for c := range liveCnames {
		live[c] = struct{}{}
	}
	return prune.ReapRelayOrphans(
		base, liveKnown, live, relayOrphanGraceSeconds, true, o.Now(),
		func(pidFile string) { o.relayKill(pidFile, "") },
	)
}

func (o *Options) waitForSocket(sockPath string, timeout time.Duration) {
	deadline := o.Now().Add(timeout)
	for o.Now().Before(deadline) {
		if o.PathExists(sockPath) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
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

// socketConnectable ports _relay_socket_connectable: a plain connect() probe.
func socketConnectable(sockPath string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", sockPath, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
