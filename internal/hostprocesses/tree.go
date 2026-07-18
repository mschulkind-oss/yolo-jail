package hostprocesses

import (
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// handleTree runs `ps -eo pid,ppid,comm,args --forest`, then filters to
// allowlisted comms + their children (two passes). Mirrors the tree branch of
// host_processes.py exactly, including the comm lstrip of "\_ " forest glyphs
// and the two-pass keep logic.
func handleTree(s *hostservice.Session, visible map[string]struct{}) {
	argv := []string{"ps", "-eo", "pid,ppid,comm,args", "--forest"}
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil {
		// Python catches any exception -> stderr + exit 1. A non-zero ps still
		// yields stdout via .stdout; exec.Command.Output only errors on
		// non-zero exit or spawn failure. Match: treat spawn/other failure as
		// the exception path, but if we got output use it (Python reads
		// out.stdout regardless of returncode).
		if len(out) == 0 {
			s.Stderr("tree mode failed: " + err.Error() + "\n")
			s.Exit(1)
			return
		}
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		s.Exit(0)
		return
	}
	header := lines[0]
	allowedPids := map[string]struct{}{}
	kept := []string{header}
	keptSet := map[string]struct{}{header: {}}

	// First pass: direct comm matches.
	for _, line := range lines[1:] {
		parts := splitN(line, 4)
		if len(parts) < 3 {
			continue
		}
		pid := parts[0]
		comm := strings.TrimLeft(parts[2], "\\_ ")
		if _, ok := visible[comm]; ok {
			allowedPids[pid] = struct{}{}
			kept = append(kept, line)
			keptSet[line] = struct{}{}
		}
	}
	// Second pass: children (ppid in allowedPids).
	for _, line := range lines[1:] {
		parts := splitN(line, 4)
		if len(parts) < 3 {
			continue
		}
		pid := parts[0]
		ppid := parts[1]
		if _, ok := allowedPids[ppid]; ok {
			if _, already := keptSet[line]; !already {
				kept = append(kept, line)
				keptSet[line] = struct{}{}
				allowedPids[pid] = struct{}{}
			}
		}
	}
	s.Stdout(strings.Join(kept, "\n") + "\n")
	s.Exit(0)
}

// splitN mirrors Python's str.split(None, maxsplit): split on runs of
// whitespace, at most maxsplit splits (so the last field keeps its spaces),
// with leading whitespace ignored.
func splitN(s string, maxsplit int) []string {
	var out []string
	i := 0
	n := len(s)
	for len(out) < maxsplit {
		// skip leading whitespace
		for i < n && isSpace(s[i]) {
			i++
		}
		if i >= n {
			return out
		}
		start := i
		for i < n && !isSpace(s[i]) {
			i++
		}
		out = append(out, s[start:i])
	}
	// remainder (after skipping whitespace) is the last field, verbatim.
	for i < n && isSpace(s[i]) {
		i++
	}
	if i < n {
		out = append(out, s[i:])
	}
	return out
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
