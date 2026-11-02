package approvalwait

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const testID = "r-0123456789abcdef0123456789abcdef"

// workspace is a workspace that has launched a jail: it has its `.yolo`.
func workspace(t *testing.T) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths.WorkspaceStateDir(ws), 0o700); err != nil {
		t.Fatal(err)
	}
	return ws
}

// clock is a fake time whose Sleep advances it, so a wait runs without sleeping.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Sleep(d time.Duration)   { c.now = c.now.Add(d) }
func (c *clock) opts(ws, id string) Opts { return c.optsOut(ws, id, &bytes.Buffer{}, &bytes.Buffer{}) }
func (c *clock) optsOut(ws, id string, out, errs *bytes.Buffer) Opts {
	return Opts{Workspace: ws, ID: id, Out: out, Err: errs, Now: c.Now, Sleep: c.Sleep}
}

func newClock() *clock { return &clock{now: time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)} }

func publish(t *testing.T, ws string, rec Record) {
	t.Helper()
	if err := Publish(ws, rec); err != nil {
		t.Fatal(err)
	}
}

// Each answer the host records maps to its own exit status, so a caller tells "retry once"
// (0) from "report, do not retry" (every other) by the status alone.
func TestWaitExitsWithTheRecordedAnswer(t *testing.T) {
	c := newClock()
	for _, tc := range []struct {
		name string
		rec  Record
		want int
	}{
		{"allowed", Record{State: StateAllowed}, ExitAllowed},
		{"denied", Record{State: StateDenied}, ExitDenied},
		{"closed", Record{State: StateRefused}, ExitClosed},
		{"expired", Record{State: StateExpired}, ExitExpired},
		{"pending past its expiry", Record{State: StatePending, Expires: c.now.Add(-time.Minute)}, ExitExpired},
		{"allowed and used", Record{State: StateAllowed, Used: true}, ExitSpent},
		{"allowed and lapsed", Record{State: StateAllowed, Lapsed: true}, ExitSpent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := workspace(t)
			rec := tc.rec
			rec.ID, rec.Source = testID, "git"
			publish(t, ws, rec)
			if got := Wait(c.opts(ws, testID)); got != tc.want {
				t.Fatalf("exit %d, want %d", got, tc.want)
			}
		})
	}
}

// A request still pending at the timeout exits 75 and gives the human's command again, alone
// on its own line; one answered while the wait polls exits with that answer.
func TestWaitTimesOutAndSeesALaterAnswer(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	publish(t, ws, Record{ID: testID, Source: "git", State: StatePending, Expires: c.now.Add(time.Hour)})
	var out bytes.Buffer
	o := c.optsOut(ws, testID, &out, &bytes.Buffer{})
	o.Timeout = 5 * time.Second
	if got := Wait(o); got != ExitTempFail {
		t.Fatalf("exit %d, want %d", got, ExitTempFail)
	}
	if !strings.Contains(out.String(), "\n    yolo approve "+testID+"\n") {
		t.Fatalf("the timeout does not give the approve command on its own line:\n%s", out.String())
	}

	// The default deadline is the request's own expiry.
	o = c.opts(ws, testID)
	answered := false
	o.Sleep = func(d time.Duration) {
		c.Sleep(d)
		if !answered {
			answered = true
			publish(t, ws, Record{ID: testID, Source: "git", State: StateAllowed, Expires: c.now.Add(time.Hour), Decided: c.now})
		}
	}
	if got := Wait(o); got != ExitAllowed {
		t.Fatalf("an answer during the wait: exit %d", got)
	}
}

// A record written before the wait started, as in a jail relaunched after the human answered,
// answers at once.
func TestWaitAfterARestartReadsTheRecordedAnswer(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	publish(t, ws, Record{ID: testID, Source: "github", State: StateAllowed, Decided: c.now.Add(-time.Hour)})
	slept := false
	o := c.opts(ws, testID)
	o.Sleep = func(time.Duration) { slept = true }
	if got := Wait(o); got != ExitAllowed || slept {
		t.Fatalf("exit %d, slept %v", got, slept)
	}
}

// No record at all stops after the grace and says so; a malformed id is a usage error.
func TestWaitWithoutARecordOrWithABadID(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	var errs bytes.Buffer
	if got := Wait(c.optsOut(ws, testID, &bytes.Buffer{}, &errs)); got != ExitUnavailable || !strings.Contains(errs.String(), "no record of "+testID) {
		t.Fatalf("exit %d: %s", got, errs.String())
	}
	if got := Wait(c.opts(ws, "r-nope")); got != ExitUsage {
		t.Fatalf("a bad id: exit %d", got)
	}
}

// One waiter per request: a second wait while the first holds the lock exits 73 at once.
func TestASecondWaiterExitsAlreadyWaiting(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	publish(t, ws, Record{ID: testID, Source: "git", State: StatePending, Expires: c.now.Add(time.Hour)})
	unlock, held, err := c.opts(ws, testID).lock()
	if err != nil || held {
		t.Fatalf("first lock: held %v, %v", held, err)
	}
	defer unlock()
	var errs bytes.Buffer
	if got := Wait(c.optsOut(ws, testID, &bytes.Buffer{}, &errs)); got != ExitAlreadyWaiting || !strings.Contains(errs.String(), "already waiting") {
		t.Fatalf("exit %d: %s", got, errs.String())
	}
}

// The bare listing names what still wants the agent: the pending, and the answered but not
// acted on; a used or lapsed one, or one answered long ago, is left out.
func TestListNamesWhatStillWaitsOnTheAgent(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	ids := map[string]Record{
		"r-00000000000000000000000000000001": {State: StatePending, Expires: c.now.Add(time.Hour), Command: "git push main"},
		"r-00000000000000000000000000000002": {State: StateAllowed, Decided: c.now.Add(-time.Minute), Command: "gh pr comment 7"},
		"r-00000000000000000000000000000003": {State: StateAllowed, Decided: c.now.Add(-time.Minute), Used: true},
		"r-00000000000000000000000000000004": {State: StateDenied, Decided: c.now.Add(-48 * time.Hour)},
	}
	for id, rec := range ids {
		rec.ID, rec.Source = id, "git"
		publish(t, ws, rec)
	}
	var out bytes.Buffer
	if got := List(c.optsOut(ws, "", &out, &bytes.Buffer{})); got != 0 {
		t.Fatalf("exit %d", got)
	}
	for id, want := range map[string]bool{"r-00000000000000000000000000000001": true, "r-00000000000000000000000000000002": true,
		"r-00000000000000000000000000000003": false, "r-00000000000000000000000000000004": false} {
		if strings.Contains(out.String(), id) != want {
			t.Errorf("listing %s: want %v:\n%s", id, want, out.String())
		}
	}
}

// A jail never writes a record, and a workspace that never launched one gets no `.yolo`.
func TestPublishIsHostOnlyAndCreatesNoStateDir(t *testing.T) {
	ws := workspace(t)
	t.Setenv("YOLO_VERSION", "1.0")
	publish(t, ws, Record{ID: testID, State: StateAllowed})
	if _, err := os.Stat(filepath.Join(paths.WorkspaceStateDir(ws), DirName)); !os.IsNotExist(err) {
		t.Fatalf("a jail wrote a record: %v", err)
	}
	t.Setenv("YOLO_VERSION", "")
	bare, _ := filepath.EvalSymlinks(t.TempDir())
	publish(t, bare, Record{ID: testID, State: StateAllowed})
	if _, err := os.Stat(paths.WorkspaceStateDir(bare)); !os.IsNotExist(err) {
		t.Fatalf("Publish created .yolo: %v", err)
	}
}
