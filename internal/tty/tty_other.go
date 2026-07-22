//go:build !linux

package tty

import "golang.org/x/sys/unix"

// isattyFD reports whether fd is a terminal (TIOCGETA on darwin/BSD).
func isattyFD(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}
