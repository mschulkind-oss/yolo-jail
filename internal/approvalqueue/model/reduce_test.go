package model

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 10, 3, 50, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func loadState(t *testing.T, name string) StateView {
	t.Helper()
	s, err := ParseState(fixture(t, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return s
}

// loadWatch returns the opening state line and the events of a recorded watch transcript.
func loadWatch(t *testing.T, name string) (StateView, []EventView) {
	t.Helper()
	var st StateView
	var evs []EventView
	sc := bufio.NewScanner(strings.NewReader(string(fixture(t, name))))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	first := true
	for sc.Scan() {
		if first {
			s, err := ParseState([]byte(sc.Text()))
			if err != nil {
				t.Fatal(err)
			}
			st, first = s, false
			continue
		}
		e, err := ParseEvent([]byte(sc.Text()))
		if err != nil {
			t.Fatal(err)
		}
		evs = append(evs, e)
	}
	return st, evs
}

func req(id, state string) RequestView {
	return RequestView{Schema: 1, ID: id, Source: "git", State: state, Headline: "head " + id, Expires: at(3600)}
}

func grant(id, reqID string) GrantView {
	return GrantView{Schema: 1, ID: id, Source: "git", Request: reqID, State: "live", UsesLeft: 1, Expires: at(900)}
}

func ids(m Model) []string {
	out := []string{}
	for _, r := range m.State.Requests {
		out = append(out, r.ID)
	}
	return out
}

func gids(m Model) []string {
	out := []string{}
	for _, g := range m.State.Grants {
		out = append(out, g.ID)
	}
	return out
}

func snap(m Model, start time.Time, rs []RequestView, gs []GrantView) Model {
	return Reduce(m, SnapshotOK{State: StateView{Schema: 1, Requests: rs, Grants: gs}, FetchStart: start, FromWatch: false}, start)
}

func histStatus(m Model, key string) (string, bool) {
	for _, e := range m.History {
		if e.Key == key {
			return e.Status, true
		}
	}
	return "", false
}

func TestEventTable(t *testing.T) {
	r1, r2 := req("r-1", "pending"), req("r-2", "pending")
	g1 := grant("g-1", "r-1")
	endedAt := at(50)
	g1ended := g1
	g1ended.State, g1ended.Ended, g1ended.EndReason = "ended", &endedAt, "used"
	g1used := g1
	g1used.UsesLeft, g1used.UsedBy = 0, "jail9"
	decided := r1
	decided.State = "denied"

	cases := []struct {
		name      string
		start     func() Model
		ev        EventView
		wantReqs  []string
		wantGrant []string
		wantHist  string
		histKey   string
	}{
		{"filed adds", func() Model { return snap(New(t0), at(1), nil, nil) },
			EventView{Schema: 1, Event: EvFiled, Source: "git", Time: at(5), Request: &r1}, []string{"r-1"}, nil, StFiled, "r-1"},
		{"filed replaces by id", func() Model { return snap(New(t0), at(1), []RequestView{r1}, nil) },
			EventView{Schema: 1, Event: EvFiled, Time: at(5), Request: &r1}, []string{"r-1"}, nil, StFiled, "r-1"},
		{"decided removes", func() Model { return snap(New(t0), at(1), []RequestView{r1, r2}, nil) },
			EventView{Schema: 1, Event: EvDecided, Time: at(5), Request: &decided}, []string{"r-2"}, nil, StDenied, "r-1"},
		{"decided missing request is a no-op", func() Model { return snap(New(t0), at(1), []RequestView{r2}, nil) },
			EventView{Schema: 1, Event: EvDecided, Time: at(5), Request: &decided}, []string{"r-2"}, nil, StDenied, "r-1"},
		{"expired removes", func() Model { return snap(New(t0), at(1), []RequestView{r1}, nil) },
			EventView{Schema: 1, Event: EvExpired, Time: at(5), Request: &r1}, []string{}, nil, StExpired, "r-1"},
		{"grant used for unknown grant adds it", func() Model { return snap(New(t0), at(1), nil, nil) },
			EventView{Schema: 1, Event: EvUsed, Time: at(5), Grant: &g1used}, nil, []string{"g-1"}, StUsed, "g-1"},
		{"grant ended removes", func() Model { return snap(New(t0), at(1), nil, []GrantView{g1}) },
			EventView{Schema: 1, Event: EvEnded, Time: at(5), Grant: &g1ended}, nil, []string{}, StEnded, "g-1"},
		{"grant revoked removes", func() Model { return snap(New(t0), at(1), nil, []GrantView{g1}) },
			EventView{Schema: 1, Event: EvRevoked, Time: at(5), Grant: &g1ended}, nil, []string{}, StRevoked, "g-1"},
		{"grant ended for unknown grant is a no-op", func() Model { return snap(New(t0), at(1), nil, nil) },
			EventView{Schema: 1, Event: EvEnded, Time: at(5), Grant: &g1ended}, nil, []string{}, StEnded, "g-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := Reduce(c.start(), EventIn{c.ev}, at(6))
			if c.wantReqs != nil && !reflect.DeepEqual(ids(m), orEmpty(c.wantReqs)) {
				t.Errorf("requests = %v, want %v", ids(m), c.wantReqs)
			}
			if c.wantGrant != nil && !reflect.DeepEqual(gids(m), orEmpty(c.wantGrant)) {
				t.Errorf("grants = %v, want %v", gids(m), c.wantGrant)
			}
			if got, ok := histStatus(m, c.histKey); !ok || got != c.wantHist {
				t.Errorf("history[%s] = %q,%v want %q", c.histKey, got, ok, c.wantHist)
			}
			// Idempotence: the same event again changes nothing observable.
			again := Reduce(m, EventIn{c.ev}, at(7))
			if !reflect.DeepEqual(ids(again), ids(m)) || !reflect.DeepEqual(gids(again), gids(m)) || !reflect.DeepEqual(again.History, m.History) {
				t.Errorf("event applied twice differs from once")
			}
		})
	}
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func TestReduceDoesNotMutateItsArgument(t *testing.T) {
	r1 := req("r-1", "pending")
	m := snap(New(t0), at(1), []RequestView{r1}, nil)
	before := ids(m)
	d := r1
	d.State = "allowed"
	_ = Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(5), Request: &d}}, at(6))
	if !reflect.DeepEqual(ids(m), before) || len(m.History) != 0 {
		t.Fatalf("argument mutated: %v %v", ids(m), m.History)
	}
}

