package packsrc

// npmeverylaunch_test.go pins an npm patched source's check at EVERY FRESH LAUNCH (docs/design/local-pack-clones.md
// LPC-D6, extended to npm by the owner's ruling that the hourly throttle is dropped wherever the check is cheap):
// a registry metadata read is cheap, so a dist-tag checked minutes ago is read again, and the launch's own deadline
// bounds the read, a miss being a failed check and never a refusal.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// AN NPM DIST-TAG CHECKED MINUTES AGO IS QUERIED AGAIN.
func TestAnNpmTagCheckedMinutesAgoIsQueriedAgain(t *testing.T) {
	hits := fakeRegistry(t, "pi-web-access", piWebAccessMeta)
	s := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	now := time.Unix(1_800_000_000, 0)
	w := PatchedWant{Owner: "p/pi-web-access", Source: "npm:pi-web-access@next"}
	if res := s.CheckPatched(w, CheckOptions{Now: func() time.Time { return now }}); !res.Ran {
		t.Fatal("the first check did not run")
	}
	later := now.Add(10 * time.Minute)
	res := s.CheckPatched(w, CheckOptions{Now: func() time.Time { return later }})
	if !res.Ran || atomic.LoadInt64(hits) != 2 {
		t.Errorf("a check ten minutes later: ran %v, registry asked %d times, want it asked again", res.Ran, atomic.LoadInt64(hits))
	}
}

// THE LAUNCH'S DEADLINE BOUNDS THE REGISTRY READ: one that does not finish is a failed check, never an error.
func TestAnNpmCheckPastTheLaunchDeadlineIsAFailedCheck(t *testing.T) {
	fakeRegistry(t, "pi-web-access", piWebAccessMeta)
	s := &Store{Dir: t.TempDir(), Getenv: noStagedTree}
	now := time.Unix(1_800_000_000, 0)
	w := PatchedWant{Owner: "p/pi-web-access", Source: "npm:pi-web-access@next"}
	s.CheckPatched(w, CheckOptions{Now: func() time.Time { return now }})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := s.CheckPatched(w, CheckOptions{Check: checkOf(ctx), Now: func() time.Time { return now.Add(time.Minute) }})
	if res.Err != nil || !res.Ran || !strings.Contains(res.Record.Check.FetchErr, "did not answer within 5s") {
		t.Fatalf("a registry read past the deadline: ran %v err %v check %+v", res.Ran, res.Err, res.Record.Check)
	}
}
