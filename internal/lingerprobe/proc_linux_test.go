//go:build linux

package lingerprobe

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// fakeProc builds a /proc tree under a temp root. Syscall numbers come from
// this build's own x/sys table, so the test reads the same arch the sampler does.
type fakeProc struct {
	t    *testing.T
	root string
}

func newFakeProc(t *testing.T) fakeProc {
	t.Helper()
	return fakeProc{t: t, root: t.TempDir()}
}

func (f fakeProc) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func statLine(pid, ppid int, comm, state string, start uint64) string {
	return fmt.Sprintf("%d (%s) %s %d%s %d 0 0\n", pid, comm, state, ppid,
		strings.Repeat(" 0", 17), start)
}

func (f fakeProc) proc(pid, ppid int, comm, state string, start uint64) {
	f.write(fmt.Sprintf("%d/stat", pid), statLine(pid, ppid, comm, state, start))
	f.write(fmt.Sprintf("%d/comm", pid), comm+"\n")
}

// thread writes task/<tid>/{stat,syscall,wchan}. args are the syscall's
// arguments; a negative nr writes a raw syscall line instead (see sc).
func (f fakeProc) thread(pid, tid int, state, syscallLine, wchan string) {
	base := fmt.Sprintf("%d/task/%d/", pid, tid)
	f.write(base+"stat", statLine(tid, pid, "t", state, 1))
	if syscallLine != "" {
		f.write(base+"syscall", syscallLine+"\n")
	}
	f.write(base+"wchan", wchan)
}

func sc(nr int, args ...uint64) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(nr))
	for i := 0; i < 6; i++ {
		var a uint64
		if i < len(args) {
			a = args[i]
		}
		fmt.Fprintf(&b, " 0x%x", a)
	}
	b.WriteString(" 0x7ffc0000 0x4000")
	return b.String()
}

func (f fakeProc) fd(pid, fd int, target string) {
	f.t.Helper()
	dir := filepath.Join(f.root, strconv.Itoa(pid), "fd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, strconv.Itoa(fd))); err != nil {
		f.t.Fatal(err)
	}
}

// mem places a 16-byte (sec, nsec) pair at addr in /proc/<pid>/mem — a sparse
// regular file standing in for the target's address space.
func (f fakeProc) mem(pid int, addr int64, sec, nsec int64) {
	f.t.Helper()
	p := filepath.Join(f.root, strconv.Itoa(pid), "mem")
	fh, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		f.t.Fatal(err)
	}
	defer fh.Close()
	var b [16]byte
	binary.LittleEndian.PutUint64(b[:8], uint64(sec))
	binary.LittleEndian.PutUint64(b[8:], uint64(nsec))
	if _, err := fh.WriteAt(b[:], addr); err != nil {
		f.t.Fatal(err)
	}
}

func (f fakeProc) sampler() Sampler {
	return Sampler{Root: f.root, Name: syscallName, Clock: func(int) (time.Duration, bool) {
		return 1000 * time.Second, true
	}}
}

// The rootless shape: a wrapper podman waiting on the real podman, whose
// threads are blocked in everything a lingering client could be blocked in.
func podmanTree(t *testing.T) fakeProc {
	f := newFakeProc(t)
	f.proc(100, 1, "podman", "S", 5000)
	f.thread(100, 100, "S", sc(unix.SYS_WAIT4, 101), "do_wait")
	f.write("100/task/100/children", "101 ")

	f.proc(101, 100, ".podman-wrapped", "S", 5001)
	f.fd(101, 0, "/dev/pts/5")
	f.fd(101, 4, "anon_inode:[eventpoll]")
	f.fd(101, 12, "/home/u/.local/share/containers/storage/storage.lock")
	f.fd(101, 7, "/home/u/.local/share/containers/storage/db.sql")
	f.thread(101, 101, "S", sc(unix.SYS_READ, 0, 0xc000100000, 32768), "wait_woken")
	f.thread(101, 102, "S", sc(unix.SYS_FUTEX, 0xc000080148, 128, 0, 0x1000), "futex_wait_queue")
	f.mem(101, 0x1000, 0, 250_000_000)
	f.thread(101, 103, "S", sc(unix.SYS_FUTEX, 0xc000080150, 128, 0, 0), "futex_wait_queue")
	f.thread(101, 104, "S", sc(unix.SYS_EPOLL_PWAIT, 4, 0xc0000, 128, 10), "do_epoll_wait")
	f.thread(101, 105, "S", sc(unix.SYS_FLOCK, 12, unix.LOCK_EX), "flock_lock_inode_wait")
	f.thread(101, 106, "S", sc(unix.SYS_FCNTL, 7, 7, 0xc0001), "")
	f.thread(101, 107, "R", "running", "0")
	f.thread(101, 108, "S", "", "0") // syscall file unreadable: rendered "?", never fatal
	f.write("101/task/101/children", "")
	return f
}

