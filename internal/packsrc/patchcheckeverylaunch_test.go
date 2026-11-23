package packsrc

// patchcheckeverylaunch_test.go pins the patched fork's upstream check at EVERY FRESH LAUNCH
// (docs/design/local-pack-clones.md §5, LPC-D6): no hourly throttle, one ls-remote that fetches only a
// moved upstream, the stamp kept as the last attempt, and the launch's own 5-second deadline whose
// expiry leaves the held list and a failed-check record, never an error. A recording git shim and
// injected contexts stand in for the network and the clock.

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A CHECK INSIDE THE OLD HOUR NOW PROBES, and an unmoved upstream fetches nothing: the record keeps its
// list and takes the attempt's stamp.
func TestACheckInsideTheOldHourProbesAndAnUnmovedUpstreamFetchesNothing(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	first := u.check(t, w, false)
	git, ran, _ := recordingGit(t)
	u.store.Git = git
	u.now = u.now.Add(10 * time.Minute)
	res := u.check(t, w, false)
	if !res.Ran {
		t.Fatal("a check ten minutes after the last did not run: the hourly throttle still holds a launch")
	}
	if ran("ls-remote") != 1 || ran("fetch") != 0 {
		t.Errorf("git runs: ls-remote %d, fetch %d, want one probe and no fetch", ran("ls-remote"), ran("fetch"))
	}
	if listLabels(res.Record.Check.List) != listLabels(first.Record.Check.List) || res.Record.Check.FetchErr != "" {
		t.Errorf("list = %q, want the unmoved %q", listLabels(res.Record.Check.List), listLabels(first.Record.Check.List))
	}
	if res.Record.CheckedAt != u.now.Unix() {
		t.Errorf("stamp = %d, want the last attempt %d", res.Record.CheckedAt, u.now.Unix())
	}
}

// A MOVED UPSTREAM ADVANCES AS BEFORE, inside the old hour.
func TestAMovedUpstreamInsideTheOldHourIsFetchedAndListed(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	git, ran, _ := recordingGit(t)
	u.store.Git = git
	u.now = u.now.Add(time.Minute)
	res := u.check(t, w, false)
	if got := listLabels(res.Record.Check.List); !res.Ran || got != "v1.1.0 v1.0.0" {
		t.Errorf("list = %q (ran %v), want the new version found", got, res.Ran)
	}
	if ran("fetch") != 1 {
		t.Errorf("fetches = %d, want one fetch of the moved upstream", ran("fetch"))
	}
}

// A CHECK THAT HAS NOT ANSWERED WHEN ITS DEADLINE FIRES records the failed check and keeps the held list.
func TestAHungProbeRecordsAFailedCheckAndKeepsTheHeldList(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	bg := newProbeGate(t)
	u.store.Git = bg.git
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { bg.awaitStarted(t, 1); cancel() }()
	res := u.store.CheckPatched(w, CheckOptions{Check: checkOf(ctx), Now: func() time.Time { return u.now.Add(time.Minute) }})
	if res.Err != nil || !res.Ran {
		t.Fatalf("a hung probe = ran %v err %v, want a recorded failed check", res.Ran, res.Err)
	}
	f := res.Record.Check
	if !strings.Contains(f.FetchErr, "ls-remote timed out after 5s") || listLabels(f.List) != "v1.0.0" || f.Problem != "" {
		t.Errorf("check = %+v, want the timeout in FetchErr and the held list", f)
	}
}

// A MIRROR LOCKED PAST THE DEADLINE is the same failed check.
func TestAMirrorLockedPastTheDeadlineIsAFailedCheck(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	a, err := Parse(u.source("main"))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := u.store.lockMirror(a.Repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res := u.store.CheckPatched(w, CheckOptions{Check: checkOf(ctx), Now: func() time.Time { return u.now.Add(time.Minute) },
		Begin: func() (func(string), func()) { return func(string) { cancel() }, func() {} }})
	if res.Err != nil || !res.Ran || !strings.Contains(res.Record.Check.FetchErr, "waiting for the mirror lock timed out after 5s") {
		t.Fatalf("a locked mirror = ran %v err %v check %+v", res.Ran, res.Err, res.Record.Check)
	}
}

// A FAILED FETCH IS RETRIED BY THE NEXT LAUNCH whoever it is, with no RetryFailedFetch to ask for it.
func TestAFailedCheckIsRetriedByTheNextLaunch(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	git, offline := unreachableGit(t)
	u.store.Git = git
	u.now = u.now.Add(time.Minute)
	if res := u.check(t, w, false); !res.Ran || res.Record.Check.FetchErr == "" {
		t.Fatalf("an offline check = %+v, want its failed probe recorded", res.Record.Check)
	}
	if err := os.Remove(offline); err != nil {
		t.Fatal(err)
	}
	u.now = u.now.Add(time.Minute)
	res := u.check(t, w, false)
	if f := res.Record.Check; !res.Ran || f.FetchErr != "" || listLabels(f.List) != "v1.1.0 v1.0.0" {
		t.Errorf("the next launch's check = %+v, want the new version found", f)
	}
}
