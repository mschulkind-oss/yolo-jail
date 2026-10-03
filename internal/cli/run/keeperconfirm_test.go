package run

// keeperconfirm_test.go pins what a keeper reads as its jail's end, and as its own right to the jail's
// name: the runtime client it started the container with ending is not the container ending
// (docs/design/jail-lifetime-last-session-wins.md §9.5 item 3: "It confirms with the same probe";
// JL-P3: "could not ask" is never "gone"), a stop that did not take leaves the jail unkept, and a
// probe's instant on the liveness lock is not another keeper.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// TestAMainProcessClientThatDiesUnderARunningJailEndsNothing: the main process's client exiting is
// the container's end only once the runtime says no container of the name runs. A client that dies
// while its container runs (killed, or a Mac's remote client losing its podman machine) leaves the
// keeper holding the jail: its host services stay up for the sessions still in it, and the jail
// ends as it always does, when its last session leaves and the keeper stops it.
func TestAMainProcessClientThatDiesUnderARunningJailEndsNothing(t *testing.T) {
	held := heldDir(t)
	die := filepath.Join(held, "die")
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		// The client prints the boot, then dies on its own; the fake runtime still says the
		// container runs, since nothing stopped it.
		p.RunCmd = []string{"sh", "-c", recordPID(held) + `echo "` + entrypoint.BootReadyLine + `" >&2; ` +
			holdUntil(die) + `; exit 1`}
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	if err := os.WriteFile(die, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(f.plan.SocketsDir); err != nil {
		t.Errorf("the keeper took the host services down under a running jail: %v", err)
	}
	if probeKeeper(f.cname) != keeperAlive {
		t.Fatal("the keeper let go of a jail that still runs, with a session in it")
	}

	// The last session leaves: the keeper ends the jail in order, stopping it.
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the keeper stopped the jail %d times, want once: a jail its client died under is still its to stop", f.jail.stopCount())
	}
	if rec, _ := readJailStop(f.cname); rec.Reason != lastSessionLeftReason {
		t.Errorf("the stop recorded %q, want %q", rec.Reason, lastSessionLeftReason)
	}
}

// TestAJailItsKeeperCouldNotStopIsLeftUnkept: a keeper whose stop did not end its container (a
// wedged runtime, a stop that timed out) takes down what it runs itself, but not the two records
// that say the jail had a keeper: its owner-PID file and its start record. With them, the jail it
// leaves behind reads as UNKEPT, which every reader already handles: an arrival is refused and
// pointed at `yolo stop` (JL-D13), the last session or `yolo stop` reaps it (JL-D30), and the
// orphan sweep reaps it once no session is in it (JL-D7). Without them the container, whose main
// process is a hold that never ends by itself, would run on with no owner anything can prove dead,
// and a new terminal would enter it with no host services and no word of why.
func TestAJailItsKeeperCouldNotStopIsLeftUnkept(t *testing.T) {
	saved := keeperGoneAttempts
	keeperGoneAttempts = 2
	t.Cleanup(func() { keeperGoneAttempts = saved })
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	f.jail.mu.Lock()
	f.jail.stuck = true
	f.jail.mu.Unlock()
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if f.jail.stopCount() != 1 {
		t.Fatalf("the keeper stopped the jail %d times, want once", f.jail.stopCount())
	}
	if pid, ok := readOwnerPID(f.cname); !ok || pid != os.Getpid() {
		t.Errorf("the owner-PID file of a jail still running names %d (%v), want its keeper", pid, ok)
	}
	if rec, ok := readKeeperRecord(f.cname); !ok || rec.PID != os.Getpid() {
		t.Errorf("the start record of a jail still running is %+v (%v), want its keeper's", rec, ok)
	}
	if probeKeeper(f.cname) != keeperGone {
		t.Error("the keeper's liveness lock is still held after its end")
	}
	// The forced removal of a stopped leftover (JL-D82) removes a running container too, so it must
	// never be run on one.
	if rm := f.jail.removals(); len(rm) != 0 {
		t.Errorf("the keeper ran a removal on a container that still runs: %q", rm)
	}
}

