//go:build linux

// Package ttyproxy is the Go port of src/cli/tty_proxy.py — the in-process TTY
// proxy that wraps `podman run` so ^Z suspends the PROXY (not the container),
// SIGWINCH resizes propagate, and window-close/SIGTERM tear the jail down
// cleanly.
//
// This is the LIBRARY form (the plan's Stage 8 pre-decided fallback,
// consumed in-process by `run` at Stage 16) — NOT the two-process
// Go-child/Python-parent split (seam #4). The library form keeps ALL signal
// teardown in one process and is what a pure-Go `run` needs.
//
// Frozen behavior (from tty_proxy.py + docs/design/ctrl-z-and-the-tty-proxy.md):
//   - non-TTY stdin -> transparent plain spawn (no pty).
//   - ^Z (0x1A) suspends the PROXY via TARGETED SIGTSTP to self (NEVER a
//     pgroup-wide signal — that would stop podman, a jail-visible change); the
//     byte never reaches the child; bytes after ^Z in the same read are queued
//     and flushed on resume.
//   - NO Setsid (setsid broke `podman -it`); NEVER signal.Notify(SIGTSTP)
//     (default disposition required to actually stop).
//   - SIGCONT -> re-raw the host TTY. SIGWINCH -> TIOCSWINSZ to the pty.
//   - SIGHUP/SIGTERM -> restore cooked termios, run onTerminate, exit 128+n.
//   - stdin EOF -> stop reading stdin, keep pumping the master until child exit
//     (the decided semantics).
//
// Source of truth: src/cli/tty_proxy.py.
package ttyproxy

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	readChunk = 65536
	suspByte  = 0x1a // ^Z
)

// getWinsize reads the terminal window size from fd.
func getWinsize(fd int) (*unix.Winsize, error) {
	return unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
}

// setWinsize writes the window size to fd (TIOCSWINSZ).
func setWinsize(fd int, ws *unix.Winsize) {
	_ = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, ws)
}

// RunWithProxy spawns cmd under a TTY proxy and returns its exit code.
//
//   - onStarted (if non-nil) runs on a goroutine after spawn (post-launch
//     housekeeping, e.g. release a lock) without blocking the pump loop.
//   - onTerminate (if non-nil) is host-side teardown run when the proxy is
//     KILLED (SIGHUP window-close / SIGTERM) rather than exiting on its own.
//
// Non-TTY stdin falls back to a plain spawn (no pty), matching Python.
func RunWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func()) (int, error) {
	inFd := int(os.Stdin.Fd())
	if !isatty(inFd) {
		return runPlain(cmd, onStarted)
	}

	// Save cooked attrs to restore on suspend/exit.
	cooked, err := unix.IoctlGetTermios(inFd, unix.TCGETS)
	if err != nil {
		return runPlain(cmd, onStarted)
	}

	master, slave, err := openPty()
	if err != nil {
		return 0, err
	}

	// Match the pty window to the host TTY at startup.
	if ws, err := getWinsize(inFd); err == nil {
		setWinsize(slave, ws)
	}

	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.NewFile(uintptr(slave), "pty-slave"),
		os.NewFile(uintptr(slave), "pty-slave"), os.NewFile(uintptr(slave), "pty-slave")
	if err := c.Start(); err != nil {
		unix.Close(master)
		unix.Close(slave)
		return 0, err
	}
	unix.Close(slave) // parent uses only the master end

	if onStarted != nil {
		go safeCallback(onStarted, c.Process)
	}

	// Raw mode on the host TTY.
	setRaw(inFd, cooked)

	restoreCooked := func() { _ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked) }

	// Signal handlers. Note: we DO NOT Notify SIGTSTP (default disposition must
	// stop us); we handle WINCH/CONT/HUP/TERM.
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGCONT, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var termOnce sync.Once
	go func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGWINCH:
				if ws, err := getWinsize(inFd); err == nil {
					setWinsize(master, ws)
				}
			case syscall.SIGCONT:
				setRaw(inFd, cooked) // host TTY was cooked while suspended
			case syscall.SIGHUP, syscall.SIGTERM:
				termOnce.Do(func() {
					restoreCooked()
					if onTerminate != nil {
						onTerminate()
					}
					n := 1
					if s == syscall.SIGHUP {
						n = int(syscall.SIGHUP)
					} else {
						n = int(syscall.SIGTERM)
					}
					os.Exit(128 + n)
				})
			}
		}
	}()

	rc := proxyLoop(inFd, master, c, cooked)

	restoreCooked()
	unix.Close(master)
	return rc, nil
}