func TestStaleEventIsRecordedButDoesNotChangeState(t *testing.T) {
	r1 := req("r-1", "pending")
	// The snapshot was fetched at +10 s and already lacks r-1.
	m := snap(New(t0), at(10), nil, nil)
	ev := EventView{Schema: 1, Event: EvFiled, Time: at(5), Request: &r1}
	m = Reduce(m, EventIn{ev}, at(11))
	if len(m.State.Requests) != 0 {
		t.Fatalf("stale filed event re-added the request: %v", ids(m))
	}
	if s, ok := histStatus(m, "r-1"); !ok || s != StFiled {
		t.Fatalf("stale event not recorded in history: %q %v", s, ok)
	}
}

func TestPhantomIsBoundedByTheNextSnapshot(t *testing.T) {
	r1 := req("r-1", "pending")
	// Snapshot fetched at +10 dropped r-1 (decided at +9 in the store), but a watch read
	// before it (event time +11, stamped when the watch saw it) re-adds it.
	m := snap(New(t0), at(10), nil, nil)
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvFiled, Time: at(11), Request: &r1}}, at(11))
	if got := ids(m); !reflect.DeepEqual(got, []string{"r-1"}) {
		t.Fatalf("expected the bounded phantom, got %v", got)
	}
	// The next snapshot erases it, and history does not pretend to know why.
	m = snap(m, at(25), nil, nil)
	if len(m.State.Requests) != 0 {
		t.Fatalf("snapshot did not erase the phantom: %v", ids(m))
	}
	if s, _ := histStatus(m, "r-1"); s != StNotObserved {
		t.Fatalf("history = %q, want %q", s, StNotObserved)
	}
	// Or the decision event erases it first.
	m2 := snap(New(t0), at(10), nil, nil)
	m2 = Reduce(m2, EventIn{EventView{Schema: 1, Event: EvFiled, Time: at(11), Request: &r1}}, at(11))
	d := r1
	d.State = "allowed"
	m2 = Reduce(m2, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(12), Request: &d}}, at(12))
	if len(m2.State.Requests) != 0 {
		t.Fatalf("decision did not erase the phantom")
	}
	if s, _ := histStatus(m2, "r-1"); s != StAllowed {
		t.Fatalf("history = %q, want allowed", s)
	}
}

