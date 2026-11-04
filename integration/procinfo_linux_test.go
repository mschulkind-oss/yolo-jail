//go:build linux

package integration

// procinfo_linux_test.go is the Linux half of the harness's view of host processes
// (procinfo_test.go): every answer is read from /proc.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// processState is pid's state letter ("S", "R", "Z", …) from /proc/<pid>/stat. found is false
// when /proc has no such process; err is any other failure to read it.
func processState(pid int) (state string, found bool, err error) {
	observation := readProcessStateWithFlags(fmt.Sprintf("/proc/%d/stat", pid))
	return observation.state, observation.found, observation.stateErr
}

// processStateWithFlags reads state, Linux stat field 9 flags, and field 22 starttime from one /proc snapshot.
func processStateWithFlags(pid int) processStateObservation {
	return readProcessStateWithFlags(fmt.Sprintf("/proc/%d/stat", pid))
}

func readProcessStateWithFlags(path string) processStateObservation {
	stat, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return processStateObservation{flagsErr: err, startTimeErr: err}
	}
	if err != nil {
		return processStateObservation{stateErr: err, flagsErr: err, startTimeErr: err}
	}
	// State is the first field after the parenthesised command name, which may contain
	// spaces or parentheses — hence the LAST ')'. Flags are field 9 and starttime field 22.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		parseErr := fmt.Errorf("unparsable %s: %q", path, s)
		return processStateObservation{stateErr: parseErr, flagsErr: parseErr, startTimeErr: parseErr}
	}
	observation := processStateObservation{state: s[i+2 : i+3], found: true}
	fields := strings.Fields(s[i+1:])
	if len(fields) <= 6 {
		observation.flagsErr = fmt.Errorf("short %s: missing stat field 9 flags", path)
	} else {
		flags, err := strconv.ParseUint(fields[6], 10, 64)
		if err != nil {
			observation.flagsErr = fmt.Errorf("unparsable %s stat field 9 flags: %w", path, err)
		} else {
			observation.flags, observation.flagsFound = flags, true
		}
	}
	if len(fields) <= 19 {
		observation.startTimeErr = fmt.Errorf("short %s: missing stat field 22 starttime", path)
	} else {
		if _, err := strconv.ParseUint(fields[19], 10, 64); err != nil {
			observation.startTimeErr = fmt.Errorf("unparsable %s stat field 22 starttime: %w", path, err)
		} else {
			observation.startTime, observation.startTimeFound = fields[19], true
		}
	}
	return observation
}

func TestReadProcessStateWithFlagsReadsStarttimeFromSameStat(t *testing.T) {
	fields := []string{"1", "2", "3", "4", "5", "4", "7", "8", "9", "10", "11", "12", "13", "14", "15", "16", "17", "18", "876543"}
	path := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(path, []byte("123 (worker ) tricky) R "+strings.Join(fields, " ")), 0o600); err != nil {
		t.Fatal(err)
	}
	observation := readProcessStateWithFlags(path)
	if observation.state != "R" || !observation.found || observation.stateErr != nil ||
		observation.flags != 4 || !observation.flagsFound || observation.flagsErr != nil ||
		observation.startTime != "876543" || !observation.startTimeFound || observation.startTimeErr != nil {
		t.Fatalf("same-stat observation = %+v; want state, flags, and field-22 starttime", observation)
	}

	fields[18] = "not-a-starttime"
	if err := os.WriteFile(path, []byte("124 (worker) R "+strings.Join(fields, " ")), 0o600); err != nil {
		t.Fatal(err)
	}
	observation = readProcessStateWithFlags(path)
	if observation.state != "R" || !observation.found || observation.stateErr != nil ||
		observation.flags != 4 || !observation.flagsFound || observation.flagsErr != nil ||
		observation.startTimeFound || observation.startTimeErr == nil {
		t.Fatalf("malformed supplemental starttime changed state/flags: %+v", observation)
	}
}

