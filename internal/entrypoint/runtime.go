package entrypoint

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/supervisor"
)

// tmpfs on podman, so a PID file here is naturally scoped to this jail and
// evaporates on restart. Package vars so tests can redirect them. Only the
// entrypoint reads/writes this file, so its name tracks the current spawn
// binary (yolo-jaild). legacySupervisorPIDFiles stay readable after a binary
// rename: a running jail can retain its old supervisor across `just install`,
// and starting a second one duplicates every service listener.
var (
	supervisorPIDFile        = "/tmp/yolo-jaild.pid"
	legacySupervisorPIDFiles = []string{"/tmp/yolo-jail-supervisor.pid"}
	procRoot                 = "/proc"
)

// where host-side socat has already created Unix sockets.
var forwardSocketDir = "/tmp/yolo-fwd"

// iptables DNAT rules so published ports reach services bound to 127.0.0.1.
// Reads YOLO_PUBLISHED_PORTS (JSON array of "PORT/PROTO" strings). Silently
// skips if iptables is unavailable (e.g. when NET_ADMIN is missing).
func setupPublishedPortLocalnet(e *Env) {
	raw := e.Getenv("YOLO_PUBLISHED_PORTS")
	if raw == "" {
		return
	}

	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		e.warn("Warning: invalid YOLO_PUBLISHED_PORTS: " + raw)
		return
	}
	ports, ok := decoded.([]any)
	if !ok {
		// Real input is always a JSON array; a non-list decode has nothing to
		// forward, so treat it as empty.
		return
	}
	if len(ports) == 0 {
		return
	}

	iptablesBin, err := exec.LookPath("iptables")
	if err != nil {
		return
	}

	for _, entry := range ports {
		s := pyStr(entry)
		parts := strings.SplitN(s, "/", 2)
		port := parts[0]
		proto := "tcp"
		if len(parts) > 1 {
			proto = parts[1]
		}
		cmd := exec.Command(iptablesBin,
			"-t", "nat",
			"-A", "PREROUTING",
			"-p", proto,
			"--dport", port,
			"-j", "DNAT",
			"--to-destination", "127.0.0.1:"+port,
		)
		cmd.Stdout = nil
		cmd.Stderr = nil
		if err := runWithTimeoutSeconds(cmd, 5); err != nil {
			e.warn("Warning: iptables DNAT for port " + port + "/" + proto + ": " + err.Error())
		}
	}
}

// supervisorIsAlive read pid_file and
// return true iff the PID it names is still a live process. Missing/unreadable/
// corrupt file -> false. Signal 0 is the canonical Unix liveness probe (EPERM
// counts as alive).
func supervisorIsAlive(pidFile string) bool {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return false
	}
	err = syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if errors.Is(err, syscall.ESRCH) {
		// ProcessLookupError -> not alive.
		return false
	}
	if errors.Is(err, syscall.EPERM) {
		// PermissionError -> PID exists but we can't signal it; still alive.
		return true
	}
	// Any other OSError -> not alive.
	return false
}

func anyJailDaemonSupervisorAlive() bool {
	if supervisorIsAlive(supervisorPIDFile) {
		return true
	}
	for _, pidFile := range legacySupervisorPIDFiles {
		if supervisorIsAlive(pidFile) {
			return true
		}
	}
	return false
}

type orphanedJailDaemon struct {
	Name string
	PID  int
}

// findOrphanedJailDaemons finds only direct children described by the current
// daemon manifest. It deliberately compares the full command (apart from the
// executable's directory): a process merely using the same TCP port is never
// ours to kill. This runs only after both the current and legacy supervisor PID
// files proved dead, so a matching process has no supervisor left to manage it.
func findOrphanedJailDaemons(raw string) ([]orphanedJailDaemon, error) {
	specs := supervisor.ParseEnv(raw)
	if len(specs) == 0 {
		return nil, nil
	}
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	var found []orphanedJailDaemon
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join(procRoot, entry.Name(), "cmdline"))
		if err != nil || len(cmdline) == 0 {
			continue
		}
		argv := strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00")
		for _, spec := range specs {
			if daemonCommandMatches(argv, spec.Cmd) {
				found = append(found, orphanedJailDaemon{Name: spec.Name, PID: pid})
				break
			}
		}
	}
	return found, nil
}

