//go:build linux

package ttyproxy

import (
	"errors"
	"os"
	"os/exec"
	"sync"
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
//
// NOTHING HERE WAITS ON A CLOCK, in either direction, because the machine decides how long
// each step takes. The SIGTERM is raised from the pump's first stdin read (Observer.Input),
// which RunWithProxy reaches only after it has registered its arm. Raised on a timer, as it
// used to be, it landed before the registration whenever the child's exec was slow — Start
// waits for that exec, and the arm is registered after it — and took Go's default action,
// killing the process with no arm at all. And the window is held open until the kill has
// ended the pump (StageDrainDone), so a slow machine cannot turn the race into a pass by
// reaching the arm's exit before the return path ever runs.
func TestTheTerminateArmOwnsTheExit(t *testing.T) {
	if os.Getenv(armRaceRoleEnv) == "proxy" {
		master, slave, err := openPty()
		if err != nil {
			os.Stdout.WriteString("SKIP-NO-PTY\n")
			os.Exit(0)
		}
		defer syscall.Close(master)
		os.Stdin = os.NewFile(uintptr(slave), "pty-slave")

		drained := make(chan struct{})
		var drainOnce, raiseOnce sync.Once
		beforeTerminateExit = func() {
			// A bound, not a pace: the kill ends the pump in milliseconds, and ten seconds
			// runs out only when it never does.
			select {
			case <-drained:
			case <-time.After(10 * time.Second):
			}
			// Then long enough for a return path that was NOT blocked to print and exit
			// first: from the end of the pump that is a handful of calls.
			time.Sleep(500 * time.Millisecond)
		}
		obs := Observer{
			Input: func(int, string) {
				raiseOnce.Do(func() { _ = syscall.Kill(os.Getpid(), syscall.SIGTERM) })
			},
			Stage: func(s string) {
				if s == StageDrainDone {
					drainOnce.Do(func() { close(drained) })
				}
			},
		}
		// Typed before the run starts: it waits in the terminal until the pump reads it.
		if _, err := syscall.Write(master, []byte("x")); err != nil {
			os.Stdout.WriteString("write to the fake terminal: " + err.Error() + "\n")
			os.Exit(2)
		}
		_, _ = RunWithProxyObserved([]string{"sleep", "5"}, nil, nil, obs)
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
