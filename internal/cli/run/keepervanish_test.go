package run

// keepervanish_test.go pins a keeper whose container the runtime loses under it (keepervanish.go):
// the keeper notices from its own `podman wait`, ends as it does when the container exits, and
// records why, naming a podman refresh when one happened since the jail started. Each test drives
// the fake runtime to the event (the wait's failure, the refresh in the event log) and waits on the
// keeper's own answer to it; the bounds are failure guards only.

import (
	"strings"
	"testing"
	"time"
)

// vanishFixture is a keeper whose fake runtime's wait blocks until the test sends its answer, with
// one session in, as a fresh launch's first session is (JL-D16).
func vanishFixture(t *testing.T, refreshes ...time.Time) (*keeperFixture, *sessionLock) {
	t.Helper()
	var session *sessionLock
	// The keeper runs only once the fake runtime is set up: its Options' last tune waits for it.
	set := make(chan struct{})
	f := startKeeperFixtureWith(t, true, func(p *keeperPlan) {
		lock, _, err := takeSessionLock(p.Cname)
		if err != nil {
			t.Fatal(err)
		}
		session = lock
	}, func(*Options) { <-set })
	defer close(set)
	f.jail.mu.Lock()
	f.jail.waitAnswer = make(chan ExecResult, 1)
	f.jail.eventsAsked = make(chan struct{})
	f.jail.refreshes = refreshes
	f.jail.mu.Unlock()
	set <- struct{}{}
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	return f, session
}

// awaitEventsAsked is the keeper asking the runtime's event log why its jail ended, which it does
// only once it has taken the container's end; the session then leaves, as a real one does once its
// exec fails on the gone container.
func awaitEventsAsked(t *testing.T, f *keeperFixture, session *sessionLock) {
	t.Helper()
	select {
	case <-f.jail.eventsAsked:
	case <-time.After(30 * time.Second):
		t.Fatalf("the keeper never took its container's removal as the jail's end; its log:\n%s", f.keeperLog())
	}
	session.release()
}

// TestTheKeeperNoticesItsContainerRemovedUnderIt: podman's database loses the container while its
// keeper holds it (the 2026-11-09 incident: a second suite's podman recreated /run/libpod, and the
// refresh forgot the --rm jail). The keeper's `podman wait` fails, the runtime then answers that no
// container of the name exists, and the keeper ends as it does when the container exits: no stop of
// its own, and a stop record saying the runtime no longer lists the container, with what starts it
// again, which every session the end cut short prints.
func TestTheKeeperNoticesItsContainerRemovedUnderIt(t *testing.T) {
	f, session := vanishFixture(t)
	f.jail.vanish()
	awaitEventsAsked(t, f, session)
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if n := f.jail.stopCount(); n != 0 {
		t.Errorf("the keeper stopped a container the runtime no longer has %d times", n)
	}
	rec, ok := readJailStop(f.cname)
	if !ok || rec.Reason != containerVanishedReason {
		t.Errorf("the stop record is %+v (%v), want %q", rec, ok, containerVanishedReason)
	}
	if !strings.Contains(containerVanishedReason, "Start it again") {
		t.Errorf("the reason names no next step: %q", containerVanishedReason)
	}
	if log := f.keeperLog(); !strings.Contains(log, containerVanishedReason) {
		t.Errorf("the keeper's log does not say why its jail ended:\n%s", log)
	}
}

// TestTheKeeperNamesThePodmanRefreshThatRemovedItsContainer: the same loss, with a refresh in
// podman's event log since the jail started: the record names the reset and when it was.
func TestTheKeeperNamesThePodmanRefreshThatRemovedItsContainer(t *testing.T) {
	at := time.Now().Add(time.Hour).Truncate(time.Second)
	f, session := vanishFixture(t, at)
	f.jail.vanish()
	awaitEventsAsked(t, f, session)
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	want := podmanRefreshReason(at, true)
	if rec, ok := readJailStop(f.cname); !ok || rec.Reason != want {
		t.Errorf("the stop record is %+v (%v), want %q", rec, ok, want)
	}
	for _, s := range []string{"podman reset its state at " + at.Format("15:04:05"), "removed this jail's container", "Start it again"} {
		if !strings.Contains(want, s) {
			t.Errorf("the refresh reason %q does not say %q", want, s)
		}
	}
	asked := false
	for _, c := range f.jail.calls {
		asked = asked || (strings.HasPrefix(c, "podman events ") && strings.Contains(c, "event=refresh") && strings.Contains(c, "--since"))
	}
	if !asked {
		t.Errorf("the keeper did not ask podman's event log for a refresh since the jail started: %q", f.jail.calls)
	}
}

// TestAFailedWaitWithTheContainerStillThereEndsNothing: a `podman wait` that fails while the runtime
// still lists the container is no end ("could not ask" is never "gone", JL-P3): the keeper holds the
// jail, says it now learns of the end from its client alone, and ends it normally when its last
// session leaves.
func TestAFailedWaitWithTheContainerStillThereEndsNothing(t *testing.T) {
	f, session := vanishFixture(t)
	f.jail.waitAnswer <- ExecResult{Ran: true, RC: 125, Stderr: "Error: database is locked"}
	session.release()
	if rc := f.wait(); rc != 0 {
		t.Errorf("the keeper ended %d", rc)
	}
	if rec, _ := readJailStop(f.cname); rec.Reason != lastSessionLeftReason {
		t.Errorf("the stop recorded %q, want the drain's %q", rec.Reason, lastSessionLeftReason)
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the keeper stopped the jail %d times, want once, at the drain", f.jail.stopCount())
	}
}

// TestPodmanRefreshSinceReadsTheEventLog pins the event query: only podman has one, a refresh
// before the jail began is not this jail's, the latest refresh is the one named, and an event log
// that cannot be read claims nothing.
func TestPodmanRefreshSinceReadsTheEventLog(t *testing.T) {
	since := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name, rt string
		res      ExecResult
		want     time.Time
		ok       bool
	}{
		{"apple container has none", "container", ExecResult{Ran: true, Stdout: `{"Status":"refresh","time":1800000100}` + "\n"}, time.Time{}, false},
		{"none", "podman", ExecResult{Ran: true}, time.Time{}, false},
		{"unreadable", "podman", ExecResult{Ran: true, RC: 125, Stdout: `{"Status":"refresh","time":1800000100}` + "\n"}, time.Time{}, false},
		{"before the jail", "podman", ExecResult{Ran: true, Stdout: `{"Status":"refresh","time":1799999999}` + "\n"}, time.Time{}, false},
		{"the latest", "podman", ExecResult{Ran: true, Stdout: `{"Status":"refresh","time":1800000100}` + "\n" + `{"Status":"refresh","time":1800000200}` + "\n"}, time.Unix(1_800_000_200, 0), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := NewDefaultOptions()
			var argv []string
			o.Exec = func(a []string, _ string, _ []string, _ time.Duration) ExecResult {
				argv = a
				return tc.res
			}
			at, ok := o.podmanRefreshSince(tc.rt, since)
			if ok != tc.ok || !at.Equal(tc.want) {
				t.Errorf("got %v %v, want %v %v", at, ok, tc.want, tc.ok)
			}
			if tc.rt != "podman" && argv != nil {
				t.Errorf("asked %q of a runtime with no refresh", argv)
			}
			if tc.rt == "podman" && !strings.Contains(strings.Join(argv, " "), "--since 1800000000") {
				t.Errorf("the query %q is not bounded by the jail's start", argv)
			}
		})
	}
}