func daemonCommandMatches(argv, wanted []string) bool {
	if len(argv) != len(wanted) || len(argv) == 0 {
		return false
	}
	if filepath.Base(argv[0]) != filepath.Base(wanted[0]) {
		return false
	}
	for i := 1; i < len(argv); i++ {
		if argv[i] != wanted[i] {
			return false
		}
	}
	return true
}

var (
	findJailDaemonOrphans = findOrphanedJailDaemons
	killJailDaemonOrphan  = killOrphanedJailDaemon
)

// killOrphanedJailDaemon is intentionally a hard reclaim. A daemon left behind
// without its supervisor is already outside the supervisor's graceful shutdown
// contract; SIGKILL gives the next boot a definite ownership boundary rather
// than waiting indefinitely for an unowned process to cooperate.
func killOrphanedJailDaemon(pid int) error {
	err := syscall.Kill(pid, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, syscall.EPERM) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process did not exit after SIGKILL")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func reclaimOrphanedJailDaemons(e *Env) error {
	orphans, err := findJailDaemonOrphans(e.Getenv("YOLO_JAIL_DAEMONS"))
	if err != nil {
		return fmt.Errorf("find orphaned in-jail daemons: %w", err)
	}
	for _, orphan := range orphans {
		e.warn(fmt.Sprintf("yolo: reclaiming orphaned in-jail daemon %s (pid %d); no live supervisor owns it", orphan.Name, orphan.PID))
		if err := killJailDaemonOrphan(orphan.PID); err != nil {
			return fmt.Errorf("reclaim orphaned in-jail daemon %s (pid %d): %w", orphan.Name, orphan.PID, err)
		}
		e.warn(fmt.Sprintf("yolo: reclaimed orphaned in-jail daemon %s (pid %d)", orphan.Name, orphan.PID))
	}
	return nil
}

