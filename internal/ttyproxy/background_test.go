//go:build linux

package ttyproxy

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The roles TestBackgroundTerminate re-executes the test binary into. A real shell's job
// control is the thing under test, so the test builds one: a SESSION on a fresh pty
// ("shell", the session leader and at first the foreground group) and, in its own
// process group, the proxy it launches ("proxy"), exactly as bash runs `yolo`.
const (
	bgRoleEnv    = "YOLO_TTYPROXY_BG_ROLE"
	bgPidfileEnv = "YOLO_TTYPROXY_BG_PIDFILE"
)

// TestBackgroundTerminate pins the escape from a lingering quit, measured on the
// maintainer's host 2026-09-25: the podman client outlived its container, the user
// pressed ^Z and then `kill %1`, and the job stayed "Stopped" — only `kill -9 %1` got
// rid of it.
//
// The cause is job control, not podman. After ^Z the shell owns the terminal, and
// bash's `kill %1` on a stopped job sends SIGTERM and then SIGCONT. Both arms used to
// start by writing termios, and a termios write from a BACKGROUND process group raises
// SIGTTOU, whose default disposition stops the process again — before the terminate arm
// reached os.Exit, and on the SIGCONT arm before it did anything at all.
//
// It also pins what the terminate arm does to a child that ignores SIGTERM, as the
// lingering podman client did: it is killed rather than left running after the proxy
// exits.
func TestBackgroundTerminate(t *testing.T) {
	switch os.Getenv(bgRoleEnv) {
	case "shell":
		os.Exit(bgShellRole())
	case "proxy":
		// The child stands in for the lingering podman client: it ignores SIGTERM,
		// which it inherits across exec, so only the proxy can end it.
		rc, _ := RunWithProxy([]string{"sh", "-c",
			`trap '' TERM; echo $$ > "$` + bgPidfileEnv + `"; exec sleep 60`}, nil, nil)
		os.Exit(rc)
	}

	// An inherited BLOCKED SIGTTOU makes the kernel allow a background termios write,
	// which is the bug's whole mechanism, and the mask survives every exec below it. Some
	// agent harnesses start commands that way (SigBlk 0x380000: TSTP, TTIN and TTOU), where
	// this test would pass against the broken code. So it refuses to vouch there; clear the
	// mask to run it, e.g. python3 -c 'import signal,os,sys;
	// signal.pthread_sigmask(signal.SIG_SETMASK, []); os.execvp(sys.argv[1], sys.argv[1:])'.
	if jobSignalsBlocked() {
		t.Skip("SIGTTIN/SIGTTOU are blocked in this process's inherited mask, which hides the bug")
	}

	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)
	slaveFile := os.NewFile(uintptr(slave), "pty-slave")
	defer slaveFile.Close()

	cmd := exec.Command(os.Args[0], "-test.run=^TestBackgroundTerminate$")
	cmd.Env = append(os.Environ(), bgRoleEnv+"=shell",
		bgPidfileEnv+"="+filepath.Join(t.TempDir(), "child.pid"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slaveFile, slaveFile, slaveFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Drain the master so neither role can block on a full pty, and keep what they
	// said for the failure message.
	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := unix.Read(master, buf)
			if n > 0 {
				mu.Lock()
				out.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil || n == 0 {
				return
			}
		}
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			mu.Lock()
			defer mu.Unlock()
			t.Fatalf("background terminate failed (%v):\n%s", err, out.String())
		}
	case <-time.After(30 * time.Second):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		t.Fatal("the shell role never finished")
	}
}

// bgShellRole plays bash: launch the proxy in the foreground, take the terminal back as
// ^Z would leave it, then do what the user did. Its exit code is the verdict.
func bgShellRole() int {
	pidfile := os.Getenv(bgPidfileEnv)

	proxy := exec.Command(os.Args[0], "-test.run=^TestBackgroundTerminate$")
	proxy.Env = append(os.Environ(), bgRoleEnv+"=proxy")
	proxy.Stdin, proxy.Stdout, proxy.Stderr = os.Stdin, os.Stdout, os.Stderr
	proxy.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	if err := proxy.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start proxy:", err)
		return 2
	}
	pgid := proxy.Process.Pid
	exited := make(chan struct{})
	go func() { _ = proxy.Wait(); close(exited) }()
	fail := func(code int, format string, args ...any) int {
		fmt.Fprintf(os.Stderr, format+"\n", args...)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		return code
	}

	childPid := 0
	for deadline := time.Now().Add(10 * time.Second); childPid == 0; {
		if b, err := os.ReadFile(pidfile); err == nil {
			childPid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if time.Now().After(deadline) {
			return fail(2, "the proxy's child never started")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond) // past the proxy's own setRaw

	// The proxy is now a background job: the shell holds the terminal.
	if err := takeTerminal(); err != nil {
		return fail(2, "take the terminal back: %v", err)
	}

	// `bg`, or the SIGCONT half of `kill %1`: resuming in the background must not stop
	// the proxy again.
	_ = syscall.Kill(pgid, syscall.SIGCONT)
	time.Sleep(500 * time.Millisecond)
	if procState(pgid) == 'T' {
		return fail(3, "SIGCONT in the background stopped the proxy (SIGTTOU from a termios write)")
	}

	// `kill %1`: SIGTERM then SIGCONT, both at the job's process group.
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	_ = syscall.Kill(-pgid, syscall.SIGCONT)
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		return fail(4, "SIGTERM in the background did not end the proxy (state %c)", procState(pgid))
	}

	// The child ignored SIGTERM, so only the proxy's terminate arm can have ended it.
	for deadline := time.Now().Add(3 * time.Second); ; {
		if s := procState(childPid); s == 0 || s == 'Z' {
			return 0
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(childPid, syscall.SIGKILL)
			fmt.Fprintln(os.Stderr, "the proxy exited and left its SIGTERM-ignoring child running")
			return 5
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// takeTerminal makes this process group the terminal's foreground, as a shell does when
// its job stops. It BLOCKS SIGTTOU on this thread for the one ioctl rather than ignoring
// it, the way a shell would: an ignored disposition survives exec, and the proxy role
// inheriting it is what hid the bug from this test's first draft.
func takeTerminal() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var set, old unix.Sigset_t
	sig := uint(syscall.SIGTTOU) - 1
	set.Val[sig/64] |= 1 << (sig % 64)
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, &set, &old); err != nil {
		return err
	}
	defer func() { _ = unix.PthreadSigmask(unix.SIG_SETMASK, &old, nil) }()
	return unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, unix.Getpgrp())
}

// jobSignalsBlocked reports whether SIGTTIN or SIGTTOU is in this process's blocked mask.
func jobSignalsBlocked() bool {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "SigBlk:"); ok {
			mask, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			if err != nil {
				return false
			}
			const ttin, ttou = uint64(1) << (syscall.SIGTTIN - 1), uint64(1) << (syscall.SIGTTOU - 1)
			return mask&(ttin|ttou) != 0
		}
	}
	return false
}

// procState is the state letter from /proc/<pid>/stat, or 0 if the process is gone.
func procState(pid int) byte {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		return 0
	}
	return b[i+2]
}
