package model

import (
	"fmt"
	"time"
)

// FailKind says why a snapshot attempt failed.
type FailKind string

const (
	FailExit        FailKind = "failed"      // non-zero exit or could not run
	FailTimeout     FailKind = "timeout"     // ran past the timeout
	FailUnsupported FailKind = "unsupported" // invalid JSON or a schema other than 1
)

// WatchStatus is the state of the long-lived watch child.
type WatchStatus string

const (
	WatchNone       WatchStatus = "none"
	WatchStarting   WatchStatus = "starting"
	WatchRunning    WatchStatus = "running"
	WatchRestarting WatchStatus = "restarting"
)

// HistoryCap is the most observed-history entries kept.
const HistoryCap = 200

// gapCap is the most gap notes kept.
const gapCap = 50

// History statuses. Final ones are an observed or honestly unobserved end; they outrank
// "filed" and "used".
const (
	StFiled       = "filed"
	StUsed        = "used"
	StAllowed     = "allowed"
	StDenied      = "denied"
	StRefused     = "refused"
	StExpired     = "expired"
	StEnded       = "ended"
	StRevoked     = "revoked"
	StNotObserved = "not-observed" // request left the pending list with no event seen
	StGrantGone   = "grant-gone"   // grant left the live list with no event seen
)

// HistoryEntry is one request or grant the queue has observed since it started.
type HistoryEntry struct {
	Key       string // request id or grant id
	Kind      string // "request" or "grant"
	Source    string
	Workspace string // the holding workspace, as the request or grant named it
	Jail      string
	Label     string
	Status    string
	Detail    string
	Observed  bool // an event said it; false for a disappearance
	Time      time.Time
}

// Gap is a stretch in which the queue may have missed watch events.
type Gap struct {
	From, To time.Time
	Reason   string
}

// Model is the queue's in-memory copy of current state, freshness inputs and observed
// history. It is a value: Reduce returns a new one and never mutates its argument's slices.
type Model struct {
	StartedAt time.Time

	Loaded bool      // a snapshot has ever succeeded
	State  StateView // current state
	AsOf   time.Time // fetch start of the last good snapshot

	SnapshotErr     string
	SnapshotErrKind FailKind

	Watch     WatchStatus
	WatchWarn string // last store-read warning, cleared by the next good snapshot
	GapOpen   time.Time

	Malformed int

	History []HistoryEntry
	Gaps    []Gap
}

// New is the model of a queue that started at t and has loaded nothing.
func New(t time.Time) Model {
	return Model{StartedAt: t, Watch: WatchNone, State: emptyState()}
}

func emptyState() StateView {
	return StateView{Schema: SchemaSupported, Requests: []RequestView{}, Grants: []GrantView{}, Working: []WorkingView{}}
}

// Input is one thing that can happen to the model.
type Input interface{ isInput() }

// SnapshotOK is a good full snapshot. FetchStart is the host time recorded immediately before
// the child that produced it was started. FromWatch marks the watch's opening line.
type SnapshotOK struct {
	State      StateView
	FetchStart time.Time
	FromWatch  bool
}

// SnapshotFail is a failed "yolo approve --json" run.
type SnapshotFail struct {
	At      time.Time
	Kind    FailKind
	Message string
}

// EventIn is one watch event.
type EventIn struct{ Event EventView }

// WatchStarted says a watch child was started.
type WatchStarted struct{ At time.Time }

// WatchExited says the watch child is gone and will be restarted.
type WatchExited struct {
	At      time.Time
	Message string
}

// WatchReadFailed says the watch's stderr reported a store read failure.
type WatchReadFailed struct {
	At      time.Time
	Message string
}

// Malformed says a watch line was unreadable and was discarded.
type Malformed struct {
	At      time.Time
	Message string
}

func (SnapshotOK) isInput()      {}
func (SnapshotFail) isInput()    {}
func (EventIn) isInput()         {}
func (WatchStarted) isInput()    {}
func (WatchExited) isInput()     {}
func (WatchReadFailed) isInput() {}
func (Malformed) isInput()       {}

