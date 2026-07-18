//go:build linux

package main

import (
	"net"
	"syscall"
)

// peerCredPID returns the connecting peer's PID via SO_PEERCRED, or 0 if
// unavailable. Mirrors the SO_PEERCRED read in _cgroup_delegate_handler (only
// the PID is used; uid/gid are ignored).
func peerCredPID(conn *net.UnixConn) int {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0
	}
	var pid int
	_ = raw.Control(func(fd uintptr) {
		ucred, cerr := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		if cerr == nil && ucred != nil {
			pid = int(ucred.Pid)
		}
	})
	return pid
}
