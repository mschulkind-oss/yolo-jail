package approvalwait

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// history_test.go pins the thin first slice of docs/design/jail-history.md (JH-D10, JH-D11,
// JH-D16): bare `yolo wait` says what it left out, a `--for` or `--add` answer is not sent back
// to run its command again, and `yolo history` reports how each request was answered.

const (
	idSpent   = "r-00000000000000000000000000000011"
	idFor     = "r-00000000000000000000000000000012"
	idAdd     = "r-00000000000000000000000000000013"
	idClosed  = "r-00000000000000000000000000000014"
	idPending = "r-00000000000000000000000000000015"
)

// incident publishes the 2026-11-02 incident's shape, and more: a request closed when its
// broker stopped, one allowed once from the toast and spent, one allowed for 15m, one added.
func incident(t *testing.T, c *clock) string {
	t.Helper()
	ws := workspace(t)
	for _, rec := range []Record{
		{ID: idClosed, State: StateRefused, Filed: c.now.Add(-10 * time.Minute), Decided: c.now.Add(-9 * time.Minute),
			Reason: ReasonBrokerStopped, Hook: HookRan, Jail: "ae3a2fd4", Command: "gh workflow run release-request.yml"},
		{ID: idSpent, State: StateAllowed, Filed: c.now.Add(-5 * time.Minute), Decided: c.now.Add(-4 * time.Minute),
			Answer: AnswerOnce, Via: ViaOnRequest, Hook: HookRan, Used: true, SpentAt: c.now.Add(-4*time.Minute + time.Second),
			Jail: "ae3a2fd4", Command: "gh workflow run release-request.yml"},
		{ID: idFor, State: StateAllowed, Filed: c.now.Add(-3 * time.Minute), Decided: c.now.Add(-2 * time.Minute),
			Answer: AnswerFor, Via: ViaHost, Until: c.now.Add(13 * time.Minute), Command: "git push main"},
		{ID: idAdd, State: StateAllowed, Filed: c.now.Add(-2 * time.Minute), Decided: c.now.Add(-time.Minute),
			Answer: AnswerAdd, Via: ViaHost, Command: "gh pr comment 7"},
	} {
		rec.Source = "github"
		publish(t, ws, rec)
	}
	return ws
}

func TestBareWaitNamesWhatItOmittedAndTheVerbThatShowsIt(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	var out bytes.Buffer
	if got := List(c.optsOut(ws, "", &out, &bytes.Buffer{})); got != 0 {
		t.Fatalf("exit %d", got)
	}
	s := out.String()
	if strings.Contains(s, idSpent) {
		t.Fatalf("a spent request is listed as waiting:\n%s", s)
	}
	if !strings.Contains(s, "1 more request(s) of this workspace were answered in the last day") || !strings.Contains(s, "`yolo history requests`") {
		t.Fatalf("no footer naming the omitted request and `yolo history requests`:\n%s", s)
	}
}