// TestAKeeperThatCannotAskWhetherItsContainerRunsRemovesNothing: the forced removal of a stopped
// leftover (JL-D82) is run only once the runtime has said the container does not run. A runtime that
// cannot answer has not said that (JL-P3: "could not ask" is never "gone"), and the container may run,
// so the keeper removes nothing, waits its bound, and leaves the jail unkept.
func TestAKeeperThatCannotAskWhetherItsContainerRunsRemovesNothing(t *testing.T) {
	saved := keeperGoneAttempts
	keeperGoneAttempts = 2
	t.Cleanup(func() { keeperGoneAttempts = saved })
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	f.jail.mu.Lock()
	f.jail.pinned, f.jail.runningUnknown = true, true
	f.jail.mu.Unlock()
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if rm := f.jail.removals(); len(rm) != 0 {
		t.Errorf("the keeper ran a removal on a container its runtime could not say was stopped: %q", rm)
	}
	if _, ok := readKeeperRecord(f.cname); !ok {
		t.Error("the keeper removed its start record, so a jail whose container is still there no longer reads as unkept")
	}
}

// TestAKeeperWaitsOutAProbesHoldOnItsLivenessLock: every probe of a keeper (probeKeeper, which a
// last session's streamed teardown, `yolo stop`, an arrival's wait and the reaper each make) holds
// the liveness lock exclusively for an instant. The fresh launch that spawns a keeper found no keeper
// under the launch lock and handed that lock over, so a hold the new keeper meets is such an instant,
// not a keeper: it waits it out and starts, rather than refusing the launch as though the previous
// jail's keeper were still running. And it leaves the log alone until it holds the lock, since the
// log is truncated at a keeper's start and one it met could be a live keeper's.
func TestAKeeperWaitsOutAProbesHoldOnItsLivenessLock(t *testing.T) {
	var session *sessionLock
	var logBefore string
	seen := make(chan string, 1) // the log as it stood while the probe held the lock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		if err := os.MkdirAll(filepath.Dir(keeperLogPath(p.Cname)), 0o755); err != nil {
			t.Fatal(err)
		}
		logBefore = "a line of the keeper that holds the lock\n"
		if err := os.WriteFile(keeperLogPath(p.Cname), []byte(logBefore), 0o600); err != nil {
			t.Fatal(err)
		}
		probe, err := holdLivenessLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(50 * time.Millisecond)
			b, _ := os.ReadFile(keeperLogPath(p.Cname))
			seen <- string(b)
			time.Sleep(150 * time.Millisecond)
			releaseLock(probe)
		}()
	})
	if !f.relay() {
		t.Fatalf("a probe's instant on the liveness lock refused the launch:\n%s", f.errOut.String())
	}
	if got := <-seen; got != logBefore {
		t.Errorf("a keeper that did not yet hold the liveness lock rewrote the log: %q", got)
	}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
}

// TestAKeeperRefusesANameAnotherKeeperHolds: a hold that outlasts keeperLivenessWait is another
// keeper, which two launches with no launch lock between them can each spawn: the new one refuses,
// starts nothing, and leaves the holder's log as it was.
func TestAKeeperRefusesANameAnotherKeeperHolds(t *testing.T) {
	saved := keeperLivenessWait
	keeperLivenessWait = 100 * time.Millisecond
	t.Cleanup(func() { keeperLivenessWait = saved })
	const theirs = "the other keeper's line\n"
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		if err := os.MkdirAll(filepath.Dir(keeperLogPath(p.Cname)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(keeperLogPath(p.Cname), []byte(theirs), 0o600); err != nil {
			t.Fatal(err)
		}
		other, err := holdLivenessLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { releaseLock(other) })
	})
	if f.relay() {
		t.Fatal("a keeper started on a name another keeper holds")
	}
	if rc := f.wait(); rc != 1 {
		t.Errorf("the keeper exited %d, want 1", rc)
	}
	if !strings.Contains(f.errOut.String(), "Refusing to start this jail's keeper") {
		t.Errorf("the refusal did not say why:\n%s", f.errOut.String())
	}
	if strings.Contains(strings.Join(f.jail.calls, "\n"), "podman run") {
		t.Error("a refused keeper started the container")
	}
	if b, _ := os.ReadFile(keeperLogPath(f.cname)); string(b) != theirs {
		t.Errorf("a refused keeper rewrote the holder's log: %q", b)
	}
}

