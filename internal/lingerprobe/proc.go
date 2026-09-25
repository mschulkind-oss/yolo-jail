package lingerprobe

// proc.go reads what a process tree is blocked on, from a /proc tree whose root
// is injected (a fake tree in tests, "/proc" in production). Every read is
// best-effort: a file that cannot be read renders "?" (EACCES under a strict
// Yama ptrace_scope, a thread that exited mid-read), never an error, because a
// half-read sample is still the only evidence there is.
//
// Nothing here takes a lock the sampled process could need, and nothing here
// writes: /proc reads, readlink and a pread of /proc/<pid>/mem (for a timeout
// the kernel only exposes as a pointer). The one read that can wait on the
// target is the mem pread, which takes the target's mmap lock — which is why the
// probe runs on its own goroutine and Stop never waits for a sample in flight.

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Thread is one task's state as /proc reports it.
type Thread struct {
	TID     int
	State   string // stat field 3: R S D Z T t I …; "?" when unreadable
	Syscall string // bare syscall name, "" when not in one or unreadable
	FD      int    // the fd argument of an fd-first syscall, else -1
	// Call is the rendered blocked call without its timeout —
	// "read(0 → /dev/pts/5)", "futex", "running", "not in a syscall", "?".
	Call string
	// Timeout is the rendered timeout argument — "timeout=0.250s",
	// "timeout=none", "deadline=+0.250s", "timeout=?" — or "" for a syscall
	// that has none.
	Timeout string
	Wchan   string // kernel wait channel, "" when 0 or unreadable
}

// Rendered is the call and its timeout together, the form a sample line shows.
func (t Thread) Rendered() string {
	if t.Timeout == "" {
		return t.Call
	}
	if strings.HasSuffix(t.Call, ")") {
		return strings.TrimSuffix(t.Call, ")") + ", " + t.Timeout + ")"
	}
	return t.Call + "(" + t.Timeout + ")"
}

// Proc is one process of the sampled tree.
type Proc struct {
	PID      int
	PPID     int
	Comm     string
	Threads  []Thread
	Children []int
	// CPU is utime+stime, -1 when unreadable. With ReadBytes (the io file's
	// read_bytes, bytes fetched from storage; -1 when unreadable) it separates a
	// client doing work from one waiting: a thread list shows a thread running, not
	// how much it has run.
	CPU       time.Duration
	ReadBytes int64
	// Files is every regular file the process holds open, deduplicated and sorted —
	// the work's subject, when the client is working (the journal, the database).
	Files []string
}

// maxOpenFiles is how many open files a sample line names before it cuts the list
// to a count.
const maxOpenFiles = 8

// userHZ is the unit of stat's utime and stime. Fixed at 100 by the kernel's ABI
// on every architecture Linux podman runs on, whatever CONFIG_HZ is.
const userHZ = 100

// Snapshot is one sample: the root process first, then its live descendants.
type Snapshot struct {
	Procs []Proc
}

// Sampler reads a process tree out of a /proc root.
type Sampler struct {
	Root string
	// Name maps a syscall number to its name, "" for one it does not know.
	Name func(nr int) string
	// Clock reads a clock by clockid (0 REALTIME, 1 MONOTONIC) — needed only to
	// turn an ABSOLUTE timeout into time remaining. nil, or false, renders the
	// absolute deadline as "deadline=?".
	Clock func(id int) (time.Duration, bool)
	// MaxProcs and MaxThreads bound one sample's work.
	MaxProcs, MaxThreads int
}

func (s Sampler) path(parts ...string) string {
	return filepath.Join(append([]string{s.Root}, parts...)...)
}

// statFields parses a /proc stat line: the comm between the FIRST '(' and the
// LAST ')' (a comm may itself contain parens and spaces), and the fields after
// it, which start at field 3 (state).
func statFields(line string) (comm string, rest []string, ok bool) {
	open := strings.IndexByte(line, '(')
	closeParen := strings.LastIndexByte(line, ')')
	if open < 0 || closeParen < open {
		return "", nil, false
	}
	return line[open+1 : closeParen], strings.Fields(line[closeParen+1:]), true
}

