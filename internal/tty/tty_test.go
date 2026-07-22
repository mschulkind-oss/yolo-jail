package tty

import (
	"os"
	"testing"
)

// TestIsTerminalFileNil confirms a nil *os.File is not a terminal (the guard
// that keeps callers from panicking on an unset stream).
func TestIsTerminalFileNil(t *testing.T) {
	if IsTerminalFile(nil) {
		t.Error("IsTerminalFile(nil) = true, want false")
	}
}

// TestIsTerminalOnPipe verifies a pipe (not a tty) reads as false — the ioctl
// fails on it. This is the redirected-output case that must stay color-free.
func TestIsTerminalOnPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	if IsTerminalFile(r) {
		t.Error("IsTerminalFile(pipe read end) = true, want false")
	}
	if IsTerminal(w.Fd()) {
		t.Error("IsTerminal(pipe write fd) = true, want false")
	}
}

// TestIsTerminalOnDevNull is the reason this probe exists: /dev/null is a
// character device, so an os.ModeCharDevice stat check false-positives on it,
// but it is NOT a terminal. The ioctl probe must report false.
func TestIsTerminalOnDevNull(t *testing.T) {
	f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	defer f.Close()
	if IsTerminalFile(f) {
		t.Errorf("IsTerminalFile(%s) = true, want false (char device, not a tty)", os.DevNull)
	}
}
