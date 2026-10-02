package entrypoint

// sessionhangup.go is the jail's half of a session's HANGUP: when a session's launcher on the host
// is signalled — its terminal window or pane closed, or a `kill` — it ends that session's own
// processes in the jail and nothing else (docs/design/jail-lifetime-last-session-wins.md §2.3
// item 1, JL-D4, and OQ-JL8, ruled A: a session's agent ends with its pane).
//
// # Why the jail has to do it
//
// Killing a `podman exec` client does not end the process it started. conmon keeps the exec
// session and its pty, so an attach whose terminal closed used to leave its agent running in the
// jail with no terminal, counted by nobody, until the jail stopped (measured with SIGKILL,
// SIGHUP, SIGTERM and SIGINT to an `exec -it` client, 2026-09-29). The launcher cannot signal
// that process itself: it is in the container's pid namespace, and on a Mac in another machine.
// So it asks the jail, with one more exec: `yolo-entrypoint --yolo-hangup-session <id>`.
//
// # How a session is found
//
// The launcher mints the session's id and hands it to the exec as SessionIDEnv. The session's
// entrypoint records its own pid and start time under that id in sessionsDir before anything
// else (registerSession), and then execs bash, which keeps the pid: bash, and the command it
// runs, are that process and its descendants. The start time is what keeps a pid the kernel
// handed on to a later process from being signalled in its place.
//
// # A hangup that comes before its session (JL-D77)
//
// A launcher signalled just after it started its session's exec runs its hangup while that exec
// is still being made, so the hangup can reach the jail before the session has recorded itself.
// It used to find no record and end nothing, and the session recorded itself a moment later and
// ran on with no terminal: its client, killed, does not end it. So a hangup first leaves a HANGUP
// MARK, a term coined here: an empty file named by the id with hangupMarkSuffix, beside where the
// record goes. Then it looks for the record. A session writes its record first and then looks for
// the mark, and one that finds it removes its record and does not begin (enterSession). Each side
// writes before it reads, so at least one of them sees the other. The hangup reaches a session
// that wrote its record first, and a session that wrote it later finds the mark. Nothing waits for
// a session that may never come, so the hangup stays inside a multiplexer's SIGKILL budget. A mark
// is never removed: an id is minted once, for one exec, and the tmpfs goes with the container.
//
// # What a hangup sends, and to whom
//
// What a terminal's hangup sends: SIGHUP, then SIGCONT, so a stopped process gets to act on it
// (the kernel's own order when a terminal hangs up). To the session's process, every
// descendant of it, and, because every exec was measured to be the leader of a session of its
// own (pid, process group and session id equal, with a terminal and without), every process
// still in that session, which is where a descendant a parent's exit reparented still is. Never
// to pid 1 or to itself. A process that ignores a hangup, `nohup make` in the agent's shell,
// runs on, as it would past a closed terminal anywhere (JL-D51).
//
// Everything here is in-jail state on the container's /run tmpfs, trusted by nothing on the
// host: a jail process that rewrote a record could only choose which of its own jail's
// processes a hangup reaches.

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// SessionIDEnv names the session an exec runs, for its hangup. Set by the launcher on each
// attach's exec when the jail has the session-hangup contract, and read here.
const SessionIDEnv = "YOLO_SESSION_ID"

// HangupSessionArg is the two-argument form that hangs up the session its payload names. Like
// HoldMainArg and FirstSessionArg it is exactly a flag and one payload, so no session's command,
// which crosses as one argument, can be read as it.
const HangupSessionArg = "--yolo-hangup-session"

// sessionsDir holds one record per session, named by its id, and the mark each hangup leaves
// (hangupMark). A var so tests can relocate it.
var sessionsDir = "/run/yolo/sessions"

// procDir is where the process table is read. A var so tests can stand in a table of their own.
var procDir = "/proc"

// signalProcess sends a signal. A var so tests can record what a hangup sends instead.
var signalProcess = func(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// validSessionID reports an id safe to use as a file name: 8 to 64 lowercase hex digits, which
// is every id the launcher mints (newSessionID in internal/cli/run).
func validSessionID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// procStat is the part of one /proc/<pid>/stat a hangup reads.
type procStat struct {
	pid, ppid, sid int
	start          uint64
}

// readProcStat parses /proc/<pid>/stat. The command name sits in parentheses and may hold spaces
// and parentheses of its own, so the fields are counted from the last ')'.
func readProcStat(pid int) (procStat, error) {
	b, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "stat"))
	if err != nil {
		return procStat{}, err
	}
	return parseProcStat(pid, string(b))
}

func parseProcStat(pid int, raw string) (procStat, error) {
	i := strings.LastIndexByte(raw, ')')
	if i < 0 {
		return procStat{}, fmt.Errorf("pid %d: no command name in its stat", pid)
	}
	// After the name: state (field 3), ppid (4), pgrp (5), session (6), … starttime (22).
	f := strings.Fields(raw[i+1:])
	if len(f) < 20 {
		return procStat{}, fmt.Errorf("pid %d: its stat has %d fields after the name", pid, len(f))
	}
	ppid, err1 := strconv.Atoi(f[1])
	sid, err2 := strconv.Atoi(f[3])
	start, err3 := strconv.ParseUint(f[19], 10, 64)
	if err := errors.Join(err1, err2, err3); err != nil {
		return procStat{}, fmt.Errorf("pid %d: %w", pid, err)
	}
	return procStat{pid: pid, ppid: ppid, sid: sid, start: start}, nil
}

