package model

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Freshness states the server computes. The browser adds a fourth, "Disconnected", itself.
const (
	FreshLive        = "live"
	FreshStale       = "stale"
	FreshNeverLoaded = "never-loaded"
)

// Visible makes every character a reader could be fooled by visible: controls other than
// newline and tab, format characters (bidirectional overrides and isolates, zero-width
// characters, the byte-order mark), line and paragraph separators, and invalid UTF-8 become
// a marker naming the code point. Nothing is stripped and nothing is interpreted.
func Visible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			fmt.Fprintf(&b, "⟨invalid byte 0x%02X⟩", s[i])
			i++
			continue
		}
		i += n
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r), unicode.Is(unicode.Zl, r), unicode.Is(unicode.Zp, r):
			fmt.Fprintf(&b, "⟨U+%04X⟩", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func visAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, Visible(s))
	}
	return out
}

// Options are the display inputs Publish needs besides the model.
type Options struct {
	Reconcile time.Duration  // the snapshot interval; a snapshot older than twice this is stale
	Loc       *time.Location // for the clock times shown; nil means UTC
}

// Freshness is the computed freshness state and the sentence the page shows for it.
type Freshness struct {
	State         string   `json:"state"`
	Message       string   `json:"message"`
	AsOf          string   `json:"as_of,omitempty"`
	CheckedAgoSec int      `json:"checked_ago_s"`
	Reasons       []string `json:"reasons,omitempty"`
}

// PendingOut is a pending request with every RequestView field, made safe to render.
type PendingOut struct {
	ID               string     `json:"id"`
	Source           string     `json:"source"`
	SourceStatus     string     `json:"source_status"`
	Authority        string     `json:"authority"`
	Jail             string     `json:"jail"`
	Workspace        string     `json:"workspace"`
	Set              string     `json:"set"`
	Argv             []ArgvItem `json:"argv"`
	Command          string     `json:"command"` // rendered from the masked argv
	Repo             string     `json:"repo"`
	CommandDigest    string     `json:"command_digest"`
	StdinDigest      string     `json:"stdin_digest"`
	Filed            string     `json:"filed"`
	Expires          string     `json:"expires"`
	Decided          string     `json:"decided,omitempty"` // a history entry's request, once yolo reported its decision
	RemainingSeconds int        `json:"remaining_s"`
	State            string     `json:"state"`
	HookFailure      string     `json:"hook_failure"`
	HookOutput       string     `json:"hook_output"`
	NeedsApproval    []string   `json:"needs_approval"`
	Action           string     `json:"action"`
	Target           string     `json:"target"`
	Headline         string     `json:"headline"`
	Title            string     `json:"title"`
	Refs             []string   `json:"refs"`
	Description      string     `json:"description"`
}

// GrantOut is a live grant.
type GrantOut struct {
	ID               string `json:"id"`
	Source           string `json:"source"`
	Request          string `json:"request"`
	Jail             string `json:"jail"`
	Workspace        string `json:"workspace"`
	UsedBy           string `json:"used_by"`
	Set              string `json:"set"`
	Coverage         string `json:"coverage"`
	UsesLeft         int    `json:"uses_left"`
	Expires          string `json:"expires"`
	RemainingSeconds int    `json:"remaining_s"`
	State            string `json:"state"`
	EndReason        string `json:"end_reason"`
}

// WorkingOut is a working-set entry.
type WorkingOut struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Workspace string `json:"workspace"`
	Repo      string `json:"repo"`
	Kind      string `json:"kind"`
	Resource  string `json:"resource"`
	By        string `json:"by"`
	Added     string `json:"added"`
	Expires   string `json:"expires"`
	File      string `json:"file"`
}

// SourceOut is a listed source.
type SourceOut struct {
	Source         string `json:"source"`
	Status         string `json:"status"`
	Authority      string `json:"authority"`
	SelectionBasis string `json:"selection_basis"`
}

// HistoryOut is one observed-history line.
type HistoryOut struct {
	Key       string `json:"key"`
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Workspace string `json:"workspace"`
	Jail      string `json:"jail"`
	Label     string `json:"label"`
	Outcome   string `json:"outcome"`
	Observed  bool   `json:"observed"`
	Time      string `json:"time"`
	// Request is a request entry's request as last seen, masked exactly as a pending request
	// is (request-history-and-privacy.md §6.2); absent for a grant.
	Request *PendingOut `json:"request,omitempty"`
}

