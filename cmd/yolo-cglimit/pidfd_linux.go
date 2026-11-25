//go:build linux

package main

import "golang.org/x/sys/unix"

// openPidfd returns a pidfd for pid in THIS process's pid namespace. The
// daemon, on the host, learns which host process it names from the kernel when
// it receives the descriptor, so the number never has to cross the boundary.
func openPidfd(pid int) (int, error) { return unix.PidfdOpen(pid, 0) }

func closeFd(fd int) { _ = unix.Close(fd) }