func TestObservedOutcomeReplacesNotObserved(t *testing.T) {
	r1 := req("r-1", "pending")
	m := snap(New(t0), at(1), []RequestView{r1}, nil)
	m = snap(m, at(20), nil, nil)
	if s, _ := histStatus(m, "r-1"); s != StNotObserved {
		t.Fatalf("want not-observed, got %q", s)
	}
	d := r1
	d.State = "denied"
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(10), Request: &d}}, at(21))
	if s, _ := histStatus(m, "r-1"); s != StDenied {
		t.Fatalf("observed outcome did not replace not-observed: %q", s)
	}
	// And a late "filed" does not undo it.
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvFiled, Time: at(2), Request: &r1}}, at(22))
	if s, _ := histStatus(m, "r-1"); s != StDenied {
		t.Fatalf("late filed undid the outcome: %q", s)
	}
}

func TestEventExplainedDisappearanceIsNotReportedAsUnobserved(t *testing.T) {
	r1 := req("r-1", "pending")
	m := snap(New(t0), at(1), []RequestView{r1}, nil)
	d := r1
	d.State = "allowed"
	m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(5), Request: &d}}, at(5))
	m = snap(m, at(20), nil, nil)
	if s, _ := histStatus(m, "r-1"); s != StAllowed {
		t.Fatalf("got %q", s)
	}
}

func TestOlderSnapshotDoesNotRegressTheModel(t *testing.T) {
	r1 := req("r-1", "pending")
	m := snap(New(t0), at(20), nil, nil)
	old := Reduce(m, SnapshotOK{State: StateView{Schema: 1, Requests: []RequestView{r1}}, FetchStart: at(10), FromWatch: true}, at(21))
	if len(old.State.Requests) != 0 || !old.AsOf.Equal(at(20)) {
		t.Fatalf("older snapshot regressed the model")
	}
	if old.Watch != WatchRunning {
		t.Fatalf("an older watch opening line still proves the watch is up")
	}
}

func TestHistoryIsCappedAt200(t *testing.T) {
	m := snap(New(t0), at(1), nil, nil)
	for i := 0; i < 250; i++ {
		r := req("r-"+string(rune('a'+i%26))+strings.Repeat("x", i/26), "denied")
		m = Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(2 + i), Request: &r}}, at(300))
	}
	if len(m.History) != HistoryCap {
		t.Fatalf("history len = %d, want %d", len(m.History), HistoryCap)
	}
	if m.History[len(m.History)-1].Status != StDenied {
		t.Fatalf("newest entry lost")
	}
	if m.Dropped != 50 {
		t.Fatalf("dropped = %d, want 50", m.Dropped)
	}
}

// TestHistoryCountsEvictionsAndPublishesThem is RH-D3's "N older entries dropped"
// (request-history-and-privacy.md §5, §7): the 201st distinct entry evicts one and the
// published history says so; an update to a kept entry evicts nothing.
func TestHistoryCountsEvictionsAndPublishesThem(t *testing.T) {
	m := snap(New(t0), at(1), nil, nil)
	decide := func(m Model, id string, i int) Model {
		r := req(id, "denied")
		return Reduce(m, EventIn{EventView{Schema: 1, Event: EvDecided, Time: at(2 + i), Request: &r}}, at(400))
	}
	for i := 0; i < HistoryCap; i++ {
		m = decide(m, fmt.Sprintf("r-%03d", i), i)
	}
	if m.Dropped != 0 {
		t.Fatalf("dropped = %d at the cap, want 0", m.Dropped)
	}
	if note := Publish(m, at(400), Options{}).History.Note; strings.Contains(note, "dropped") {
		t.Fatalf("a history with nothing evicted claims a drop: %q", note)
	}
	m = decide(m, "r-199", 300) // a kept entry again: no eviction
	if m.Dropped != 0 || len(m.History) != HistoryCap {
		t.Fatalf("re-recording a kept entry evicted: dropped=%d len=%d", m.Dropped, len(m.History))
	}
	m = decide(m, "r-new", 301)
	h := Publish(m, at(400), Options{}).History
	if m.Dropped != 1 || h.Dropped != 1 || !strings.Contains(h.Note, "; 1 older entry dropped") {
		t.Fatalf("the 201st entry: dropped=%d published=%d note=%q", m.Dropped, h.Dropped, h.Note)
	}
	m = decide(m, "r-newer", 302)
	if h := Publish(m, at(400), Options{}).History; h.Dropped != 2 || !strings.Contains(h.Note, "; 2 older entries dropped") {
		t.Fatalf("the 202nd entry: %+v", h)
	}
}