// HistoryBlock is the observed history with its honest labels.
type HistoryBlock struct {
	Since   string       `json:"since"`
	Cap     int          `json:"cap"`
	Dropped int          `json:"dropped"`
	Note    string       `json:"note"`
	Gaps    []string     `json:"gaps"`
	Entries []HistoryOut `json:"entries"`
}

// Published is the whole model as the browser receives it: one message, whole state.
type Published struct {
	Schema      int          `json:"schema"`
	Now         string       `json:"now"`
	Freshness   Freshness    `json:"freshness"`
	Malformed   int          `json:"malformed_lines"`
	Pending     []PendingOut `json:"pending"`
	Grants      []GrantOut   `json:"grants"`
	Working     []WorkingOut `json:"working"`
	Sources     []SourceOut  `json:"sources"`
	Diagnostics []string     `json:"diagnostics"`
	History     HistoryBlock `json:"history"`
}

func clock(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("15:04:05")
}

func stamp(t time.Time, loc *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(loc).Format("2006-01-02 15:04:05 MST")
}

// ComputeFreshness derives the freshness state from the model and the time.
func ComputeFreshness(m Model, now time.Time, o Options) Freshness {
	loc := o.Loc
	if loc == nil {
		loc = time.UTC
	}
	f := Freshness{}
	if !m.Loaded {
		f.State = FreshNeverLoaded
		msg := "waiting for the first snapshot from yolo"
		if m.SnapshotErr != "" {
			msg = m.SnapshotErr
		}
		f.Message = "Never loaded: " + Visible(msg)
		return f
	}
	f.AsOf = clock(m.AsOf, loc)
	age := now.Sub(m.AsOf)
	if age < 0 {
		age = 0
	}
	f.CheckedAgoSec = int(age.Seconds())
	var why []string
	if m.SnapshotErr != "" {
		if m.SnapshotErrKind == FailUnsupported {
			why = append(why, "unsupported output: "+m.SnapshotErr)
		} else {
			why = append(why, "the last snapshot failed: "+m.SnapshotErr)
		}
	}
	if m.WatchWarn != "" {
		why = append(why, "the watch reported a store read failure: "+m.WatchWarn)
	}
	switch m.Watch {
	case WatchRunning:
	case WatchRestarting:
		why = append(why, "the watch is restarting")
	default:
		why = append(why, "the watch is not running yet")
	}
	if o.Reconcile > 0 && age > 2*o.Reconcile {
		why = append(why, fmt.Sprintf("the last snapshot is %ds old", int(age.Seconds())))
	}
	if len(why) == 0 {
		f.State = FreshLive
		f.Message = fmt.Sprintf("Live; checked %ds ago", f.CheckedAgoSec)
		return f
	}
	f.State = FreshStale
	f.Reasons = visAll(why)
	f.Message = fmt.Sprintf("STALE: showing state from %s; %s", clock(m.AsOf, loc), strings.Join(f.Reasons, "; "))
	return f
}

// droppedNote is the history's eviction claim (request-history-and-privacy.md §7): nothing
// when no entry was evicted, else "; N older entries dropped".
func droppedNote(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "; 1 older entry dropped"
	}
	return fmt.Sprintf("; %d older entries dropped", n)
}

