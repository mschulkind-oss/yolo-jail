package hostservice

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const (
	SettingsCheckTimeout   = 2 * time.Second
	SettingsCheckOutputMax = 4 * 1024
	commandDrainGrace      = 40 * time.Millisecond
)

type CommandOutcome string

const (
	CommandAccepted    CommandOutcome = "accepted"
	CommandRefused     CommandOutcome = "refused"
	CommandTimedOut    CommandOutcome = "timeout"
	CommandStartFailed CommandOutcome = "start-failed"
)

type BoundedCommandResult struct {
	Outcome  CommandOutcome
	Stdout   string
	Stderr   string
	ExitCode int
}

type commandCapture struct {
	mu        sync.Mutex
	remaining int
	stdout    bytes.Buffer
	stderr    bytes.Buffer
}

// RunBoundedCommand runs an argv directly, captures at most 4 KiB across stdout/stderr, and
// bounds both the command and inherited output descriptors. Child stdout/stderr are socketpair
// endpoints installed directly as files (not exec.Cmd writer pipes); closing the parent endpoints
// after the direct child's exit releases readers even if an escaped descendant retained fd 1/2.
func RunBoundedCommand(argv []string, timeout time.Duration) BoundedCommandResult {
	return RunBoundedCommandWithEnv(argv, nil, timeout)
}

// RunBoundedCommandWithEnv is RunBoundedCommand with an explicit child environment; nil inherits
// the caller's environment unchanged.
func RunBoundedCommandWithEnv(argv, env []string, timeout time.Duration) BoundedCommandResult {
	if len(argv) == 0 {
		return BoundedCommandResult{Outcome: CommandStartFailed, ExitCode: -1}
	}
	if timeout <= 0 {
		timeout = SettingsCheckTimeout
	}
	stdoutParent, stdoutChild, err := commandSocketPair()
	if err != nil {
		return BoundedCommandResult{Outcome: CommandStartFailed, ExitCode: -1}
	}
	stderrParent, stderrChild, err := commandSocketPair()
	if err != nil {
		_ = stdoutParent.Close()
		_ = stdoutChild.Close()
		return BoundedCommandResult{Outcome: CommandStartFailed, ExitCode: -1}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	if env != nil {
		cmd.Env = env
	}
	cmd.Stdout, cmd.Stderr = stdoutChild, stderrChild
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = stdoutParent.Close()
		_ = stderrParent.Close()
		_ = stdoutChild.Close()
		_ = stderrChild.Close()
		return BoundedCommandResult{Outcome: CommandStartFailed, ExitCode: -1}
	}
	_ = stdoutChild.Close()
	_ = stderrChild.Close()

	capture := &commandCapture{remaining: SettingsCheckOutputMax}
	readers := make(chan struct{}, 2)
	go capture.read(stdoutParent, &capture.stdout, readers)
	go capture.read(stderrParent, &capture.stderr, readers)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	timer := time.NewTimer(timeout)
	timedOut := false
	select {
	case <-waited:
	case <-timer.C:
		timedOut = true
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Process.Kill()
		<-waited
	}
	if !timer.Stop() && !timedOut {
		select {
		case <-timer.C:
		default:
		}
	}

	// Drain bytes already written, but never wait for EOF: a daemonizing or escaped descendant
	// may retain stdout/stderr for an arbitrary time. Closing either endpoint also makes future
	// writes fail rather than keeping a reader goroutine alive.
	deadline := time.Now().Add(commandDrainGrace)
	_ = stdoutParent.SetReadDeadline(deadline)
	_ = stderrParent.SetReadDeadline(deadline)
	drained := 0
	for drained < 2 {
		select {
		case <-readers:
			drained++
		case <-time.After(commandDrainGrace):
			_ = stdoutParent.Close()
			_ = stderrParent.Close()
			for drained < 2 {
				<-readers
				drained++
			}
		}
	}
	_ = stdoutParent.Close()
	_ = stderrParent.Close()

	capture.mu.Lock()
	stdout, stderr := capture.stdout.String(), capture.stderr.String()
	capture.mu.Unlock()
	result := BoundedCommandResult{Stdout: stdout, Stderr: stderr, ExitCode: commandExitCode(cmd.ProcessState)}
	switch {
	case timedOut:
		result.Outcome = CommandTimedOut
	case result.ExitCode == 0:
		result.Outcome = CommandAccepted
	default:
		result.Outcome = CommandRefused
	}
	return result
}

func (c *commandCapture) read(conn net.Conn, target *bytes.Buffer, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()
	var buf [1024]byte
	for {
		n, err := conn.Read(buf[:])
		if n > 0 {
			c.mu.Lock()
			if c.remaining > 0 {
				keep := n
				if keep > c.remaining {
					keep = c.remaining
				}
				_, _ = target.Write(buf[:keep])
				c.remaining -= keep
			}
			c.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func commandExitCode(state *os.ProcessState) int {
	if state == nil {
		return -1
	}
	return state.ExitCode()
}

// SafeCommandText collapses control, format (Unicode Cf: bidi overrides, zero-width characters)
// and terminal markup characters, then caps user-visible text.
func SafeCommandText(value string) string {
	var clean strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == '[' || r == ']' {
			r = ' '
		}
		clean.WriteRune(r)
	}
	fields := strings.Fields(clean.String())
	var out strings.Builder
	for i, field := range fields {
		if i > 0 {
			out.WriteByte(' ')
		}
		if out.Len()+len(field) > startupReasonTextMax*4 {
			break
		}
		out.WriteString(field)
	}
	text := []rune(out.String())
	if len(text) > startupReasonTextMax {
		text = text[:startupReasonTextMax]
	}
	return string(text)
}

// commandSocketPair is shared by the validator output capture and the attempt-reason channel.
func commandSocketPair() (net.Conn, *os.File, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	parentFile := os.NewFile(uintptr(fds[0]), "yolo-command-parent")
	child := os.NewFile(uintptr(fds[1]), "yolo-command-child")
	parent, err := net.FileConn(parentFile)
	_ = parentFile.Close()
	if err != nil {
		_ = child.Close()
		return nil, nil, err
	}
	return parent, child, nil
}