func TestSnapshotReplacesWholesale(t *testing.T) {
	m := snap(New(t0), at(1), []RequestView{req("r-1", "pending"), req("r-2", "pending")}, []GrantView{grant("g-1", "r-1")})
	m = snap(m, at(20), []RequestView{req("r-3", "pending")}, nil)
	if !reflect.DeepEqual(ids(m), []string{"r-3"}) || len(m.State.Grants) != 0 {
		t.Fatalf("not replaced: %v %v", ids(m), gids(m))
	}
	if s, _ := histStatus(m, "r-1"); s != StNotObserved {
		t.Fatalf("r-1 history = %q", s)
	}
	if s, _ := histStatus(m, "g-1"); s != StGrantGone {
		t.Fatalf("g-1 history = %q", s)
	}
}

// The recorded gap: a watch saw some decisions, stopped, and its successor's opening state
// shows nothing of what happened in between.
func TestReconcileAfterAMissedEventUsingTheRecordedGap(t *testing.T) {
	open1, evs1 := loadWatch(t, "watch-1.jsonl")
	open2, _ := loadWatch(t, "watch-2-after-gap.jsonl")
	if len(open1.Requests) != 6 {
		t.Fatalf("fixture changed: %d pending", len(open1.Requests))
	}
	m := New(t0)
	m = Reduce(m, WatchStarted{At: at(0)}, at(0))
	m = Reduce(m, SnapshotOK{State: open1, FetchStart: at(1), FromWatch: true}, at(1))
	// Only the first decision event arrived before the watch died.
	first := evs1[0]
	if first.Event != EvDecided {
		t.Fatalf("fixture: first event is %s", first.Event)
	}
	m = Reduce(m, EventIn{first}, first.Time)
	if len(m.State.Requests) != 5 {
		t.Fatalf("after one decision, %d pending", len(m.State.Requests))
	}
	decidedID := first.Request.ID
	stillPending := ids(m)

	gapStart, gapEnd := at(100), at(112)
	m = Reduce(m, WatchExited{At: gapStart, Message: "exit status 0"}, gapStart)
	if m.Watch != WatchRestarting {
		t.Fatalf("watch = %s", m.Watch)
	}
	if f := ComputeFreshness(m, at(101), Options{Reconcile: 15 * time.Second}); f.State != FreshStale {
		t.Fatalf("restarting watch must be stale, got %s", f.State)
	}
	m = Reduce(m, SnapshotOK{State: open2, FetchStart: gapEnd, FromWatch: true}, gapEnd)

	// The model ends exactly as the second watch's snapshot says.
	if len(m.State.Requests) != 0 || len(m.State.Grants) != len(open2.Grants) || len(m.State.Working) != len(open2.Working) {
		t.Fatalf("model did not converge on the new snapshot: %d/%d/%d", len(m.State.Requests), len(m.State.Grants), len(m.State.Working))
	}
	if got, _ := histStatus(m, decidedID); got != first.Request.State {
		t.Fatalf("the observed decision kept its outcome: got %q want %q", got, first.Request.State)
	}
	for _, id := range stillPending {
		if got, ok := histStatus(m, id); !ok || got != StNotObserved {
			t.Errorf("%s history = %q,%v want not-observed", id, got, ok)
		}
	}
	p := Publish(m, at(113), Options{Reconcile: 15 * time.Second})
	var gap string
	for _, g := range p.History.Gaps {
		if strings.Contains(g, "history may be missing events between 03:51:40 and 03:51:52") {
			gap = g
		}
	}
	if gap == "" {
		t.Fatalf("gap note missing: %v", p.History.Gaps)
	}
	var lines []string
	for _, e := range p.History.Entries {
		lines = append(lines, e.Outcome)
	}
	if n := strings.Count(strings.Join(lines, "|"), "no longer pending; outcome not observed"); n != 5 {
		t.Fatalf("want 5 'outcome not observed' lines, got %d in %v", n, lines)
	}
	if f := p.Freshness; f.State != FreshLive {
		t.Fatalf("after reconcile: %+v", f)
	}
}