// ProcStat is the stat facts the probe needs about a process.
type ProcStat struct {
	Comm      string
	State     string
	PPID      int
	StartTime uint64        // field 22, clock ticks since boot: pid-reuse detection
	CPU       time.Duration // fields 14+15, utime+stime; -1 when unparseable
}

// Stat reads /proc/<pid>/stat.
func (s Sampler) Stat(pid int) (ProcStat, bool) {
	b, err := os.ReadFile(s.path(strconv.Itoa(pid), "stat"))
	if err != nil {
		return ProcStat{}, false
	}
	comm, rest, ok := statFields(string(b))
	if !ok || len(rest) < 20 {
		return ProcStat{}, false
	}
	ppid, _ := strconv.Atoi(rest[1])
	start, _ := strconv.ParseUint(rest[19], 10, 64) // field 22 = rest[22-3]
	cpu := time.Duration(-1)
	utime, uerr := strconv.ParseUint(rest[11], 10, 64) // field 14
	stime, serr := strconv.ParseUint(rest[12], 10, 64) // field 15
	if uerr == nil && serr == nil {
		cpu = time.Duration(utime+stime) * time.Second / userHZ
	}
	return ProcStat{Comm: comm, State: rest[0], PPID: ppid, StartTime: start, CPU: cpu}, true
}

// readBytes is /proc/<pid>/io's read_bytes, or -1. The io file needs ptrace read
// access, the same access the syscall file needs, so a host that hides one hides both.
func (s Sampler) readBytes(pid int) int64 {
	b, err := os.ReadFile(s.path(strconv.Itoa(pid), "io"))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "read_bytes:"); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
				return n
			}
		}
	}
	return -1
}

// openFiles is the regular files among pid's open fds: absolute link targets outside
// /dev and /proc. Sockets, pipes and anon inodes are not paths and never match.
func (s Sampler) openFiles(pid int) []string {
	dir := s.path(strconv.Itoa(pid), "fd")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range ents {
		l, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil || !strings.HasPrefix(l, "/") ||
			strings.HasPrefix(l, "/dev/") || strings.HasPrefix(l, "/proc/") || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Alive reports whether pid is still the process whose start time was
// recorded, and not a zombie: a reaped pid can be reused, and a zombie has no
// threads left to be blocked in anything.
func (s Sampler) Alive(pid int, startTime uint64) bool {
	st, ok := s.Stat(pid)
	return ok && st.StartTime == startTime && st.State != "Z" && st.State != "X"
}

// Sample reads pid and its live descendants, breadth first, bounded.
func (s Sampler) Sample(pid int) Snapshot {
	maxProcs := s.MaxProcs
	if maxProcs <= 0 {
		maxProcs = 8
	}
	var snap Snapshot
	queue := []int{pid}
	seen := map[int]bool{}
	for len(queue) > 0 && len(snap.Procs) < maxProcs {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true
		proc, ok := s.readProc(p)
		if !ok {
			continue
		}
		snap.Procs = append(snap.Procs, proc)
		queue = append(queue, proc.Children...)
	}
	return snap
}

func (s Sampler) readProc(pid int) (Proc, bool) {
	st, ok := s.Stat(pid)
	if !ok {
		return Proc{}, false
	}
	proc := Proc{PID: pid, PPID: st.PPID, Comm: st.Comm, CPU: st.CPU, ReadBytes: s.readBytes(pid)}
	if b, err := os.ReadFile(s.path(strconv.Itoa(pid), "comm")); err == nil {
		proc.Comm = strings.TrimSpace(string(b))
	}
	proc.Files = s.openFiles(pid)
	tids := s.tids(pid)
	maxThreads := s.MaxThreads
	if maxThreads <= 0 {
		maxThreads = 64
	}
	if len(tids) > maxThreads {
		tids = tids[:maxThreads]
	}
	mem := s.openMem(pid)
	if mem != nil {
		defer mem.Close()
	}
	for _, tid := range tids {
		if t, ok := s.readThread(pid, tid, mem); ok {
			proc.Threads = append(proc.Threads, t)
		}
	}
	proc.Children = s.children(pid, tids)
	return proc, true
}

func (s Sampler) tids(pid int) []int {
	ents, err := os.ReadDir(s.path(strconv.Itoa(pid), "task"))
	if err != nil {
		return []int{pid}
	}
	var out []int
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, n)
		}
	}
	sort.Ints(out)
	return out
}

