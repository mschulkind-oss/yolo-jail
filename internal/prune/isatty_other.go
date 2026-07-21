//go:build !linux

package prune

import (
	"os"

	"golang.org/x/sys/unix"
)

// isTTY reports whether f is a real terminal (TIOCGETA on darwin/BSD), matching
// the run package's isatty probe. See the linux variant for why this is a real
// tty check and not a char-device mode check.
func isTTY(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TIOCGETA)
	return err == nil
}
