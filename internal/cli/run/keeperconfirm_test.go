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
