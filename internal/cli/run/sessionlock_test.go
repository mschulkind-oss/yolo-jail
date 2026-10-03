package run

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// sharedHeld reports whether some process holds cname's session lock SHARED: an exclusive
// non-blocking take fails while a shared lock exists and a shared one would succeed.
func sessionLockHeld(t *testing.T, cname string) bool {
	t.Helper()
	l, ok := tryExclusiveSessionLock(cname)
	if ok {
		l.release()
		return false
	}
	return true
}

// TestTheSessionLockCountsSessionsAndRefusesTheReaper: while any session holds it shared the
// reaper's exclusive take fails, two sessions share it, and the take succeeds once both have
// let go — the file is one per name and outlives them.
func TestTheSessionLockCountsSessionsAndRefusesTheReaper(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a, _, err := takeSessionLock("yolo-ws-1")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := takeSessionLock("yolo-ws-1")
	if err != nil {
		t.Fatalf("a second session could not share the lock: %v", err)
	}
	if !sessionLockHeld(t, "yolo-ws-1") {
		t.Fatal("the reaper's exclusive take succeeded with two sessions in the jail")
	}
	a.release()
	if !sessionLockHeld(t, "yolo-ws-1") {
		t.Fatal("the reaper's exclusive take succeeded with one session still in the jail")
	}
	b.release()
	if sessionLockHeld(t, "yolo-ws-1") {
		t.Fatal("the reaper's exclusive take failed with no session left")
	}
	if _, err := os.Stat(sessionLockPath("yolo-ws-1")); err != nil {
		t.Errorf("the session lock file went with its sessions: %v", err)
	}
	if got := filepath.Base(filepath.Dir(sessionLockPath("x"))); got != "locks" {
		t.Errorf("the session lock lives in %q, want the host-only locks dir", got)
	}
}

// TestASessionWaitsForAReaperAndSaysItWaited: a session arriving while a reaper holds the lock
// exclusively waits for it, and reports that it waited so the caller looks at the jail again;
// past the bound it gives up uncounted.
func TestASessionWaitsForAReaperAndSaysItWaited(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	savedWait, savedPoll := sessionLockWait, sessionLockPoll
	sessionLockWait, sessionLockPoll = 2*time.Second, 5*time.Millisecond
	t.Cleanup(func() { sessionLockWait, sessionLockPoll = savedWait, savedPoll })

	reaper, ok := tryExclusiveSessionLock("yolo-ws-2")
	if !ok {
		t.Fatal("the reaper could not take a free lock")
	}
	go func() { time.Sleep(50 * time.Millisecond); reaper.release() }()
	l, contended, err := takeSessionLock("yolo-ws-2")
	if err != nil || !contended {
		t.Fatalf("got contended=%v err=%v, want a wait that succeeded", contended, err)
	}
	l.release()

	sessionLockWait = 30 * time.Millisecond
	stuck, _ := tryExclusiveSessionLock("yolo-ws-2")
	defer stuck.release()
	if _, contended, err := takeSessionLock("yolo-ws-2"); err == nil || !contended {
		t.Errorf("got contended=%v err=%v, want the bound to end the wait with an error", contended, err)
	}
}

// TestTheReaperSparesAnOrphanWithASessionInIt is the §2.3 item 4 fix: a jail whose owner is
// dead but whose session lock is held is left running; the same jail with no session is
// reaped, and the reaper holds the lock exclusively across the stop, so no session can count
// itself into a jail being stopped.
func TestTheReaperSparesAnOrphanWithASessionInIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	const cname = "yolo-orphan-with-session"
	if err := os.WriteFile(ownerPIDFile(cname), []byte(strconv.Itoa(999999)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stopped []string
	heldDuringStop := false
	o := goldenOptions("/ws", home)
	o.PIDAlive = func(int) bool { return false }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		key := strings.Join(argv, " ")
		switch {
		case strings.Contains(key, "ps -a --format"):
			return ExecResult{Ran: true, RC: 0, Stdout: cname + " running\n"}
		case len(argv) > 1 && argv[1] == "stop":
			stopped = append(stopped, argv[len(argv)-1])
			// A session arriving now must not get in.
			f, err := openSessionLock(cname)
			if err == nil {
				heldDuringStop = syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) != nil
				_ = f.Close()
			}
			return ExecResult{Ran: true, RC: 0}
		}
		return ExecResult{Ran: false}
	}

	session, _, err := takeSessionLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	o.reapOrphanedJails("podman")
	if len(stopped) != 0 {
		t.Fatalf("the reaper stopped %v with a session in it", stopped)
	}
	session.release()

	o.reapOrphanedJails("podman")
	if len(stopped) != 1 || stopped[0] != cname {
		t.Fatalf("the reaper did not reap the orphan once its last session left: %v", stopped)
	}
	if !heldDuringStop {
		t.Error("the reaper did not hold the session lock exclusively across the stop")
	}
	if sessionLockHeld(t, cname) {
		t.Error("the reaper kept the session lock after the reap")
	}
}

