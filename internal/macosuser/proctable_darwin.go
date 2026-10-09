//go:build darwin

package macosuser

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// proctable_darwin.go is the memory guard's process-table read on macOS, with no process
// started: the pid/ppid pairs from the kern.proc.all sysctl, and each process's resident size
// from proc_pidinfo(PROC_PIDTASKINFO).
//
// WHY NOT /bin/ps. ps is setuid root on macOS, and Seatbelt refuses a setuid exec inside any
// sandbox regardless of the profile's content (docs/plans/macos-revival-and-distribution-plan.md
// says the same of sudo). The guard runs inside the agent's sandbox, so its ps never started:
// `fork/exec /bin/ps: operation not permitted`, a 768m hog in a 256m session slept its full
// minute (macos-user CI run 37940733418, TestMacosUserMemoryGuard).
//
// WHAT THE SANDBOX LETS IT SEE. kern.proc.all is a sysctl-read, which the profile allows. A
// size is process-info-pidinfo, which the profile denies for any process outside the sandbox
// and re-allows for the same sandbox (seatbelt.go): the guard's descendants share its sandbox,
// so every size it sums is readable, and a refused one renders "-", which parsePSTable reads
// as 0 — never the session's.
//
// proc_pidinfo is a libSystem function with no syscall-number spelling of its own, so it is
// reached the way internal/ioprio reaches setiopolicy_np: a //go:cgo_import_dynamic, one JMP
// stub (proctable_darwin.s), and the runtime's libc call path. Every shipped binary is
// CGO_ENABLED=0.

//go:linkname syscall_syscall6 syscall.syscall6
func syscall_syscall6(fn, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err syscall.Errno)

var libc_proc_pidinfo_trampoline_addr uintptr

//go:cgo_import_dynamic libc_proc_pidinfo proc_pidinfo "/usr/lib/libSystem.B.dylib"

// procPidTaskInfo is PROC_PIDTASKINFO from <sys/proc_info.h>.
const procPidTaskInfo = 4

// procTaskInfo is struct proc_taskinfo from <sys/proc_info.h>: six uint64s, then twelve
// int32s, 96 bytes on both darwin architectures.
type procTaskInfo struct {
	VirtualSize   uint64
	ResidentSize  uint64
	TotalUser     uint64
	TotalSystem   uint64
	ThreadsUser   uint64
	ThreadsSystem uint64
	Policy        int32
	Faults        int32
	Pageins       int32
	CowFaults     int32
	MessagesSent  int32
	MessagesRecv  int32
	SyscallsMach  int32
	SyscallsUnix  int32
	Csw           int32
	Threadnum     int32
	Numrunning    int32
	Priority      int32
}

// residentBytes is pid's resident size, or false when the kernel would not say (another
// sandbox's process, a process that has exited, a zombie).
func residentBytes(pid int) (int64, bool) {
	var ti procTaskInfo
	size := unsafe.Sizeof(ti)
	r, _, _ := syscall_syscall6(libc_proc_pidinfo_trampoline_addr,
		uintptr(pid), procPidTaskInfo, 0, uintptr(unsafe.Pointer(&ti)), size, 0)
	if int32(r) != int32(size) {
		return 0, false
	}
	return int64(ti.ResidentSize), true
}

// readProcessTable reads every process's pid, parent and resident size. The reader pid is 0:
// no process is started, so there is no instrument of the guard's own to leave out.
func readProcessTable() (string, int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return "", 0, fmt.Errorf("kern.proc.all: %w", err)
	}
	samples := make([]procSample, 0, len(procs))
	for i := range procs {
		pid := int(procs[i].Proc.P_pid)
		if pid < 0 {
			continue
		}
		s := procSample{pid: pid, ppid: int(procs[i].Eproc.Ppid)}
		s.rssBytes, s.sized = residentBytes(pid)
		samples = append(samples, s)
	}
	return renderProcTable(samples), 0, nil
}