// Reduce is the pure reducer: it applies one input to a model and returns the new model.
func Reduce(m Model, in Input, now time.Time) Model {
	switch in := in.(type) {
	case SnapshotOK:
		return reduceSnapshot(m, in)
	case SnapshotFail:
		m.SnapshotErr, m.SnapshotErrKind = in.Message, in.Kind
		return m
	case EventIn:
		return reduceEvent(m, in.Event)
	case WatchStarted:
		if m.Watch != WatchRunning {
			m.Watch = WatchStarting
		}
		return m
	case WatchExited:
		m.Watch = WatchRestarting
		if m.GapOpen.IsZero() {
			m.GapOpen = atOr(in.At, now)
		}
		return m
	case WatchReadFailed:
		m.WatchWarn = in.Message
		return m
	case Malformed:
		m.Malformed++
		at := atOr(in.At, now)
		m.Gaps = appendGap(m.Gaps, Gap{From: at, To: at, Reason: "an unreadable watch line was discarded"})
		return m
	}
	return m
}

func atOr(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t
}

func appendGap(g []Gap, add Gap) []Gap {
	out := append(append([]Gap(nil), g...), add)
	if len(out) > gapCap {
		out = out[len(out)-gapCap:]
	}
	return out
}

func reduceSnapshot(m Model, in SnapshotOK) Model {
	st := in.State
	if st.Requests == nil {
		st.Requests = []RequestView{}
	}
	if st.Grants == nil {
		st.Grants = []GrantView{}
	}
	if st.Working == nil {
		st.Working = []WorkingView{}
	}
	// A snapshot fetched before the one already held would regress the model; it still
	// proves the watch is up.
	if m.AsOf.IsZero() || !in.FetchStart.Before(m.AsOf) {
		m = noteVanished(m, st, in.FetchStart)
		m.State, m.AsOf, m.Loaded = st, in.FetchStart, true
	}
	m.SnapshotErr, m.SnapshotErrKind, m.WatchWarn = "", "", ""
	if in.FromWatch {
		m.Watch = WatchRunning
		if !m.GapOpen.IsZero() {
			m.Gaps = appendGap(m.Gaps, Gap{From: m.GapOpen, To: in.FetchStart, Reason: "the watch was restarted"})
			m.GapOpen = time.Time{}
		}
	}
	return m
}

// noteVanished records, for every pending request or live grant the new snapshot no longer
// holds and no event explained, that its end was not observed.
func noteVanished(m Model, next StateView, at time.Time) Model {
	reqs := make(map[string]bool, len(next.Requests))
	for _, r := range next.Requests {
		reqs[r.ID] = true
	}
	for _, r := range m.State.Requests {
		if reqs[r.ID] {
			continue
		}
		m = record(m, HistoryEntry{Key: r.ID, Kind: "request", Source: r.Source, Workspace: r.Workspace, Jail: r.Jail, Label: r.Headline,
			Status: StNotObserved, Time: at})
	}
	grants := make(map[string]bool, len(next.Grants))
	for _, g := range next.Grants {
		grants[g.ID] = true
	}
	for _, g := range m.State.Grants {
		if grants[g.ID] {
			continue
		}
		m = record(m, HistoryEntry{Key: g.ID, Kind: "grant", Source: g.Source, Workspace: g.Workspace, Jail: g.Jail, Label: "grant for " + g.Request,
			Status: StGrantGone, Time: at})
	}
	return m
}

func rank(status string) int {
	switch status {
	case StFiled:
		return 0
	case StUsed:
		return 1
	case StNotObserved, StGrantGone:
		return 2
	}
	return 3
}

// record keeps one entry per key. A lower-ranked update never replaces a higher-ranked
// entry, so a later observed outcome replaces "not observed" but a late "filed" does not
// undo an outcome.
func record(m Model, e HistoryEntry) Model {
	hist := make([]HistoryEntry, 0, len(m.History)+1)
	for _, old := range m.History {
		if old.Key == e.Key {
			if rank(e.Status) < rank(old.Status) {
				return m
			}
			if e.Workspace == "" {
				e.Workspace, e.Jail = old.Workspace, old.Jail
			}
			continue
		}
		hist = append(hist, old)
	}
	hist = append(hist, e)
	if len(hist) > HistoryCap {
		hist = hist[len(hist)-HistoryCap:]
	}
	m.History = hist
	return m
}

