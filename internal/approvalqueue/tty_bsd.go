//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package approvalqueue

import "syscall"

const getTermios = syscall.TIOCGETA
