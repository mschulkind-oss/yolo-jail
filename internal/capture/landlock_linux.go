//go:build linux

package capture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// landlock_linux.go is the HOST CAPTURE's confinement on Linux (docs/design/host-tool-provisioning.md
// HP-D18): a vendor installer run on the host, with no container, under Landlock (the kernel's
// unprivileged path-based access control, Documentation/userspace-api/landlock.rst) and a small
// seccomp filter, so `yolo capture <bin>` works on a Linux machine with no container runtime.
//
// # The shape
//
// A process restricts ITSELF and then execs the program it confines. Landlock has no "restrict that
// child" call: landlock_restrict_self applies to the calling thread, and an exec from that thread
// carries the restriction into the new program and every process it starts. Go's os/exec has no hook
// between fork and exec, so the restriction is a re-exec: `yolo internal landlock-exec` (internal/cli)
// parses a LandlockPolicy, locks its goroutine to one OS thread, and calls ExecConfined, which never
// returns on success. golang.org/x/sys/unix has Landlock's types and syscall numbers and no wrappers,
// so the three calls are raw syscalls; nothing here adds a dependency.
//
// # What a confined process may do
//
//   - write (create, remove, rename, link, truncate) only beneath LandlockPolicy.ReadWrite;
//   - read and execute only beneath ReadWrite and ReadExec, and read beneath Read;
//   - create no AF_UNIX socket and set up no io_uring (the seccomp half, below);
//   - on ABI 6 and later, signal no process outside its own domain.
//
// Everything else the kernel allows a process of the user's is allowed: the network in particular,
// which a vendor installer needs to download what it installs.
//
// # Why a seccomp filter too
//
// None of the filesystem rights this ruleset handles covers CONNECTING to a UNIX socket that
// already exists: a path lookup is not an access they mediate. A user's machine keeps sockets that
// run commands with the user's full authority one connect away — tmux's server, an ssh-agent, a
// docker socket, the D-Bus session bus — at paths an installer can guess, so a Landlock-only
// confinement would let the installer it confines leave through any of them. The filter refuses
// socket(AF_UNIX, …) with EACCES, which closes every such door at once (socketpair, which only ever
// connects a process to itself, stays allowed: Node's child pipes use it). io_uring is refused
// because its ring can create and connect a socket without the socket(2) call the filter sees. A
// syscall made through another architecture's table (an i386 binary on x86-64, the x32 table) is
// refused outright, since the filter could not tell its socket call from any other.
//
// # The residual, stated
//
// Below ABI 3 (Linux 6.2) a confined process may still truncate a file it can name outside its write
// set, since truncate(2) is a right only ABI 3 knows; LandlockMinABI is 2 because ABI 1 denies every
// rename across directories, which installers do. Reads outside the excluded branches — every
// directory not under the user's home or runtime directory — stay open, as they are to any program the
// user runs; so does the network, and so does writing beneath /tmp.

// LandlockMinABI is the oldest Landlock ABI the host capture runs under: 2 (Linux 5.19), the first in
// which a confined process may rename and link across directories (LANDLOCK_ACCESS_FS_REFER). Under
// ABI 1 every such move is denied, and an installer's own moves would fail.
const LandlockMinABI = 2

// ErrLandlockUnavailable is why a kernel offers no Landlock at all: built without it (ENOSYS), or
// built with it and not enabled at boot (EOPNOTSUPP: `lsm=` leaves it out).
var ErrLandlockUnavailable = errors.New("this kernel offers no Landlock")

// LandlockABI is the running kernel's Landlock ABI version, or why there is none.
func LandlockABI() (int, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		if errno == unix.ENOSYS || errno == unix.EOPNOTSUPP {
			return 0, fmt.Errorf("%w (%v)", ErrLandlockUnavailable, errno)
		}
		return 0, fmt.Errorf("probing Landlock: %w", errno)
	}
	return int(abi), nil
}

// HostConfinementABI is the Landlock ABI a host capture would run under on this machine, or why it
// cannot run here: no Landlock, one older than LandlockMinABI, or an architecture the seccomp half has
// no filter for.
func HostConfinementABI() (int, error) {
	if _, ok := seccompArch(); !ok {
		return 0, fmt.Errorf("the host capture's seccomp filter is built for amd64 and arm64 only, and this "+
			"machine is linux/%s", runtime.GOARCH)
	}
	abi, err := LandlockABI()
	if err != nil {
		return 0, err
	}
	if abi < LandlockMinABI {
		return abi, fmt.Errorf("this kernel's Landlock is ABI %d, and a host capture needs ABI %d or later "+
			"(Linux 5.19): under ABI 1 a confined installer cannot rename a file across directories", abi,
			LandlockMinABI)
	}
	return abi, nil
}