func TestWholeRecordedWatchLeavesTheModelAsTheFinalSnapshot(t *testing.T) {
	open, evs := loadWatch(t, "watch-1.jsonl")
	m := New(t0)
	m = Reduce(m, SnapshotOK{State: open, FetchStart: t0, FromWatch: true}, t0)
	for _, e := range evs {
		m = Reduce(m, EventIn{e}, e.Time)
	}
	final := loadState(t, "approve-json-final.json")
	if !reflect.DeepEqual(ids(m), idsOf(final)) {
		t.Fatalf("pending after the whole watch = %v; final snapshot says %v", ids(m), idsOf(final))
	}
	if len(m.State.Grants) != len(final.Grants) {
		t.Fatalf("grants after the whole watch = %d; final snapshot says %d", len(m.State.Grants), len(final.Grants))
	}
	// Idempotent over the whole transcript.
	m2 := m
	for _, e := range evs {
		m2 = Reduce(m2, EventIn{e}, e.Time)
	}
	if !reflect.DeepEqual(ids(m2), ids(m)) || !reflect.DeepEqual(gids(m2), gids(m)) {
		t.Fatalf("replaying the transcript changed the state")
	}
}

func idsOf(s StateView) []string {
	out := []string{}
	for _, r := range s.Requests {
		out = append(out, r.ID)
	}
	if len(out) == 0 {
		return []string{}
	}
	return out
}

func TestFreshnessTransitions(t *testing.T) {
	opts := Options{Reconcile: 15 * time.Second}
	m := New(t0)
	f := ComputeFreshness(m, at(0), opts)
	if f.State != FreshNeverLoaded || !strings.Contains(f.Message, "waiting for the first snapshot") {
		t.Fatalf("fresh model: %+v", f)
	}
	m = Reduce(m, SnapshotFail{At: at(1), Kind: FailExit, Message: "exit status 1: could not read the store"}, at(1))
	f = ComputeFreshness(m, at(2), opts)
	if f.State != FreshNeverLoaded || !strings.Contains(f.Message, "could not read the store") {
		t.Fatalf("never loaded must show the CLI's message: %+v", f)
	}
	m = Reduce(m, WatchStarted{At: at(2)}, at(2))
	m = Reduce(m, SnapshotOK{State: emptyState(), FetchStart: at(3), FromWatch: true}, at(3))
	f = ComputeFreshness(m, at(8), opts)
	if f.State != FreshLive || f.Message != "Live; checked 5s ago" {
		t.Fatalf("live: %+v", f)
	}
	// 30 s is still live, 31 s is stale.
	if f = ComputeFreshness(m, at(33), opts); f.State != FreshLive {
		t.Fatalf("exactly 2x interval is live: %+v", f)
	}
	f = ComputeFreshness(m, at(34), opts)
	if f.State != FreshStale || !strings.HasPrefix(f.Message, "STALE: showing state from 03:50:03; ") {
		t.Fatalf("old snapshot: %+v", f)
	}
	// A failed --json makes it stale with the message, not empty and not live.
	m = Reduce(m, SnapshotFail{At: at(10), Kind: FailExit, Message: "exit status 1: store unreadable"}, at(10))
	f = ComputeFreshness(m, at(11), opts)
	if f.State != FreshStale || !strings.Contains(f.Message, "store unreadable") {
		t.Fatalf("failed json: %+v", f)
	}
	m = snap(m, at(12), nil, nil)
	if f = ComputeFreshness(m, at(13), opts); f.State != FreshLive {
		t.Fatalf("recovery by snapshot: %+v", f)
	}
	// A watch store-read warning is stale until the next good snapshot.
	m = Reduce(m, WatchReadFailed{At: at(14), Message: "reading the store failed: boom"}, at(14))
	if f = ComputeFreshness(m, at(15), opts); f.State != FreshStale || !strings.Contains(f.Message, "reading the store failed") {
		t.Fatalf("read warning: %+v", f)
	}
	m = snap(m, at(16), nil, nil)
	if f = ComputeFreshness(m, at(17), opts); f.State != FreshLive {
		t.Fatalf("recovery: %+v", f)
	}
	// Unsupported output names itself.
	m = Reduce(m, SnapshotFail{At: at(18), Kind: FailUnsupported, Message: "yolo printed schema 2 and this queue reads schema 1 only; update the queue"}, at(18))
	if f = ComputeFreshness(m, at(19), opts); f.State != FreshStale || !strings.Contains(f.Message, "unsupported output") || !strings.Contains(f.Message, "schema 2") {
		t.Fatalf("unsupported: %+v", f)
	}
}

func TestMalformedLinesAreCountedAndMarkedAsAGap(t *testing.T) {
	m := Reduce(New(t0), Malformed{At: at(3), Message: "bad json"}, at(3))
	m = Reduce(m, Malformed{At: at(4)}, at(4))
	if m.Malformed != 2 || len(m.Gaps) != 2 {
		t.Fatalf("%d %d", m.Malformed, len(m.Gaps))
	}
}

