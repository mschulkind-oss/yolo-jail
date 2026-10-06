package entrypoint

// prelaunchrefreshdue_test.go RUNS the pre-launch refresh's DUE-ON-CHANGE trigger
// (packdecl.Refresh.DueOnChange; docs/design/pi-git-extension-caching.md, "The first-install
// race"): the refresh is also due when a watched file's CONTENT has never been refreshed with
// in the store its lock names, and a launch that finds the lock held for content it has never refreshed
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

// seenDir is the refresh's seen-content markers where production names them, beside its lock
// (XB-D14).
func (p *prelaunchProbe) seenDir() string {
	return filepath.Join(p.home, filepath.FromSlash(RefreshSeenRel(probeLockRel, "tool")))
}

// The wait loop's poll, as the launcher bakes it and as the waiting cells replace it.
// The loop counts one unit of UPDATE_TIMEOUT per poll, so under the shortened poll a unit
// is waitPoll rather than a second. The baked spelling pairs the one-second sleep with that
// count, and bodyPatch refuses to run a cell whose literal is gone, so a loop that stopped
// sleeping a second per counted unit still fails these cells, as the real-second waits
// they used to spend did.
//
// The replacement also writes waitPollMark to stderr on every poll, with a shell builtin, and
// the cells COUNT polls instead of timing the launcher. Elapsed time measures the machine as
// much as the loop: one launcher run starts about twenty processes (mkdir, date, stat, cksum
// and the rest) around the wait, so where starting a process costs tens of milliseconds
// instead of one, the budgets these cells used to set were spent before the loop was reached
// or while it ran: with every exec delayed on Linux, the timed versions of all three passed at
// 60ms a process start and failed at 80ms. A count of polls is the same number on every
// machine.
const (
	waitPollBaked = "\n        sleep 1\n        waited=$((waited + 1))\n"
	waitPollMark  = "yolo-test: refresh-lock wait poll"
	waitPollFast  = "\n        echo '" + waitPollMark + "' >&2\n        sleep 0.1\n        waited=$((waited + 1))\n"
	waitPoll      = 100 * time.Millisecond
)

// waitPolls counts the polls a launcher run made under waitPollFast.
func waitPolls(stderr string) int { return strings.Count(stderr, waitPollMark+"\n") }

// onStderrMark is a probe stderrWatch that calls fire once the launcher has written mark n
// times. It runs on os/exec's one copying goroutine for stderr, so it needs no lock, and it
// fires as the launcher writes the mark, not after a sleep chosen to be long enough.
type onStderrMark struct {
	mark  string
	n     int
	fire  func()
	seen  strings.Builder
	fired bool
}

func (w *onStderrMark) Write(b []byte) (int, error) {
	w.seen.Write(b)
	if !w.fired && strings.Count(w.seen.String(), w.mark) >= w.n {
		w.fired = true
		w.fire()
	}
	return len(b), nil
}

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

// TestRefreshDueOnChangeFailureIsThrottled: a refresh that exits non-zero records no content key,
// so its content stays due and a later launch retries the install under the lock instead of
// leaving it to the program — but it records WHEN it failed (XB-D26 of
// docs/design/pi-extension-store-builds.md), so that content is due again only once the failure
// is older than the interval, never at every launch an offline hour makes. A success then
// records the key and drops the failure.
func TestRefreshDueOnChangeFailureIsThrottled(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "", "FAKE_REFRESH_RC=1")
	entries, _ := os.ReadDir(p.seenDir())
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".failed") {
		t.Fatalf("a failed refresh must record its failure and no content key, found %v", entries)
	}
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 1 {
		t.Fatalf("a launch inside the interval retried a refresh that failed on this content (ran %d)", n)
	}
	failed := filepath.Join(p.seenDir(), entries[0].Name())
	backdatePath(t, failed, 2*time.Hour)
	backdatePath(t, p.stampPath(), 2*time.Hour)
	p.run(t, "")
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("want the failure retried once past the interval and then settled (ran %d)", n)
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Errorf("a refresh that succeeded left the failure behind (err=%v)", err)
	}
}

// TestAFailureOnOtherContentDoesNotThrottleNewContent: the failure is the CONTENT's, so a launch
// whose watched file changed since is due at once.
func TestAFailureOnOtherContentDoesNotThrottleNewContent(t *testing.T) {
	p := newDueProbe(t)
	p.setWatched(t, `{"packages":["git:a"]}`)
	p.run(t, "", "FAKE_REFRESH_RC=1")
	p.setWatched(t, `{"packages":["git:b"]}`)
	p.run(t, "")
	if n := countLine(p.logLines(t), "REFRESH"); n != 2 {
		t.Errorf("new content after a failure on other content was not refreshed (ran %d)", n)
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
	// UPDATE_TIMEOUT stays 60, so the bound is 60 polls.
	p.bodyPatch = map[string]string{waitPollBaked: waitPollFast}
	// The holder releases when the launcher writes its SECOND poll mark, never on a clock. The
	// loop writes that mark only after its first poll found the lock still held and kept
	// waiting, so however long this machine takes to reach the wait, the launcher has waited
	// on a real holder before the release, and can only have refreshed after it.
	p.stderrWatch = &onStderrMark{mark: waitPollMark + "\n", n: 2, fire: func() {
		_ = os.Remove(p.lockPath())
	}}
	stdout, stderr := p.run(t, "")
	log := p.logLines(t)
	if n := waitPolls(stderr); n < 2 {
		t.Errorf("the launch did not wait for the holder: %d polls, want at least 2 "+
			"(the holder releases on the second):\n%s", n, stderr)
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
	// Counted, not timed: a launch that entered the wait loop would write one mark per poll,
	// sixty of them before giving up.
	p.bodyPatch = map[string]string{waitPollBaked: waitPollFast}
	_, stderr := p.run(t, "")
	if n := waitPolls(stderr); n != 0 || strings.Contains(stderr, "waiting for it") {
		t.Errorf("a held lock for seen content must not be waited on (%d polls):\n%s", n, stderr)
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
	// Five polls of waitPoll: more polls than the two one-second ones this cell used to wait,
	// so a loop that gives up early still falls short of the bound.
	p.bodyPatch = map[string]string{
		"\nUPDATE_TIMEOUT=60 ": "\nUPDATE_TIMEOUT=5 ",
		waitPollBaked:          waitPollFast,
	}
	start := time.Now()
	stdout, stderr := p.run(t, "")
	// The poll COUNT pins UPDATE_TIMEOUT being honored: a loop ignoring the patched value
	// polls 60 times, one giving up early fewer than 5. There is no upper time bound, because
	// the run around those polls costs whatever this machine charges to start its processes.
	// The lower bound stays: a slow machine only lengthens the wait, so it fails only a loop
	// whose polls stopped sleeping.
	if n := waitPolls(stderr); n != 5 {
		t.Errorf("the wait must last UPDATE_TIMEOUT polls: want 5, got %d\n%s", n, stderr)
	}
	if el := time.Since(start); el < 5*waitPoll {
		t.Errorf("five polls of %s took only %s: the polls stopped sleeping", waitPoll, el)
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
