//go:build darwin

package ioprio

import (
	"syscall"
	_ "unsafe" // go:linkname
)

// diskpolicy_darwin.go is the macOS half of `resources.io.priority`: setiopolicy_np and
// getiopolicy_np on the CALLING PROCESS, at process scope, which every child it starts
// afterwards inherits (getiopolicy_np(3): "the I/O policy of a newly created process is
// inherited from its parent process"). The macos-user launcher is the one caller; it sets the
// policy on itself before the bootstrap, so the bootstrap, the provisioning stage, the jail
// daemons and the agent all start under it (docs/design/io-priority.md §5.5, IO-D7). Measured
// to survive the launch's sudo, env -i and sandbox-exec: GitHub Actions run 37121866798.
//
// WHY A TRAMPOLINE AND NOT cgo. Every shipped binary is built with CGO_ENABLED=0
// (scripts/build-go.sh and flake.nix), so these two libSystem functions are reached the way
// golang.org/x/sys/unix reaches every libSystem call on darwin: a //go:cgo_import_dynamic for
// the symbol, one assembly JMP stub per function (diskpolicy_darwin.s), and the runtime's own
// libc call path, syscall.syscall, which sets errno for a -1 return. Neither function has a
// syscall-number spelling to use instead: both are libc wrappers over a private one.

//go:linkname syscall_syscall syscall.syscall
func syscall_syscall(fn, a1, a2, a3 uintptr) (r1, r2 uintptr, err syscall.Errno)

var libc_setiopolicy_np_trampoline_addr uintptr
var libc_getiopolicy_np_trampoline_addr uintptr

//go:cgo_import_dynamic libc_setiopolicy_np setiopolicy_np "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_getiopolicy_np getiopolicy_np "/usr/lib/libSystem.B.dylib"

// SetProcessDiskPolicy sets the calling process's disk I/O policy to the raw policy v
// (IopolThrottle, IopolUtility, …) at process scope.
func SetProcessDiskPolicy(v int) error {
	r, _, e := syscall_syscall(libc_setiopolicy_np_trampoline_addr,
		uintptr(IopolTypeDisk), uintptr(IopolScopeProcess), uintptr(v))
	if int32(r) != 0 {
		if e == 0 {
			e = syscall.EINVAL
		}
		return e
	}
	return nil
}

// GetProcessDiskPolicy reads the calling process's disk I/O policy at process scope.
func GetProcessDiskPolicy() (int, error) {
	r, _, e := syscall_syscall(libc_getiopolicy_np_trampoline_addr,
		uintptr(IopolTypeDisk), uintptr(IopolScopeProcess), 0)
	if int32(r) < 0 {
		if e == 0 {
			e = syscall.EINVAL
		}
		return 0, e
	}
	return int(int32(r)), nil
}
