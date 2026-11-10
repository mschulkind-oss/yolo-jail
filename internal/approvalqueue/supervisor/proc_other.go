//go:build unix && !linux

package supervisor

import "syscall"

func platformAttr(a *syscall.SysProcAttr) {}