// TestAClientThatDiesBeforeReadyUnderARunningContainerHasItStopped is §9.5 item 1's "stops what it
// started": a main process's client that exits before the boot is done is a boot that failed, and
// the launch fails with it, but when the runtime says the container still runs, it is the keeper's
// to stop. The first build stopped nothing on that path, which only a refused boot's own exit had
// ever needed, and left a container whose hold never ends by itself.
func TestAClientThatDiesBeforeReadyUnderARunningContainerHasItStopped(t *testing.T) {
	f := startKeeperFixture(t, false, func(p *keeperPlan) {
		p.RunCmd = []string{"sh", "-c", `echo "a boot line" >&2; exit 1`}
	})
	if f.relay() {
		t.Fatal("a boot that never finished reached ready")
	}
	if rc := f.wait(); rc != 1 {
		t.Errorf("the keeper exited %d, want the client's 1", rc)
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the keeper stopped the container %d times, want once: it still ran when the client died", f.jail.stopCount())
	}
	if _, ok := readKeeperRecord(f.cname); ok {
		t.Error("the keeper left its start record for a container it stopped")
	}
}

// TestARefusedBootIsNotStoppedAgain: the ordinary way a client exits before ready is a refused boot,
// whose container is gone with it. The keeper asks, finds nothing running, and stops nothing.
func TestARefusedBootIsNotStoppedAgain(t *testing.T) {
	f := startKeeperFixture(t, false, func(p *keeperPlan) {
		p.RunCmd = []string{"sh", "-c", `echo "refused" >&2; exit 3`}
	})
	f.jail.mu.Lock()
	f.jail.stopped = true // the container went with its refused boot
	f.jail.mu.Unlock()
	if f.relay() {
		t.Fatal("a refused boot reached ready")
	}
	if rc := f.wait(); rc != 3 {
		t.Errorf("the keeper exited %d, want the refusal's 3", rc)
	}
	if f.jail.stopCount() != 0 {
		t.Errorf("the keeper stopped a container that had already gone (%d stops)", f.jail.stopCount())
	}
}

// TestAStoppedContainerItsRemovalLeftBehindIsRemovedAtOnce: a stop whose --rm removal failed leaves
// the stopped container behind, and waiting does not change that. Podman was measured doing it in a
// nested jail, on an exec session it still counted as live: one whose client was killed while podman
// was starting it, which a signal in the ready window does. A plain rm fails on the same session,
// and a forced one removes the container and still exits 125. The keeper used to poll the whole
// bound, 75 tries 200 ms apart, which its last session's quit and `yolo stop` stream, and then try a
// plain rm and leave the jail unkept, with a container that held its scratch volumes, so their
// detached remover waited out its own minute too. It removes the leftover at once, by force, and
// judges by whether the container is gone (JL-D82).
func TestAStoppedContainerItsRemovalLeftBehindIsRemovedAtOnce(t *testing.T) {
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	})
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	f.jail.mu.Lock()
	f.jail.pinned = true
	f.jail.mu.Unlock()
	left := time.Now()
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	bound := time.Duration(keeperGoneAttempts) * restartPollInterval
	if took := time.Since(left); took >= bound/4 {
		t.Errorf("the keeper took %s to end the jail after its last session left, waiting on a removal that had "+
			"already failed (its bound is %s)", took.Round(time.Millisecond), bound)
	}
	f.jail.mu.Lock()
	pinned := f.jail.pinned
	calls := strings.Join(f.jail.calls, "\n")
	f.jail.mu.Unlock()
	if pinned {
		t.Errorf("the keeper left the stopped container behind; the runtime saw:\n%s", calls)
	}
	if _, ok := readKeeperRecord(f.cname); ok {
		t.Error("the keeper left its start record, so the jail reads as unkept though its container is gone")
	}
	if _, ok := readOwnerPID(f.cname); ok {
		t.Error("the keeper left the owner-PID file, so the jail reads as unkept though its container is gone")
	}
	if log := f.keeperLog(); !strings.Contains(log, "removed the stopped container") {
		t.Errorf("the keeper's log does not say it removed the leftover:\n%s", log)
	}
}