func TestVisible(t *testing.T) {
	cases := map[string]string{
		"plain text":                "plain text",
		"a\nb\tc":                   "a\nb\tc",
		"x\x1b[31my":                "x⟨U+001B⟩[31my",
		"rtl\u202egnp":              "rtl⟨U+202E⟩gnp",
		"zero\u200bwidth":           "zero⟨U+200B⟩width",
		"bom\ufeff":                 "bom⟨U+FEFF⟩",
		"nul\x00":                   "nul⟨U+0000⟩",
		"c1\u0085":                  "c1⟨U+0085⟩",
		"sep ":                      "sep⟨U+2028⟩",
		"isolate\u2066x\u2069":      "isolate⟨U+2066⟩x⟨U+2069⟩",
		"bad\xffbyte":               "bad⟨invalid byte 0xFF⟩byte",
		"<script>alert(1)</script>": "<script>alert(1)</script>",
		"cr\r\n":                    "cr⟨U+000D⟩\n",
	}
	for in, want := range cases {
		if got := Visible(in); got != want {
			t.Errorf("Visible(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRefusesOtherSchemasAndToleratesUnknownFields(t *testing.T) {
	if _, err := ParseState([]byte(`{"schema":2,"requests":[],"grants":[],"working":[]}`)); err == nil || !strings.Contains(err.Error(), "schema 2") {
		t.Fatalf("schema 2: %v", err)
	}
	if _, err := ParseState([]byte(`{"schema":1,"requests":[{"schema":2,"id":"r-1"}],"grants":[],"working":[]}`)); err == nil {
		t.Fatalf("nested schema 2 accepted")
	}
	if _, err := ParseState([]byte(`{"schema":1,"future_field":{"a":1},"requests":[{"schema":1,"id":"r-1","new":true}],"grants":[],"working":[]}`)); err != nil {
		t.Fatalf("unknown fields must be ignored: %v", err)
	}
	if _, err := ParseEvent([]byte(`{"schema":1,"event":"request-filed","grant":{"schema":3}}`)); err == nil {
		t.Fatalf("nested event schema 3 accepted")
	}
	if _, err := ParseState([]byte(`not json`)); err == nil {
		t.Fatalf("invalid json accepted")
	}
	for _, name := range []string{"approve-json-populated.json", "approve-json-final.json", "empty-home-approve-json.json"} {
		loadState(t, name)
	}
}

func TestPublishCarriesEveryFieldAndNeverEmitsRawControls(t *testing.T) {
	st := loadState(t, "approve-json-populated.json")
	m := snap(New(t0), at(1), st.Requests, st.Grants)
	m.State.Sources = st.Sources
	p := Publish(m, at(2), Options{Reconcile: 15 * time.Second})
	if len(p.Pending) != 6 {
		t.Fatalf("pending = %d", len(p.Pending))
	}
	var hostile *PendingOut
	for i := range p.Pending {
		if p.Pending[i].ID == "r-c31635034bbbaff00f3cb41aaa26cb88" {
			hostile = &p.Pending[i]
		}
		if p.Pending[i].Authority != "core" {
			t.Errorf("%s authority %q", p.Pending[i].ID, p.Pending[i].Authority)
		}
	}
	if hostile == nil || !strings.Contains(hostile.Title, "<img src=x onerror=alert(1)>") {
		t.Fatalf("hostile title must be present as text: %+v", hostile)
	}
	for _, p := range p.Pending {
		for _, s := range append(append([]string{p.Command, p.Title, p.Description}, argvText(p.Argv)...), p.Refs...) {
			for _, r := range s {
				if r < 0x20 && r != '\n' && r != '\t' || r == 0x7f || (r >= 0x80 && r <= 0x9f) || r == 0x202e {
					t.Fatalf("raw control %U survived in %q", r, s)
				}
			}
		}
	}
	// A 5000-character ref is whole.
	long := 0
	for _, p := range p.Pending {
		for _, a := range argvText(p.Argv) {
			if len(a) > long {
				long = len(a)
			}
		}
	}
	if long < 4000 {
		t.Fatalf("longest argv value is %d bytes; the 5000-char fixture must not be elided", long)
	}
}

func argvText(items []ArgvItem) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Prefix+it.Value)
	}
	return out
}
