package run

import (
	"testing"
)

// TestAProbeIsNeverMistakenForALiveKeeper: two launches of one workspace probe the same keeper key
// at the same instant (refuseLegacyJail asks the legacy name before any lock). One probe's own
// instant on the liveness lock must not read to the other as a live keeper, and must not make a
// keeper taking the lock meanwhile conclude that another keeper holds it.
//
// Event-driven: the first probe stops while it holds the lock (livenessHeldHook) and waits until
// the second has either finished or reached the gate every take of the lock passes
// (livenessGateHook), whichever comes first. Without a gate the second finishes while the first
// holds the lock, and reads it.
func TestAProbeIsNeverMistakenForALiveKeeper(t *testing.T) {
	for _, tc := range []struct {
		name   string
		second func(cname string) bool // true when the second caller read a live keeper
	}{
		{"a concurrent probe reads no keeper", func(cname string) bool {
			return probeKeeper(cname) != keeperGone
		}},
		{"a keeper taking the lock meets no other keeper", func(cname string) bool {
			f, err := holdLivenessLock(cname)
			if err != nil {
				return true
			}
			releaseLock(f)
			return false
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			const cname = "yolo-probe-race"
			t.Cleanup(func() { livenessHeldHook, livenessGateHook = nil, nil })

			secondAtGate := make(chan struct{}, 1)
			secondDone := make(chan bool, 1)
			first := true
			livenessGateHook = func(string) {
				if first {
					return // the first caller's own pass
				}
				select {
				case secondAtGate <- struct{}{}:
				default:
				}
			}
			livenessHeldHook = func(c string) {
				if !first {
					return
				}
				first = false
				// The first probe holds the lock now: run the second, and wait for it to finish or
				// to reach the gate the first still holds.
				go func() { secondDone <- tc.second(c) }()
				select {
				case alive := <-secondDone:
					secondDone <- alive
				case <-secondAtGate:
				}
			}
			if got := probeKeeper(cname); got != keeperGone {
				t.Fatalf("the first probe read %v, want gone", got)
			}
			if <-secondDone {
				t.Errorf("the second caller read the first probe's instant on %s as a live keeper",
					livenessLockPath(cname))
			}

			// A real keeper still reads alive.
			livenessHeldHook, livenessGateHook = nil, nil
			live, err := holdLivenessLock(cname)
			if err != nil {
				t.Fatal(err)
			}
			defer releaseLock(live)
			if got := probeKeeper(cname); got != keeperAlive {
				t.Errorf("with a keeper holding the lock a probe read %v, want alive", got)
			}
		})
	}
}
