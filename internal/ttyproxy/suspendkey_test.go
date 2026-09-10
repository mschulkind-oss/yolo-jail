//go:build linux

package ttyproxy

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// THE ENCODING TABLE. Every row that must match is a wedge if it stops
// matching, and every row that must not is a keystroke stolen from the agent
// — the two failure directions are equally bad, so both are pinned here.
func TestFindSuspendKeyEncodings(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string // the bytes that must survive (prefix+suffix), "" when no match
		match bool
	}{
		// The legacy encoding, which is all the proxy understood until 2026-09-10.
		{"legacy bare", "\x1a", "", true},
		{"legacy with prefix and suffix", "ab\x1acd", "abcd", true},

		// The kitty keyboard protocol — the encoding that shipped the wedge.
		{"kitty ctrl-z", "\x1b[122;5u", "", true},
		{"kitty ctrl-z with prefix and suffix", "hi\x1b[122;5uthere", "hithere", true},
		{"kitty explicit press event", "\x1b[122;5:1u", "", true},
		{"kitty repeat event", "\x1b[122;5:2u", "", true},
		// Caps/Num lock ride along in the modifier field and mean nothing here.
		{"kitty ctrl-z with caps lock", "\x1b[122;69u", "", true},
		{"kitty ctrl-z with num lock", "\x1b[122;133u", "", true},
		{"kitty ctrl-z with both locks", "\x1b[122;197u", "", true},

		// xterm's older modifyOtherKeys form.
		{"xterm modifyOtherKeys ctrl-z", "\x1b[27;5;122~", "", true},
		{"xterm with num lock", "\x1b[27;133;122~", "", true},

		// MUST NOT match: a release is the second half of ONE keypress, and
		// suspending on it would re-stop the proxy the instant it resumed.
		{"kitty release event", "\x1b[122;5:3u", "\x1b[122;5:3u", false},
		// MUST NOT match: these are other keys, and stealing them is a bug.
		{"kitty ctrl-shift-z", "\x1b[122;6u", "\x1b[122;6u", false},
		{"kitty ctrl-alt-z", "\x1b[122;7u", "\x1b[122;7u", false},
		{"kitty plain z reported as escape", "\x1b[122u", "\x1b[122u", false},
		{"kitty ctrl-y", "\x1b[121;5u", "\x1b[121;5u", false},
		{"xterm ctrl-y", "\x1b[27;5;121~", "\x1b[27;5;121~", false},
		{"arrow up", "\x1b[A", "\x1b[A", false},
		{"bracketed paste start", "\x1b[200~", "\x1b[200~", false},
		{"private mode sequence", "\x1b[?1049h", "\x1b[?1049h", false},
		{"plain text", "hello", "hello", false},
		{"empty", "", "", false},
		// A split sequence passes through — the documented LIMITATION. If this
		// row ever needs to change, read the comment before changing it.
		{"incomplete sequence", "\x1b[122;5", "\x1b[122;5", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := findSuspendKey([]byte(tc.in))
			if ok != tc.match {
				t.Fatalf("match = %v, want %v (in %q, range %d:%d)", ok, tc.match, tc.in, start, end)
			}
			if !ok {
				return
			}
			got := tc.in[:start] + tc.in[end:]
			if got != tc.want {
				t.Errorf("surviving bytes = %q, want %q", got, tc.want)
			}
		})
	}
}

// The suspend byte must never reach the child, and neither may the escape
// form: the range returned has to cover the WHOLE sequence. A range that is
// one byte short leaks a stray `u` into the agent's input, which is the kind
// of bug that reads as "the TUI is glitching" for months.
func TestFindSuspendKeyConsumesWholeSequence(t *testing.T) {
	for _, seq := range []string{"\x1b[122;5u", "\x1b[122;5:1u", "\x1b[27;5;122~", "\x1a"} {
		start, end, ok := findSuspendKey([]byte("A" + seq + "B"))
		if !ok {
			t.Fatalf("%q did not match", seq)
		}
		if start != 1 || end != 1+len(seq) {
			t.Errorf("%q consumed [%d:%d], want [1:%d]", seq, start, end, 1+len(seq))
		}
	}
}

