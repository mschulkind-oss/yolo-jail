//go:build linux

package tty

import "golang.org/x/sys/unix"

// isattyFD reports whether fd is a terminal (a TCGETS ioctl succeeds).
func isattyFD(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}