func reduceEvent(m Model, ev EventView) Model {
	m = recordEvent(m, ev)
	// The snapshot already reflects any read at or after its fetch start; an older event
	// must not undo it.
	if !m.AsOf.IsZero() && !ev.Time.IsZero() && ev.Time.Before(m.AsOf) {
		return m
	}
	st := m.State
	switch ev.Event {
	case EvFiled:
		if ev.Request != nil {
			st.Requests = upsertRequest(st.Requests, *ev.Request)
		}
	case EvDecided, EvExpired:
		if ev.Request != nil {
			st.Requests = dropRequest(st.Requests, ev.Request.ID)
		}
	case EvUsed:
		if ev.Grant != nil {
			if ev.Grant.State == "ended" || ev.Grant.Ended != nil {
				st.Grants = dropGrant(st.Grants, ev.Grant.ID)
			} else {
				st.Grants = upsertGrant(st.Grants, *ev.Grant)
			}
		}
	case EvEnded, EvRevoked:
		if ev.Grant != nil {
			st.Grants = dropGrant(st.Grants, ev.Grant.ID)
		}
	}
	m.State = st
	return m
}

func recordEvent(m Model, ev EventView) Model {
	e := HistoryEntry{Source: ev.Source, Observed: true, Time: ev.Time}
	switch ev.Event {
	case EvFiled, EvDecided, EvExpired:
		if ev.Request == nil {
			return m
		}
		e.Key, e.Kind, e.Label = ev.Request.ID, "request", ev.Request.Headline
		e.Workspace, e.Jail = ev.Request.Workspace, ev.Request.Jail
		switch ev.Event {
		case EvFiled:
			e.Status = StFiled
		case EvExpired:
			e.Status = StExpired
		default:
			e.Status = StAllowed
			switch ev.Request.State {
			case StDenied, StRefused, StAllowed, StExpired:
				e.Status = ev.Request.State
			default:
				e.Detail = "state " + ev.Request.State
			}
		}
	case EvUsed, EvEnded, EvRevoked:
		if ev.Grant == nil {
			return m
		}
		e.Key, e.Kind, e.Label = ev.Grant.ID, "grant", "grant for "+ev.Grant.Request
		e.Workspace, e.Jail = ev.Grant.Workspace, ev.Grant.Jail
		switch ev.Event {
		case EvUsed:
			e.Status = StUsed
			if ev.Grant.UsedBy != "" {
				e.Detail = "used by jail " + ev.Grant.UsedBy
			}
		case EvEnded:
			e.Status, e.Detail = StEnded, ev.Grant.EndReason
		default:
			e.Status, e.Detail = StRevoked, ev.Grant.EndReason
		}
	default:
		return m
	}
	if e.Source == "" && ev.Request != nil {
		e.Source = ev.Request.Source
	}
	return record(m, e)
}

func upsertRequest(rs []RequestView, r RequestView) []RequestView {
	out := make([]RequestView, 0, len(rs)+1)
	replaced := false
	for _, x := range rs {
		if x.ID == r.ID {
			out = append(out, r)
			replaced = true
			continue
		}
		out = append(out, x)
	}
	if !replaced {
		out = append(out, r)
	}
	return out
}

func dropRequest(rs []RequestView, id string) []RequestView {
	out := make([]RequestView, 0, len(rs))
	for _, x := range rs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

func upsertGrant(gs []GrantView, g GrantView) []GrantView {
	out := make([]GrantView, 0, len(gs)+1)
	replaced := false
	for _, x := range gs {
		if x.ID == g.ID {
			out = append(out, g)
			replaced = true
			continue
		}
		out = append(out, x)
	}
	if !replaced {
		out = append(out, g)
	}
	return out
}

func dropGrant(gs []GrantView, id string) []GrantView {
	out := make([]GrantView, 0, len(gs))
	for _, x := range gs {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return out
}

// Describe renders a history entry's outcome as the page's plain sentence.
func (e HistoryEntry) Describe() string {
	s := ""
	switch e.Status {
	case StFiled:
		s = "filed"
	case StNotObserved:
		return "no longer pending; outcome not observed"
	case StGrantGone:
		return "no longer live; end not observed"
	case StUsed:
		s = "grant used"
	case StEnded:
		s = "grant ended"
	case StRevoked:
		s = "grant revoked"
	default:
		s = e.Status
	}
	if e.Detail != "" {
		s = fmt.Sprintf("%s (%s)", s, e.Detail)
	}
	return s
}