// Publish turns the model into the message the browser receives. Every string derived from
// yolo's output or an agent passes through Visible.
func Publish(m Model, now time.Time, o Options) Published {
	loc := o.Loc
	if loc == nil {
		loc = time.UTC
	}
	p := Published{
		Schema:      1,
		Now:         stamp(now, loc),
		Freshness:   ComputeFreshness(m, now, o),
		Malformed:   m.Malformed,
		Pending:     []PendingOut{},
		Grants:      []GrantOut{},
		Working:     []WorkingOut{},
		Sources:     []SourceOut{},
		Diagnostics: []string{},
	}
	authority := map[string]string{}
	for _, s := range m.State.Sources {
		authority[s.Source] = s.Authority
		p.Sources = append(p.Sources, SourceOut{Visible(s.Source), Visible(s.Status), Visible(s.Authority), Visible(s.SelectionBasis)})
	}
	p.Diagnostics = append(p.Diagnostics, visAll(m.State.Diagnostics)...)
	for _, r := range m.State.Requests {
		p.Pending = append(p.Pending, requestOut(r, authority, now, loc))
	}
	for _, g := range m.State.Grants {
		p.Grants = append(p.Grants, GrantOut{
			ID: Visible(g.ID), Source: Visible(g.Source), Request: Visible(g.Request), Jail: Visible(g.Jail),
			Workspace: Visible(g.Workspace), UsedBy: Visible(g.UsedBy), Set: Visible(g.Set), Coverage: Visible(g.Coverage),
			UsesLeft: g.UsesLeft, Expires: stamp(g.Expires, loc), RemainingSeconds: int(g.Expires.Sub(now).Seconds()),
			State: Visible(g.State), EndReason: Visible(g.EndReason),
		})
	}
	for _, w := range m.State.Working {
		p.Working = append(p.Working, WorkingOut{
			ID: Visible(w.ID), Source: Visible(w.Source), Workspace: Visible(w.Workspace), Repo: Visible(w.Repo),
			Kind: Visible(w.Kind), Resource: Visible(w.Resource), By: Visible(w.By),
			Added: stamp(w.Added, loc), Expires: stamp(w.Expires, loc), File: Visible(w.File),
		})
	}
	h := HistoryBlock{
		Since:   stamp(m.StartedAt, loc),
		Cap:     HistoryCap,
		Dropped: m.Dropped,
		Note: "Observed since this queue started at " + stamp(m.StartedAt, loc) +
			", kept in memory only, never saved, newest " + fmt.Sprint(HistoryCap) +
			" entries" + droppedNote(m.Dropped) + ". It is not a complete history; `yolo audit` is the record.",
		Gaps:    []string{},
		Entries: []HistoryOut{},
	}
	for _, g := range m.Gaps {
		if g.From.Equal(g.To) {
			h.Gaps = append(h.Gaps, fmt.Sprintf("%s at %s; history may be missing events", g.Reason, clock(g.From, loc)))
			continue
		}
		h.Gaps = append(h.Gaps, fmt.Sprintf("history may be missing events between %s and %s (%s)", clock(g.From, loc), clock(g.To, loc), g.Reason))
	}
	if !m.GapOpen.IsZero() {
		h.Gaps = append(h.Gaps, fmt.Sprintf("history may be missing events since %s (the watch is down)", clock(m.GapOpen, loc)))
	}
	for i := len(m.History) - 1; i >= 0; i-- {
		e := m.History[i]
		out := HistoryOut{
			Key: Visible(e.Key), Kind: e.Kind, Source: Visible(e.Source), Workspace: Visible(e.Workspace), Jail: Visible(e.Jail), Label: Visible(e.Label),
			Outcome: Visible(e.Describe()), Observed: e.Observed, Time: clock(e.Time, loc),
		}
		if e.Request != nil {
			ro := requestOut(*e.Request, authority, now, loc)
			ro.RemainingSeconds = 0 // a past request has no time left to show
			out.Request = &ro
		}
		h.Entries = append(h.Entries, out)
	}
	p.History = h
	return p
}

// requestOut is one request with every RequestView field made safe to render and the values
// after secret-bearing flags masked. Pending requests and history's last-seen requests both
// go through it, so the two are never masked differently.
func requestOut(r RequestView, authority map[string]string, now time.Time, loc *time.Location) PendingOut {
	auth := authority[r.Source]
	if auth == "" {
		auth = "unknown (source not listed)"
	}
	decided := ""
	if r.Decided != nil {
		decided = stamp(*r.Decided, loc)
	}
	return PendingOut{
		ID: Visible(r.ID), Source: Visible(r.Source), SourceStatus: Visible(r.SourceStatus), Authority: Visible(auth),
		Jail: Visible(r.Jail), Workspace: Visible(r.Workspace), Set: Visible(r.Set),
		Argv: MaskArgv(r.Argv), Command: MaskedCommand(r.Argv), Repo: Visible(r.Repo),
		CommandDigest: Visible(r.CommandDigest), StdinDigest: Visible(r.StdinDigest),
		Filed: stamp(r.Filed, loc), Expires: stamp(r.Expires, loc), Decided: decided, RemainingSeconds: int(r.Expires.Sub(now).Seconds()),
		State: Visible(r.State), HookFailure: Visible(r.HookFailure), HookOutput: Visible(r.HookOutput),
		NeedsApproval: visAll(r.NeedsApproval), Action: Visible(r.Action), Target: Visible(r.Target),
		Headline: Visible(r.Headline), Title: Visible(r.Title), Refs: visAll(r.Refs), Description: Visible(r.Description),
	}
}
