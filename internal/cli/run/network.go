package run

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// planPortForwards parses the host forwards a fresh launch's gate answered (hostForwardPorts, merged
// with the implicit provider forwards) into the ones its keeper starts, printing each warning the
// parse has on the launch's own terminal, before the spawn. nil when there are none, or the entries
// do not parse.
func (o *Options) planPortForwards(forwardHostPorts []any) []PortForward {
	if len(forwardHostPorts) == 0 {
		return nil
	}
	parsed, err := ParsePortForwards(forwardHostPorts, o.pr(o.Stderr).print)
	if err != nil {
		return nil
	}
	return parsed
}

// startPortForwards spawns one
// `socat UNIX-LISTEN:<sock>,fork,mode=777 TCP:127.0.0.1:<hostPort>` per planned
// forward, waits (condition-poll) for the socket files to appear, and returns the
// live process handles (native proxying is out of scope). Must run BEFORE the
// container so the socket files exist when the container-side socat connects.
//
// cname keys the socat log; socketDir is the per-jail /tmp/yolo-fwd-<cname> dir. Returns the socat
// *exec.Cmd handles.
//
// THE WHOLE DIR GOES FIRST, not just the sockets about to be forwarded (JL-D32). It is bind-mounted
// read-write into the workspace's next jail, and a socat a SIGKILLed launch or keeper left behind
// for a forward the config has since dropped would stay reachable from that jail through its socket.
// And each socat a keeper starts gets the kernel's death signal, so none outlives the keeper.
func (o *Options) startPortForwards(parsed []PortForward, cname string, socketDir string) []*exec.Cmd {
	if len(parsed) == 0 {
		return nil
	}
	out := o.pr(o.Stderr)
	_ = os.RemoveAll(socketDir)
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
		argv := SocatArgv(sockPath, pf.HostPort)
		cmd := exec.Command(argv[0], argv[1:]...)
		if o.keeperMode {
			cmd.SysProcAttr = &syscall.SysProcAttr{}
			setChildDeathSignal(cmd.SysProcAttr)
		}
		cmd.Stdout = nil
		if logFile != nil {
			cmd.Stderr = logFile
		}
		if err := cmd.Start(); err != nil {
			// socat absent → warn once and stop.
			out.print("Warning: socat not found on host, cannot forward ports. " +
				"Install socat (e.g., nix-shell -p socat, apt install socat).")
			break
		}
		procs = append(procs, cmd)
		expected = append(expected, sockPath)
	}

	// Wait for the socket files (condition-poll, fast path + deadline).
	if len(procs) > 0 {
		// Real wall clock, deliberately NOT o.Now() — see waitServiceReady's rationale
		// in loopholesruntime.go. o.Now is an injectable logical clock that
		// tests freeze to make decisions deterministic; a drain/poll loop built
		// on it never advances past its deadline and spins forever.
		deadline := time.Now().Add(SocketWaitDeadline)
		for {
			if allExist(expected) {
				break
			}
			if !time.Now().Before(deadline) {
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

// cleanupPortForwarding SIGTERMs each socat (SIGKILL on timeout) and removes the
// socket dir. Best-effort.
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
