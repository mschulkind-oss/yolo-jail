// Package tty is the single terminal-detection helper for every yolo command.
// It reports whether a file descriptor is a real terminal via a TCGETS/TIOCGETA
// ioctl — NOT an os.ModeCharDevice stat check, which false-positives on the
// container `-t` flag and on /dev/null (an observed divergence). Interactive-
// prompt decisions route through here so the ioctl truth is used consistently,
// and so does color: Color (color.go) is the one color gate, combining the
// request, this probe's answer and the NO_COLOR convention.
package tty

import "os"

// IsTerminal reports whether fd is a real terminal (the platform ioctl
// succeeds). The syscall itself is in the platform-split isattyFD.
func IsTerminal(fd uintptr) bool { return isattyFD(int(fd)) }

// IsTerminalFile is IsTerminal for an *os.File (nil → not a terminal).
func IsTerminalFile(f *os.File) bool {
	if f == nil {
		return false
	}
	return IsTerminal(f.Fd())
}
