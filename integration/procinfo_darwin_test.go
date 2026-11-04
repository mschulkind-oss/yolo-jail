//go:build darwin

package integration

// procinfo_darwin_test.go is the darwin half of the harness's view of host processes
// (procinfo_test.go). A Mac has no /proc, so every answer comes from ps(1), which every macOS
// install ships at /bin/ps.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// processPIDs is every pid the kernel's process table (kern.proc.all) lists.
func processPIDs() ([]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(procs))
	for _, p := range procs {
		pids = append(pids, int(p.Proc.P_pid))
	}
	return pids, nil
}

// processParent is pid's parent, from kern.proc.pid; 0 when unreadable.
func processParent(pid int) int {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0
	}
	return int(k.Eproc.Ppid)
}

// processStartTime is pid's start time from kern.proc.pid, to the microsecond, which
// distinguishes a live process from a different process that later reused its pid.
func processStartTime(pid int) (string, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("sysctl kern.proc.pid %d: %w", pid, err)
	}
	return fmt.Sprintf("%d.%06d", k.Proc.P_starttime.Sec, k.Proc.P_starttime.Usec), nil
}

// processEnviron is pid's environment as the kernel recorded it at exec (kern.procargs2, the
// source `ps -E` reads). The kernel answers it for this user's own processes only, which is all
// the harness asks about; any other is an error, so it is never taken for this test's.
func processEnviron(pid int) ([]string, error) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.procargs2 %d: %w", pid, err)
	}
	_, env, err := parseProcargs2(buf)
	return env, err
}

// parseProcargs2 reads the arguments and the environment out of a kern.procargs2 buffer: a native
// int32 argc, the executable path, NUL padding, argc NUL-terminated arguments, then
// NUL-terminated environment entries up to the first empty one.
func parseProcargs2(buf []byte) (args, env []string, err error) {
	if len(buf) < 4 {
		return nil, nil, fmt.Errorf("short kern.procargs2: %d bytes", len(buf))
	}
	argc := int(binary.NativeEndian.Uint32(buf[:4]))
	rest := buf[4:]
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, nil, errors.New("kern.procargs2: unterminated executable path")
	}
	rest = rest[i:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}
	for n := 0; n < argc; n++ {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			return nil, nil, fmt.Errorf("kern.procargs2: %d of %d arguments terminated", n, argc)
		}
		args = append(args, string(rest[:i]))
		rest = rest[i+1:]
	}
	for len(rest) > 0 {
		i := bytes.IndexByte(rest, 0)
		if i <= 0 {
			break
		}
		env = append(env, string(rest[:i]))
		rest = rest[i+1:]
	}
	return args, env, nil
}

// processState is pid's state as ps prints it ("S", "R+", "Z", …). found is false when ps
// matched no process, which it reports by exiting 1 with nothing printed; err is any other
// failure to ask.
func processState(pid int) (state string, found bool, err error) {
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	s := strings.TrimSpace(string(out))
	var exitErr *exec.ExitError
	if err != nil && errors.As(err, &exitErr) && s == "" {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if s == "" {
		return "", false, nil
	}
	return s, true, nil
}

func processStateWithFlags(pid int) processStateObservation {
	state, found, err := processState(pid)
	return processStateObservation{
		state: state, found: found, stateErr: err, flagsErr: errProcessStatFlagsUnavailable,
		startTimeErr: errProcessStatFlagsUnavailable,
	}
}

// processArgs is pid's argv, read exactly from kern.procargs2 (the buffer ps itself reads) for
// this user's processes. For any other, which the kernel does not answer, it is the command line
// as ps prints it, split at spaces: ps joins argv with spaces, so an argument that itself holds
// one comes back as two. The exact read matters to the harness's stop (preparationworkers_test.go),
// whose stand-ins carry `internal <verb>` inside a script argument as well as after it.
func processArgs(pid int) ([]string, error) {
	if buf, err := unix.SysctlRaw("kern.procargs2", pid); err == nil {
		if args, _, err := parseProcargs2(buf); err == nil && len(args) > 0 {
			return args, nil
		}
	}
	out, err := exec.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// processChildren is every pid whose parent is ppid, from one `ps -A` listing.
func processChildren(ppid int) []int {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	var kids []int
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[1] != strconv.Itoa(ppid) {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			kids = append(kids, pid)
		}
	}
	return kids
}

// processTable is every process one `ps -A` listing shows, with its parent and command name.
func processTable() []procRow {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,comm=").Output()
	if err != nil {
		return nil
	}
	var rows []procRow
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		rows = append(rows, procRow{pid: pid, ppid: ppid, comm: strings.Join(f[2:], " ")})
	}
	return rows
}

