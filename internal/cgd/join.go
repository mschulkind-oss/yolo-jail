package cgd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// The join op moves an ALREADY RUNNING process into a named group. It is the
// one op in which the caller names a process other than itself, so it is the
// one that needs a check beyond SO_PEERCRED.
//
// WHAT IS CHECKED. The jail cannot be believed about which process it means:
// the PID it knows is in its own pid namespace, and any number it sent would be
// a number in the HOST's. So the client sends a PIDFD over the socket
// (SCM_RIGHTS), the daemon reads the host PID the kernel attests for it
// (TargetFromPidfd), and then the daemon verifies, host-side, that
// /proc/<pid>/cgroup places that process inside THIS container's cgroup subtree.
// A process outside it is refused, and nothing is created or written.
//
// THE RESIDUAL TOCTOU WINDOW, and why it is accepted. Linux can write a pid
// number to cgroup.procs but not a pidfd, so the number is used after the
// check. Between the cgroup check and that write the target could exit, be
// reaped, and its number be reused by an unrelated host process. The pidfd
// protects every step but the last: Alive() re-reads the pidfd's
// kernel-attested pid immediately before the write and refuses if the process
// has been reaped (a reaped process's pidfd reports -1). What remains is the
// microseconds between that final Alive() and the write(2): the target would
// have to be reaped there, and its number reallocated to a process outside the
// container, at that exact moment. Host pids are allocated cyclically up to
// pid_max, so a number is not reissued until the counter wraps. And the damage
// is bounded: the stranger moves into a group under the jail's own cgroup
// subtree, which can only lower its resources, and the audit line records the
// host pid. Closing it entirely would need a cgroup.procs interface that takes
// a pidfd, which the kernel does not have.

// Target is a process a join was asked to move, in HOST terms.
type Target struct {
	// PID is the target's pid in the daemon's pid namespace.
	PID int
	// Alive reports whether the pidfd still refers to an unreaped process. Nil
	// means "assume so".
	Alive func() bool
}

// Roots the join check reads. Variables so a test can point them at a fake tree.
var (
	procRoot   = "/proc"
	cgroupRoot = "/sys/fs/cgroup"
)

// MinCPUWeight and MaxCPUWeight are the kernel's cpu.weight range.
const (
	MinCPUWeight = 1
	MaxCPUWeight = 10000
)

// TargetFromPidfd resolves a pidfd the peer passed to a Target. The kernel
// reports the pid in the reader's namespace in /proc/self/fdinfo/<fd>. The fd
// must be a pidfd: anything else the peer could pass is refused.
func TargetFromPidfd(f *os.File) (*Target, error) {
	fd := strconv.Itoa(int(f.Fd()))
	if l, err := os.Readlink(filepath.Join(procRoot, "self", "fd", fd)); err != nil || l != "anon_inode:[pidfd]" {
		return nil, os.ErrInvalid
	}
	info := filepath.Join(procRoot, "self", "fdinfo", fd)
	pid := fdinfoPid(info)
	if pid <= 0 {
		return nil, os.ErrProcessDone
	}
	return &Target{PID: pid, Alive: func() bool { return fdinfoPid(info) == pid }}, nil
}

// fdinfoPid parses the "Pid:" line of a pidfd's fdinfo: the pid, -1 once the
// process is reaped, or 0 when absent or unreadable.
func fdinfoPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Pid:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// insideContainer reports whether pid's cgroup is the container's cgroup or a
// descendant of it, reading /proc/<pid>/cgroup (the daemon is on the host, so
// the path is relative to the host's cgroup root). why is non-empty when the
// question could not be answered.
func insideContainer(containerCgroup string, pid int) (in bool, why string) {
	rel, err := filepath.Rel(cgroupRoot, containerCgroup)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false, "cannot place the container cgroup under the cgroup root"
	}
	b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return false, "cannot read the process's cgroup: " + err.Error()
	}
	for _, line := range strings.Split(string(b), "\n") {
		path, ok := strings.CutPrefix(line, "0::")
		if !ok {
			continue
		}
		path = strings.TrimPrefix(filepath.Clean("/"+path), "/")
		return path == rel || strings.HasPrefix(path, rel+"/"), ""
	}
	return false, "the process has no cgroup v2 entry"
}

func joinPID(containerCgroup, name string, r *Request, t *Target) *jsonx.OrderedMap {
	pid := t.PID
	if pid <= 1 {
		return errResp("Invalid target PID")
	}
	in, why := insideContainer(containerCgroup, pid)
	if why != "" {
		return errResp("Refusing to join PID " + strconv.Itoa(pid) + ": " + why)
	}
	if !in {
		return errResp("Refusing to join PID " + strconv.Itoa(pid) + ": it is not inside this container's cgroup")
	}
	jobCg, errs, fail := prepareJob(containerCgroup, name, r)
	if fail != nil {
		return fail
	}
	// See the TOCTOU argument at the top of this file: this is the last check
	// before the write.
	if t.Alive != nil && !t.Alive() {
		return errResp("PID " + strconv.Itoa(pid) + " exited before it could be moved")
	}
	return moveInto(jobCg, pid, errs)
}
