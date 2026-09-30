package run

// keeperconfirm_test.go pins how the keeper tells its container's end from the end of the runtime
// client it started the container with (docs/design/jail-lifetime-last-session-wins.md §9.5 item
// 3: "It confirms with the same probe"; JL-P3: "could not ask" is never "gone").

import (
	"os"
	"path/filepath"
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
	die := filepath.Join(t.TempDir(), "die")
	var session *sessionLock
	f := startKeeperFixture(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
		// The client prints the boot, then dies on its own; the fake runtime still says the
		// container runs, since nothing stopped it.
		p.RunCmd = []string{"sh", "-c", `echo "` + entrypoint.BootReadyLine + `" >&2; ` +
			`while [ ! -e "` + die + `" ]; do sleep 0.02; done; exit 1`}
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
}