// processDetail is one line about pid for a hang report, as ps prints it: parent, state, elapsed
// time, wait channel and command line. darwin has no /proc to read open files from.
func processDetail(pid int) string {
	out, err := exec.Command("ps", "-ww", "-o", "pid=,ppid=,stat=,etime=,wchan=,command=",
		"-p", strconv.Itoa(pid)).Output()
	if s := strings.TrimSpace(string(out)); err == nil && s != "" {
		return "pid ppid stat elapsed wchan command: " + s
	}
	return "pid " + strconv.Itoa(pid) + ": gone before it could be read"
}

// parseProcargs2 skips the executable path and its padding, reads every argument, an empty one
// included, and stops at the first empty environment entry.
func TestParseProcargs2ReadsTheEnvironmentAfterTheArguments(t *testing.T) {
	buf := binary.NativeEndian.AppendUint32(nil, 3)
	buf = append(buf, "/bin/sh\x00\x00\x00\x00sh\x00\x00HOME=decoy\x00HOME=/h\x00PATH=/bin\x00\x00junk\x00"...)
	args, env, err := parseProcargs2(buf)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(args, ","), "sh,,HOME=decoy"; got != want {
		t.Errorf("arguments = %q, want %q", got, want)
	}
	if got, want := strings.Join(env, ","), "HOME=/h,PATH=/bin"; got != want {
		t.Errorf("environment = %q, want %q", got, want)
	}
	if _, _, err := parseProcargs2(buf[:2]); err == nil {
		t.Error("a buffer shorter than argc parsed")
	}
}

// envVisibleShellDir is a directory to put first on a stand-in's PATH so that its `sh` is one
// whose environment the harness can read. macOS 26 withholds a PLATFORM binary's environment
// from kern.procargs2, its owner's read included (the Mac run of 2026-11-03, §6: `/bin/sh` and
// `/bin/sleep` return their arguments and no environment, a nix-store `sleep` its full one), so a
// stand-in that is `/bin/sh` can never show the HOME the stop matches on. The first `sh` on PATH,
// or in a nix profile, that resolves outside the system's own directories is not a platform
// binary. With none, the test skips naming the install that gives it one.
func envVisibleShellDir(t *testing.T) string {
	t.Helper()
	dirs := filepath.SplitList(os.Getenv("PATH"))
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".nix-profile", "bin"))
	}
	if u := os.Getenv("USER"); u != "" {
		dirs = append(dirs, filepath.Join("/etc/profiles/per-user", u, "bin"))
	}
	dirs = append(dirs, "/run/current-system/sw/bin", "/nix/var/nix/profiles/default/bin")
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		real, err := filepath.EvalSymlinks(filepath.Join(dir, "sh"))
		if err != nil || platformBinaryPath(real) {
			continue
		}
		if info, err := os.Stat(real); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return dir
		}
	}
	t.Skip("no `sh` resolves outside the system's own directories, and kern.procargs2 hides a " +
		"platform binary's environment on this macOS, so no stand-in can show its HOME. Put a nix " +
		"one on PATH: nix profile install nixpkgs#bash")
	return ""
}

// platformBinaryPath reports a path under the directories macOS ships its own signed binaries in.
func platformBinaryPath(p string) bool {
	for _, root := range []string{"/bin/", "/sbin/", "/usr/", "/System/", "/Library/Apple/"} {
		if strings.HasPrefix(p, root) {
			return true
		}
	}
	return false
}

// processEnviron reads a live child's environment, and its start time and parent come from the
// same kernel table the listing reads. The child is a non-platform `sh` (envVisibleShellDir),
// since this macOS hides a platform binary's environment; it waits in the background so it stays
// the shell, and takes its sleep with it on SIGTERM.
func TestProcessEnvironReadsALiveChildsHome(t *testing.T) {
	home := t.TempDir()
	cmd := exec.Command(filepath.Join(envVisibleShellDir(t), "sh"), "-c", standInScript)
	cmd.Env = append(os.Environ(), "HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Signal(syscall.SIGTERM); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for !processRunsWithHome(cmd.Process.Pid, home) {
		if time.Now().After(deadline) {
			env, err := processEnviron(cmd.Process.Pid)
			t.Fatalf("the child never showed HOME=%s: environment %q, %v", home, env, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := processParent(cmd.Process.Pid); got != os.Getpid() {
		t.Errorf("parent = %d, want this test binary %d", got, os.Getpid())
	}
	if start, err := processStartTime(cmd.Process.Pid); err != nil || start == "" {
		t.Errorf("start time = %q, %v", start, err)
	}
	pids, err := processPIDs()
	if err != nil || !slices.Contains(pids, cmd.Process.Pid) {
		t.Errorf("the process table (%d pids, %v) does not list the child %d", len(pids), err, cmd.Process.Pid)
	}
}
