//go:build linux

// Package ttyproxy is the in-process TTY proxy that wraps `podman run` so ^Z
// suspends the PROXY (not the container), SIGWINCH resizes propagate, and
// Ctrl-C, window-close, and SIGTERM tear the jail down cleanly. All signal teardown stays
// in one process.
//
// Frozen behavior (from docs/reference/ctrl-z-and-the-tty-proxy.md):
//   - non-TTY stdin -> transparent plain spawn (no pty).
//   - ^Z suspends the PROXY via TARGETED SIGTSTP to self (NEVER a pgroup-wide
//     signal — that would stop podman, a jail-visible change); the terminal is
//     restored to cooked mode and reset (cursor shown, mouse tracking disabled,
//     attributes cleared) before stopping; the keypress never reaches the
//     child; bytes after it in the same read are queued and flushed on resume.
//     WHICH BYTES ARE A ^Z is suspendkey.go's job and is no longer just 0x1A —
//     a terminal running the kitty keyboard protocol sends an escape sequence
//     instead, and missing it is a live wedge.
//   - NO Setsid (setsid broke `podman -it`); NEVER signal.Notify(SIGTSTP)
//     (default disposition required to actually stop).
//   - SIGCONT -> re-raw the host TTY, and resync the window size (a resize while
//     stopped raises no signal we will see). SIGWINCH -> TIOCSWINSZ to the pty,
//     then a TARGETED SIGWINCH at the child pid — the runtime shares our process
//     group on the host tty and reads its size from the proxy pty, so without the
//     poke it can read a stale size and nothing ever corrects it (resyncWinsize).
//   - Ctrl-C, SIGHUP or SIGTERM -> restore cooked termios and reset the terminal,
//     run onTerminate, exit 130 or 128+n. Raw mode delivers Ctrl-C as a byte, so
//     the proxy turns it into a TARGETED SIGINT rather than leaking it to the jail.
//   - stdin EOF -> stop reading stdin, keep pumping the master until child exit
//     (the decided semantics).
//
// Stage observation (StageHook/RunWithProxyHooked): the proxy is where the
// shutdown-delay question lives — the gap between the child exiting and the
// drain finishing is a named gap (docs/reference/perf-logging.md, Known gaps) — so
// the proxy reports its own transitions to whoever wants them, without this
// package learning what a timing span is.
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
	// interruptByte is no longer consulted: ^C forwards to the jail like any other
	// byte (proxyLoop states the ruling). Kept as the name for the value the
	// forwarding test writes, so a test asserting "the jail saw a ^C" says so.
	interruptByte = 0x03 // ^C
	suspByte      = 0x1a // ^Z
)

// getWinsize reads the terminal window size from fd.
func getWinsize(fd int) (*unix.Winsize, error) {
	return unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
}

