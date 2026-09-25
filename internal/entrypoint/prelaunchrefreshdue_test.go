package entrypoint

// prelaunchrefreshdue_test.go RUNS the pre-launch refresh's DUE-ON-CHANGE trigger
// (packdecl.Refresh.DueOnChange; docs/design/pi-git-extension-caching.md, "The first-install
// race"): the refresh is also due when a watched file's CONTENT has never been refreshed with
// on this machine, and a launch that finds the lock held for content it has never refreshed
// waits for the holder instead of letting the program install outside the lock.
//
// Same fake program and probe as prelaunchrefresh_test.go: nothing reaches a network and no
// agent starts.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dueRel = ".cfg/settings.json"

// newDueProbe is a probe whose refresh watches dueRel.
func newDueProbe(t *testing.T) *prelaunchProbe {
	t.Helper()
	p := newPrelaunchProbe(t, false)
	p.refresh.DueOnChange = []string{dueRel}
	return p
}

func (p *prelaunchProbe) setWatched(t *testing.T, content string) {
	t.Helper()
	f := filepath.Join(p.home, dueRel)
	if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (p *prelaunchProbe) seenDir() string { return filepath.Join(p.stamps, "refresh", "tool.seen") }

// TestRefreshDueOnChangeFollowsContent: inside UPDATE_INTERVAL, a launch refreshes when and only
// when the watched content is one no refresh has succeeded for — a rewrite with the same bytes
// (what every boot does to a composed file) is not a change, new bytes are, and bytes already
// refreshed with once are not again.
func TestRefreshDueOnChangeFollowsContent(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "")
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Fatalf("unchanged content inside the interval must not refresh again (ran %d)", n)
	}
	p.setWatched(t, `{"packages":["git:a"]}`) // same bytes, new mtime
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Fatalf("a rewrite with the same bytes is not a change (ran %d)", n)
	}
	p.setWatched(t, `{"packages":["git:a","git:b"]}`)
	_, stderr := p.run(t, "")
	log := p.logLines(t)
	if n := countLine(log, "REFRESH"); n != 2 {
		t.Fatalf("changed content must make the refresh due inside the interval (ran %d)\n%s", n, stderr)
	}
	if countLine(log, "LOCKED") != 2 {
		t.Errorf("the change-triggered refresh must run under the lock: %v", log)
	}
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("content already refreshed with once is not due again (ran %d)", n)
	}
}

// TestRefreshDueOnChangeCountsAbsentToPresent: an absent file has a content of its own, so the
// file appearing is a change.
func TestRefreshDueOnChangeCountsAbsentToPresent(t *testing.T) {
	p := newDueProbe(t)
	p.run(t, "")
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Fatalf("an absent file that stays absent is not a change (ran %d)", n)
	}
	p.setWatched(t, `{}`)
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("the watched file appearing must make the refresh due (ran %d)", n)
	}
}

// TestRefreshDueOnChangeFailureRecordsNothing: a refresh that exits non-zero leaves its content
// due, so the next launch retries the install under the lock instead of leaving it to the
// program; a success then records it.
func TestRefreshDueOnChangeFailureRecordsNothing(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "", "FAKE_REFRESH_RC=1")
	if entries, _ := os.ReadDir(p.seenDir()); len(entries) != 0 {
		t.Fatalf("a failed refresh must record no content key, found %d", len(entries))
	}
	p.run(t, "")
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("want the failure retried once and then settled (ran %d)", n)
	}
}

// TestRefreshWaitsOutAHolderForNewContent: the lock is held and this launch's content has never
// been refreshed with — the one case a held lock is waited on, because proceeding lets the
// program install what the content names outside the lock, into the store the holder is
// writing. Once the holder releases, the launch takes the lock and refreshes.
func TestRefreshWaitsOutAHolderForNewContent(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:new"]}`)
	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(1500 * time.Millisecond)
		_ = os.Remove(p.lockPath())
	}()
	start := time.Now()
	stdout, stderr := p.run(t, "")
	log := p.logLines(t)
	if time.Since(start) < time.Second {
		t.Errorf("the launch did not wait for the holder")
	}
	if !strings.Contains(stderr, "waiting for it") {
		t.Errorf("the wait must be said:\n%s", stderr)
	}
	if countLine(log, "REFRESH") != 1 || countLine(log, "LOCKED") != 1 {
		t.Errorf("after the holder released, the launch must refresh under the lock: %v\n%s", log, stderr)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestRefreshDoesNotWaitForContentAlreadyRefreshed: a held lock for content this machine has
// refreshed with still means "run what is installed" (OQ-2) — the stamp being old is not a
// reason to block the launch.
func TestRefreshDoesNotWaitForContentAlreadyRefreshed(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "")
	backdatePath(t, p.stampPath(), 2*time.Hour)
	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, stderr := p.run(t, "")
	if time.Since(start) > 900*time.Millisecond || strings.Contains(stderr, "waiting for it") {
		t.Errorf("a held lock for seen content must not be waited on:\n%s", stderr)
	}
	if !strings.Contains(stderr, "another refresh holds") {
		t.Errorf("the skip must be said:\n%s", stderr)
	}
}

// TestRefreshWaitIsBounded: a holder that never finishes costs UPDATE_TIMEOUT, then the program
// launches with what is installed.
func TestRefreshWaitIsBounded(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:new"]}`)
	if err := os.Mkdir(p.lockPath(), 0o755); err != nil {
		t.Fatal(err)
	}
	p.bodyPatch = map[string]string{"\nUPDATE_TIMEOUT=60 ": "\nUPDATE_TIMEOUT=2 "}
	start := time.Now()
	stdout, stderr := p.run(t, "")
	if el := time.Since(start); el < 2*time.Second || el > 20*time.Second {
		t.Errorf("the wait must last about UPDATE_TIMEOUT, took %s", el)
	}
	log := p.logLines(t)
	if countLine(log, "REFRESH") != 0 || !strings.Contains(stderr, "another refresh holds") {
		t.Errorf("after the bound the launch must run what is installed:\n%v\n%s", log, stderr)
	}
	assertProgramLaunched(t, log, stdout)
}

// TestRefreshWithoutDueOnChangeRendersTheTriggerOff: a refresh declaring no watched files bakes
// the trigger off and never writes a content key.
func TestRefreshWithoutDueOnChangeRendersTheTriggerOff(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "npm", true: "native"}[native], func(t *testing.T) {
			p := newPrelaunchProbe(t, native)
			p.run(t, "")
			body, err := os.ReadFile(p.script)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), "\nHAS_REFRESH_DUE=0\n") ||
				!strings.Contains(string(body), "\nREFRESH_DUE_ON_CHANGE=()\n") {
				t.Errorf("a refresh with no due_on_change must bake the trigger off")
			}
			if _, err := os.Stat(p.seenDir()); !os.IsNotExist(err) {
				t.Errorf("no content key may be written without the trigger (err=%v)", err)
			}
		})
	}
}