func runPlain(cmd []string, onStarted func(*os.Process)) (int, error) {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		return 0, err
	}
	if onStarted != nil {
		go safeCallback(onStarted, c.Process)
	}
	err := c.Wait()
	return exitCode(err), nil
}

// proxyLoop pumps bytes between the host TTY and the master pty until the child
// exits. Mirrors _proxy_loop, including the ^Z self-suspend + pending-flush and
// the stdin-EOF semantics (stop reading stdin, keep pumping master).
func proxyLoop(inFd, master int, c *exec.Cmd, cooked *unix.Termios) int {
	outFd := int(os.Stdout.Fd())
	var pending []byte
	stdinClosed := false

	// Reap the child in the background so poll() equivalent works.
	exitedCh := make(chan int, 1)
	go func() { exitedCh <- exitCode(c.Wait()) }()

	buf := make([]byte, readChunk)
	for {
		select {
		case rc := <-exitedCh:
			// Drain any final child output, then return.
			for {
				n, err := unix.Read(master, buf)
				if n > 0 {
					_, _ = unix.Write(outFd, buf[:n])
				}
				if err != nil || n == 0 {
					break
				}
			}
			return rc
		default:
		}

		fds := []unix.PollFd{{Fd: int32(master), Events: unix.POLLIN}}
		if !stdinClosed {
			fds = append(fds, unix.PollFd{Fd: int32(inFd), Events: unix.POLLIN})
		}
		_, err := unix.Poll(fds, 100)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			// keep going; the exit check handles teardown
			continue
		}

		// master readable
		if fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0 {
			n, rerr := unix.Read(master, buf)
			if n > 0 {
				_, _ = unix.Write(outFd, buf[:n])
			}
			if rerr != nil || n == 0 {
				// master closed (child exited) — wait for the reaper.
				rc := <-exitedCh
				return rc
			}
		}

		// stdin readable
		if !stdinClosed && len(fds) > 1 && fds[1].Revents&unix.POLLIN != 0 {
			n, rerr := unix.Read(inFd, buf)
			if n == 0 || (rerr != nil && !errors.Is(rerr, syscall.EINTR)) {
				// EOF on host stdin: stop reading stdin, keep pumping master
				// until child exit (the decided semantics). We do NOT close the
				// master (that would kill an interactive child prematurely);
				// just stop polling stdin.
				stdinClosed = true
				continue
			}
			data := append([]byte(nil), buf[:n]...)
			if len(pending) > 0 {
				data = append(pending, data...)
				pending = nil
			}
			idx := indexByte(data, suspByte)
			if idx < 0 {
				_, _ = unix.Write(master, data)
				continue
			}
			if idx > 0 {
				_, _ = unix.Write(master, data[:idx])
			}
			pending = append([]byte(nil), data[idx+1:]...)
			selfSuspend(inFd, cooked)
			if len(pending) > 0 {
				_, _ = unix.Write(master, pending)
				pending = nil
			}
		}
	}
}

// selfSuspend restores cooked termios then raises SIGTSTP on OUR pid only —
// TARGETED, never pgroup-wide (that would stop podman). Mirrors _self_suspend.
func selfSuspend(inFd int, cooked *unix.Termios) {
	_ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked)
	// SIGTSTP with the DEFAULT disposition stops us; the shell prints
	// "[1]+ Stopped" and `fg` later sends SIGCONT (handled -> re-raw).
	_ = syscall.Kill(os.Getpid(), syscall.SIGTSTP)
	// Control returns here after SIGCONT.
}

func safeCallback(cb func(*os.Process), p *os.Process) {
	defer func() { _ = recover() }()
	cb(p)
}

func setRaw(fd int, cooked *unix.Termios) {
	raw := *cooked
	// cfmakeraw equivalent.
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP |
		unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	_ = unix.IoctlSetTermios(fd, unix.TCSETS, &raw)
}

func isatty(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// openPty opens a new pty master/slave pair (no setsid — setsid broke
// `podman -it`).
func openPty() (master, slave int, err error) {
	master, err = unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return -1, -1, err
	}
	// grantpt + unlockpt: unlock the slave.
	var unlock int
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCSPTLCK,
		uintptr(unsafe.Pointer(&unlock))); e != 0 {
		unix.Close(master)
		return -1, -1, e
	}
	// TIOCGPTN: get the pty number.
	var ptn uint32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCGPTN,
		uintptr(unsafe.Pointer(&ptn))); e != 0 {
		unix.Close(master)
		return -1, -1, e
	}
	slavePath := ptsPath(ptn)
	slave, err = unix.Open(slavePath, unix.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		unix.Close(master)
		return -1, -1, err
	}
	return master, slave, nil
}

func ptsPath(n uint32) string {
	return "/dev/pts/" + uitoa(uint64(n))
}

func uitoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
