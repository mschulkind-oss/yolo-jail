//go:build linux

package ttyproxy

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// callerarm_test.go pins Observer.Arm: a caller that runs its own SIGINT/SIGHUP/SIGTERM arm for
// the whole of a run (the fresh launch's, run.launchSignalArm) gets the proxy's half of the
// teardown through a Handle, and the proxy then installs no arm of its own. Before it existed
// the fresh launch handed its arm to the proxy's and took it back afterwards, and a signal in
// either gap was lost or took Go's default action.

const (
	callerArmRoleEnv    = "YOLO_TTYPROXY_CALLER_ARM_ROLE"
	callerArmPidfileEnv = "YOLO_TTYPROXY_CALLER_ARM_PIDFILE"
)

// callerArmExit is the caller arm's own exit status, distinct from 128+SIGTERM, so the parent
// can tell the caller's arm from a proxy arm that should not have been installed.
const callerArmExit = 77

// TestACallerArmOwnsTheSignalsOfARun drives both paths in a child process, with a real SIGTERM:
// on a pty the proxy leaves SIGTERM to the caller (its own arm would have run onTerminate and
// exited 143), the handle's Terminate puts the host tty back in cooked mode exactly once, Kill
// ends a child that ignores SIGTERM, and the proxy never returns once the caller's arm has
// begun. With no terminal on stdin the same handle arrives, with nothing to restore.
func TestACallerArmOwnsTheSignalsOfARun(t *testing.T) {
	if role := os.Getenv(callerArmRoleEnv); role != "" {
		os.Exit(callerArmRole(role == "pty"))
	}
	for _, role := range []string{"pty", "plain"} {
		t.Run(role, func(t *testing.T) {
			pidfile := filepath.Join(t.TempDir(), "child.pid")
			cmd := exec.Command(os.Args[0], "-test.run=^TestACallerArmOwnsTheSignalsOfARun$", "-test.count=1")
			cmd.Env = append(os.Environ(), callerArmRoleEnv+"="+role, callerArmPidfileEnv+"="+pidfile)
			out, err := cmd.CombinedOutput()
			if strings.Contains(string(out), "SKIP-NO-PTY") {
				t.Skip("no pty available")
			}
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != callerArmExit {
				t.Fatalf("exit = %v, want the caller arm's %d\n%s", err, callerArmExit, out)
			}
			for _, bad := range []string{"PROXY-TERMINATE", "RETURNED"} {
				if strings.Contains(string(out), bad) {
					t.Errorf("%s: the proxy acted on a run its caller's arm owns\n%s", bad, out)
				}
			}
			if !strings.Contains(string(out), "CALLER-ARM-DONE") {
				t.Errorf("the caller's arm did not finish its checks\n%s", out)
			}
		})
	}
}

// callerArmRole is the child: a caller with its own SIGTERM arm around one proxied run.
func callerArmRole(pty bool) int {
	pidfile := os.Getenv(callerArmPidfileEnv)
	var master, slave int
	if pty {
		var err error
		master, slave, err = openPty()
		if err != nil {
			fmt.Println("SKIP-NO-PTY")
			return callerArmExit
		}
		defer unix.Close(master)
		os.Stdin = os.NewFile(uintptr(slave), "pty-slave")
		go drainMaster(master)
	} else {
		devnull, err := os.Open(os.DevNull)
		if err != nil {
			fmt.Println("open /dev/null:", err)
			return 2
		}
		os.Stdin = devnull
	}

	handles := make(chan Handle, 1)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	go func() {
		var h Handle
		select {
		case h = <-handles:
		case <-time.After(10 * time.Second):
			fmt.Println("the proxy never handed its caller a Handle")
			os.Exit(2)
		}
		childPid := awaitPidfile(pidfile)
		if childPid == 0 {
			fmt.Println("the proxy's child never started")
			os.Exit(2)
		}
		if pty && lflag(slave)&unix.ICANON != 0 {
			fmt.Println("the host tty is not raw while the proxy runs")
			os.Exit(2)
		}
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		select {
		case <-sigs:
		case <-time.After(5 * time.Second):
			fmt.Println("the caller's arm never saw its SIGTERM")
			os.Exit(2)
		}
		if !h.Terminate() {
			fmt.Println("Terminate refused a run still in progress")
			os.Exit(2)
		}
		if pty && lflag(slave)&unix.ICANON == 0 {
			fmt.Println("Terminate left the host tty raw")
			os.Exit(2)
		}
		if h.Terminate() {
			fmt.Println("a second Terminate claimed the run again")
			os.Exit(2)
		}
		h.Kill()
		for deadline := time.Now().Add(3 * time.Second); ; {
			if s := procState(childPid); s == 0 || s == 'Z' {
				break
			}
			if time.Now().After(deadline) {
				fmt.Println("Kill left the SIGTERM-ignoring child running")
				os.Exit(2)
			}
			time.Sleep(20 * time.Millisecond)
		}
		// The child is gone, so proxyLoop has returned or is about to: give the proxy's own
		// return the chance to run, which it must not take.
		time.Sleep(500 * time.Millisecond)
		fmt.Println("CALLER-ARM-DONE")
		os.Exit(callerArmExit)
	}()

	_, _ = RunWithProxyObserved([]string{"sh", "-c",
		`trap '' TERM; echo $$ > "$` + callerArmPidfileEnv + `"; exec sleep 30`}, nil,
		func() { fmt.Println("PROXY-TERMINATE") },
		Observer{Arm: func(h Handle) { handles <- h }})
	fmt.Println("RETURNED")
	return 3
}