// children is the union of every thread's task/<tid>/children (the file is per
// THREAD: a child forked by a non-main thread is listed only under that
// thread). A kernel without CONFIG_PROC_CHILDREN has none of these files, so
// that case falls back to a ppid scan of the whole tree — slower, still bounded
// by the number of processes.
func (s Sampler) children(pid int, tids []int) []int {
	set := map[int]bool{}
	anyFile := false
	for _, tid := range tids {
		b, err := os.ReadFile(s.path(strconv.Itoa(pid), "task", strconv.Itoa(tid), "children"))
		if err != nil {
			continue
		}
		anyFile = true
		for _, f := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(f); err == nil {
				set[n] = true
			}
		}
	}
	if !anyFile {
		ents, err := os.ReadDir(s.Root)
		if err == nil {
			for _, e := range ents {
				n, err := strconv.Atoi(e.Name())
				if err != nil || n == pid {
					continue
				}
				if st, ok := s.Stat(n); ok && st.PPID == pid {
					set[n] = true
				}
			}
		}
	}
	out := make([]int, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

func (s Sampler) openMem(pid int) *os.File {
	f, err := os.Open(s.path(strconv.Itoa(pid), "mem"))
	if err != nil {
		return nil
	}
	return f
}

func (s Sampler) readThread(pid, tid int, mem *os.File) (Thread, bool) {
	base := s.path(strconv.Itoa(pid), "task", strconv.Itoa(tid))
	t := Thread{TID: tid, State: "?", FD: -1, Call: "?"}
	b, err := os.ReadFile(filepath.Join(base, "stat"))
	if err != nil {
		if os.IsNotExist(err) {
			return Thread{}, false // exited between the listing and the read
		}
	} else if _, rest, ok := statFields(string(b)); ok && len(rest) > 0 {
		t.State = rest[0]
	}
	if w, err := os.ReadFile(filepath.Join(base, "wchan")); err == nil {
		if v := strings.TrimSpace(string(w)); v != "0" {
			t.Wchan = v
		}
	}
	sc, err := os.ReadFile(filepath.Join(base, "syscall"))
	if err != nil {
		return t, true // EACCES (Yama ptrace_scope ≥ 2, another uid): "?"
	}
	s.decodeSyscall(&t, strings.TrimSpace(string(sc)), pid, mem)
	return t, true
}

// decodeSyscall renders /proc/<pid>/task/<tid>/syscall, whose three shapes are
// "running", "-1 <sp> <pc>" (blocked, but not in a syscall — a page fault) and
// "<nr> <arg1> … <arg6> <sp> <pc>" with the args in hex.
func (s Sampler) decodeSyscall(t *Thread, line string, pid int, mem *os.File) {
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return
	case fields[0] == "running":
		t.Call = "running"
		return
	case fields[0] == "-1":
		t.Call = "not in a syscall"
		return
	}
	nr, err := strconv.Atoi(fields[0])
	if err != nil {
		return
	}
	var args [6]uint64
	for i := 0; i < 6 && i+1 < len(fields); i++ {
		args[i], _ = strconv.ParseUint(strings.TrimPrefix(fields[i+1], "0x"), 16, 64)
	}
	name := ""
	if s.Name != nil {
		name = s.Name(nr)
	}
	if name == "" {
		t.Call = fmt.Sprintf("syscall_%d", nr)
		return
	}
	t.Syscall = name
	t.Call = name
	if fdFirst[name] {
		t.FD = int(int32(uint32(args[0])))
		target := "?"
		if l, err := os.Readlink(s.path(strconv.Itoa(pid), "fd", strconv.Itoa(t.FD))); err == nil {
			target = l
		}
		inner := fmt.Sprintf("%d → %s", t.FD, target)
		if name == "fcntl" {
			if c, ok := fcntlCmds[args[1]]; ok {
				inner += ", " + c
			}
		}
		t.Call = name + "(" + inner + ")"
	}
	t.Timeout = s.timeout(name, args, mem)
}