func TestBareWaitListsAForOrAddAnswerAsWaitingOnNothing(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	var out bytes.Buffer
	List(c.optsOut(ws, "", &out, &bytes.Buffer{}))
	s := out.String()
	for _, want := range []string{idFor + "  allowed for 15m, until ", idAdd + "  allowed, added to the working set", "waits on nothing"} {
		if !strings.Contains(s, want) {
			t.Fatalf("listing lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "run its command again") {
		t.Fatalf("a --for or --add answer is sent back to run its command again:\n%s", s)
	}
}

// No omitted request: no footer.
func TestBareWaitWithNothingOmittedPrintsNoFooter(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	publish(t, ws, Record{ID: idPending, Source: "git", State: StatePending, Filed: c.now, Expires: c.now.Add(time.Hour)})
	var out bytes.Buffer
	List(c.optsOut(ws, "", &out, &bytes.Buffer{}))
	if strings.Contains(out.String(), "yolo history") {
		t.Fatalf("a footer with nothing omitted:\n%s", out.String())
	}
}

func historyOpts(c *clock, ws, id string, out, errs *bytes.Buffer) HistoryOpts {
	return HistoryOpts{Workspace: ws, ID: id, InJail: true, Out: out, Err: errs, Now: c.Now}
}

func TestHistoryRequestsSaysHowEachWasAnsweredOldestFirst(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	publish(t, ws, Record{ID: idPending, Source: "git", State: StatePending, Filed: c.now, Expires: c.now.Add(time.Hour), Command: "git push next"})
	var out, errs bytes.Buffer
	if got := History(historyOpts(c, ws, "", &out, &errs)); got != 0 {
		t.Fatalf("exit %d: %s", got, errs.String())
	}
	s := out.String()
	order := []string{idClosed, idSpent, idFor, idAdd, idPending}
	last := -1
	for _, id := range order {
		i := strings.Index(s, id)
		if i < last {
			t.Fatalf("%s is out of filing order:\n%s", id, s)
		}
		last = i
	}
	for _, want := range []string{
		"its broker stopped",
		"allowed once at ", "spent at ", "by `yolo approve` from the on_request hook", "Next: nothing to do: it ran.",
		" for 15m, until ", ", added to the working set, by",
		"Next: " + idPending + " still wait(s) on the host",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("history lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "`yolo approve` shows the request store") {
		t.Fatalf("in a jail, the listing names the host-only store:\n%s", s)
	}
}

func TestHistoryOfOneRequestShowsItsAnswerEndToEnd(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	var out, errs bytes.Buffer
	if got := History(historyOpts(c, ws, idSpent, &out, &errs)); got != 0 {
		t.Fatalf("exit %d: %s", got, errs.String())
	}
	s := out.String()
	for _, want := range []string{"filed ", "by jail ae3a2fd4; on_request hook ran", "answered ", "allowed once, by `yolo approve` from the on_request hook",
		"spent ", "Next: nothing to do: it ran."} {
		if !strings.Contains(s, want) {
			t.Fatalf("history of %s lacks %q:\n%s", idSpent, want, s)
		}
	}
	out.Reset()
	History(historyOpts(c, ws, idFor, &out, &errs))
	if s := out.String(); !strings.Contains(s, "allowed for 15m, until") || !strings.Contains(s, "Next: nothing waits on you") {
		t.Fatalf("a --for answer is not said to wait on nothing:\n%s", s)
	}
}

func TestHistoryOfAnUnknownOrBadIDNamesTheNextStep(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	var out, errs bytes.Buffer
	if got := History(historyOpts(c, ws, idPending, &out, &errs)); got != ExitUnavailable || !strings.Contains(errs.String(), "`yolo history requests`") {
		t.Fatalf("unknown id: exit %d, %q", got, errs.String())
	}
	errs.Reset()
	if got := History(historyOpts(c, ws, "r-nope", &out, &errs)); got != ExitUsage || !strings.Contains(errs.String(), "`yolo history requests`") {
		t.Fatalf("bad id: exit %d, %q", got, errs.String())
	}
}

func TestHistoryWithNoRecordsSaysNothingWaits(t *testing.T) {
	ws := workspace(t)
	c := newClock()
	var out, errs bytes.Buffer
	if got := History(historyOpts(c, ws, "", &out, &errs)); got != 0 || !strings.Contains(out.String(), "Next: nothing waits on you.") {
		t.Fatalf("exit %d, %q %q", got, out.String(), errs.String())
	}
}

func TestHistoryOnTheHostNamesTheStoreAndSinceNarrows(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	var out, errs bytes.Buffer
	o := historyOpts(c, ws, "", &out, &errs)
	o.InJail, o.Since = false, c.now.Add(-150*time.Second)
	if got := History(o); got != 0 {
		t.Fatalf("exit %d", got)
	}
	s := out.String()
	if strings.Contains(s, idClosed) || strings.Contains(s, idSpent) || !strings.Contains(s, idAdd) {
		t.Fatalf("--since did not narrow to the recent ones:\n%s", s)
	}
	if !strings.Contains(s, "`yolo approve` shows the request store's own view") {
		t.Fatalf("on the host, the listing does not name the store:\n%s", s)
	}
}

func TestHistoryJSONPrintsTheRecordsAsStored(t *testing.T) {
	c := newClock()
	ws := incident(t, c)
	var out, errs bytes.Buffer
	o := historyOpts(c, ws, "", &out, &errs)
	o.JSON = true
	if got := History(o); got != 0 {
		t.Fatalf("exit %d", got)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("%d lines:\n%s", len(lines), out.String())
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["id"] != idSpent || rec["answer"] != "once" || rec["via"] != "on_request" || rec["hook"] != "ran" || rec["spent_at"] == nil {
		t.Fatalf("record %v", rec)
	}
}

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{15 * time.Minute: "15m", 90 * time.Minute: "1h30m", 24 * time.Hour: "24h", 30 * time.Second: "30s"} {
		if got := shortDuration(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}
