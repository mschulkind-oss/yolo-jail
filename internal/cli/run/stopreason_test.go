package run

// stopreason_test.go pins the stop record (stopreason.go; docs/design/jail-lifetime-last-session-
// wins.md §2.3 item 3, JL-D53): every stop yolo makes records why before it stops, a later cause
// replaces an earlier record, and a session whose jail ended under it prints the record, reading only one written after it began, asking the runtime only for a
// status a jail's end gives and claiming nothing while the jail runs or the runtime cannot say.

import (
	"os"
	"strings"
	"testing"
	"time"
)

// stopRecordHome gives a test a home of its own, so the records land there.
func stopRecordHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

// fastStopRecordWait shrinks the attach's wait for a late record.
func fastStopRecordWait(t *testing.T) {
	t.Helper()
	savedWait, savedPoll := stopRecordWait, stopRecordPoll
	stopRecordWait, stopRecordPoll = 300*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { stopRecordWait, stopRecordPoll = savedWait, savedPoll })
}

// TestAStopRecordIsReplacedByTheNextCause: a stop replaces any record, since it is the cause, and
// nothing else writes one: the first session's end ends nothing now that the jail lives while any
// session does (endSession never records).
func TestAStopRecordIsReplacedByTheNextCause(t *testing.T) {
	stopRecordHome(t)
	o := goldenOptions("/ws", t.TempDir())
	now := time.Unix(1000, 0)
	o.Now = func() time.Time { return now }
	o.Getpid = func() int { return 77 }

	if _, ok := readJailStop("yolo-ws-1"); ok {
		t.Fatal("a record before anything wrote one")
	}
	o.recordJailStop("yolo-ws-1", "an earlier reason")
	now = time.Unix(2000, 0)
	o.recordJailStop("yolo-ws-1", YoloStopReason(9))
	if rec, _ := readJailStop("yolo-ws-1"); rec.Reason != YoloStopReason(9) || rec.PID != 77 ||
		!rec.At.Equal(time.Unix(2000, 0)) {
		t.Errorf("the later cause did not replace the earlier record: %+v", rec)
	}
}

// TestStopJailRecordsWhyBeforeItStops: the record is on disk when the runtime is asked to stop.
func TestStopJailRecordsWhyBeforeItStops(t *testing.T) {
	stopRecordHome(t)
	o := goldenOptions("/ws", t.TempDir())
	var atStop string
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) > 1 && argv[1] == "stop" {
			rec, _ := readJailStop("yolo-ws-1")
			atStop = rec.Reason
		}
		return ExecResult{Ran: true}
	}
	o.stopJail("yolo-ws-1", "podman", "the reason")
	if atStop != "the reason" {
		t.Errorf("at the stop the record said %q, want the reason written before it", atStop)
	}
}

// probeRuntime answers the attach's `ps -q` probe: running, gone, or no answer.
func probeRuntime(answer string, calls *[]string) func([]string, string, []string, time.Duration) ExecResult {
	return func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		*calls = append(*calls, strings.Join(argv, " "))
		if len(argv) > 1 && argv[1] == "ps" {
			switch answer {
			case "running":
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case "gone":
				return ExecResult{Ran: true, RC: 0}
			}
			return ExecResult{Ran: true, RC: 125}
		}
		return ExecResult{Ran: false}
	}
}

// TestWhyTheJailEndedAsksOnlyAboutAJailsEnd: a status a jail's end does not give costs no probe
// and claims nothing; a jail still running, or a runtime that cannot say, claims nothing either.
func TestWhyTheJailEndedAsksOnlyAboutAJailsEnd(t *testing.T) {
	stopRecordHome(t)
	fastStopRecordWait(t)
	o := goldenOptions("/ws", t.TempDir())
	o.recordJailStop("yolo-ws-1", "a reason")
	for _, rc := range []int{0, 1, 2, 126, 127, 130, 143} {
		var calls []string
		o.Exec = probeRuntime("gone", &calls)
		if _, ended := o.whyTheJailEnded("yolo-ws-1", "podman", rc, time.Unix(0, 0)); ended || len(calls) != 0 {
			t.Errorf("rc %d: ended=%v, runtime asked %q; want neither", rc, ended, calls)
		}
	}
	for _, answer := range []string{"running", "no answer"} {
		var calls []string
		o.Exec = probeRuntime(answer, &calls)
		if _, ended := o.whyTheJailEnded("yolo-ws-1", "podman", 137, time.Unix(0, 0)); ended {
			t.Errorf("%s: claimed the jail ended", answer)
		}
	}
}

// TestWhyTheJailEndedReadsOnlyThisAttachsRecord: once the runtime says the jail is gone, a record
// written after the attach began is its reason, one written before is not, and one that lands
// during the short wait is read.
func TestWhyTheJailEndedReadsOnlyThisAttachsRecord(t *testing.T) {
	stopRecordHome(t)
	fastStopRecordWait(t)
	o := goldenOptions("/ws", t.TempDir())
	var calls []string
	o.Exec = probeRuntime("gone", &calls)
	since := time.Now()

	writeJailStop("yolo-ws-1", jailStopRecord{At: since.Add(-time.Hour), Reason: "an earlier jail's"})
	if reason, ended := o.whyTheJailEnded("yolo-ws-1", "podman", 137, since); !ended || reason != "" {
		t.Errorf("a stale record: reason %q ended %v; want ended with no reason", reason, ended)
	}

	writeJailStop("yolo-ws-1", jailStopRecord{At: since.Add(time.Second), Reason: "this jail's"})
	for _, rc := range []int{137, 125, 255} {
		if reason, ended := o.whyTheJailEnded("yolo-ws-1", "podman", rc, since); !ended || reason != "this jail's" {
			t.Errorf("rc %d: reason %q ended %v", rc, reason, ended)
		}
	}

	writeJailStop("yolo-ws-1", jailStopRecord{At: since.Add(-time.Hour), Reason: "an earlier jail's"})
	go func() {
		time.Sleep(50 * time.Millisecond)
		writeJailStop("yolo-ws-1", jailStopRecord{At: since.Add(2 * time.Second), Reason: "late"})
	}()
	if reason, _ := o.whyTheJailEnded("yolo-ws-1", "podman", 137, since); reason != "late" {
		t.Errorf("a record written as the jail ended was missed: %q", reason)
	}
}