// fdFirst is every syscall worth naming whose FIRST argument is a file
// descriptor, so the sample can say what it is blocked ON.
var fdFirst = map[string]bool{
	"read": true, "write": true, "readv": true, "writev": true, "pread64": true, "pwrite64": true,
	"flock": true, "fcntl": true, "ioctl": true, "fsync": true, "fdatasync": true, "close": true,
	"connect": true, "accept": true, "accept4": true, "recvfrom": true, "recvmsg": true,
	"sendto": true, "sendmsg": true, "getdents64": true,
	"splice": true, "tee": true, "vmsplice": true, "copy_file_range": true, "sendfile": true,
	"epoll_wait": true, "epoll_pwait": true, "epoll_pwait2": true,
}

// fcntlCmds names the lock commands — the ones that say "waiting for a record
// lock" (containers/storage's lockfile takes F_SETLKW). The values are the
// generic Linux ones, identical on amd64 and arm64.
var fcntlCmds = map[uint64]string{6: "F_SETLK", 7: "F_SETLKW", 37: "F_OFD_SETLK", 38: "F_OFD_SETLKW"}

// timeout renders a blocked syscall's timeout argument — the fact that tells a
// poll-with-backoff loop (short, repeating, often growing) from one long wait.
// Integer-millisecond forms read straight from the args; pointer forms are a
// timespec/timeval in the TARGET's memory, read through /proc/<pid>/mem.
func (s Sampler) timeout(name string, a [6]uint64, mem *os.File) string {
	switch name {
	case "epoll_wait", "epoll_pwait":
		return msTimeout(a[3])
	case "poll":
		return msTimeout(a[2])
	case "epoll_pwait2":
		return s.relTimespec(a[3], mem)
	case "ppoll":
		return s.relTimespec(a[2], mem)
	case "pselect6":
		return s.relTimespec(a[4], mem)
	case "select":
		if a[4] == 0 {
			return "timeout=none"
		}
		sec, usec, ok := readPair(mem, a[4])
		if !ok {
			return "timeout=?"
		}
		return "timeout=" + fmtDur(time.Duration(sec)*time.Second+time.Duration(usec)*time.Microsecond)
	case "nanosleep":
		return s.relTimespec(a[0], mem)
	case "clock_nanosleep":
		if a[1]&1 != 0 { // TIMER_ABSTIME
			return s.absTimespec(int(a[0]), a[2], mem)
		}
		return s.relTimespec(a[2], mem)
	case "futex":
		const privateFlag, clockRealtime = 128, 256
		switch cmd := a[1] &^ (privateFlag | clockRealtime); cmd {
		case 0: // FUTEX_WAIT: relative timeout
			return s.relTimespec(a[3], mem)
		case 9: // FUTEX_WAIT_BITSET: absolute
			clock := 1 // CLOCK_MONOTONIC
			if a[1]&clockRealtime != 0 {
				clock = 0
			}
			return s.absTimespec(clock, a[3], mem)
		}
	}
	return ""
}

func msTimeout(v uint64) string {
	ms := int32(uint32(v))
	if ms < 0 {
		return "timeout=none"
	}
	return "timeout=" + fmtDur(time.Duration(ms)*time.Millisecond)
}

func (s Sampler) relTimespec(addr uint64, mem *os.File) string {
	if addr == 0 {
		return "timeout=none"
	}
	sec, nsec, ok := readPair(mem, addr)
	if !ok {
		return "timeout=?"
	}
	return "timeout=" + fmtDur(time.Duration(sec)*time.Second+time.Duration(nsec))
}

func (s Sampler) absTimespec(clock int, addr uint64, mem *os.File) string {
	if addr == 0 {
		return "timeout=none"
	}
	sec, nsec, ok := readPair(mem, addr)
	if !ok || s.Clock == nil {
		return "deadline=?"
	}
	now, ok := s.Clock(clock)
	if !ok {
		return "deadline=?"
	}
	return "deadline=" + fmt.Sprintf("%+.3fs", (time.Duration(sec)*time.Second+time.Duration(nsec)-now).Seconds())
}

