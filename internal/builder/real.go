package builder

import (
	"bufio"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// realProc adapts an already-started *exec.Cmd to the Proc interface for
// poll-based liveness. A dedicated goroutine calls cmd.Wait() and records the
// exit state under a mutex, so Poll() can report done=true once the child dies.
// The previous implementation never called Wait(): a detached child was left
// unreaped, ProcessState stayed nil forever, and a Signal(0) probe on an
// unreaped zombie still succeeds — so Poll() could NEVER report done, making
// pollUntilReachable's "builder process exited early" fast-fail branch dead
// code.
type realProc struct {
	cmd  *exec.Cmd
	mu   sync.Mutex
	done bool
	code int
}

// newRealProc starts cmd (if not already started) and spawns the reaper
// goroutine. cmd must have its stdio/SysProcAttr configured before the call.
func newRealProc(cmd *exec.Cmd) (*realProc, error) {
	if cmd.Process == nil {
		if err := cmd.Start(); err != nil {
			return nil, err
		}
	}
	p := &realProc{cmd: cmd}
	go func() {
		err := cmd.Wait()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		} else if err != nil {
			code = 1
		}
		p.mu.Lock()
		p.done = true
		p.code = code
		p.mu.Unlock()
	}()
	return p, nil
}

// Poll reports (returncode, done). done=false while running (matches Python's
// proc.poll() is None); done=true with the exit code once the reaper goroutine
// has observed the child exit.
func (p *realProc) Poll() (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.code, p.done
}

// RealDeps returns Deps backed by real sockets / subprocesses / filesystem,
// mirroring builder.py's helpers. Confirm/Out default to the terminal; the
// front door may override Out.
func RealDeps() Deps {
	return Deps{
		IsMacOS:               func() bool { return runtime.GOOS == "darwin" },
		Reachable:             builderReachableReal,
		FileIsFile:            fileIsFileReal,
		ReadFileText:          readFileTextReal,
		NixCustomConfIncluded: storage.NixCustomConfIncluded,
		CurrentTrustedUsers:   currentTrustedUsersReal,
		DetectNixDaemonLabel:  storage.DetectNixDaemonLabel,
		HostUser:              hostUserReal,
		RunSetupScript:        runSetupScriptReal,
		StartVMForeground:     startVMForegroundReal,
		StartVMDetached:       startVMDetachedReal,
		ReadBuilderPID:        readBuilderPIDReal,
		PIDIsLive:             pidIsLiveReal,
		StopVM:                stopVMReal,
		Sleep:                 func(s float64) { time.Sleep(time.Duration(s * float64(time.Second))) },
		Now:                   func() float64 { return float64(time.Now().UnixNano()) / 1e9 },
		Confirm:               confirmReal,
		Out:                   os.Stdout,
		Color:                 true,
		IsTTYStdout:           isTTYStdoutReal,
	}
}

// isTTYStdoutReal reports whether os.Stdout is a real terminal (the shared
// internal/tty ioctl probe), so color is emitted only to a terminal and never
// to a pipe/file.
func isTTYStdoutReal() bool {
	return tty.IsTerminalFile(os.Stdout)
}

// 127.0.0.1:BUILDER_PORT with a 1s timeout; any error → false.
func builderReachableReal() bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(BuilderPort), time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func fileIsFileReal(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func readFileTextReal(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// (timeout 10s), find the trusted-users line, split its value. Best-effort.
func currentTrustedUsersReal() []string {
	cmd := exec.Command("nix", "config", "show")
	var buf strings.Builder
	cmd.Stdout = &buf
	if err := cmd.Start(); err != nil {
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return nil
	case err := <-done:
		if err != nil {
			return nil
		}
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "trusted-users") && strings.Contains(line, "=") {
			return strings.Fields(strings.SplitN(line, "=", 2)[1])
		}
	}
	return nil
}

func hostUserReal() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("LOGNAME"); u != "" {
		return u
	}
	return ""
}

// runSetupScriptReal pipes the script to `sudo bash -s` with the tty inherited
// (timeout 120s). Returns (returncode, ok).
func runSetupScriptReal(script string) (int, bool) {
	cmd := exec.Command("sudo", "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(120 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return 1, true
	case err := <-done:
		if err == nil {
			return 0, true
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode(), true
		}
		return 1, true
	}
}

// startVMForegroundReal inherit stdio.
// A Ctrl-C (SIGINT to the group) is expected — the caller treats a nil error
// (or interrupt) as "proceed to the key check".
func startVMForegroundReal() error {
	cmd := exec.Command("nix", "run", "nixpkgs#darwin.linux-builder")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	// KeyboardInterrupt equivalent: a SIGINT-terminated child is expected.
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && ws.Signal() == syscall.SIGINT {
			return nil
		}
	}
	return err
}

// or reachable), else spawn `nix run …` in its own session with output to the
// log file, record the PID file, and short-circuit on an immediate corpse.
func startVMDetachedReal() (Proc, error) {
	if pid, ok := readBuilderPIDReal(); (ok && pidIsLiveReal(pid)) || builderReachableReal() {
		return nil, nil
	}
	logPath := BuilderLogFilePath()
	if err := os.MkdirAll(parentOf(logPath), 0o755); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("nix", "run", "nixpkgs#darwin.linux-builder")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	proc, err := newRealProc(cmd)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(BuilderPIDFilePath(), []byte(strconv.Itoa(cmd.Process.Pid)+"\n"), 0o644)
	return proc, nil
}

func readBuilderPIDReal() (int, bool) {
	data, err := os.ReadFile(BuilderPIDFilePath())
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, false
	}
	return n, true
}

func pidIsLiveReal(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// SIGTERM) fallback, then remove the PID file. Returns (ok, errMsg).
func stopVMReal() (bool, string) {
	pid, ok := readBuilderPIDReal()
	if !ok || !pidIsLiveReal(pid) {
		_ = os.Remove(BuilderPIDFilePath())
		return true, ""
	}
	pgid, err := syscall.Getpgid(pid)
	if err == nil {
		err = syscall.Kill(-pgid, syscall.SIGTERM)
	}
	if err != nil {
		if kerr := syscall.Kill(pid, syscall.SIGTERM); kerr != nil {
			return false, err.Error()
		}
	}
	_ = os.Remove(BuilderPIDFilePath())
	return true, ""
}

// confirmReal is a minimal typer.confirm: prints the prompt and reads y/n from
// stdin (default no). The front door may substitute a non-interactive confirm.
func confirmReal(prompt string) bool {
	os.Stdout.WriteString(prompt + " [y/N]: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	resp := strings.ToLower(strings.TrimSpace(line))
	return resp == "y" || resp == "yes"
}

func parentOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return p
}