// TestAHandleIsInertOnceTheRunIsOver: after the proxy returned on its own, a caller arm that
// fires then gets false from Terminate at once, and the terminal the proxy restored stays
// restored.
func TestAHandleIsInertOnceTheRunIsOver(t *testing.T) {
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)
	go drainMaster(master)
	origIn := os.Stdin
	os.Stdin = os.NewFile(uintptr(slave), "pty-slave-stdin")
	defer func() { os.Stdin = origIn; unix.Close(slave) }()

	var h Handle
	rc, err := RunWithProxyObserved([]string{"sh", "-c", "exit 4"}, nil, nil,
		Observer{Arm: func(got Handle) { h = got }})
	if err != nil || rc != 4 {
		t.Fatalf("rc %d err %v, want the child's 4", rc, err)
	}
	if h.s == nil {
		t.Fatal("the proxy never handed its caller a Handle")
	}
	done := make(chan bool, 1)
	go func() { done <- h.Terminate() }()
	select {
	case got := <-done:
		if got {
			t.Error("Terminate claimed a run the proxy had already returned from")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Terminate blocked after the proxy returned")
	}
	if lflag(slave)&unix.ICANON == 0 {
		t.Error("the host tty is raw after the proxy returned")
	}
}

// TestASignalAfterTheReturnIsClaimedIsLeftToTheCaller: the proxy's OWN arm (no Observer.Arm)
// no longer acts on a signal that arrives once the child has exited and the proxy is
// returning. It used to run onTerminate and exit 128+n there, racing the caller's own
// teardown; now the return goes ahead and the caller carries on. The seam holds the window
// open so the signal lands inside it every time.
func TestASignalAfterTheReturnIsClaimedIsLeftToTheCaller(t *testing.T) {
	if os.Getenv(callerArmRoleEnv) == "claimed" {
		master, slave, err := openPty()
		if err != nil {
			fmt.Println("SKIP-NO-PTY")
			os.Exit(0)
		}
		defer unix.Close(master)
		go drainMaster(master)
		os.Stdin = os.NewFile(uintptr(slave), "pty-slave")
		afterReturnClaimed = func() {
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			time.Sleep(300 * time.Millisecond)
		}
		rc, _ := RunWithProxy([]string{"true"}, nil, func() { fmt.Println("TERMINATED") })
		fmt.Println("RETURNED " + strconv.Itoa(rc))
		os.Exit(0)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestASignalAfterTheReturnIsClaimedIsLeftToTheCaller$", "-test.count=1")
	cmd.Env = append(os.Environ(), callerArmRoleEnv+"=claimed")
	out, err := cmd.CombinedOutput()
	if strings.Contains(string(out), "SKIP-NO-PTY") {
		t.Skip("no pty available")
	}
	if err != nil || !strings.Contains(string(out), "RETURNED 0") || strings.Contains(string(out), "TERMINATED") {
		t.Fatalf("exit %v: want the proxy's ordinary return and no terminate arm\n%s", err, out)
	}
}

// awaitPidfile reads the child's pid from path, waiting up to five seconds; 0 when it never
// appears.
func awaitPidfile(path string) int {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
	}
	return 0
}

// lflag is fd's termios local flags, 0 when it cannot be read.
func lflag(fd int) uint32 {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return 0
	}
	return t.Lflag
}

// drainMaster reads a pty master until it closes, so nothing written to its slave blocks.
func drainMaster(master int) {
	buf := make([]byte, 4096)
	for {
		if n, err := unix.Read(master, buf); err != nil || n == 0 {
			return
		}
	}
}