// `yolo-jaild supervise` as a detached child, once, guarded by a tmpfs PID
// file so repeated `podman exec yolo-entrypoint` calls don't stack
// supervisors. Absent/empty YOLO_JAIL_DAEMONS means nothing to do.
// The supervisor is the baked-in Go binary (cmd/yolo-jaild) invoked with the
// `supervise` subcommand, resolved on PATH from the image /bin. It reads
// YOLO_JAIL_DAEMONS from the inherited environment — no argv, no PYTHONPATH.
func startJailDaemonSupervisor(e *Env) error {
	if strings.TrimSpace(e.Getenv("YOLO_JAIL_DAEMONS")) == "" {
		return nil
	}
	if anyJailDaemonSupervisorAlive() {
		if e.Getenv(paths.VerboseEnv) != "" || e.Getenv("YOLO_JAIL_TIMING") != "" {
			e.warn("yolo: reusing the live in-jail daemon supervisor; it owns existing service listeners")
		}
		return nil
	}
	if err := reclaimOrphanedJailDaemons(e); err != nil {
		return err
	}
	readyNames := strings.Fields(strings.ReplaceAll(e.Getenv(paths.JailDaemonReadyNamesEnv), ",", " "))
	ready := map[string]bool{}
	for _, name := range readyNames {
		ready[name] = true
	}
	bin, err := exec.LookPath("yolo-jaild")
	if err != nil {
		if len(ready) == 0 {
			// Preserve the established best-effort behavior for daemon groups
			// with no endpoint readiness dependency.
			return nil
		}
		return fmt.Errorf("find jail daemon supervisor: %w", err)
	}
	cmd := exec.Command(bin, "supervise")
	cmd.Env = os.Environ()
	var readyRead, readyWrite *os.File
	if len(ready) > 0 {
		readyRead, readyWrite, err = os.Pipe()
		if err != nil {
			return fmt.Errorf("create jail-daemon readiness pipe: %w", err)
		}
		cmd.ExtraFiles = []*os.File{readyWrite}
		cmd.Env = append(cmd.Env, paths.JailDaemonReadyFDEnv+"=3")
	}
	// stdout/stderr DEVNULL, close_fds default. start_new_session=False: stay in
	// the same process group as PID 1 (Go's default — no Setsid).
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		if readyRead != nil {
			// Closing our own never-written pipe ends: the only error either can
			// report is EBADF on a double close, which this branch cannot reach, and
			// the error we are about to return is the one that matters.
			_ = readyRead.Close()
			_ = readyWrite.Close()
		}
		if len(ready) == 0 {
			return nil
		}
		return fmt.Errorf("start jail daemon supervisor: %w", err)
	}
	if readyWrite != nil {
		// Dropping our copy of the write end is what makes the scanner below see EOF
		// when the child dies. A close error here can only be EBADF (we opened it and
		// have not closed it), and the readiness wait reports the consequence anyway:
		// a write end nobody closed shows up as a hang, not as a lost error.
		_ = readyWrite.Close()
	}
	// The PID file is the ONLY guard against a second supervisor, and this file's
	// header states the consequence of losing it: "starting a second one duplicates
	// every service listener". A silent loss therefore degrades the next
	// `podman exec` into a jail whose services are bound twice, so say so — the
	// write still does not gate the boot, because the supervisor this launch started
	// is already running and refusing now would be strictly worse.
	if err := os.WriteFile(supervisorPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644); err != nil {
		e.warn("Warning: could not record the in-jail daemon supervisor's pid at " +
			supervisorPIDFile + ": " + err.Error() + "; a later re-entry into this " +
			"container cannot see that a supervisor is already running and may start a " +
			"second one, duplicating every service listener")
	}
	// Reap the child asynchronously so it doesn't linger as a zombie if it
	// exits while PID 1 is still alive; a background Wait keeps the supervisor
	// detached without blocking boot.
	//
	// The Wait error is dropped and must stay dropped: it arrives whenever the
	// supervisor exits, which is normally long after execBash has replaced this
	// process — and before that, after Main closed boot.log, so e.Stderr is a
	// MultiWriter over a closed file. Reporting from here would be a write race on a
	// sink that no longer exists, against a reader who has already been handed the
	// terminal. The readiness pipe above is the in-band channel for the failures a
	// boot CAN act on.
	go func() { _ = cmd.Wait() }()
	if readyRead == nil {
		return nil
	}
	defer readyRead.Close()
	e.warn("yolo: waiting for required in-jail service readiness: " + strings.Join(readyNames, ", "))
	logPaths := make([]string, 0, len(readyNames))
	for _, name := range readyNames {
		logPaths = append(logPaths, filepath.Join(e.Home, ".local", "state", "yolo-jail-daemons", name+".log"))
	}
	e.warn("  Daemon diagnostics: " + strings.Join(logPaths, ", "))
	scanner := bufio.NewScanner(readyRead)
	for len(ready) > 0 {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return fmt.Errorf("wait for jail-daemon readiness: %w", err)
			}
			return fmt.Errorf("jail daemon supervisor exited before ready services %s", strings.Join(readyNames, ", "))
		}
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || (fields[0] != "ready" && fields[0] != "failed") || !ready[fields[1]] {
			return fmt.Errorf("jail daemon reported unexpected readiness %q", scanner.Text())
		}
		if fields[0] == "failed" {
			reason := "no reason reported"
			if len(fields) > 2 {
				reason = strings.Join(fields[2:], " ")
			}
			return fmt.Errorf("jail daemon %q cannot publish its required endpoint: %s", fields[1], reason)
		}
		if e.Getenv(paths.VerboseEnv) != "" || e.Getenv("YOLO_JAIL_TIMING") != "" {
			e.warn("yolo: required in-jail service ready: " + fields[1])
		}
		delete(ready, fields[1])
	}
	return nil
}

// portInUse check if a TCP port is already bound
// on localhost by attempting to bind it.
func portInUse(port int) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return true
	}
	// The probe's whole answer is the successful Listen above; the close only gives
	// the port back. A failure to close cannot change the answer, and the caller
	// would learn of it as a bind failure on the very next line.
	_ = ln.Close()
	return false
}

