package hostprocesses

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// pyReprStrList renders a []string the way Python's repr(list) does:
// [<repr(e0)>, <repr(e1)>, …] with each element single-quoted via repr. Used to
// byte-match str(subprocess.TimeoutExpired), whose message embeds the argv list.
func pyReprStrList(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = pytext.Repr(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// handleTree runs `ps -eo pid,ppid,comm,args --forest` (15s timeout, matching
// Python's subprocess.run(timeout=15)), then filters to allowlisted comms +
// their children (two passes). Mirrors the tree branch of host_processes.py
// exactly, including the comm lstrip of "\_ " forest glyphs, the two-pass keep
// logic, and the failure paths:
//   - timeout -> "tree mode failed: ..." + exit 1
//   - Python reads out.stdout REGARDLESS of ps's return code, so a non-zero ps
//     with EMPTY stdout yields exit 0 (empty), NOT an error. Go's
//     exec.Command.Output() errors on non-zero exit; we deliberately IGNORE
//     that error and use whatever stdout we captured, mirroring Python.
func handleTree(s *hostservice.Session, visible map[string]struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	argv := []string{"ps", "-eo", "pid,ppid,comm,args", "--forest"}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		// Byte-match Python's str(subprocess.TimeoutExpired): "Command '<argv
		// list repr>' timed out after 15 seconds" (the `f"...{e}..."` in the
		// except). A hardcoded "timed out" diverged from the wire bytes.
		s.Stderr("tree mode failed: Command '" + pyReprStrList(argv) + "' timed out after 15 seconds\n")
		s.Exit(1)
		return
	}
	if err != nil {
		// Distinguish a spawn failure (ps absent — Python's subprocess.run
		// raises FileNotFoundError -> the except -> exit 1) from a non-zero
		// exit (Python reads out.stdout anyway -> may be exit 0). exec's
		// *ExitError means ps ran but exited non-zero; anything else is a
		// spawn/other failure.
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			s.Stderr("tree mode failed: " + err.Error() + "\n")
			s.Exit(1)
			return
		}
		// ps ran and exited non-zero: fall through and use its stdout (which
		// may be empty -> the exit-0 empty path below), exactly like Python.
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