// LandlockPolicy is what one confined process may touch, as paths: each grant covers the path and
// everything beneath it. A path that is a file takes the file half of its grant (read, execute,
// write, truncate), a directory all of it.
type LandlockPolicy struct {
	// ReadWrite are the paths beneath which the process may read, execute, write, create, remove,
	// rename, link and truncate.
	ReadWrite []string
	// ReadExec are the paths beneath which it may read and execute.
	ReadExec []string
	// Read are the paths beneath which it may only read.
	Read []string
}

// Args renders p as `yolo internal landlock-exec`'s flags, the form it crosses a re-exec in.
func (p LandlockPolicy) Args() []string {
	var out []string
	for _, g := range []struct {
		flag  string
		paths []string
	}{{"--rw=", p.ReadWrite}, {"--rx=", p.ReadExec}, {"--ro=", p.Read}} {
		for _, path := range g.paths {
			out = append(out, g.flag+path)
		}
	}
	return out
}

// ParseLandlockArgs reads Args' flags back, up to `--`, and returns the policy and the argv after
// `--`. Every path must be absolute: a relative one would resolve against wherever the confining
// process happened to start.
func ParseLandlockArgs(args []string) (LandlockPolicy, []string, error) {
	var p LandlockPolicy
	for i, a := range args {
		if a == "--" {
			if i+1 >= len(args) {
				return p, nil, errors.New("no command after --")
			}
			return p, args[i+1:], nil
		}
		var dst *[]string
		var path string
		switch {
		case strings.HasPrefix(a, "--rw="):
			dst, path = &p.ReadWrite, strings.TrimPrefix(a, "--rw=")
		case strings.HasPrefix(a, "--rx="):
			dst, path = &p.ReadExec, strings.TrimPrefix(a, "--rx=")
		case strings.HasPrefix(a, "--ro="):
			dst, path = &p.Read, strings.TrimPrefix(a, "--ro=")
		default:
			return p, nil, fmt.Errorf("unexpected argument %q", a)
		}
		if !filepath.IsAbs(path) {
			return p, nil, fmt.Errorf("%q is not an absolute path", path)
		}
		*dst = append(*dst, path)
	}
	return p, nil, errors.New("no -- before the command")
}

// The rights, by Landlock ABI. handledFS is every right the ruleset governs on this kernel: a right
// it does not handle is a right it allows everywhere, and a right newer than the kernel is EINVAL.
const (
	llRead  = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	llExec  = unix.LANDLOCK_ACCESS_FS_EXECUTE
	llWrite = unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
		unix.LANDLOCK_ACCESS_FS_MAKE_REG | unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
		unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK | unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
		unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE
	// llFile is the subset a rule on a FILE may grant; the rest are about a directory's entries.
	llFile = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_TRUNCATE
)

func handledFS(abi int) uint64 {
	h := uint64(llRead | llExec | llWrite)
	if abi < 2 {
		h &^= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi < 3 {
		h &^= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	return h
}

// scopedFor is the IPC scoping a ruleset asks for: from ABI 6, a confined process may neither signal
// a process outside its domain nor connect to an abstract UNIX socket another domain made.
func scopedFor(abi int) uint64 {
	if abi < 6 {
		return 0
	}
	return unix.LANDLOCK_SCOPE_SIGNAL | unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET
}

// ExecConfined restricts this process to p and execs argv (argv[0] an absolute path) with env. It
// returns only when something failed, and never execs argv unconfined: every error is returned before
// the exec, and the caller exits on it.
//
// It must run in a process that will do nothing else: it locks the calling goroutine to its OS thread
// and never unlocks it, because the restriction it builds is that thread's, and the exec is what hands
// it to the whole process.
func ExecConfined(p LandlockPolicy, argv, env []string) error {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return errors.New("the confined command must start with an absolute path")
	}
	abi, err := HostConfinementABI()
	if err != nil {
		return err
	}
	filter, err := seccompFilter()
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	attr := unix.LandlockRulesetAttr{Access_fs: handledFS(abi), Scoped: scopedFor(abi)}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("creating the Landlock ruleset: %w", errno)
	}
	handled := handledFS(abi)
	for _, g := range []struct {
		access uint64
		paths  []string
	}{
		{handled, p.ReadWrite},
		{llRead | llExec, p.ReadExec},
		{llRead, p.Read},
	} {
		for _, path := range g.paths {
			if err := addLandlockRule(int(fd), path, g.access&handled); err != nil {
				return err
			}
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("setting no_new_privs: %w", err)
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	err = unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&prog)), 0, 0)
	if err != nil {
		return fmt.Errorf("installing the seccomp filter: %w", err)
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("restricting this process with Landlock: %w", errno)
	}
	_ = unix.Close(int(fd))
	if err := syscall.Exec(argv[0], argv, env); err != nil {
		return fmt.Errorf("running %s: %w", argv[0], err)
	}
	return errors.New("exec returned") // unreachable: a successful exec does not return
}