// TestTheSessionCountLeavesOutAHoldMainProcess: a jail whose main process is a hold counts its
// execs alone; one launched before counts its main process too.
func TestTheSessionCountLeavesOutAHoldMainProcess(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	for _, tc := range []struct {
		env  string
		want int
	}{
		{entrypoint.JailMainEnv + "=" + entrypoint.JailMainHold + "\nYOLO_VERSION=1\n", 2},
		{"YOLO_VERSION=1\n", 3},
	} {
		rt := &skewRuntime{env: tc.env, execIDs: "2"}
		o.Exec = rt.exec
		if n, ok := o.jailSessionCount("podman", "yolo-ws-1"); !ok || n != tc.want {
			t.Errorf("env %q: count %d (ok %v), want %d", tc.env, n, ok, tc.want)
		}
	}
}

// TestAnArrivalAtAJailWhoseKeeperIsGoneIsRefused is JL-D13, as OQ-JL7 ruled: a running jail whose
// keeper is dead (a start record beside a free liveness lock), or one an earlier yolo started whose
// launcher is dead, is not entered; the refusal names what runs in it and `yolo stop`. A jail whose
// keeper is alive, or whose older launcher still is, or that records no owner at all, is entered as
// it always was.
//
// The remedy is the same on Apple Container: run 37133569003 (2026-10-03, at 5ca9b7485) recorded
// the refusal there naming `container stop <name>`, which ends the jail without waiting for its
// teardown, and 6f881d8fd made every remedy name `yolo stop`, which JL-D79 lets read an Apple
// Container jail.
func TestAnArrivalAtAJailWhoseKeeperIsGoneIsRefused(t *testing.T) {
	const cname = "yolo-ws-abcd1234"
	type arrival struct {
		name          string
		owner         string
		alive, record bool
		keeperHolds   bool
		refuse        bool
	}
	var cases []struct {
		rt string
		arrival
	}
	for _, rt := range []string{"podman", "container"} {
		for _, a := range []arrival{
			{"a keeper that died", "4242", false, true, false, true},
			{"a keeper whose pid was reused", "4242", true, true, false, true},
			{"an earlier launcher that died", "4242", false, false, false, true},
			{"a live keeper", "4242", false, true, true, false},
			{"an earlier launcher still alive", "4242", true, false, false, false},
			{"no owner recorded", "", false, false, false, false},
		} {
			cases = append(cases, struct {
				rt string
				arrival
			}{rt, a})
		}
	}
	for _, tc := range cases {
		t.Run(tc.rt+"/"+tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			o := goldenOptions("/ws", t.TempDir())
			var errBuf bytes.Buffer
			o.Stderr = &errBuf
			o.PIDAlive = func(int) bool { return tc.alive }
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				return ExecResult{Ran: true, Stdout: "2\n"} // inspect's exec count
			}
			if tc.owner != "" {
				if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(ownerPIDFile(cname), []byte(tc.owner+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.record {
				if err := writeKeeperRecord(cname, keeperRecord{PID: 4242}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.keeperHolds {
				live, err := holdLivenessLock(cname)
				if err != nil {
					t.Fatal(err)
				}
				defer releaseLock(live)
			}
			if got := o.refuseUnkeptJail(cname, tc.rt); got != tc.refuse {
				t.Fatalf("refused=%v, want %v:\n%s", got, tc.refuse, errBuf.String())
			}
			if tc.refuse && (!strings.Contains(errBuf.String(), "Refusing to enter "+cname) ||
				!strings.Contains(errBuf.String(), "'yolo stop' from this workspace")) {
				t.Errorf("the refusal does not name the jail and the remedy:\n%s", errBuf.String())
			}
			if strings.Contains(errBuf.String(), "container stop") {
				t.Errorf("the refusal names `container stop`, which skips the keeper's teardown:\n%s", errBuf.String())
			}
		})
	}
	// The call site: the arrival refuses before it attaches.
	var refusePos, attachPos token.Pos
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			switch skelCallee(call) {
			case "refuseUnkeptJail":
				if refusePos == token.NoPos {
					refusePos = call.Pos()
				}
			case "attachExisting":
				if attachPos == token.NoPos {
					attachPos = call.Pos()
				}
			}
		}
		return true
	})
	if refusePos == token.NoPos || attachPos == token.NoPos || refusePos > attachPos {
		t.Error("runContainer no longer refuses an unkept jail before its attach decision attaches")
	}
}

// TestAnAttachIsCountedBeforeItsExec: the attach arm holds the jail's session lock by the time
// it execs, taken while the launch lock was still held, and its quit lets it go (endSession) before
// it looks at what it left behind.
func TestAnAttachIsCountedBeforeItsExec(t *testing.T) {
	s := newSkewAttach(t, false, false, "", map[string]string{AllowAttachSkewEnv: "1"})
	counted := false
	lockReleased := false
	s.o.Exec = func(argv []string, dir string, env []string, d time.Duration) ExecResult {
		return s.rt.exec(argv, dir, env, d)
	}
	rc, restarted := s.o.attachExisting("yolo-ws-abcd1234", "podman", "true", s.cfg,
		stagedPacks{root: "/ctx/packs", packs: s.packs}, s.channel, false, func() {
			lockReleased = true
			counted = s.o.sessionLock != nil
		})
	if restarted || rc != 0 || !s.didExec() {
		t.Fatalf("the attach did not reach its exec: rc=%d restarted=%v\n%s", rc, restarted, s.stderr.String())
	}
	if !lockReleased || !counted {
		t.Errorf("the attach was not counted before it released the launch lock (released=%v counted=%v)",
			lockReleased, counted)
	}
	if sessionLockHeld(t, "yolo-ws-abcd1234") || s.o.sessionLock != nil {
		t.Error("the attach's quit left its session lock held")
	}
}
