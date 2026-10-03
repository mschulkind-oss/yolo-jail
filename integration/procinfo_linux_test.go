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
)

// processState is pid's state letter ("S", "R", "Z", …) from /proc/<pid>/stat. found is false
// when /proc has no such process; err is any other failure to read it.
func processState(pid int) (state string, found bool, err error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// The state is the first field after the parenthesised command name, which may itself
	// contain spaces or parentheses — hence the LAST ')'.
	s := string(stat)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return "", false, fmt.Errorf("unparsable /proc/%d/stat: %q", pid, s)
	}
	return s[i+2 : i+3], true, nil
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