func TestSampleNamesEveryBlockedCallInAFakeTree(t *testing.T) {
	f := podmanTree(t)
	snap := f.sampler().Sample(100)
	if len(snap.Procs) != 2 || snap.Procs[0].PID != 100 || snap.Procs[1].PID != 101 {
		t.Fatalf("tree walk wrong: %+v", snap.Procs)
	}
	line := snap.Line()
	for _, want := range []string{
		"pid 100 podman: 1× S wait4 [do_wait]",
		"S read(0 → /dev/pts/5) [wait_woken]",
		"S futex(timeout=0.250s) [futex_wait_queue]",
		"S futex(timeout=none) [futex_wait_queue]",
		"S epoll_pwait(4 → anon_inode:[eventpoll], timeout=0.010s) [do_epoll_wait]",
		"S flock(12 → /home/u/.local/share/containers/storage/storage.lock) [flock_lock_inode_wait]",
		"S fcntl(7 → /home/u/.local/share/containers/storage/db.sql, F_SETLKW)",
		"R running",
		"S ?",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("sample line missing %q:\n%s", want, line)
		}
	}
}

func TestDominantPrefersALockOverTheEverPresentStdinRead(t *testing.T) {
	f := podmanTree(t)
	var ty tally
	ty.add(f.sampler().Sample(100))
	if got := ty.dominant(); got != "blocked in flock(12 → /home/u/.local/share/containers/storage/storage.lock)" {
		t.Errorf("dominant = %q", got)
	}
}

func TestDominantFallsBackToStdinThenToTheParkedRuntime(t *testing.T) {
	f := newFakeProc(t)
	f.proc(200, 1, "podman", "S", 1)
	f.fd(200, 0, "/dev/pts/9")
	f.thread(200, 200, "S", sc(unix.SYS_READ, 0, 0, 1), "wait_woken")
	f.thread(200, 201, "S", sc(unix.SYS_FUTEX, 0, 128, 0, 0x2000), "")
	f.mem(200, 0x2000, 1, 0)
	var ty tally
	ty.add(f.sampler().Sample(200))
	if got := ty.dominant(); got != "blocked in read(0 → /dev/pts/9)" {
		t.Errorf("dominant = %q", got)
	}

	g := newFakeProc(t)
	g.proc(300, 1, "podman", "S", 1)
	g.fd(300, 4, "anon_inode:[eventpoll]")
	g.thread(300, 300, "S", sc(unix.SYS_EPOLL_PWAIT, 4, 0, 128, 100), "")
	g.thread(300, 301, "S", sc(unix.SYS_FUTEX, 0, 128, 0, 0), "")
	var ty2 tally
	ty2.add(g.sampler().Sample(300))
	ty2.add(g.sampler().Sample(300))
	got := ty2.dominant()
	if !strings.HasPrefix(got, "with every thread parked in the Go runtime") ||
		!strings.Contains(got, "epoll_pwait timeout=0.100s ×2") {
		t.Errorf("parked dominant = %q, want the parked phrase with the timed wait counted", got)
	}
}

