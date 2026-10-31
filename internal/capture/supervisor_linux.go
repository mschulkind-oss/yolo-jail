//go:build linux

package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const InstallerSupervisorVerb = "capture-installer-supervise"
const descendantCleanupBound = 3 * time.Second

// supervisorCancelWait is how long a cancelled capture waits for its supervisor to finish its own
// bounded descendant cleanup before killing it: that bound and a second. A var so a test of a
// supervisor that never answers need not wait it out.
var supervisorCancelWait = descendantCleanupBound + time.Second

// InstallerSupervisorMain runs only in a dedicated self-exec process. Subreaping
// never changes the parent CLI's or a legacy installer's child/signal semantics.
// Fd 3 is the parent's lifetime pipe; fd 4 acknowledges ECHILD after cleanup.
func InstallerSupervisorMain(args []string) int {
	if len(args) == 0 {
		return 2
	}
	lifetime := os.NewFile(3, "capture-lifetime")
	evidence := os.NewFile(4, "capture-completion")
	if lifetime == nil || evidence == nil {
		return 1
	}
	defer lifetime.Close()
	defer evidence.Close()
	for _, fd := range []int{3, 4} {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFIFO {
			return 1
		}
		unix.CloseOnExec(fd)
	}
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		fmt.Fprintln(os.Stderr, "capture supervisor: cannot own descendants — use a supported sealed Linux runtime:", err)
		return 1
	}
	cancelled := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, lifetime); close(cancelled) }()
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // selected installer argv
	cmd.Stdin = nil
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mainPID := cmd.Process.Pid
	failed, stopping := false, false
	var deadline time.Time
	for {
		select {
		case <-cancelled:
			failed = true
			stopping = true
		default:
		}
		for {
			var status unix.WaitStatus
			pid, err := unix.Wait4(-1, &status, unix.WNOHANG, nil)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ECHILD) {
				if !stopping {
					return 1
				}
				if failed {
					return 1
				}
				if _, err := io.WriteString(evidence, "reaped\n"); err != nil {
					return 1
				}
				return 0
			}
			if err != nil {
				return 1
			}
			if pid == 0 {
				break
			}
			if pid == mainPID {
				stopping = true
				if !status.Exited() || status.ExitStatus() != 0 {
					failed = true
				}
			}
		}
		if stopping {
			if deadline.IsZero() {
				deadline = time.Now().Add(descendantCleanupBound)
			}
			if time.Now().After(deadline) {
				fmt.Fprintln(os.Stderr, "capture supervisor: descendants not known reaped — stop the owned sealed runtime, then retry")
				return 1
			}
			if err := killSupervisorChildren(); err != nil {
				fmt.Fprintln(os.Stderr, "capture supervisor: ownership unknown — stop the owned sealed runtime, then retry:", err)
				return 1
			}
		}
		time.Sleep(time.Millisecond)
	}
}

// These are kernel-reported DIRECT children of this isolated subreaper, not an
// ancestry guess. They cannot be PID-reused until this process reaps them. Killing
// each generation adopts its escaped/session-changing children; ECHILD is the proof.
func killSupervisorChildren() error {
	tasks, err := filepath.Glob("/proc/self/task/*/children")
	if err != nil || len(tasks) == 0 {
		return fmt.Errorf("kernel child ownership unavailable: %w", err)
	}
	for _, task := range tasks {
		data, err := os.ReadFile(task)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, s := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(s)
			if err != nil {
				return err
			}
			if err := unix.Kill(pid, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
				return err
			}
		}
	}
	return nil
}

func (d *driver) installArtifacts(ctx context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	lifetimeR, lifetimeW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer lifetimeR.Close()
	defer lifetimeW.Close()
	evidenceR, evidenceW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer evidenceR.Close()
	defer evidenceW.Close()
	cmd := exec.Command(exe, append([]string{"internal", InstallerSupervisorVerb}, d.opts.Command...)...) //nolint:gosec // own executable
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Dir = d.home
	cmd.Env = programEnvironment(d.opts.Env, d.home)
	if d.opts.Env == nil {
		cmd.Env = programEnvironment(os.Environ(), d.home)
	}
	cmd.Stdout = d.opts.Stdout
	cmd.Stderr = d.opts.Stderr
	cmd.ExtraFiles = []*os.File{lifetimeR, evidenceW}
	cmd.WaitDelay = time.Second
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = lifetimeR.Close()
	_ = evidenceW.Close()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err = <-waited:
	case <-ctx.Done():
		_ = lifetimeW.Close()
		timer := time.NewTimer(supervisorCancelWait)
		select {
		case err = <-waited:
			timer.Stop()
		case <-timer.C:
			// Only our direct helper is killed. Descendant ownership is now unknown;
			// never validate artifacts or claim cleanup, and let the sealed runtime reap.
			_ = cmd.Process.Kill()
			err = <-waited
		}
	}
	proof, readErr := io.ReadAll(evidenceR)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || readErr != nil || string(proof) != "reaped\n" {
		return fmt.Errorf("program capture: installer or descendant ownership failed — stop the owned sealed Linux runtime and retry (installer: %v, evidence: %v)", err, readErr)
	}
	return nil
}