func TestReadProcessStateWithFlagsFromProcFixture(t *testing.T) {
	cases := []struct {
		name         string
		stat         string
		state        string
		flags        uint64
		flagsFound   bool
		wantStateErr bool
		wantFlagsErr bool
	}{
		{name: "PF_EXITING value", stat: "123 (worker ) tricky) R 1 2 3 4 5 4 7 8 9 10", state: "R", flags: 4, flagsFound: true},
		{name: "zero flags", stat: "124 (worker) S 1 2 3 4 5 0 7 8 9 10", state: "S", flagsFound: true},
		{name: "invalid flags retain parsed state", stat: "125 (worker) R 1 2 3 4 5 nope 7 8 9 10", state: "R", wantFlagsErr: true},
		{name: "short flags retain parsed state", stat: "126 (worker) R 1 2 3 4 5", state: "R", wantFlagsErr: true},
		{name: "malformed stat", stat: "not a stat", wantStateErr: true, wantFlagsErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "stat")
			if err := os.WriteFile(path, []byte(tc.stat), 0o600); err != nil {
				t.Fatal(err)
			}
			observation := readProcessStateWithFlags(path)
			if got, want := observation.state, tc.state; got != want {
				t.Errorf("state = %q, want %q", got, want)
			}
			if got, want := observation.found, !tc.wantStateErr; got != want {
				t.Errorf("found = %v, want %v", got, want)
			}
			if got := observation.stateErr != nil; got != tc.wantStateErr {
				t.Errorf("stateErr present = %v, want %v (%v)", got, tc.wantStateErr, observation.stateErr)
			}
			if got := observation.flagsFound; got != tc.flagsFound {
				t.Errorf("flagsFound = %v, want %v", got, tc.flagsFound)
			}
			if got := observation.flags; observation.flagsFound && got != tc.flags {
				t.Errorf("flags = %d, want %d", got, tc.flags)
			}
			if got := observation.flagsErr != nil; got != tc.wantFlagsErr {
				t.Errorf("flagsErr present = %v, want %v (%v)", got, tc.wantFlagsErr, observation.flagsErr)
			}
		})
	}

	missing := readProcessStateWithFlags(filepath.Join(t.TempDir(), "missing-stat"))
	if missing.found || missing.stateErr != nil || missing.flagsFound || !errors.Is(missing.flagsErr, fs.ErrNotExist) {
		t.Errorf("missing stat observation = %+v, want not-found state and unavailable flags", missing)
	}
	readError := readProcessStateWithFlags(t.TempDir())
	if readError.found || readError.stateErr == nil || readError.flagsFound || readError.flagsErr == nil {
		t.Errorf("read-error observation = %+v, want independent state and flags read errors", readError)
	}
}

// processArgs is pid's argv, from /proc/<pid>/cmdline, whose arguments are NUL-separated.
func processArgs(pid int) ([]string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(raw), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}

// processChildren is every pid whose parent is ppid, from the fourth field of each
// /proc/<pid>/stat.
func processChildren(ppid int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var kids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		s := string(stat)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		fields := strings.Fields(s[i+1:])
		if len(fields) >= 2 && fields[1] == strconv.Itoa(ppid) {
			kids = append(kids, pid)
		}
	}
	return kids
}

// processTable is every process /proc lists, with its parent and command name.
func processTable() []procRow {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var rows []procRow
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		comm, fields, ok := readStat(pid)
		if !ok || len(fields) < 2 {
			continue
		}
		ppid, _ := strconv.Atoi(fields[1])
		rows = append(rows, procRow{pid: pid, ppid: ppid, comm: comm})
	}
	return rows
}

// readStat is pid's command name and the fields of /proc/<pid>/stat after it (state first).
func readStat(pid int) (comm string, fields []string, ok bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", nil, false
	}
	s := string(raw)
	open, end := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || end < open {
		return "", nil, false
	}
	return s[open+1 : end], strings.Fields(s[end+1:]), true
}