func TestTimeoutsAbsoluteAndPointerForms(t *testing.T) {
	f := newFakeProc(t)
	f.proc(400, 1, "podman", "S", 1)
	// FUTEX_WAIT_BITSET|PRIVATE, CLOCK_MONOTONIC deadline 1000.5s; clock reads 1000s.
	f.thread(400, 401, "S", sc(unix.SYS_FUTEX, 0, 9|128, 0, 0x3000), "")
	f.mem(400, 0x3000, 1000, 500_000_000)
	// clock_nanosleep relative 2s (C-level sleep, e.g. sqlite's busy handler).
	f.thread(400, 402, "S", sc(unix.SYS_CLOCK_NANOSLEEP, 1, 0, 0x4000, 0), "")
	f.mem(400, 0x4000, 2, 0)
	// ppoll with a NULL timeout waits forever.
	f.thread(400, 403, "S", sc(unix.SYS_PPOLL, 0, 1, 0), "")
	// A pointer that is not readable renders "?", not an error.
	f.thread(400, 404, "S", sc(unix.SYS_NANOSLEEP, 0x999999999), "")
	line := f.sampler().Sample(400).Line()
	for _, want := range []string{
		"futex(deadline=+0.500s)", "clock_nanosleep(timeout=2.000s)",
		"ppoll(timeout=none)", "nanosleep(timeout=?)",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %s", want, line)
		}
	}
}

func TestAliveRejectsReusedPidsAndZombies(t *testing.T) {
	f := newFakeProc(t)
	f.proc(500, 1, "podman", "S", 777)
	s := f.sampler()
	if !s.Alive(500, 777) {
		t.Error("the recorded process reads as dead")
	}
	if s.Alive(500, 778) {
		t.Error("a pid with a different start time is a REUSED pid, not the client")
	}
	f.proc(500, 1, "podman", "Z", 777)
	if s.Alive(500, 777) {
		t.Error("a zombie has nothing left to be blocked in")
	}
	if s.Alive(501, 777) {
		t.Error("a missing pid reads as alive")
	}
}

func TestChildrenFallBackToAPpidScan(t *testing.T) {
	f := newFakeProc(t)
	f.proc(600, 1, "podman", "S", 1)
	f.thread(600, 600, "S", sc(unix.SYS_WAITID, 1, 601), "")
	f.proc(601, 600, "netavark", "S", 2)
	f.thread(601, 601, "S", sc(unix.SYS_READ, 3), "")
	f.fd(601, 3, "socket:[1234]")
	// No task/*/children file anywhere: a kernel without CONFIG_PROC_CHILDREN.
	snap := f.sampler().Sample(600)
	if len(snap.Procs) != 2 || snap.Procs[1].Comm != "netavark" {
		t.Fatalf("ppid scan did not find the child: %+v", snap.Procs)
	}
	var ty tally
	ty.add(snap)
	if got := ty.dominant(); got != "blocked in read(3 → socket:[1234])" {
		t.Errorf("dominant = %q", got)
	}
	// A wait on a NON-podman child with nothing better to say names the child.
	g := newFakeProc(t)
	g.proc(700, 1, "podman", "S", 1)
	g.thread(700, 700, "S", sc(unix.SYS_WAITID, 1, 701), "")
	g.write("700/task/700/children", "701")
	g.proc(701, 700, "netavark", "S", 2)
	g.thread(701, 701, "S", sc(unix.SYS_FUTEX, 0, 128, 0, 0), "")
	var ty2 tally
	ty2.add(g.sampler().Sample(700))
	if got := ty2.dominant(); got != "waiting in waitid on child netavark (pid 701)" {
		t.Errorf("dominant = %q", got)
	}
}