// addLandlockRule grants access beneath path. A path that is gone is skipped: a grant of nothing
// changes nothing, and the policy is built from a listing a moment older than this. Any other failure
// is the confinement's, and refuses the run.
func addLandlockRule(ruleset int, path string, access uint64) error {
	pfd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return fmt.Errorf("opening %s for a Landlock rule: %w", path, err)
	}
	defer unix.Close(pfd)
	var st unix.Stat_t
	if err := unix.Fstat(pfd, &st); err != nil {
		return fmt.Errorf("reading %s for a Landlock rule: %w", path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		access &= llFile
	}
	if access == 0 {
		return nil
	}
	rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(pfd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("adding a Landlock rule for %s: %w", path, errno)
	}
	return nil
}

// seccompArch is the audit architecture this build's syscalls are made under, and the socket(2)
// number on it, for the architectures the filter is built for.
func seccompArch() (arch uint32, ok bool) {
	switch runtime.GOARCH {
	case "amd64":
		return unix.AUDIT_ARCH_X86_64, true
	case "arm64":
		return unix.AUDIT_ARCH_AARCH64, true
	}
	return 0, false
}

// seccompFilter is the classic-BPF program the confined process runs under (see the file comment):
// socket(AF_UNIX, …) is EACCES, io_uring is ENOSYS (what a kernel without it answers, which every
// user of it falls back from), a syscall through another table is EPERM, and everything else is
// allowed. The argument read is the low 32 bits of socket's first argument, an int, at seccomp_data's
// args[0] on the two little-endian architectures this builds for.
func seccompFilter() ([]unix.SockFilter, error) {
	arch, ok := seccompArch()
	if !ok {
		return nil, fmt.Errorf("no seccomp filter for linux/%s", runtime.GOARCH)
	}
	const (
		offNr   = 0
		offArch = 4
		offArg0 = 16
		// x32Bit marks a syscall made through x86-64's x32 table; arm64 numbers never carry it.
		x32Bit = 0x40000000
	)
	ret := func(k uint32) unix.SockFilter { return unix.SockFilter{Code: unix.BPF_RET | unix.BPF_K, K: k} }
	ld := func(off uint32) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: off}
	}
	// jt and jf count instructions from the one after the jump.
	jeq := func(k uint32, jt, jf uint8) unix.SockFilter {
		return unix.SockFilter{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: k, Jt: jt, Jf: jf}
	}
	return []unix.SockFilter{
		/*  0 */ ld(offArch),
		/*  1 */ jeq(arch, 1, 0), // → 3, else 2
		/*  2 */ ret(unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)),
		/*  3 */ ld(offNr),
		/*  4 */ {Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, K: x32Bit, Jt: 10}, // → 15
		/*  5 */ jeq(uint32(unix.SYS_SOCKET), 4, 0), // → 10
		/*  6 */ jeq(uint32(unix.SYS_IO_URING_SETUP), 7, 0), // → 14
		/*  7 */ jeq(uint32(unix.SYS_IO_URING_ENTER), 6, 0), // → 14
		/*  8 */ jeq(uint32(unix.SYS_IO_URING_REGISTER), 5, 0), // → 14
		/*  9 */ ret(unix.SECCOMP_RET_ALLOW),
		/* 10 */ ld(offArg0),
		/* 11 */ jeq(unix.AF_UNIX, 0, 1), // → 12, else 13
		/* 12 */ ret(unix.SECCOMP_RET_ERRNO | uint32(unix.EACCES)),
		/* 13 */ ret(unix.SECCOMP_RET_ALLOW),
		/* 14 */ ret(unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)),
		/* 15 */ ret(unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)),
	}, nil
}