// start container-side socat (TCP-LISTEN on localhost -> host service) for each
// port in YOLO_FORWARD_HOST_PORTS (JSON array). Unix-socket mode (Linux) or TCP
// gateway mode (macOS, via YOLO_FWD_HOST_GATEWAY). Skips already-bound ports.
func startContainerPortForwarding(e *Env) {
	raw := e.Getenv("YOLO_FORWARD_HOST_PORTS")
	if raw == "" {
		return
	}

	decoded, err := jsonx.Decode([]byte(raw))
	if err != nil {
		e.warn("Warning: invalid YOLO_FORWARD_HOST_PORTS: " + raw)
		return
	}
	ports, ok := decoded.([]any)
	if !ok {
		return
	}
	if len(ports) == 0 {
		return
	}

	hostGateway := e.Getenv("YOLO_FWD_HOST_GATEWAY")

	logPath := filepath.Join(e.Home, ".yolo-socat.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// Without a log sink we can't fork socat safely, so bail.
		return
	}
	// Keep logFile open for the lifetime of the spawned socats (they inherit the
	// fd). We intentionally do NOT close it — the forked children write to it.

	for _, entry := range ports {
		localPort, hostPort, ok := forwardEntryPorts(entry)
		if !ok {
			e.warn("Warning: invalid port forward entry: " + pyStr(entry))
			continue
		}

		if portInUse(localPort) {
			continue
		}

		target, sockPath := containerForwardTarget(hostGateway, forwardSocketDir, localPort, hostPort)
		if sockPath != "" && !pathExists(sockPath) {
			e.warn("Warning: socket " + sockPath + " not found for port " + strconv.Itoa(localPort))
			continue
		}

		cmd := exec.Command("socat",
			"TCP-LISTEN:"+strconv.Itoa(localPort)+",bind=127.0.0.1,fork,reuseaddr",
			target,
		)
		cmd.Stdout = nil
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			// A missing socat binary is fatal to forwarding: close the log and
			// return. Any other error warns and continues to the next port.
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
				e.warn("Warning: socat not found, cannot forward host ports")
				// Nothing was written to this log, so a close error has no buffered
				// bytes to lose and nothing left to report about; the warning above is
				// the finding.
				_ = logFile.Close()
				return
			}
			e.warn("Warning: failed to forward port " + strconv.Itoa(localPort) + ": " + err.Error())
			continue
		}
		// Reap asynchronously; the socat runs for the jail's lifetime. Its Wait error
		// is dropped for the same reason the supervisor's is: it arrives after the
		// boot has handed over, when e.Stderr is a MultiWriter over a closed boot.log.
		// The forward's own diagnostics go to the log file this socat inherited
		// (~/.yolo-socat.log), which outlives the boot and is the right reader for a
		// forward that dies hours in.
		go func() { _ = cmd.Wait() }()
	}
}

// forwardEntryPorts resolves a port-forward entry into the port the JAIL listens
// on and the HOST port it reaches. A forward_host_ports string is
// "<local>:<host>" — jail side FIRST, the opposite order from `network.ports`
// (podman's "<host>:<container>"). An int, or a string with no colon, is the same
// port on both sides. Anything else is invalid (warn + skip).
//
// jsonx.Decode of a JSON array yields string / float64 / bool / nil / jsonInt
// elements — JSON integers decode to jsonInt, JSON floats to float64 (which hits
// the warn branch). INVARIANT: a bare non-numeric string is a hard error —
// mustAtoiPort panics so boot aborts before the exec rather than starting bash
// with a broken forward. That is also what catches a publish-style
// "ip:host:container" string that got past `yolo check`.
func forwardEntryPorts(entry any) (local, host int, ok bool) {
	if isJSONInt(entry) {
		p := mustAtoiPort(pyStr(entry))
		return p, p, true
	}
	if v, ok := entry.(string); ok {
		if strings.Contains(v, ":") {
			parts := strings.SplitN(v, ":", 2)
			return mustAtoiPort(parts[0]), mustAtoiPort(parts[1]), true
		}
		p := mustAtoiPort(v)
		return p, p, true
	}
	return 0, 0, false
}

// containerForwardTarget returns the socat destination for one forwarded port,
// plus the Unix socket that must exist first ("" when there is none).
//
// It takes BOTH ports because the two backends need different ones, and the bug
// this function exists to make unrepresentable was a caller reaching for the
// wrong one:
//
//   - TCP-gateway mode (macOS podman, hostGateway set) has no host-side socat, so
//     nothing has applied the remap yet — it must dial the HOST port.
//   - Unix-socket mode (Linux podman, Apple Container) connects to the host-side
//     socat, which already targets the host port and named its socket after the
//     LOCAL port. Re-applying the remap here would look for a socket nobody made.
func containerForwardTarget(hostGateway, socketDir string, localPort, hostPort int) (target, sockPath string) {
	if hostGateway != "" {
		return "TCP:" + hostGateway + ":" + strconv.Itoa(hostPort), ""
	}
	sockPath = filepath.Join(socketDir, "port-"+strconv.Itoa(localPort)+".sock")
	return "UNIX-CONNECT:" + sockPath, sockPath
}

