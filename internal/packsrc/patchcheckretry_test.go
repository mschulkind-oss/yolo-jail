package packsrc

// patchcheckretry_test.go pins what a check that could not REACH its upstream leaves on the record
// (patchcheck.go; docs/reference/patched-forks.md §4.2): a failed fetch — DNS, a refused connection,
// TLS — is a fault of the moment, which a caller with nothing built to run (RetryFailedFetch) does
// not wait out; an answer from the upstream (a base it lacks after a fetch that worked) still
// holds for the interval; and a by-id fetch of the base that could not reach the upstream is a
// failed fetch, never the verdict that the upstream lacks the commit.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// unreachableGit is a git under which every fetch fails as a machine with no DNS yet does, while the
// file it returns exists.
func unreachableGit(t *testing.T) (git, offline string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	offline = filepath.Join(dir, "offline")
	git = filepath.Join(dir, "git")
	script := "#!/bin/sh\nif [ -e " + shquote.Quote(offline) + " ]; then for a in \"$@\"; do if [ \"$a\" = fetch ] || [ \"$a\" = ls-remote ]; then " +
		"echo \"fatal: unable to access 'https://github.com/example/tool/': Could not resolve host: github.com\" >&2; " +
		"exit 128; fi; done; fi\nexec " + shquote.Quote(real) + " \"$@\"\n"
	if err := os.WriteFile(git, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(offline, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return git, offline
}

func (u *patchedUpstream) retryCheck(t *testing.T, w PatchedWant) CheckResult {
	t.Helper()
	res := u.store.CheckPatched(w, CheckOptions{RetryFailedFetch: true, Now: func() time.Time { return u.now }})
	if res.Err != nil {
		t.Fatalf("CheckPatched: %v", res.Err)
	}
	return res
}

// A FAILED FETCH IS NOT THE UPSTREAM'S STATE for a caller with nothing to run: inside the hour the
// attempt stamped, RetryFailedFetch checks again, and once the network is up finds the list. A
// caller with a good build keeps the throttle (TestAFailedFetchStillStampsTheAttempt).
func TestAFailedFetchIsRetriedByACallerWithNothingToRun(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	u.release(t, "v1.1.0", map[int]string{14: "fourteen"})
	git, offline := unreachableGit(t)
	u.store.Git = git
	u.now = u.now.Add(2 * time.Hour)
	if res := u.check(t, w, false); !res.Ran || res.Record.Check.FetchErr == "" {
		t.Fatalf("an offline check = %+v, want its failed fetch recorded", res.Record.Check)
	}
	u.now = u.now.Add(10 * time.Minute)
	if again := u.check(t, w, false); again.Ran {
		t.Error("a caller with a good build re-checked inside the interval after a failed fetch")
	}
	if err := os.Remove(offline); err != nil {
		t.Fatal(err)
	}
	res := u.retryCheck(t, w)
	if f := res.Record.Check; !res.Ran || f.FetchErr != "" || f.Problem != "" || listLabels(f.List) != "v1.1.0 v1.0.0" ||
		!strings.Contains(res.Due, "could not reach the upstream") {
		t.Errorf("a retry inside the hour = %+v (due %q), want it run and the new version found", f, res.Due)
	}
}

// AN ANSWER FROM THE UPSTREAM STILL HOLDS: a base the upstream lacks, found after a fetch that
// worked, is the upstream's verdict, and RetryFailedFetch does not re-ask it inside the hour.
func TestAnUpstreamsAnswerHoldsForTheIntervalEvenForARetry(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	w.Base = strings.Repeat("b", 40)
	res := u.check(t, w, false)
	if f := res.Record.Check; f.FetchErr != "" || !strings.Contains(f.Problem, "is not a commit of") {
		t.Fatalf("a missing base = %+v, want the upstream's verdict", f)
	}
	u.now = u.now.Add(10 * time.Minute)
	if again := u.retryCheck(t, w); again.Ran {
		t.Error("a retry re-asked an upstream that had answered, inside the interval")
	}
}

// A BY-ID FETCH OF THE BASE THAT COULD NOT REACH THE UPSTREAM is a failed fetch: the mirror's branch
// is fresh, so the step fetches nothing, and the base's own fetch meets no DNS. The record says the
// fetch failed, not that the upstream lacks the commit, and a retry once the network is up finds it.
func TestABaseFetchThatCouldNotReachTheUpstreamIsAFailedFetch(t *testing.T) {
	u := newPatchedUpstream(t)
	w := u.want(t, "main", "")
	u.check(t, w, false)
	newBase := u.release(t, "", map[int]string{14: "fourteen"})
	git, offline := unreachableGit(t)
	u.store.Git = git
	w.Base = newBase
	u.now = u.now.Add(10 * time.Minute)
	res := u.check(t, w, false)
	f := res.Record.Check
	if !res.Ran || f.FetchErr == "" || strings.Contains(f.Problem, "is not a commit of") ||
		!strings.Contains(f.Problem, "is not in this machine's copy") || !strings.Contains(f.Problem, "network") {
		t.Fatalf("an unreachable base fetch = %+v, want a failed fetch naming the network", f)
	}
	if err := os.Remove(offline); err != nil {
		t.Fatal(err)
	}
	u.now = u.now.Add(time.Minute)
	res = u.retryCheck(t, w)
	if f := res.Record.Check; !res.Ran || f.FetchErr != "" || f.Problem != "" {
		t.Errorf("the retry once the network was up = %+v, want the base fetched", f)
	}
}

// TRANSPORT FAILURES ARE TOLD FROM ANSWERS by git's, curl's and Go's own words.
func TestTransportFailureTellsReachingFromAnswers(t *testing.T) {
	for _, c := range []struct {
		err  string
		want bool
	}{
		{"fatal: unable to access 'https://github.com/x/': Could not resolve host: github.com", true},
		{"fatal: unable to access 'https://h/x/': Failed to connect to h port 443 after 3 ms: Connection refused", true},
		{"fatal: unable to access 'https://h/x/': SSL certificate problem: unable to get local issuer certificate", true},
		{"fatal: unable to access 'https://h/x/': The requested URL returned error: 503", true},
		{"git fetch origin timed out after 1m0s: context deadline exceeded", true},
		{`Get "https://registry.npmjs.org/x": dial tcp: lookup registry.npmjs.org: no such host`, true},
		{"fatal: remote error: upload-pack: not our ref 0123", false},
		{"fatal: couldn't find remote ref refs/heads/nope", false},
		{"fatal: repository 'https://github.com/x/nope/' not found", false},
		{"remote: Invalid username or password. fatal: Authentication failed", false},
	} {
		if got := TransportFailure(c.err); got != c.want {
			t.Errorf("TransportFailure(%q) = %v, want %v", c.err, got, c.want)
		}
	}
}