func TestHostContextCountsFromProcAlone(t *testing.T) {
	f := newFakeProc(t)
	f.write("loadavg", "3.10 2.00 1.50 4/900 12345\n")
	cmd := func(args ...string) string { return strings.Join(args, "\x00") + "\x00" }
	// this launch's client (rootless: wrapper + child), the first as nix names
	// it — measured: a nix podman's comm is ".podman-wrapped"
	f.proc(10, 1, ".podman-wrapped", "S", 1)
	f.write("10/cmdline", cmd("podman", "run", "--name", "yolo-ws-me"))
	f.proc(11, 10, "podman", "S", 1)
	f.write("11/cmdline", cmd("podman", "run", "--name", "yolo-ws-me"))
	// a second terminal attached to THIS jail: one client, two processes
	f.proc(20, 1, "podman", "S", 1)
	f.write("20/cmdline", cmd("podman", "exec", "-it", "yolo-ws-me", "bash"))
	f.proc(21, 20, "podman", "S", 1)
	f.write("21/cmdline", cmd("podman", "exec", "-it", "yolo-ws-me", "bash"))
	// an exec into ANOTHER jail does not count as this jail's
	f.proc(30, 1, "podman", "S", 1)
	f.write("30/cmdline", cmd("podman", "exec", "yolo-ws-other", "true"))
	f.proc(40, 1, ".conmon-wrapped", "S", 1)
	f.write("40/cmdline", cmd("conmon", "--api-version", "1", "-c", "abc", "-n", "yolo-ws-me"))
	f.proc(41, 1, "conmon", "S", 1)
	f.write("41/cmdline", cmd("conmon", "-c", "def", "-n", "yolo-ws-other"))
	f.proc(42, 1, "conmon", "S", 1)
	f.write("42/cmdline", cmd("conmon", "-c", "ghi", "--name=unrelated"))

	h := f.sampler().ReadHostContext(os.Getuid(), "yolo-ws-me")
	want := HostContext{Podman: 5, Conmon: 3, ExecClients: 1, OtherYoloConmons: 1,
		Loadavg: "3.10 2.00 1.50 4/900 12345"}
	if h != want {
		t.Errorf("host context = %+v, want %+v", h, want)
	}
	if other := f.sampler().ReadHostContext(os.Getuid()+1, "yolo-ws-me"); other.Podman != 0 {
		t.Errorf("another uid's processes were counted: %+v", other)
	}
	if !strings.Contains(h.Line(), `exec_clients_this_jail=1`) {
		t.Errorf("line = %s", h.Line())
	}
}

// The 2026-09-25 linger showed one podman thread running in every sample and one
// blocked opening a file on disk, which is a client doing work rather than waiting on
// a lock — and a thread list cannot say what work. The process's CPU time, its disk
// reads and the regular files it holds open can: a client scanning the journal for an
// exit code holds journal files open while both counters climb.
func TestSampleRecordsCPUDiskReadsAndOpenFiles(t *testing.T) {
	f := newFakeProc(t)
	// utime 120 + stime 30 ticks = 1.500 s at USER_HZ 100.
	f.write("200/stat", "200 (podman) S 1 0 0 0 0 0 0 0 0 0 120 30 0 0 0 0 0 0 7000 0 0\n")
	f.write("200/comm", "podman\n")
	f.write("200/io", "rchar: 999999999\nwchar: 1\nsyscr: 5\nsyscw: 1\nread_bytes: 2097152\nwrite_bytes: 0\ncancelled_write_bytes: 0\n")
	f.thread(200, 200, "R", "running", "0")
	f.fd(200, 0, "/dev/pts/5")
	f.fd(200, 3, "socket:[123]")
	f.fd(200, 5, "/var/log/journal/abc/user-1000.journal")
	f.fd(200, 6, "/var/log/journal/abc/system.journal")
	f.fd(200, 7, "/home/u/.local/share/containers/storage/db.sql")
	f.fd(200, 8, "/var/log/journal/abc/system.journal") // a second fd on one file counts once

	line := f.sampler().Sample(200).Line()
	for _, want := range []string{
		"cpu 1.500s",
		"disk read 2.0MB",
		"open: /home/u/.local/share/containers/storage/db.sql, /var/log/journal/abc/system.journal, /var/log/journal/abc/user-1000.journal",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("sample line missing %q:\n%s", want, line)
		}
	}
	for _, not := range []string{"/dev/pts/5, ", "socket:[123]"} {
		if strings.Contains(line, "open: ") && strings.Contains(line[strings.Index(line, "open: "):], not) {
			t.Errorf("open files list %q, which is not a regular file:\n%s", not, line)
		}
	}
}

// Many open files are cut to a count, so one sample cannot flood the log.
func TestSampleCapsTheOpenFileList(t *testing.T) {
	f := newFakeProc(t)
	f.proc(300, 1, "podman", "S", 7000)
	f.thread(300, 300, "S", sc(unix.SYS_FUTEX, 0, 128, 0, 0), "futex_wait_queue")
	for i := 0; i < maxOpenFiles+3; i++ {
		f.fd(300, 10+i, fmt.Sprintf("/data/f%02d", i))
	}
	line := f.sampler().Sample(300).Line()
	if !strings.Contains(line, fmt.Sprintf("/data/f%02d (+3 more)", maxOpenFiles-1)) {
		t.Errorf("open-file list not capped at %d with a count:\n%s", maxOpenFiles, line)
	}
}
