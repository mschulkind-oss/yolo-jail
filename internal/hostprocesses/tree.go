package hostprocesses

import (
	"context"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/pytext"
)

// pyReprStrList renders a []string as [<repr(e0)>, <repr(e1)>, …] with each
// element single-quoted via repr. The timeout stderr embeds the argv list in
// this form — Frozen contract (must not drift — the tree-timeout wire message
// depends on the exact bytes).
func pyReprStrList(xs []string) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = pytext.Repr(x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// gnuTreeArgv is GNU tree mode's one ps. Frozen: TestTreeTimeoutStderrGolden pins it
// inside the timeout message.
var gnuTreeArgv = []string{"ps", "-eo", "pid,ppid,comm,args", "--forest"}

// treeDeadlineSeconds bounds the whole of tree mode, on either dialect.
const treeDeadlineSeconds = 15

// handleTree runs `ps -eo pid,ppid,comm,args --forest` (15s timeout), then
// filters to allowlisted comms + their children (two passes). Failure paths:
// - timeout -> "tree mode failed: ..." + exit 1
// - stdout is read REGARDLESS of ps's return code, so a non-zero ps with EMPTY
// stdout yields exit 0 (empty), NOT an error. Go's exec.Command.Output()
// errors on non-zero exit; we deliberately IGNORE that error and use
// whatever stdout we captured.
//
// BSD ps has no --forest: handleTreeBSD builds the forest itself.
func handleTree(s *hostservice.Session, visible map[string]struct{}, d dialect) {
	if d == bsdPS {
		handleTreeBSD(s, visible)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), treeDeadlineSeconds*time.Second)
	defer cancel()
	run, err := runPS(ctx, treeDeadlineSeconds, gnuTreeArgv)
	if err != nil {
		// Frozen contract (must not drift — the wire message is
		// "Command '<argv list repr>' timed out after 15 seconds"; a hardcoded
		// "timed out" diverged from the expected bytes). A spawn failure (ps absent)
		// is exit 1 too; a ps that RAN and exited non-zero is not an error at all, and
		// its stdout is used below (which may be empty -> the exit-0 empty path).
		s.Stderr("tree mode failed: " + err.Error() + "\n")
		s.Exit(1)
		return
	}
	lines := strings.Split(strings.TrimRight(string(run.stdout), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		s.Exit(0)
		return
	}
	header := lines[0]
	allowedPids := map[string]struct{}{}
	kept := []string{header}
	keptSet := map[string]struct{}{header: {}}

	// First pass: direct comm matches.
	//
	// THE NAME COMES FROM /proc/<pid>/comm, NEVER FROM THE LINE. --forest draws its tree
	// INSIDE the comm column (` \_ sway`, ` |   \_ sway`), so splitting a line on
	// whitespace hands back the glyph `\_` or `|` as the comm of every process below
	// the root, and the column is cut to its width besides (`chrome-devt`). Until
	// 2026-10-04 this read that column, so tree mode matched only processes whose
	// parent is pid 0, and an allowlisted process anywhere else was never shown. The
	// file is the name pid mode already matches on.
	for _, line := range lines[1:] {
		parts := splitN(line, 4)
		if len(parts) < 3 {
			continue
		}
		pid := parts[0]
		comm, ok := linuxComm(pid)
		if !ok {
			continue
		}
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

// bsdTreeSnapshotArgv is the BSD tree's question about every process: pid, parent and
// name, the name LAST for the reason bsdListSnapshotArgv gives.
var bsdTreeSnapshotArgv = []string{"ps", "-ax", "-o", "pid=,ppid=,ucomm="}

// handleTreeBSD is tree mode on BSD ps, which has no --forest, so the forest is built
// here: the bsdTreeSnapshotArgv snapshot gives the shape and the names, every
// allowlisted process and all of its descendants are kept (GNU tree mode's set), and
// one `ps -o pid=,args= -p <kept pids>` supplies their command lines.
//
// TWO EXECS RATHER THAN ONE `ps -axo pid,ppid,ucomm,args`, because that line cannot be
// split: a ucomm may contain spaces (`Google Chrome He`), and so may the args after it,
// so no column boundary is recoverable from the text. With the name LAST in the first
// query and the args LAST in the second, each is simply the rest of its line. A kept
// process gone by the second query keeps its row, with `(ucomm)` for its args, the form
// BSD ps itself prints for a command line it cannot read.
//
// The output keeps GNU's shape, a header and then rows whose name and args columns
// carry --forest's own glyphs (` \_ `, ` |  `), with one deliberate difference: rows
// come in tree order, indented from the kept roots, where GNU prints the matches and
// then their descendants with depth counted from pid 0.
//
// Failure paths are GNU's: the deadline (treeDeadlineSeconds for the whole mode) names
// the argv that was running when it passed, a ps that could not run is exit 1, and a
// snapshot with no rows is exit 0 with no output, as GNU's non-zero-and-empty ps is.
func handleTreeBSD(s *hostservice.Session, visible map[string]struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), treeDeadlineSeconds*time.Second)
	defer cancel()
	snap, err := runPS(ctx, treeDeadlineSeconds, bsdTreeSnapshotArgv)
	if err != nil {
		s.Stderr("tree mode failed: " + err.Error() + "\n")
		s.Exit(1)
		return
	}
	procs := parseBSDSnapshot(snap.stdout, true)
	if len(procs) == 0 {
		s.Exit(0)
		return
	}
	kept := keptPids(procs, visible)
	argsOf := map[int]string{}
	if len(kept) > 0 {
		var pids []string
		for _, p := range procs {
			if kept[p.pid] {
				pids = append(pids, strconv.Itoa(p.pid))
			}
		}
		run, err := runPS(ctx, treeDeadlineSeconds, []string{"ps", "-o", "pid=,args=", "-p", strings.Join(pids, ",")})
		if err != nil {
			s.Stderr("tree mode failed: " + err.Error() + "\n")
			s.Exit(1)
			return
		}
		for _, line := range strings.Split(string(run.stdout), "\n") {
			pidTok, rest := cutField(line)
			if pid, err := strconv.Atoi(pidTok); err == nil && kept[pid] {
				argsOf[pid] = strings.TrimSpace(rest)
			}
		}
	}
	s.Stdout(renderForest(procs, kept, argsOf))
	s.Exit(0)
}

// keptPids is the set tree mode shows: every process whose name is allowlisted, and
// every descendant of one. GNU tree mode reaches the same set in two passes over
// --forest's line order; here it is a walk over the parent links.
func keptPids(procs []bsdProc, visible map[string]struct{}) map[int]bool {
	children := map[int][]int{}
	for _, p := range procs {
		if p.ppid != p.pid {
			children[p.ppid] = append(children[p.ppid], p.pid)
		}
	}
	kept := map[int]bool{}
	var queue []int
	for _, p := range procs {
		if _, ok := visible[p.comm]; ok && !kept[p.pid] {
			kept[p.pid] = true
			queue = append(queue, p.pid)
		}
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if !kept[c] {
				kept[c] = true
				queue = append(queue, c)
			}
		}
	}
	return kept
}