// processDetail is one line about pid for a hang report: its parent, name, state, age, the kernel
// function each of its threads sleeps in, the files it holds open, and its command line. Every
// part a reader cannot get (another user's descriptors, a process already gone) says so instead.
func processDetail(pid int) string {
	comm, f, ok := readStat(pid)
	if !ok || len(f) < 2 {
		return fmt.Sprintf("pid %d: gone before it could be read", pid)
	}
	age := "?"
	// Field 22 of stat, starttime, in clock ticks since boot; /proc reports USER_HZ, 100.
	if len(f) > 19 {
		if start, err := strconv.ParseFloat(f[19], 64); err == nil {
			if up, err := os.ReadFile("/proc/uptime"); err == nil {
				if secs, err := strconv.ParseFloat(strings.Fields(string(up))[0], 64); err == nil {
					age = fmt.Sprintf("%.0fs", secs-start/100)
				}
			}
		}
	}
	line := fmt.Sprintf("pid %d ppid %s %s state %s age %s wchan %s; %s", pid, f[1], comm, f[0], age,
		threadWchans(pid), openFiles(pid))
	args, _ := processArgs(pid)
	cmdline := strings.Join(args, " ")
	if len(cmdline) > 400 {
		cmdline = cmdline[:400] + "…"
	}
	return line + "\n    argv: " + cmdline
}

// threadWchans is the kernel function pid's main thread sleeps in, then those of its other
// threads, counted: "unix_stream_read_generic; threads: futex_wait_queue×7". A thread that is
// running, or one the kernel will not name to this reader, reads as "-".
func threadWchans(pid int) string {
	read := func(path string) string {
		raw, err := os.ReadFile(path)
		if w := strings.TrimSpace(string(raw)); err == nil && w != "" && w != "0" {
			return w
		}
		return "-"
	}
	main := read(fmt.Sprintf("/proc/%d/wchan", pid))
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil || len(tasks) <= 1 {
		return main
	}
	counts := map[string]int{}
	for _, task := range tasks {
		if task.Name() == strconv.Itoa(pid) {
			continue
		}
		counts[read(fmt.Sprintf("/proc/%d/task/%s/wchan", pid, task.Name()))]++
	}
	var parts []string
	for w, n := range counts {
		parts = append(parts, fmt.Sprintf("%s×%d", w, n))
	}
	sort.Strings(parts)
	return main + "; threads: " + strings.Join(parts, ", ")
}

// openFiles summarizes pid's open descriptors: how many are sockets and pipes, and the paths of
// the files, which name the lock or database a process blocked on holds.
func openFiles(pid int) string {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	fds, err := os.ReadDir(dir)
	if err != nil {
		return "open files unreadable"
	}
	sockets, pipes := 0, 0
	seen := map[string]bool{}
	var files []string
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(dir, fd.Name()))
		switch {
		case err != nil:
		case strings.HasPrefix(target, "socket:"):
			sockets++
		case strings.HasPrefix(target, "pipe:"):
			pipes++
		case strings.HasPrefix(target, "anon_inode:"), target == "/dev/null",
			strings.HasPrefix(target, "/dev/pts/"), strings.HasPrefix(target, "/dev/tty"):
		case !seen[target]:
			seen[target] = true
			files = append(files, target)
		}
	}
	sort.Strings(files)
	if len(files) > 15 {
		files = append(files[:15], fmt.Sprintf("…%d more", len(files)-15))
	}
	return fmt.Sprintf("%d socket(s), %d pipe(s), files [%s]", sockets, pipes, strings.Join(files, " "))
}

// processPIDs is every pid /proc lists.
func processPIDs() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// processParent is pid's parent, from the fourth field of /proc/<pid>/stat; 0 when unreadable.
func processParent(pid int) int {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(s[i+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}

// processStartTime is the starttime field in /proc/<pid>/stat, which distinguishes a live process
// from a different process that later reused its pid.
func processStartTime(pid int) (string, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", err
	}
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return "", fmt.Errorf("malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(string(stat)[i+1:])
	if len(fields) <= 19 {
		return "", fmt.Errorf("short /proc/%d/stat", pid)
	}
	return fields[19], nil
}

// processEnviron is pid's environment, from /proc/<pid>/environ, whose entries are
// NUL-separated. Empty (nil, nil) when the kernel shows none, as it does for a moment after an
// exec and once an exiting process has released its memory.
func processEnviron(pid int) ([]string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil, fmt.Errorf("read /proc/%d/environ: %w", pid, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return strings.Split(string(raw), "\x00"), nil
}

// envVisibleShellDir is a directory to put first on a stand-in's PATH so that its `sh` is one
// whose environment the harness can read. Linux's /proc/<pid>/environ shows every process its
// owner starts, so nothing needs to move: "".
func envVisibleShellDir(t *testing.T) string {
	t.Helper()
	return ""
}