// CapturePolicyOptions are the facts a host capture's policy is built from.
type CapturePolicyOptions struct {
	// Staging is the capture's staging directory: the installer's HOME and the delta's out dir are
	// beneath it, and it is the one place under the user's home the installer may write.
	Staging string
	// Home is the user's home. Neither it nor anything in Exclude — the account's home when HOME
	// names another, the user's runtime directory ($XDG_RUNTIME_DIR, /run/user/<uid>) — is readable
	// or writable, beyond what Staging grants.
	Home    string
	Exclude []string
	// Exec are files the installer may run although they are under an excluded branch: the yolo
	// executable, which the launcher calls and the capture driver is.
	Exec []string
	// Read are files and directories it may read although they are under an excluded branch: the CA
	// bundles its environment names.
	Read []string
	// Root is the filesystem root to grant beneath, "" for "/". A test points it at a tree of its own.
	Root string
}

// CapturePolicy builds a host capture's LandlockPolicy:
//
//   - read and write beneath Staging, beneath /tmp, on /dev's stand-in devices (/dev/null, /dev/zero,
//     /dev/full, /dev/random, /dev/urandom, /dev/tty) and beneath /dev/shm;
//   - read and execute beneath every directory of the root except the branches holding Home and
//     Exclude: the root's entries are listed, an ancestor of an excluded path is descended into and
//     the same done there, and a symbolic link is left out — what it names is covered, or not, where
//     it really is, since a rule follows the link it is opened through, and /home → /var/home would
//     otherwise grant the home;
//   - read and execute Exec, read Read.
//
// /tmp is listed the same way, so a home under /tmp (a test's) stays excluded from it too.
func CapturePolicy(o CapturePolicyOptions) (LandlockPolicy, error) {
	root := o.Root
	if root == "" {
		root = "/"
	}
	if o.Home == "" || !filepath.IsAbs(o.Home) || resolved(o.Home) == resolved(root) {
		return LandlockPolicy{}, fmt.Errorf("the user's home %q is unknown or is the filesystem root, so there "+
			"is no branch to keep the installer out of", o.Home)
	}
	if o.Staging == "" || !filepath.IsAbs(o.Staging) {
		return LandlockPolicy{}, fmt.Errorf("the staging directory %q is not an absolute path", o.Staging)
	}
	exclude := []string{resolved(o.Home)}
	for _, x := range o.Exclude {
		if filepath.IsAbs(x) && resolved(x) != resolved(root) {
			exclude = append(exclude, resolved(x))
		}
	}
	tmp, dev := resolved(filepath.Join(root, "tmp")), filepath.Join(root, "dev")
	p := LandlockPolicy{ReadWrite: []string{resolved(o.Staging)}}
	p.ReadWrite = append(p.ReadWrite, coverExcept(tmp, exclude)...)
	for _, d := range []string{"null", "zero", "full", "random", "urandom", "tty", "shm"} {
		p.ReadWrite = append(p.ReadWrite, filepath.Join(dev, d))
	}
	p.ReadExec = coverExcept(resolved(root), append(append([]string{}, exclude...), tmp))
	for _, f := range o.Exec {
		p.ReadExec = append(p.ReadExec, resolved(f))
	}
	for _, f := range o.Read {
		p.Read = append(p.Read, resolved(f))
	}
	return p, nil
}

// coverExcept lists paths beneath dir whose grants together cover everything under dir except the
// excluded branches, as CapturePolicy describes. A directory that cannot be listed while descending is
// left out whole: its children are unknown, so none is granted.
func coverExcept(dir string, exclude []string) []string {
	for _, x := range exclude {
		if x == dir {
			return nil
		}
	}
	if !holdsExcluded(dir, exclude) {
		return []string{dir}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		child := filepath.Join(dir, e.Name())
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		out = append(out, coverExcept(child, exclude)...)
	}
	sort.Strings(out)
	return out
}

// holdsExcluded reports whether an excluded path is strictly beneath dir.
func holdsExcluded(dir string, exclude []string) bool {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, x := range exclude {
		if strings.HasPrefix(x, prefix) {
			return true
		}
	}
	return false
}

// resolved is p with its symbolic links resolved, or p cleaned when it does not resolve.
func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
