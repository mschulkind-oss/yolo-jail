package entrypoint

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// tmpfs on podman, so a PID file here is naturally scoped to this jail and
// evaporates on restart. Package vars so tests can redirect them.
var supervisorPIDFile = "/tmp/yolo-jail-supervisor.pid"

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
		// Python: json.loads of a non-array yields a value that the `if not
		// ports` guard / for-loop handles. A JSON object/scalar that decodes
		// without error but isn't a list: `for entry in ports` would iterate
		// dict keys or raise. Real input is always a list; treat non-list as
		// empty (nothing to do), which matches `if not ports: return` for the
		// common empty cases.
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

// `yolo-jail-supervisor` as a detached child, once, guarded by a tmpfs PID
// file so repeated `podman exec yolo-entrypoint` calls don't stack
// supervisors. Absent/empty YOLO_JAIL_DAEMONS means nothing to do.
// The supervisor is the baked-in Go binary (cmd/yolo-jail-supervisor),
// resolved on PATH from the image /bin. It reads YOLO_JAIL_DAEMONS from the
// inherited environment — no argv, no PYTHONPATH.
func startJailDaemonSupervisor(e *Env) {
	if strings.TrimSpace(e.Getenv("YOLO_JAIL_DAEMONS")) == "" {
		return
	}
	if supervisorIsAlive(supervisorPIDFile) {
		return
	}
	bin, err := exec.LookPath("yolo-jail-supervisor")
	if err != nil {
		// Supervisor binary not on PATH — best-effort, don't abort boot.
		return
	}
	cmd := exec.Command(bin)
	cmd.Env = os.Environ()
	// stdout/stderr DEVNULL, close_fds default. start_new_session=False: stay in
	// the same process group as PID 1 (Go's default — no Setsid).
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return
	}
	// Best-effort PID-file write; losing it just risks a redundant supervisor.
	_ = os.WriteFile(supervisorPIDFile, []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	// Reap the child asynchronously so it doesn't linger as a zombie if it
	// exits while PID 1 is still alive (Python's Popen leaves reaping to the OS
	// on PID-1 exit; a background Wait matches "detached, kernel-reaped" without
	// blocking boot).
	go func() { _ = cmd.Wait() }()
}

// portInUse check if a TCP port is already bound
// on localhost by attempting to bind it.
func portInUse(port int) bool {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return true
	}
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
		// Python opens unconditionally; a failure to open would raise. Best-
		// effort: without a log sink we can't fork socat safely, so bail.
		return
	}
	// Keep logFile open for the lifetime of the spawned socats (they inherit the
	// fd). We intentionally do NOT close it — the forked children write to it.

	for _, entry := range ports {
		localPort, ok := forwardEntryPort(entry)
		if !ok {
			e.warn("Warning: invalid port forward entry: " + pyStr(entry))
			continue
		}

		if portInUse(localPort) {
			continue
		}

		var target string
		if hostGateway != "" {
			// TCP gateway mode: connect directly to host via host gateway.
			target = "TCP:" + hostGateway + ":" + strconv.Itoa(localPort)
		} else {
			// Unix socket mode: connect to bind-mounted socket from host.
			sockPath := filepath.Join(forwardSocketDir, "port-"+strconv.Itoa(localPort)+".sock")
			if !pathExists(sockPath) {
				e.warn("Warning: socket " + sockPath + " not found for port " + strconv.Itoa(localPort))
				continue
			}
			target = "UNIX-CONNECT:" + sockPath
		}

		cmd := exec.Command("socat",
			"TCP-LISTEN:"+strconv.Itoa(localPort)+",bind=127.0.0.1,fork,reuseaddr",
			target,
		)
		cmd.Stdout = nil
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			// Python catches FileNotFoundError (socat missing) and returns,
			// closing the log; any other exception warns and continues.
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
				e.warn("Warning: socat not found, cannot forward host ports")
				_ = logFile.Close()
				return
			}
			e.warn("Warning: failed to forward port " + strconv.Itoa(localPort) + ": " + err.Error())
			continue
		}
		// Reap asynchronously; the socat runs for the jail's lifetime.
		go func() { _ = cmd.Wait() }()
	}
}

// start_container_port_forwarding: an int is the port; a string with ":" takes
// the part before the first colon; a bare string is parsed as an int; anything
// else is invalid (warn + skip). jsonx.Decode of a JSON array yields string /
// float64 / bool / nil / jsonInt elements — JSON integers decode to jsonInt
// (Python's isinstance(entry, int) True); JSON floats to float64 (isinstance
// int False, str False -> warn branch).
// PARITY QUIRK: on a bare non-numeric string, Python's int(...) raises an
// uncaught ValueError that propagates out of main() and CRASHES boot before the
// exec (module map flags this as a quirk to preserve, not fix). mustAtoiPort
// panics on the same input to preserve the "boot aborts, never execs bash"
// behavior.
func forwardEntryPort(entry any) (int, bool) {
	if isJSONInt(entry) {
		return mustAtoiPort(pyStr(entry)), true
	}
	if v, ok := entry.(string); ok {
		if strings.Contains(v, ":") {
			return mustAtoiPort(strings.SplitN(v, ":", 2)[0]), true
		}
		return mustAtoiPort(v), true
	}
	return 0, false
}

// mustAtoiPort a valid integer parses; garbage raises
// ValueError (uncaught -> boot crash). We panic to preserve the crash behavior.
func mustAtoiPort(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		// Match Python's uncaught ValueError crashing the entrypoint.
		panic("invalid literal for int(): " + strconv.Quote(s))
	}
	return n
}

// runWithTimeoutSeconds runs cmd, killing it after `secs` seconds, mirroring
// subprocess.run(..., timeout=secs). A timeout returns an error so callers can
// warn like Python's TimeoutExpired path.
func runWithTimeoutSeconds(cmd *exec.Cmd, secs int) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(time.Duration(secs) * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return errTimeout
	}
}

var errTimeout = errors.New("timeout")

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
