//go:build linux

package lingerprobe

import (
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// sysNames is the syscall table for THIS build's architecture, built from
// x/sys's per-arch constants so an arm64 host reads arm64 numbers. It names
// only the calls worth naming in a lingering process; anything else renders
// as syscall_<nr>, which is still greppable against the arch's table.
var sysNames = func() map[int]string {
	m := map[int]string{
		unix.SYS_READ: "read", unix.SYS_WRITE: "write", unix.SYS_READV: "readv",
		unix.SYS_WRITEV: "writev", unix.SYS_PREAD64: "pread64", unix.SYS_PWRITE64: "pwrite64",
		unix.SYS_FUTEX: "futex", unix.SYS_EPOLL_PWAIT: "epoll_pwait", unix.SYS_EPOLL_PWAIT2: "epoll_pwait2",
		unix.SYS_NANOSLEEP: "nanosleep", unix.SYS_CLOCK_NANOSLEEP: "clock_nanosleep",
		unix.SYS_FLOCK: "flock", unix.SYS_FCNTL: "fcntl", unix.SYS_IOCTL: "ioctl",
		unix.SYS_WAIT4: "wait4", unix.SYS_WAITID: "waitid",
		unix.SYS_PPOLL: "ppoll", unix.SYS_PSELECT6: "pselect6",
		unix.SYS_CONNECT: "connect", unix.SYS_ACCEPT: "accept", unix.SYS_ACCEPT4: "accept4",
		unix.SYS_RECVFROM: "recvfrom", unix.SYS_RECVMSG: "recvmsg",
		unix.SYS_SENDTO: "sendto", unix.SYS_SENDMSG: "sendmsg",
		unix.SYS_OPENAT: "openat", unix.SYS_CLOSE: "close", unix.SYS_FSYNC: "fsync",
		unix.SYS_FDATASYNC: "fdatasync", unix.SYS_UMOUNT2: "umount2", unix.SYS_UNLINKAT: "unlinkat",
		unix.SYS_RENAMEAT: "renameat", unix.SYS_RENAMEAT2: "renameat2", unix.SYS_MOUNT: "mount", unix.SYS_GETDENTS64: "getdents64",
		unix.SYS_RT_SIGTIMEDWAIT: "rt_sigtimedwait", unix.SYS_RT_SIGSUSPEND: "rt_sigsuspend",
		unix.SYS_EXIT_GROUP: "exit_group",
		// The zero-copy family: coreutils `cat` blocks in splice(0, …), not read.
		unix.SYS_SPLICE: "splice", unix.SYS_TEE: "tee", unix.SYS_VMSPLICE: "vmsplice",
		unix.SYS_COPY_FILE_RANGE: "copy_file_range", unix.SYS_SENDFILE: "sendfile",
	}
	// The calls amd64 has and arm64 never had (arm64's table starts from the generic one,
	// which dropped the non-p/non-at variants), as literal amd64 numbers: x/sys defines
	// these constants only for amd64, and a `_linux_amd64.go` file would be invisible to
	// every lint pass on an arm64 host (TestEveryGoFileIsAnalyzedBySomeLintPass).
	if runtime.GOARCH == "amd64" {
		for nr, name := range map[int]string{7: "poll", 23: "select", 34: "pause", 232: "epoll_wait"} {
			m[nr] = name
		}
	}
	return m
}()

// syscallName is the production Sampler.Name.
func syscallName(nr int) string { return sysNames[nr] }

// clockNow is the production Sampler.Clock.
func clockNow(id int) (time.Duration, bool) {
	var ts unix.Timespec
	if err := unix.ClockGettime(int32(id), &ts); err != nil {
		return 0, false
	}
	return time.Duration(ts.Sec)*time.Second + time.Duration(ts.Nsec), true
}
