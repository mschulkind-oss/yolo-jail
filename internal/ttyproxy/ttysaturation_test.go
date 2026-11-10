//go:build linux

package ttyproxy

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A TTY a test fills until EAGAIN does not stay full. The kernel moves what was written to
// a pty into its master's line discipline from deferred work, and each move frees write
// room after the EAGAIN was seen; on a loaded machine that work runs late. The full-TTY
// signal fixture (runFullTTYSignalCancellation) then had room for its owned helper's
// pending ^Z reset: the suspension completed, the helper STOPPED, and the signal the test
// sent next stays pending in a stopped process until a SIGCONT nobody sends. The test
// waited on an exit that could not come until the package's own timeout.
//
// pinTTYOutput closes that: TCOOFF stops the TTY's output, so its write room is zero
// (pty_write_room) whatever the buffers hold, until the returned release. tco_stopped also
// survives the proxy's termios writes clearing and setting IXON, which restart only a
// flow-control (^S) stop.
func pinTTYOutput(t *testing.T, fd int) (release func()) {
	t.Helper()
	if err := unix.IoctlSetInt(fd, unix.TCXONC, unix.TCOOFF); err != nil {
		t.Fatalf("stop the saturated TTY's output: %v", err)
	}
	released := false
	return func() {
		if !released {
			released = true
			_ = unix.IoctlSetInt(fd, unix.TCXONC, unix.TCOON)
		}
	}
}

// TestPinnedTTYOutputGivesNoRoomWhenMasterDrains stands in for the kernel's late flush with
// the strongest form of it, a read of everything master holds: once pinned, the TTY gives a
// writer no room until the test releases it.
func TestPinnedTTYOutputGivesNoRoomWhenMasterDrains(t *testing.T) {
	master, slave, err := openPty()
	if err != nil {
		t.Skipf("cannot open pty: %v", err)
	}
	defer unix.Close(master)
	defer unix.Close(slave)
	probe, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", slave), unix.O_WRONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(probe)
	if filled, err := fillTTYUntilEAGAIN(probe); err != nil || filled == 0 {
		t.Fatalf("fill the TTY: bytes=%d err=%v", filled, err)
	}
	release := pinTTYOutput(t, slave)
	defer release()
	drained, err := readPTYAvailable(master, time.Now().Add(childDeadline))
	if err != nil || len(drained) == 0 {
		t.Fatalf("drain master: bytes=%d err=%v", len(drained), err)
	}
	if n, err := unix.Write(probe, []byte{'x'}); !errors.Is(err, unix.EAGAIN) {
		t.Fatalf("pinned TTY took a write after master drained: n=%d err=%v", n, err)
	}
	fds := []unix.PollFd{{Fd: int32(probe), Events: unix.POLLOUT}}
	if _, err := unix.Poll(fds, 0); err != nil || fds[0].Revents&unix.POLLOUT != 0 {
		t.Fatalf("pinned TTY polls writable after master drained: revents=%#x err=%v", fds[0].Revents, err)
	}
	release()
	// Released, the room the drain freed is the writer's; a blocking poll waits for the
	// kernel's flush to hand it over, bounded only as a hang guard.
	fds[0].Revents = 0
	if n, err := unix.Poll(fds, int(childDeadline/time.Millisecond)); err != nil || n != 1 || fds[0].Revents&unix.POLLOUT == 0 {
		t.Fatalf("released TTY never became writable: n=%d revents=%#x err=%v", n, fds[0].Revents, err)
	}
}

type ownedHelperOutcome int

const (
	ownedHelperExited ownedHelperOutcome = iota
	ownedHelperStopped
	ownedHelperHung
)

// awaitOwnedHelper waits for an owned helper's exit, as reported on done by its sole Wait
// goroutine, and returns early when the helper stops instead: a stopped process acts on no
// signal until it is continued, so its exit is not coming and waiting longer only turns a
// named failure into a timeout. The hang guard bounds every other way of never exiting.
func awaitOwnedHelper(done <-chan error, pid int, hangGuard time.Duration) (ownedHelperOutcome, error) {
	guard := time.NewTimer(hangGuard)
	defer guard.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			return ownedHelperExited, err
		case <-guard.C:
			return ownedHelperHung, nil
		case <-tick.C:
			if ownedProcessStopped(pid) {
				return ownedHelperStopped, nil
			}
		}
	}
}

// TestAwaitOwnedHelperNamesAStoppedHelper reproduces the shape of the hang: a helper that
// stopped never exits, and a wait on its exit alone sat out the test's whole deadline.
func TestAwaitOwnedHelperNamesAStoppedHelper(t *testing.T) {
	if os.Getenv("YOLO_TTYPROXY_BLOCK_ON_STDIN") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestAwaitOwnedHelperNamesAStoppedHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), "YOLO_TTYPROXY_BLOCK_ON_STDIN=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		_ = stdin.Close()
		<-done
	}()
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	if outcome, _ := awaitOwnedHelper(done, cmd.Process.Pid, exitHangGuard(t)); outcome != ownedHelperStopped {
		t.Fatalf("outcome = %d, want ownedHelperStopped", outcome)
	}
	// Continued, it is running again, and its exit is what the wait reports.
	if err := cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	outcome, err := awaitOwnedHelper(done, cmd.Process.Pid, exitHangGuard(t))
	if outcome != ownedHelperExited || err != nil {
		t.Fatalf("outcome after SIGCONT and EOF = %d (err=%v), want a clean ownedHelperExited", outcome, err)
	}
	done <- err // the deferred cleanup's receive
}