// mustAtoiPort parses a valid integer; garbage panics, aborting boot rather
// than proceeding with an invalid forward.
func mustAtoiPort(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		// A non-numeric port literal aborts the entrypoint.
		panic("invalid literal for int(): " + strconv.Quote(s))
	}
	return n
}

// runWithTimeoutSeconds runs cmd, killing it after `secs` seconds. A timeout
// returns an error so callers can warn.
func runWithTimeoutSeconds(cmd *exec.Cmd, secs int) error {
	return runWithTimeout(cmd, time.Duration(secs)*time.Second)
}

// runWithTimeout is the same bound expressed as a Duration, which is what
// runBoundedStep needs so a test can hand it a bound short enough to reach the
// timeout branch. Seconds remain the spelling at the call sites, because seconds
// are what the bounds are chosen in.
func runWithTimeout(cmd *exec.Cmd, limit time.Duration) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		// Kill's error is deliberately dropped: the only failure it can report is
		// that the process already exited (ESRCH), and the `<-done` below is the
		// authority on that either way — it cannot return until Wait has reaped the
		// child, killed or not. Reporting ESRCH here would announce a race we just
		// won.
		_ = cmd.Process.Kill()
		<-done
		return errTimeout
	}
}

var errTimeout = errors.New("timeout")

// boundedStepSlowNotice is the elapsed time past which a bounded step that
// SUCCEEDED is still worth the terminal's attention. A wait the user paid for is
// a fact about their launch whether or not it ended in an error, and a boot that
// stalls for seconds and then says nothing is the shape that gets reported as "it
// just hangs": the time is charged to the user and the reason to nobody. A var,
// not a const, so a test can reach the branch without sleeping for it.
var boundedStepSlowNotice = 2 * time.Second

// runBoundedStep runs one bounded boot subprocess and REPORTS WHAT HAPPENED. It is
// the answer to this package's densest silent-decision shape: a `_ =
// runWithTimeoutSeconds(cmd, 30)` spends up to thirty seconds of the user's launch
// and then discards the only record that it did.
//
// Three dispositions, and each is a different reader:
//
//   - TIMEOUT -> warn. The worst outcome available, because the step did not happen
//     AND the boot got slower, so it must name what was waited on and how long.
//   - FAILURE -> warn. The step did not happen; the command said why.
//   - SUCCESS -> note (boot.log only), which is what makes the log answer "did it
//     happen?" rather than only "what went wrong?" — unless it was SLOW, in which
//     case the terminal gets it too.
//
// Callers keep the best-effort POLARITY they had: nothing here refuses a boot. What
// changes is that the degradation is visible.
func runBoundedStep(e *Env, what string, limit time.Duration, cmd *exec.Cmd) {
	start := time.Now()
	err := runWithTimeout(cmd, limit)
	elapsed := time.Since(start).Round(time.Millisecond)
	switch {
	case errors.Is(err, errTimeout):
		e.warn(fmt.Sprintf("Warning: %s timed out after %s and was killed; "+
			"it did not complete, and the boot continues without it", what, limit))
	case err != nil:
		e.warn(fmt.Sprintf("Warning: %s failed after %s: %v; "+
			"the boot continues without it", what, elapsed, err))
	case elapsed >= boundedStepSlowNotice:
		e.warn(fmt.Sprintf("Note: %s took %s (bound %s)", what, elapsed, limit))
	default:
		e.note(fmt.Sprintf("%s: ok in %s", what, elapsed))
	}
}

// envWith returns environ with key set to val (appended or overriding). Mirrors
// {**os.environ, key: val}: a later assignment wins.
func envWith(environ []string, key, val string) []string {
	out := make([]string, 0, len(environ)+1)
	prefix := key + "="
	replaced := false
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			out = append(out, prefix+val)
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced {
		out = append(out, prefix+val)
	}
	return out
}
