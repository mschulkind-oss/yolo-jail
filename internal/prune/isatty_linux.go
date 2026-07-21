//go:build linux

package prune

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTTY reports whether f is a real terminal (a TCGETS ioctl succeeds), matching
// the run package's isatty probe. This is a genuine tty check, NOT a
// char-device mode check — /dev/null is a char device but not a tty, so a mode
// check would wrongly emit ANSI to redirected output and break the byte-parity
// contract on the stripped text.
func isTTY(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}
