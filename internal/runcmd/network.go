package runcmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// startHostPortForwarding ports start_host_port_forwarding: spawn one
// `socat UNIX-LISTEN:<sock>,fork,mode=777 TCP:127.0.0.1:<hostPort>` per parsed
// forward, wait (condition-poll) for the socket files to appear, and return the
// live process handles. Go spawns the IDENTICAL socat argv (native proxying is
// out of scope). Must run BEFORE the container so the socket files exist when
// the container-side socat connects.
//
// forwardHostPorts are the raw config entries; cname keys the socat log; socketDir
// is the per-jail /tmp/yolo-fwd-<cname> dir. Returns the socat *exec.Cmd handles.
func (o *Options) startHostPortForwarding(forwardHostPorts []any, cname string, socketDir string) []*exec.Cmd {
	if len(forwardHostPorts) == 0 {
		return nil
	}
	out := o.pr(o.Stderr)
	parsed, err := ParsePortForwards(forwardHostPorts, out.print)
	if err != nil || len(parsed) == 0 {
		return nil
	}
	if err := os.MkdirAll(socketDir, 0o755); err != nil {
		return nil
	}
	logDir := filepath.Join(paths.GlobalStorage(), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logFile, _ := os.OpenFile(filepath.Join(logDir, cname+"-socat.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)

	var procs []*exec.Cmd
	var expected []string
	for _, pf := range parsed {
		sockPath := SocketPath(socketDir, pf.LocalPort)
		_ = os.Remove(sockPath) // remove stale socket from a previous run
		argv := SocatArgv(sockPath, pf.HostPort)
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Stdout = nil
		if logFile != nil {
			cmd.Stderr = logFile
		}
		if err := cmd.Start(); err != nil {
			// socat absent → warn once and stop (FileNotFoundError branch).
			out.print("Warning: socat not found on host, cannot forward ports. " +
				"Install socat (e.g., nix-shell -p socat, apt install socat).")
			break
		}
		procs = append(procs, cmd)
		expected = append(expected, sockPath)
	}

	// Wait for the socket files (condition-poll, fast path + deadline).
	if len(procs) > 0 {
		deadline := o.Now().Add(SocketWaitDeadline)
		for {
			if allExist(expected) {
				break
			}
			if !o.Now().Before(deadline) {
				var missing []string
				for _, s := range expected {
					if !fileExists(s) {
						missing = append(missing, s)
					}
				}
				out.print(SocketNotReadyWarning(missing))
				break
			}
			time.Sleep(SocketWaitPollInterval)
		}
	}
	return procs
}

// cleanupPortForwarding ports cleanup_port_forwarding: SIGTERM each socat
// (SIGKILL on timeout) and rmtree the socket dir. Best-effort.
func cleanupPortForwarding(procs []*exec.Cmd, socketDir string) {
	for _, cmd := range procs {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		_ = cmd.Process.Signal(syscall.SIGTERM) // terminate() == SIGTERM
		done := make(chan struct{})
		go func(c *exec.Cmd) { _ = c.Wait(); close(done) }(cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	if socketDir != "" && fileExists(socketDir) {
		_ = os.RemoveAll(socketDir)
	}
}

func allExist(socketPaths []string) bool {
	for _, p := range socketPaths {
		if !fileExists(p) {
			return false
		}
	}
	return true
}