// hangupMarkSuffix names a hangup's mark: the session's id, then this. A record is the bare id
// and a record being written starts with a dot, so none of the three can be taken for another.
const hangupMarkSuffix = ".hungup"

// hangupMark is the path of the mark a hangup of the session named id leaves.
func hangupMark(id string) string { return filepath.Join(sessionsDir, id+hangupMarkSuffix) }

// errHungUpBeforeItBegan is registerSession's answer for a session whose hangup came first: its
// launcher was signalled before the session had recorded itself, so the session must not begin.
var errHungUpBeforeItBegan = errors.New("its launcher hung it up before it began")

// enterSession is a session's first act in Main: it records this process as the session named id,
// for its launcher's hangup. A session that finds its hangup already came ends here, with the status
// a hung-up session's process ends with (128+SIGHUP), because nothing will end it later: its
// launcher has gone or is going, and its client's death does not reach it. Any other failure to
// record is a warning on warn and the session goes on, as it always did, since it can still run and
// only its hangup is lost.
func enterSession(id string, warn io.Writer) error {
	err := registerSession(id)
	if errors.Is(err, errHungUpBeforeItBegan) {
		return &ExitStatus{Code: 128 + int(syscall.SIGHUP),
			Message: "the yolo that started this session was signalled before the session began, so it does not begin"}
	}
	if err != nil {
		fmt.Fprintf(warn, "yolo-entrypoint: warning: could not record this session for "+
			"its launcher (%v); if its terminal closes, what it runs may go on in the jail\n", err)
	}
	return nil
}

// registerSession records this process as the session named id, and then looks for its hangup's
// mark, in that order (the file's header, JL-D77): errHungUpBeforeItBegan, with the record removed,
// when the mark is there. Only a session registers; the main process never does, since nothing
// hangs it up.
func registerSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("%s=%q is not a session id", SessionIDEnv, id)
	}
	self, err := readProcStat(os.Getpid())
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(sessionsDir, "."+id+".*")
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tmp, "%d %d\n", self.pid, self.start); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	record := filepath.Join(sessionsDir, id)
	if err := os.Rename(tmp.Name(), record); err != nil {
		return err
	}
	if _, err := os.Lstat(hangupMark(id)); err == nil {
		_ = os.Remove(record)
		return errHungUpBeforeItBegan
	}
	return nil
}

// hangUpSession leaves the session named id its hangup's mark, then hangs the session up, if it
// still runs, and forgets it. A session that already ended leaves nothing to hang up and is not
// an error: the launcher's arm asks whatever state its session is in. One that has not recorded
// itself yet finds the mark when it does, and does not begin (registerSession); a mark that could
// not be left is this hangup's error only then, since a session already recorded is hung up here.
func hangUpSession(id string) error {
	if !validSessionID(id) {
		return fmt.Errorf("%q is not a session id", id)
	}
	markErr := os.MkdirAll(sessionsDir, 0o755)
	if markErr == nil {
		markErr = os.WriteFile(hangupMark(id), nil, 0o644)
	}
	record := filepath.Join(sessionsDir, id)
	raw, err := os.ReadFile(record)
	if errors.Is(err, fs.ErrNotExist) {
		return markErr
	}
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(record) }()
	var pid int
	var start uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d %d", &pid, &start); err != nil || pid <= 1 {
		return fmt.Errorf("session %s: unreadable record %q", id, strings.TrimSpace(string(raw)))
	}
	leader, err := readProcStat(pid)
	if err != nil || leader.start != start {
		return nil // gone, or its pid is another process's now
	}
	for _, p := range hangupTargets(procTable(), leader, os.Getpid()) {
		_ = signalProcess(p, syscall.SIGHUP)
		_ = signalProcess(p, syscall.SIGCONT)
	}
	return nil
}

// procTable reads every process's stat; one that exits meanwhile is skipped.
func procTable() []procStat {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var out []procStat
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if s, err := readProcStat(pid); err == nil {
			out = append(out, s)
		}
	}
	return out
}

// hangupTargets is the session's processes, in pid order: the leader, its descendants, and,
// when the leader leads a session of its own, every process in that session. Never pid 1, and
// never self, the hangup's own process.
func hangupTargets(procs []procStat, leader procStat, self int) []int {
	children := map[int][]int{}
	for _, p := range procs {
		children[p.ppid] = append(children[p.ppid], p.pid)
	}
	set := map[int]bool{leader.pid: true}
	queue := []int{leader.pid}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, c := range children[next] {
			if !set[c] {
				set[c] = true
				queue = append(queue, c)
			}
		}
	}
	if leader.sid == leader.pid {
		for _, p := range procs {
			if p.sid == leader.pid {
				set[p.pid] = true
			}
		}
	}
	delete(set, 1)
	delete(set, self)
	out := make([]int, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