// readPair reads two little-endian int64s at addr: a 64-bit timespec or
// timeval. Both supported arches (amd64, arm64) are 64-bit little-endian.
func readPair(mem *os.File, addr uint64) (int64, int64, bool) {
	if mem == nil || addr > 1<<62 {
		return 0, 0, false
	}
	var b [16]byte
	if n, err := mem.ReadAt(b[:], int64(addr)); n != len(b) || (err != nil && n != len(b)) {
		return 0, 0, false
	}
	return int64(binary.LittleEndian.Uint64(b[:8])), int64(binary.LittleEndian.Uint64(b[8:])), true
}

func fmtDur(d time.Duration) string { return fmt.Sprintf("%.3fs", d.Seconds()) }

// rank orders a thread's state by how much it can explain a lingering client.
// Higher explains more.
//
//	5  uninterruptible sleep (D) — the kernel is holding it
//	4  any other named blocked syscall: a lock (flock, fcntl F_SETLKW), a
//	   socket, a C-level sleep (clock_nanosleep — sqlite's busy handler)
//	3  read (or splice) on fd 0 — ranked BELOW the rest because an attached `podman run -it`
//	   has a thread in read(stdin) for its whole life, so its presence alone
//	   proves nothing; the keystroke timing is what convicts or clears it
//	2  running on CPU
//	1  waiting on a child that is not podman itself
//	0  the Go runtime parked (futex, epoll, nanosleep) and the rootless
//	   wrapper's wait on its own podman child: structural, present always
//	-1 unreadable
func rank(p Proc, t Thread, childComms map[int]string) int {
	switch {
	case t.Call == "?" && t.State == "?":
		return -1
	case t.State == "D":
		return 5
	case idle[t.Syscall]:
		return 0
	case t.Syscall == "wait4" || t.Syscall == "waitid":
		for _, c := range p.Children {
			if baseComm(childComms[c]) == "podman" {
				return 0
			}
		}
		return 1
	case t.Call == "running" || t.State == "R":
		return 2
	case (t.Syscall == "read" || t.Syscall == "splice") && t.FD == 0:
		return 3
	case t.Call == "?":
		return -1
	}
	return 4
}

// idle is the Go runtime's own parking: every Go program shows these all the
// time, so they explain nothing on their own — but their TIMEOUTS do, which is
// why a sample keeps them.
var idle = map[string]bool{
	"futex": true, "epoll_wait": true, "epoll_pwait": true, "epoll_pwait2": true, "nanosleep": true,
}

// Line renders a snapshot as one log line: per process, its threads grouped by
// identical state, sorted by count.
func (snap Snapshot) Line() string {
	var parts []string
	for _, p := range snap.Procs {
		counts := map[string]int{}
		for _, t := range p.Threads {
			sig := t.State + " " + t.Rendered()
			if t.Wchan != "" {
				sig += " [" + t.Wchan + "]"
			}
			counts[sig]++
		}
		keys := make([]string, 0, len(counts))
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if counts[keys[i]] != counts[keys[j]] {
				return counts[keys[i]] > counts[keys[j]]
			}
			return keys[i] < keys[j]
		})
		groups := make([]string, len(keys))
		for i, k := range keys {
			groups[i] = fmt.Sprintf("%d× %s", counts[k], k)
		}
		part := fmt.Sprintf("pid %d %s: %s", p.PID, p.Comm, strings.Join(groups, ", "))
		if extra := p.workLine(); extra != "" {
			part += " {" + extra + "}"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "no process readable"
	}
	return strings.Join(parts, "; ")
}

// workLine renders what the process has done and holds — "cpu 1.50s, disk read
// 2.0MB, open: a, b" — leaving out any part that was unreadable.
func (p Proc) workLine() string {
	var parts []string
	if p.CPU >= 0 {
		parts = append(parts, "cpu "+fmtDur(p.CPU))
	}
	if p.ReadBytes >= 0 {
		parts = append(parts, fmt.Sprintf("disk read %.1fMB", float64(p.ReadBytes)/(1<<20)))
	}
	if n := len(p.Files); n > 0 {
		shown := p.Files
		more := ""
		if n > maxOpenFiles {
			shown, more = p.Files[:maxOpenFiles], fmt.Sprintf(" (+%d more)", n-maxOpenFiles)
		}
		parts = append(parts, "open: "+strings.Join(shown, ", ")+more)
	}
	return strings.Join(parts, ", ")
}