// resyncWinsize copies the host tty's window size onto the proxy pty and then
// POKES THE CHILD, in that order. It is the fix for size drift — the ptys in a
// jail chain disagreeing about rows and columns, which shows up as an agent TUI
// wrapping in the wrong place until something forces a redraw.
//
// THE RACE. yolo does not setsid the runtime child (it cannot — setsid leaves
// podman with no controlling tty and breaks `-it`'s pty allocation; see the
// frozen-behavior notes above), so podman stays in this process's group ON THE
// HOST TTY and the kernel signals it with the same SIGWINCH, simultaneously. Its
// STDIO, though, is the proxy pty. If podman's handler runs before ours it reads
// the size we have not written yet and pushes that stale value into the
// container, and nothing corrects it: TIOCSWINSZ on the proxy pty raises no
// SIGWINCH because that pty has no foreground process group to raise it at
// (verified — tcgetpgrp on it returns ENOTTY). A coin flip with no recovery path,
// decided per resize.
//
// Measured 2026-09-09 across eight live jails on terrapin: both outer ptys agreed
// and several containers were a row short, three of them re-drifting on their own
// within fifteen minutes. A bare `kill -WINCH` at the podman pid fixed each
// instantly, which is what places the fault at this boundary.
//
// TARGETED AT THE PID, never the process group: a pgroup signal from here would
// be jail-visible, which the frozen-behavior block rules out for the same reason
// it rules out the ^Z broadcast. Unconditional rather than compared against a
// last-sent size — a redundant signal costs one redraw, while tracking the last
// size adds a second source of truth that can itself go stale.
//
// This does not suppress the racing first signal; podman may still act once on a
// stale size and then correct itself. And it cannot help a window that is garbled
// while every pty already AGREES: the kernel raises no SIGWINCH for a TIOCSWINSZ
// that writes the size already there, so there is nothing to re-signal. That case
// is a repaint fault in the TUI or the emulator, and no amount of this fixes it.
func resyncWinsize(inFd, master int, c *exec.Cmd) {
	ws, err := getWinsize(inFd)
	if err != nil {
		return
	}
	setWinsize(master, ws)
	// AFTER the write, which is the whole point: podman re-reads from the proxy
	// pty, so it must be authoritative before we ask. Errors are ignored in the
	// existing style of this file — after the child exits this is a signal to a
	// dead pid, which is the normal way a session ends.
	if c != nil && c.Process != nil {
		_ = c.Process.Signal(syscall.SIGWINCH)
	}
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
//     interrupted (Ctrl-C / SIGHUP window-close / SIGTERM) rather than exiting on its own.
//
// Non-TTY stdin falls back to a plain spawn (no pty), matching Python.
func RunWithProxy(cmd []string, onStarted func(*os.Process), onTerminate func()) (int, error) {
	return RunWithProxyHooked(cmd, onStarted, onTerminate, nil)
}

// Stage names handed to a StageHook, in the order a healthy pty session sees
// them. A path that skips a stage (the plain fallback has no drain, no
// termios) simply never reports it — the report shows what happened.
const (
	StageSpawned         = "spawned"
	StageExited          = "exited"
	StageDrainDone       = "drain_done"
	StageTermiosRestored = "termios_restored"
)

// StageHook observes the proxy's own transitions as they happen. It must not
// block, must not error, and runs on whatever goroutine got there first
// (including the signal goroutine) — observers that can panic are wrapped by
// the caller, not by this package.
type StageHook func(stage string)

func callStage(hook StageHook, stage string) {
	if hook == nil {
		return
	}
	defer func() { _ = recover() }()
	hook(stage)
}

// RunWithProxyHooked is RunWithProxy plus a stage observer. The hook is the
// seam the timing collector hangs `child.*` marks on; a nil hook is the
// everyday no-observer case and costs nothing.
func RunWithProxyHooked(cmd []string, onStarted func(*os.Process), onTerminate func(), hook StageHook) (int, error) {
	inFd := int(os.Stdin.Fd())
	if !isatty(inFd) {
		return runPlain(cmd, onStarted, hook)
	}

	// Save cooked attrs to restore on suspend/exit.
	cooked, err := unix.IoctlGetTermios(inFd, unix.TCGETS)
	if err != nil {
		return runPlain(cmd, onStarted, hook)
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
	callStage(hook, StageSpawned)
	if onStarted != nil {
		go safeCallback(onStarted, c.Process)
	}

	// Raw mode on the host TTY.
	setRaw(inFd, cooked)

	restoreCooked := func() { restoreTerminal(inFd, cooked) }

	// Signal handlers. Note: we DO NOT Notify SIGTSTP (default disposition must
	// stop us); we handle WINCH/CONT/INT/HUP/TERM. Ctrl-C arrives as a byte
	// while the host TTY is raw; proxyLoop raises the targeted SIGINT below.
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGWINCH, syscall.SIGCONT, syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var termOnce sync.Once
	go func() {
		for s := range sigCh {
			switch s {
			case syscall.SIGWINCH:
				resyncWinsize(inFd, master, c)
			case syscall.SIGCONT:
				setRaw(inFd, cooked) // host TTY was cooked while suspended
				// AND RESIZE. A window resized while we were stopped changed the
				// host tty behind our back, and the resulting SIGWINCH is only
				// delivered if one was pending — a resize that happened before the
				// stop, or several that coalesced, leaves us resumed at the wrong
				// size with no further signal coming. Same drift, different
				// trigger.
				resyncWinsize(inFd, master, c)
			case syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM:
				termOnce.Do(func() {
					restoreCooked()
					callStage(hook, StageTermiosRestored)
					if onTerminate != nil {
						onTerminate()
					}
					n := int(s.(syscall.Signal))
					os.Exit(128 + n)
				})
			}
		}
	}()

	rc := proxyLoop(inFd, master, c, cooked, hook)

	restoreCooked()
	callStage(hook, StageTermiosRestored)
	unix.Close(master)
	return rc, nil
}

func runPlain(cmd []string, onStarted func(*os.Process), hook StageHook) (int, error) {
	c := exec.Command(cmd[0], cmd[1:]...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Start(); err != nil {
		return 0, err
	}
	callStage(hook, StageSpawned)
	if onStarted != nil {
		go safeCallback(onStarted, c.Process)
	}
	err := c.Wait()
	callStage(hook, StageExited)
	return exitCode(err), nil
}

// proxyLoop pumps bytes between the host TTY and the master pty until the child
// exits.
// the stdin-EOF semantics (stop reading stdin, keep pumping master).
func proxyLoop(inFd, master int, c *exec.Cmd, cooked *unix.Termios, hook StageHook) int {
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
			callStage(hook, StageExited)
			for {
				n, err := unix.Read(master, buf)
				if n > 0 {
					_, _ = unix.Write(outFd, buf[:n])
				}
				if err != nil || n == 0 {
					break
				}
			}
			callStage(hook, StageDrainDone)
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
				callStage(hook, StageExited)
				callStage(hook, StageDrainDone)
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
			// ^C IS FORWARDED LIKE ANY OTHER BYTE, and that is a 2026-09-19 ruling
			// reversing what this loop used to do.
			//
			// It used to scan for 0x03, drop it, and raise a targeted SIGINT at the
			// proxy — "keep it out of the jail and signal only this proxy". The host
			// TTY is raw, so that was the only way Ctrl-C could mean anything at all
			// here; what it meant was QUIT THE LAUNCHER. Measured on a real host: at
			// a jail's bash prompt, Ctrl-C to clear the line tore the whole session
			// down instead. It is wrong for an agent too — Claude Code uses ^C to
			// interrupt generation, and under the old rule that killed the jail.
			//
			// Forwarded, the byte reaches the pty and the JAIL's own line discipline
			// raises SIGINT at the jail's foreground process group: bash clears the
			// line, an agent in raw mode gets 0x03 as input and decides for itself.
			// That is the ordinary `podman exec -it` contract, and it is what a user
			// expects from every other terminal they have.
			//
			// WHAT THIS COSTS is the escape hatch: there is no longer a keystroke
			// that quits the launcher from outside the jail. Leaving is exiting the
			// shell, or the child exiting. ^Z still suspends the proxy (suspendkey.go),
			// and an explicit `kill -INT` still reaches the signal arm below, which is
			// why that arm stays.
			if len(pending) > 0 {
				data = append(pending, data...)
				pending = nil
			}
			// NOT a byte scan: a terminal asked for the kitty keyboard
			// protocol sends Ctrl-Z as an escape sequence holding no 0x1A at
			// all, and forwarding that is the wedge this package exists to
			// prevent (suspendkey.go, THE WEDGE OF 2026-09-10).
			start, stop, found := findSuspendKey(data)
			if !found {
				_, _ = unix.Write(master, data)
				continue
			}
			if start > 0 {
				_, _ = unix.Write(master, data[:start])
			}
			pending = append([]byte(nil), data[stop:]...)
			selfSuspend(inFd, cooked)
			if len(pending) > 0 {
				_, _ = unix.Write(master, pending)
				pending = nil
			}
		}
	}
}

// termReset contains ANSI escape sequences that restore the terminal to a
// clean state without clearing the screen, altering scrollback, or moving the cursor:
//   - \x1b[0m: reset SGR attributes (colors, bold, underline, etc.)
//   - \x1b[?25h: show cursor (DECTCEM)
//   - \x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l: disable mouse tracking (X10, button-event, any-event, SGR)
//   - \x1b[?2004l: disable bracketed paste mode
//   - \x1b[?1l: normal cursor keys mode (DECCKM)
//   - \x1b>: normal keypad mode (DECKPNM)
//   - \x1b[?1004l: disable focus reporting
//   - \x1b[<u: pop the kitty keyboard protocol flags
//   - \x1b[>4;0m: disable xterm modifyOtherKeys
//
// THE LAST THREE ARE THE KEYSTROKE ONES, and they are why this constant exists
// in the shape it does rather than as "whatever `reset` sends". Under an enhanced
// keyboard protocol the terminal encodes every key as a CSI sequence, so a shell
// that inherits the mode echoes a burst of punctuation for each keypress — the
// state a user escapes by pasting `reset`. Claude Code turns one on (its own
// defaults carry `kittyKeyboard:!0`) and probes with `CSI ? u` and `CSI ? 6 n`,
// whose REPLY is `CSI ? <row> ; <col> R` — the stray `32;87` fragments that show
// up in the wreckage. None of the mouse or paste disables above touch any of it.
//
// It bites hardest on the ATTACH arm. A second terminal's `exec` dies when the
// container does, which is whenever the launching terminal is interrupted, and
// the agent inside it is killed with no chance to pop what it pushed. This
// process survives that, so it is the only thing that can.
//
// ⚠ DELIBERATELY ABSENT: \x1b[?1049l (leave the alternate screen). restoreTerminal
// is also the Ctrl-Z path (selfSuspend), where dropping the alt screen would wipe
// the suspended program's display and `fg` would not bring it back. Screen state
// is the one thing this must not touch.
const termReset = "\x1b[0m\x1b[?25h\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?2004l\x1b[?1l\x1b>" +
	"\x1b[?1004l\x1b[<u\x1b[>4;0m"

// resetTerminal sends escape sequences to restore cursor visibility, reset
// text attributes, and disable mouse tracking, bracketed paste, and application
// keypad/cursor modes. It writes to the first available terminal among stdout,
// stderr, and inFd.
func resetTerminal(inFd int) {
	fd := -1
	switch {
	case isatty(int(os.Stdout.Fd())):
		fd = int(os.Stdout.Fd())
	case isatty(int(os.Stderr.Fd())):
		fd = int(os.Stderr.Fd())
	case isatty(inFd):
		fd = inFd
	}
	if fd >= 0 {
		_, _ = unix.Write(fd, []byte(termReset))
	}
}

// restoreTerminal restores cooked termios on the host terminal and resets
// terminal modes (shows the cursor, resets attributes, and disables mouse
// tracking, bracketed paste, application cursor/keypad modes, focus reporting
// and the enhanced keyboard protocols).
func restoreTerminal(inFd int, cooked *unix.Termios) {
	_ = unix.IoctlSetTermios(inFd, unix.TCSETS, cooked)
	resetTerminal(inFd)
}

// selfSuspend restores cooked termios and resets the terminal, then raises
// SIGTSTP on OUR pid only — TARGETED, never pgroup-wide (that would stop podman).
func selfSuspend(inFd int, cooked *unix.Termios) {
	restoreTerminal(inFd, cooked)
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
