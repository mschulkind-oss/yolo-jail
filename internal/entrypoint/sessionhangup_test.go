package entrypoint

// sessionhangup_test.go pins the jail's half of a session's hangup (sessionhangup.go;
// docs/design/jail-lifetime-last-session-wins.md JL-D4, JL-D51): what a record holds, which
// processes a hangup reaches and what it sends them, that a pid handed on to another process is
// left alone, and that Main takes the hangup form before any boot and records a session first.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// withSessionState relocates the session records and the process table, and records every
// signal a hangup sends instead of sending it.
func withSessionState(t *testing.T) (proc string, sent *[]string) {
	t.Helper()
	savedDir, savedProc, savedSignal := sessionsDir, procDir, signalProcess
	sessionsDir = filepath.Join(t.TempDir(), "sessions")
	procDir = t.TempDir()
	var log []string
	signalProcess = func(pid int, sig syscall.Signal) error {
		log = append(log, fmt.Sprintf("%d:%s", pid, sigName(sig)))
		return nil
	}
	t.Cleanup(func() { sessionsDir, procDir, signalProcess = savedDir, savedProc, savedSignal })
	return procDir, &log
}

func sigName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "HUP"
	case syscall.SIGCONT:
		return "CONT"
	}
	return sig.String()
}

// fakeProc writes one /proc/<pid>/stat under dir, with a command name holding the spaces and
// parentheses a real one may.
func fakeProc(t *testing.T, dir string, pid, ppid, sid int, start uint64) {
	t.Helper()
	p := filepath.Join(dir, strconv.Itoa(pid))
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	// Fields 3 to 22: state, ppid, pgrp, session, then 15 more, then starttime.
	rest := []string{"S", strconv.Itoa(ppid), strconv.Itoa(sid), strconv.Itoa(sid)}
	for i := 0; i < 15; i++ {
		rest = append(rest, "0")
	}
	rest = append(rest, strconv.FormatUint(start, 10), "123", "456")
	line := fmt.Sprintf("%d (a (b) c) %s\n", pid, strings.Join(rest, " "))
	if err := os.WriteFile(filepath.Join(p, "stat"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestParseProcStatCountsFromTheLastParenthesis: a command name with spaces and parentheses
// does not shift the fields after it.
func TestParseProcStatCountsFromTheLastParenthesis(t *testing.T) {
	dir := t.TempDir()
	fakeProc(t, dir, 42, 7, 42, 987654)
	b, _ := os.ReadFile(filepath.Join(dir, "42", "stat"))
	got, err := parseProcStat(42, string(b))
	if err != nil {
		t.Fatal(err)
	}
	if got != (procStat{pid: 42, ppid: 7, sid: 42, start: 987654}) {
		t.Errorf("parsed %+v", got)
	}
	if _, err := parseProcStat(1, "1 no parens here"); err == nil {
		t.Error("a stat with no command name parsed")
	}
}

// TestHangupTargetsAreTheSessionsOwnProcesses: the leader, every descendant, and every process
// still in the leader's session (a descendant reparented by its parent's exit), in pid order —
// and never pid 1, the hangup's own process, or another session's processes. A leader that does
// not lead a session of its own reaches its descendants only, since its session is someone
// else's.
func TestHangupTargetsAreTheSessionsOwnProcesses(t *testing.T) {
	procs := []procStat{
		{pid: 1, ppid: 0, sid: 1},
		{pid: 10, ppid: 0, sid: 10},  // the session
		{pid: 11, ppid: 10, sid: 10}, // its agent
		{pid: 12, ppid: 11, sid: 12}, // a tool the agent started in a session of its own
		{pid: 13, ppid: 1, sid: 10},  // reparented, still in the session
		{pid: 20, ppid: 0, sid: 20},  // another session
		{pid: 21, ppid: 20, sid: 20},
		{pid: 30, ppid: 0, sid: 30}, // the hangup itself
	}
	got := hangupTargets(procs, procs[1], 30)
	if !slices.Equal(got, []int{10, 11, 12, 13}) {
		t.Errorf("targets = %v, want the session's own [10 11 12 13]", got)
	}
	notLeader := procStat{pid: 11, ppid: 10, sid: 10}
	if got := hangupTargets(procs, notLeader, 30); !slices.Equal(got, []int{11, 12}) {
		t.Errorf("a leader of no session reached %v, want its descendants [11 12]", got)
	}
	self := procStat{pid: 30, ppid: 0, sid: 30}
	if got := hangupTargets(procs, self, 30); len(got) != 0 {
		t.Errorf("the hangup reached itself: %v", got)
	}
}

// TestASessionRecordsItsPidAndStartTime: a session's record, named by its id, is this process's
// pid and its start time from the process table.
func TestASessionRecordsItsPidAndStartTime(t *testing.T) {
	proc, _ := withSessionState(t)
	self := os.Getpid()
	fakeProc(t, proc, self, 1, self, 5555)
	const id = "0123456789abcdef0123456789abcdef"
	if err := registerSession(id); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(sessionsDir, id))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(raw)) != strconv.Itoa(self)+" 5555" {
		t.Errorf("record = %q, want this process's pid and start time", raw)
	}
}

// TestAHangupSendsHupThenContToTheRecordedSessionAndForgetsIt: the hangup form reads a
// session's record, sends SIGHUP then SIGCONT to each of the session's processes and to nothing
// else, and removes the record. A second hangup of the same id finds nothing and sends nothing.
func TestAHangupSendsHupThenContToTheRecordedSessionAndForgetsIt(t *testing.T) {
	proc, sent := withSessionState(t)
	fakeProc(t, proc, 5000, 0, 5000, 7000)    // the session
	fakeProc(t, proc, 5001, 5000, 5000, 7001) // its agent
	fakeProc(t, proc, 5002, 0, 5002, 7002)    // another session
	const id = "0123456789abcdef"
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, id), []byte("5000 7000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hangUpSession(id); err != nil {
		t.Fatal(err)
	}
	want := []string{"5000:HUP", "5000:CONT", "5001:HUP", "5001:CONT"}
	if !slices.Equal(*sent, want) {
		t.Errorf("sent %v, want %v", *sent, want)
	}
	if _, err := os.Stat(filepath.Join(sessionsDir, id)); !os.IsNotExist(err) {
		t.Errorf("the record survived its hangup: %v", err)
	}
	*sent = nil
	if err := hangUpSession(id); err != nil || len(*sent) != 0 {
		t.Errorf("a second hangup: err %v, sent %v; want nothing", err, *sent)
	}
}