// THE CALL-SITE PIN. findSuspendKey being correct proves nothing if the pump
// loop still scans for a bare byte — that is exactly the "test pins the callee
// while the call site is unpinned" shape AGENTS.md records this repo shipping
// five times. This drives the REAL proxyLoop over a real pty and asserts the
// escape-encoded Ctrl-Z suspends it. Revert the loop to indexByte and this
// fails; the table above still passes.
//
// It observes the suspend through selfSuspend's own side effect — cooked
// termios restored on the input fd — because SIGTSTP is ignored here (as in
// TestSuspendTargetedSelfOnly) so the stop itself cannot land.
func TestProxyLoopSuspendsOnEscapeEncodedCtrlZ(t *testing.T) {
	t.Run("kitty ctrl-z suspends", func(t *testing.T) {
		assertProxySuspends(t, "\x1b[122;5u", true)
	})
	t.Run("kitty ctrl-y does not", func(t *testing.T) {
		assertProxySuspends(t, "\x1b[121;5u", false)
	})
}

func assertProxySuspends(t *testing.T, input string, want bool) {
	t.Helper()
	signal.Ignore(syscall.SIGTSTP)
	defer signal.Reset(syscall.SIGTSTP)

	// Stand in for the host terminal: the proxy reads the user from hostSlave.
	hostMaster, hostSlave, err := openPty()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer unix.Close(hostMaster)
	defer unix.Close(hostSlave)

	// And for the runtime's end.
	childMaster, childSlave, err := openPty()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	defer unix.Close(childMaster)

	// No echo on the child end: `sleep` never reads, so the line discipline
	// would bounce our input back through the proxy onto the test's stdout.
	if tio, err := unix.IoctlGetTermios(childSlave, unix.TCGETS); err == nil {
		tio.Lflag &^= unix.ECHO
		_ = unix.IoctlSetTermios(childSlave, unix.TCSETS, tio)
	}
	slave := os.NewFile(uintptr(childSlave), "pty-slave")
	c := exec.Command("sleep", "10")
	c.Stdin, c.Stdout, c.Stderr = slave, slave, slave
	if err := c.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer func() { _ = c.Process.Kill() }()
	unix.Close(childSlave)

	cooked, err := unix.IoctlGetTermios(hostSlave, unix.TCGETS)
	if err != nil {
		t.Fatalf("get termios: %v", err)
	}
	setRaw(hostSlave, cooked) // what RunWithProxyHooked does before pumping

	go proxyLoop(hostSlave, childMaster, c, cooked, nil)

	if _, err := unix.Write(hostMaster, []byte(input)); err != nil {
		t.Fatalf("write to host pty: %v", err)
	}

	// selfSuspend restores cooked termios before raising SIGTSTP, so ISIG
	// coming back is the observable that the loop matched.
	// A match is proved by an event, a non-match by the absence of one — so the
	// two need very different waits, and giving the negative case the positive
	// case's deadline just makes the unit suite three seconds slower per row.
	wait := 3 * time.Second
	if !want {
		wait = 300 * time.Millisecond
	}
	deadline := time.Now().Add(wait)
	for {
		tio, err := unix.IoctlGetTermios(hostSlave, unix.TCGETS)
		if err == nil && tio.Lflag&unix.ISIG != 0 {
			if !want {
				t.Fatal("proxy suspended on a key that is not Ctrl-Z")
			}
			return
		}
		if time.Now().After(deadline) {
			if want {
				t.Fatal("proxy did not suspend on an escape-encoded Ctrl-Z " +
					"— the pump loop is not using findSuspendKey")
			}
			return // stayed raw, which is the correct answer for a non-match
		}
		time.Sleep(10 * time.Millisecond)
	}
}
