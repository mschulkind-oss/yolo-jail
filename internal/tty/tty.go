// Package tty is the single terminal-detection helper for every yolo command.
// It reports whether a file descriptor is a real terminal via a TCGETS/TIOCGETA
// ioctl — NOT an os.ModeCharDevice stat check, which false-positives on the
// container `-t` flag and on /dev/null (an observed divergence). Interactive-
// prompt decisions route through here so the ioctl truth is used consistently,
// and so does color: Color (color.go) is the one color gate, combining the
// request, this probe's answer and the NO_COLOR convention.
//
// Architecture and invariants: docs/reference/cli-color.md
package tty

import (
	"os"

	"golang.org/x/sys/unix"
)

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

// Width reports the column count of the terminal on fd, or 0 when fd is not a
// terminal or the size cannot be read. A live progress line truncates to it,
// because a line that wraps cannot be redrawn in place.
func Width(fd uintptr) int {
	ws, err := unix.IoctlGetWinsize(int(fd), unix.TIOCGWINSZ)
	if err != nil || ws == nil {
		return 0
	}
	return int(ws.Col)
}