// forestRow is one rendered tree row; prefix is drawn on both name columns, as GNU
// draws it.
type forestRow struct {
	pid, ppid, prefix, comm, args string
}

// renderForest draws the kept processes (procs sorted by pid) as a forest: a kept
// process whose parent is not kept is a root, children are ordered by pid, and the
// glyphs are --forest's own (forestPrefix). Every kept process is drawn exactly once,
// even one a racy snapshot left on a parent cycle, which is drawn as a root.
func renderForest(procs []bsdProc, kept map[int]bool, argsOf map[int]string) string {
	byPid := map[int]bsdProc{}
	children := map[int][]int{}
	var roots, order []int
	for _, p := range procs {
		if !kept[p.pid] {
			continue
		}
		byPid[p.pid] = p
		order = append(order, p.pid)
		if p.ppid != p.pid && kept[p.ppid] {
			children[p.ppid] = append(children[p.ppid], p.pid)
		} else {
			roots = append(roots, p.pid)
		}
	}
	rows := []forestRow{{pid: "PID", ppid: "PPID", comm: "UCOMM", args: "ARGS"}}
	drawn := map[int]bool{}
	var walk func(pid int, hasSibling []bool)
	walk = func(pid int, hasSibling []bool) {
		if drawn[pid] {
			return
		}
		drawn[pid] = true
		p := byPid[pid]
		args, ok := argsOf[pid]
		if !ok {
			args = "(" + p.comm + ")"
		}
		prefix := forestPrefix(hasSibling)
		rows = append(rows, forestRow{
			pid: strconv.Itoa(p.pid), ppid: strconv.Itoa(p.ppid),
			prefix: prefix, comm: p.comm, args: args,
		})
		kids := children[pid]
		for i, c := range kids {
			// A full-slice expression, so a sibling's append never writes into the
			// slice an earlier sibling's subtree is still reading.
			walk(c, append(hasSibling[:len(hasSibling):len(hasSibling)], i < len(kids)-1))
		}
	}
	for _, r := range roots {
		walk(r, nil)
	}
	for _, pid := range order {
		walk(pid, nil)
	}

	pidW, ppidW, commW := 0, 0, 0
	for _, r := range rows {
		pidW = max(pidW, utf8.RuneCountInString(r.pid))
		ppidW = max(ppidW, utf8.RuneCountInString(r.ppid))
		commW = max(commW, utf8.RuneCountInString(r.prefix+r.comm))
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(pad(r.pid, pidW, true) + " " + pad(r.ppid, ppidW, true) + " " +
			pad(r.prefix+r.comm, commW, false) + " " + r.prefix + r.args + "\n")
	}
	return b.String()
}

// forestPrefix is GNU --forest's glyph run for a row: one 4-column cell per level above
// it (` |  ` while that ancestor has a later sibling, blank once it has none) and
// ` \_ ` for its own level. hasSibling[k] says whether the row's ancestor at depth k+1,
// or the row itself at the last index, has a later sibling. A root has no prefix.
func forestPrefix(hasSibling []bool) string {
	if len(hasSibling) == 0 {
		return ""
	}
	var b strings.Builder
	for _, more := range hasSibling[:len(hasSibling)-1] {
		if more {
			b.WriteString(" |  ")
		} else {
			b.WriteString("    ")
		}
	}
	b.WriteString(" \\_ ")
	return b.String()
}

// pad fills s with spaces to width w, on the left when right is set.
func pad(s string, w int, right bool) string {
	fill := strings.Repeat(" ", max(0, w-utf8.RuneCountInString(s)))
	if right {
		return fill + s
	}
	return s + fill
}

// splitN splits on runs of whitespace, at most maxsplit splits (so the last
// field keeps its spaces), with leading whitespace ignored.
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