// TestEveryStopYoloMakesRecordsItsCause: the reaper and an attach-skew restart each record their own
// reason as they stop the jail; the keeper's drain and its signal record theirs
// (TestTheKeeperDrainsOnTheLastSessionAndTearsDown, TestASignalledKeeperEndsTheJailInOrder).
func TestEveryStopYoloMakesRecordsItsCause(t *testing.T) {
	t.Run("an attach-skew restart", func(t *testing.T) {
		s := newSkewAttach(t, true, true, "y\n", nil)
		s.o.Getpid = func() int { return 4242 }
		if _, restarted := s.attach(); !restarted {
			t.Fatalf("the attach did not restart:\n%s", s.stderr)
		}
		if rec, _ := readJailStop("yolo-ws-abcd1234"); rec.Reason != attachRestartReason(4242) {
			t.Errorf("the restart recorded %q", rec.Reason)
		}
	})
	t.Run("the orphan reaper", func(t *testing.T) {
		stopRecordHome(t)
		if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(ownerPIDFile("yolo-orphan"), []byte("999999\n"), 0o644)
		o := goldenOptions("/ws", t.TempDir())
		o.Getpid = func() int { return 31 }
		o.PIDAlive = func(int) bool { return false }
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if strings.Contains(strings.Join(argv, " "), "ps -a --format") {
				return ExecResult{Ran: true, Stdout: "yolo-orphan running\n"}
			}
			return ExecResult{Ran: true}
		}
		o.reapOrphanedJails("podman")
		if rec, _ := readJailStop("yolo-orphan"); rec.Reason != orphanReapReason(31, 999999) {
			t.Errorf("the reaper recorded %q", rec.Reason)
		}
	})
}

// TestAnAttachWhoseJailEndedSaysWhy drives attachExisting to an exec that returns 137, against a
// runtime that then says the jail is gone: with a record written after the attach began, the
// attach prints it and skips the OOM hint a stop would make wrong; with none, it says nothing
// recorded why and keeps the hint. An exec that returns 0 asks nothing.
func TestAnAttachWhoseJailEndedSaysWhy(t *testing.T) {
	fastStopRecordWait(t)
	for _, tc := range []struct {
		name, reason string
		rc           string
	}{
		{"a recorded stop", "`yolo stop` (pid 9) stopped it", "137"},
		{"nothing recorded", "", "137"},
		{"the session's own end", "", "0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o, cfg, channel, stderr := attachFixture(t, currentJailEnv, zaiSelected(t), hydratedKey(), nil)
			o.IsMacOS = true
			var calls []string
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				joined := strings.Join(argv, " ")
				calls = append(calls, joined)
				switch {
				case len(argv) > 1 && argv[1] == "inspect":
					return ExecResult{Ran: true, Stdout: currentJailEnv}
				case len(argv) > 1 && argv[1] == "ps":
					return ExecResult{Ran: true}
				}
				return ExecResult{Ran: false}
			}
			if tc.reason != "" {
				writeJailStop("yolo-ws-abcd1234", jailStopRecord{At: o.Now().Add(time.Second), Reason: tc.reason})
			}
			exitWith(t, tc.rc)
			rc, _ := o.attachExisting("yolo-ws-abcd1234", "podman", "true", cfg,
				stagedPacks{root: "/ctx/packs", packs: zaiSelected(t)}, channel, false, nil)
			out := stderr.String()
			probed := false
			machine := false
			for _, c := range calls {
				probed = probed || strings.HasPrefix(c, "podman ps -q")
				machine = machine || strings.Contains(c, "machine inspect")
			}
			switch {
			case tc.rc == "0":
				if rc != 0 || probed || strings.Contains(out, "its jail stopped") {
					t.Errorf("a session's own end: rc %d, probed %v:\n%s", rc, probed, out)
				}
			case tc.reason != "":
				if !strings.Contains(out, "This session ended because its jail stopped: "+tc.reason+".") {
					t.Errorf("the attach did not say why its jail ended:\n%s", out)
				}
				if machine {
					t.Error("a recorded stop still ran the OOM hint's probe")
				}
			default:
				if !strings.Contains(out, "nothing recorded why") || !strings.Contains(out, "`podman stop`") {
					t.Errorf("the attach did not say that nothing recorded why:\n%s", out)
				}
				if !machine {
					t.Error("with no recorded stop the OOM hint must still be asked")
				}
			}
		})
	}
}

// exitWith puts a fake podman first on PATH that exits with rc.
func exitWith(t *testing.T, rc string) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(bin+"/podman", []byte("#!/bin/sh\nexit "+rc+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}
