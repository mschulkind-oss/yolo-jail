//go:build linux

package prune

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// statLine builds a /proc/<pid>/stat line with the given comm, state, flags and starttime
// in their real field positions (3, 9 and 22).
func statLine(comm, state string, flags, start int) string {
	f := make([]string, 50)
	for i := range f {
		f[i] = "0"
	}
	f[0] = state
	f[6] = strconv.Itoa(flags)
	f[19] = strconv.Itoa(start)
	return "4242 (" + comm + ") " + strings.Join(f, " ")
}

func TestParseProcStat(t *testing.T) {
	const btime = 1_700_000_000
	p, ok := parseProcStat(statLine("a (weird) name", "S", 0, 12345), btime)
	if !ok || p.Name != "a (weird) name" {
		t.Fatalf("comm with parens and spaces: ok=%v %+v", ok, p)
	}
	if want := time.Unix(btime, 0).Add(123450 * time.Millisecond); !p.Start.Equal(want) {
		t.Errorf("start = %v, want %v (starttime is in USER_HZ ticks)", p.Start, want)
	}
	if p.NameTruncated {
		t.Error("a 14-char comm is not truncated")
	}
	if p, _ := parseProcStat(statLine("yolo-entrypoin", "S", 0, 1), btime); p.NameTruncated {
		t.Error("14 chars is below the cut")
	}
	if p, _ := parseProcStat(statLine("yolo-entrypoint", "S", 0, 1), btime); !p.NameTruncated {
		t.Error("a 15-char comm may have been cut")
	}
	if _, ok := parseProcStat(statLine("yolo", "Z", 0, 1), btime); ok {
		t.Error("a zombie was listed")
	}
	if _, ok := parseProcStat(statLine("kworker/0:1-eve", "I", linuxPFKthread, 1), btime); ok {
		t.Error("a kernel thread was listed")
	}
	if _, ok := parseProcStat("garbage", btime); ok {
		t.Error("garbage parsed")
	}
}

// The real table lists this test binary by its exe name, started in the past.
func TestReadProcStartsSeesThisProcess(t *testing.T) {
	procs, ok := readProcStarts()
	if !ok {
		t.Skip("/proc not readable here")
	}
	exe, _ := os.Executable()
	for _, p := range procs {
		if p.PID != os.Getpid() {
			continue
		}
		if p.Name != filepath.Base(exe) || !p.testCandidate() {
			t.Errorf("own entry %+v, want name %s and a test candidate", p, filepath.Base(exe))
		}
		if p.Start.After(time.Now().Add(2*time.Second)) || time.Since(p.Start) > time.Hour {
			t.Errorf("own start time %v is not plausible", p.Start)
		}
		return
	}
	t.Errorf("this process (pid %d) is not in the table of %d", os.Getpid(), len(procs))
}

func TestReadProcStartsUnreadableIsUnknown(t *testing.T) {
	if _, ok := readProcStartsFrom(filepath.Join(t.TempDir(), "noproc"), os.Geteuid()); ok {
		t.Error("a missing /proc must be \"could not ask\"")
	}
}
