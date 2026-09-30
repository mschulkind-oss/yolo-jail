package run

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFinishStopWaitsForAKeeperAndReapsAnUnkeptJail is JL-D25 and JL-D30 for `yolo stop`: a keeper
// still holding the jail is waited for, its teardown streamed, and the stop returns once it is gone;
// a jail whose keeper died gets the keeper's chain from the stop itself, holding both locks, without
// a second stop, whose record would replace the one `yolo stop` wrote; a jail an older yolo started
// is left to its launcher.
func TestFinishStopWaitsForAKeeperAndReapsAnUnkeptJail(t *testing.T) {
	t.Run("a live keeper", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		o := goldenOptions("/ws", t.TempDir())
		var out bytes.Buffer
		o.Stdout, o.Stderr = &out, &out
		const cname = "yolo-stopped"
		live, err := holdLivenessLock(cname)
		if err != nil {
			t.Fatal(err)
		}
		logF, err := openKeeperLog(cname)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(100 * time.Millisecond)
			_, _ = logF.WriteString("12:00:00.000 keeper: done\n")
			_ = logF.Close()
			releaseLock(live)
		}()
		if rc := o.finishStop(cname, "podman", 0); rc != 0 {
			t.Errorf("rc %d:\n%s", rc, out.String())
		}
		if !strings.Contains(out.String(), "Waiting for the jail's keeper") || !strings.Contains(out.String(), "keeper: done") {
			t.Errorf("the stop did not stream the keeper's teardown:\n%s", out.String())
		}
		if probeKeeper(cname) != keeperGone {
			t.Error("the stop returned before the keeper was gone")
		}
	})
	t.Run("an unkept jail", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		emptyLoopholeDirs(t)
		o := goldenOptions("/ws", t.TempDir())
		var out bytes.Buffer
		o.Stdout, o.Stderr = &out, &out
		const cname = "yolo-unkept"
		if err := writeKeeperRecord(cname, keeperRecord{PID: 999999}); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ownerPIDFile(cname), []byte("999999\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		RecordJailStop(cname, YoloStopReason(4))
		stops := 0
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			if len(argv) > 1 && argv[1] == "stop" {
				stops++
			}
			return ExecResult{Ran: true} // no container of the name exists
		}
		if rc := o.finishStop(cname, "podman", 0); rc != 0 {
			t.Errorf("rc %d:\n%s", rc, out.String())
		}
		if stops != 0 {
			t.Errorf("the reap stopped the jail %d more times after `yolo stop` did", stops)
		}
		if rec, _ := readJailStop(cname); rec.Reason != YoloStopReason(4) {
			t.Errorf("the reap replaced the stop's record with %q", rec.Reason)
		}
		if _, ok := readKeeperRecord(cname); ok {
			t.Error("the reap left the dead keeper's start record")
		}
		if _, ok := readOwnerPID(cname); ok {
			t.Error("the reap left the dead keeper's owner-PID file")
		}
		if probeKeeper(cname) != keeperGone || sessionLockHeld(t, cname) {
			t.Error("the reap left a lock held")
		}
	})
	t.Run("an older yolo's jail", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		o := goldenOptions("/ws", t.TempDir())
		var out bytes.Buffer
		o.Stdout, o.Stderr = &out, &out
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			t.Errorf("the stop touched an older yolo's jail: %v", argv)
			return ExecResult{Ran: true}
		}
		if rc := o.finishStop("yolo-old", "podman", 0); rc != 0 || out.Len() != 0 {
			t.Errorf("rc %d:\n%s", rc, out.String())
		}
	})
}
