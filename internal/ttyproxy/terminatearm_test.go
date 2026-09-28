//go:build linux

package ttyproxy

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

const armRaceRoleEnv = "YOLO_TTYPROXY_ARM_RACE_ROLE"

// TestTheTerminateArmOwnsTheExit pins the race CI hit on 2026-09-28 (run 36443241222,
// TestSignalArmReleasesTheFallbackTree): the arm kills the child before it exits the
// process, and that kill ends proxyLoop on the main goroutine, so RunWithProxy could
// return — and its caller carry on with an exit code and a teardown of its own — before
// the arm's os.Exit. The helper holds the window between the kill and the exit open, so
// the race is certain rather than a matter of scheduling: the process must still leave
// with the signal's status, 143, and never through the return path.
func TestTheTerminateArmOwnsTheExit(t *testing.T) {
	if os.Getenv(armRaceRoleEnv) == "proxy" {
		master, slave, err := openPty()
		if err != nil {
			os.Stdout.WriteString("SKIP-NO-PTY\n")
			os.Exit(0)
		}
		defer syscall.Close(master)
		os.Stdin = os.NewFile(uintptr(slave), "pty-slave")
		beforeTerminateExit = func() { time.Sleep(500 * time.Millisecond) }
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		}()
		_, _ = RunWithProxy([]string{"sleep", "5"}, nil, nil)
		os.Stdout.WriteString("RETURNED\n")
		os.Exit(3)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestTheTerminateArmOwnsTheExit$", "-test.count=1")
	cmd.Env = append(os.Environ(), armRaceRoleEnv+"=proxy")
	out, err := cmd.CombinedOutput()
	if string(out) == "SKIP-NO-PTY\n" {
		t.Skip("no pty available")
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 128+int(syscall.SIGTERM) {
		t.Fatalf("proxy exit = %v, want %d from the terminate arm (RunWithProxy must not return once the arm has begun)\n%s",
			err, 128+int(syscall.SIGTERM), out)
	}
}
