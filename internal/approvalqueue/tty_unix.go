//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package approvalqueue

import (
	"os"
	"syscall"
	"unsafe"
)

// isTerminal reports whether f is a terminal, by asking the kernel for its terminal attributes.
// A character device such as /dev/null is not a terminal, which is why the file mode alone is
// not enough: redirecting the queue's output to /dev/null must not look like a person.
func isTerminal(f *os.File) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), getTermios, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}
