//go:build linux

package prune

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// linuxUserHZ is the unit of /proc/<pid>/stat's starttime. It is USER_HZ, a fixed
// userspace ABI constant (100 on every architecture Linux exports to userspace), not
// the kernel's CONFIG_HZ — which is why it is a constant here rather than a sysconf.
const linuxUserHZ = 100

// linuxPFKthread is PF_KTHREAD from include/linux/sched.h: the flags bit marking a kernel
// thread, which has no exe and a comm that is often exactly TASK_COMM_LEN-1 long — so a
// root prune would otherwise read every kernel thread as a truncated, possibly-yolo name.
const linuxPFKthread = 0x00200000

// linuxCommMax is TASK_COMM_LEN-1: a comm this long may have been cut.
const linuxCommMax = 15

func init() { platformProcStarts = linuxProcStarts }

// linuxProcStarts lists this euid's live processes from /proc. false — "could not ask" —
// when /proc cannot be listed or the boot time cannot be read, since without either no
// start time can be computed and every legacy tree must be kept.
func linuxProcStarts() ([]ProcStart, bool) {
	return readProcStartsFrom("/proc", os.Geteuid())
}

func readProcStartsFrom(proc string, euid int) ([]ProcStart, bool) {
	btime, ok := readBootTime(filepath.Join(proc, "stat"))
	if !ok {
		return nil, false
	}
	entries, err := os.ReadDir(proc)
	if err != nil {
		return nil, false
	}
	var out []ProcStart
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(proc, e.Name())
		fi, err := os.Lstat(dir)
		if err != nil {
			continue // exited between the listing and here
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != euid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		p, ok := parseProcStat(string(raw), btime)
		if !ok {
			continue
		}
		p.PID = pid
		if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
			p.Name = filepath.Base(strings.TrimSuffix(exe, " (deleted)"))
			p.NameTruncated = false
		}
		out = append(out, p)
	}
	return out, true
}

// parseProcStat reads the fields prune needs out of one /proc/<pid>/stat line. The comm is
// parenthesized and may itself contain spaces or parentheses, so the fixed fields are
// counted from the LAST ')'. A zombie or a kernel thread is not a process that can hold
// a tree open, and returns false.
func parseProcStat(line string, btime int64) (ProcStart, bool) {
	open := strings.IndexByte(line, '(')
	close := strings.LastIndexByte(line, ')')
	if open < 0 || close < open {
		return ProcStart{}, false
	}
	comm := line[open+1 : close]
	fields := strings.Fields(line[close+1:])
	// fields[0] is field 3 (state); flags is field 9, starttime field 22.
	if len(fields) < 20 {
		return ProcStart{}, false
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return ProcStart{}, false
	}
	flags, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil || flags&linuxPFKthread != 0 {
		return ProcStart{}, false
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return ProcStart{}, false
	}
	start := time.Unix(btime, 0).Add(time.Duration(ticks) * time.Second / linuxUserHZ)
	return ProcStart{Name: comm, NameTruncated: len(comm) >= linuxCommMax, Start: start}, true
}

func readBootTime(stat string) (int64, bool) {
	f, err := os.Open(stat)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if rest, ok := strings.CutPrefix(sc.Text(), "btime "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
			return n, err == nil && n > 0
		}
	}
	return 0, false
}