// TestAHangupLeavesAPidAnotherProcessNowHolds: a record whose pid now has another start time is
// a session that ended and a pid the kernel handed on; nothing is signalled.
func TestAHangupLeavesAPidAnotherProcessNowHolds(t *testing.T) {
	proc, sent := withSessionState(t)
	fakeProc(t, proc, 4242, 1, 4242, 9000)
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "feedfacefeedface"
	if err := os.WriteFile(filepath.Join(sessionsDir, id), []byte("4242 1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hangUpSession(id); err != nil {
		t.Fatal(err)
	}
	if len(*sent) != 0 {
		t.Errorf("signalled another process that took the pid: %v", *sent)
	}
}

// TestSessionIDsAreFileNamesOnly: an id is lowercase hex of a sane length, so a record cannot
// be named outside its directory.
func TestSessionIDsAreFileNamesOnly(t *testing.T) {
	withSessionState(t)
	for _, bad := range []string{"", "short", "../../etc/passwd", "ABCDEF0123456789", strings.Repeat("a", 65)} {
		if validSessionID(bad) {
			t.Errorf("%q passed as a session id", bad)
		}
		if err := registerSession(bad); err == nil {
			t.Errorf("registered %q", bad)
		}
		if err := hangUpSession(bad); err == nil {
			t.Errorf("hung up %q", bad)
		}
	}
}

// TestAHangupEndsARealSessionAndItsChildren, on Linux, with real processes: a stand-in session
// leads a session of its own and has a child; the hangup form, run with the real process table
// and the real kill, ends both with SIGHUP.
func TestAHangupEndsARealSessionAndItsChildren(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	savedDir := sessionsDir
	sessionsDir = filepath.Join(t.TempDir(), "sessions")
	t.Cleanup(func() { sessionsDir = savedDir })
	marker := filepath.Join(t.TempDir(), "child-pid")
	cmd := exec.Command("sh", "-c", `sleep 300 & echo $! > "$1"; wait`, "sh", marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
	var child int
	for deadline := time.Now().Add(5 * time.Second); child == 0; {
		if b, err := os.ReadFile(marker); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		if time.Now().After(deadline) {
			t.Fatal("the stand-in session never started its child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	leader, err := readProcStat(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	const id = "00000000deadbeef"
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := fmt.Sprintf("%d %d\n", leader.pid, leader.start)
	if err := os.WriteFile(filepath.Join(sessionsDir, id), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := hangUpSession(id); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ws.Signaled() || ws.Signal() != syscall.SIGHUP {
		t.Errorf("the session ended %v (%v), want a death by SIGHUP", err, cmd.ProcessState)
	}
	for deadline := time.Now().Add(5 * time.Second); syscall.Kill(child, 0) == nil; {
		if s, err := readProcStat(child); err == nil && s.ppid != 1 && s.ppid != leader.pid {
			break // the pid is someone else's now
		}
		if b, _ := os.ReadFile(filepath.Join("/proc", strconv.Itoa(child), "stat")); strings.Contains(string(b), ") Z ") {
			break // a zombie waiting for its reaper has ended
		}
		if time.Now().After(deadline) {
			t.Fatalf("the session's child %d outlived the hangup", child)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMainTakesTheHangupFormFirstAndRecordsASessionBeforeItWaits: the hangup form returns right
// after the disk I/O priority, which stays Main's first statement, and before everything a boot
// does; a session records itself right after its argv is read and before the gate's first wait.
// Read from the source, since Main execs bash.
func TestMainTakesTheHangupFormFirstAndRecordsASessionBeforeItWaits(t *testing.T) {
	b, err := os.ReadFile("boot.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	start := strings.Index(body, "func Main(args []string) error {")
	if start < 0 {
		t.Fatal("Main not found")
	}
	body = body[start:]
	order := []string{
		"ioOutcome := applyIOPriority(os.Args)",
		"if len(args) == 2 && args[0] == HangupSessionArg {",
		"return hangUpSession(args[1])",
		"mode, command := parseEntryArgs(args)",
		"os.Getenv(SessionIDEnv); id != \"\" && mode != modeHold",
		"registerSession(id)",
		"markBoot(bootBooting)",
		"gate.awaitBoot()",
	}
	last := -1
	for _, frag := range order {
		i := strings.Index(body, frag)
		if i < 0 {
			t.Errorf("Main no longer contains %q", frag)
			continue
		}
		if i < last {
			t.Errorf("%q is out of order in Main", frag)
		}
		last = i
	}
}
