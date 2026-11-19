package tracefile

import (
	"sort"
	"time"
)

// SessionExecMark is the mark that ends the startup window (design §4, TR-D10).
const SessionExecMark = "session.exec"

// Summary defaults (design §6): the fifteen spans with the most time of their own, and every
// stretch of unattributed time over 100 ms.
const (
	DefaultTop          = 15
	DefaultGapThreshold = 100 * time.Millisecond
)

// Options tunes Summarize. Zero values take the defaults. Others are the traces to test for
// overlap with this one's startup window; the caller reads them, so this package names no trace
// root.
type Options struct {
	Top          int
	GapThreshold time.Duration
	Others       []*Trace
}

// Summary is what one trace says about its startup window.
type Summary struct {
	Trace *Trace
	// The startup window: from the earliest instant a host lane recorded to the first
	// session.exec mark. WindowEnded is false when no such mark exists, and the window then runs
	// to the trace's last instant.
	WindowStart, WindowEnd int64
	WindowEnded            bool
	Lanes                  []LaneShare
	// Top ranks spans by their own time inside the window: duration less what their children
	// (spans and child lanes) cover, overlapping children counted once.
	Top []OwnTime
	// Gaps are the stretches of unattributed time over the threshold inside a span that has
	// children, or inside the window where no top-level span runs (Within nil).
	Gaps []Gap
	// Unattributed sums every such stretch, of any length. Stretches in concurrent lanes are each
	// counted, so it can exceed the window on a launch that ran lanes side by side.
	Unattributed time.Duration
	NotEnded     []*Span
	// JailMissing is true when the trace has no jail lane.
	JailMissing bool
	Overlapping []Overlap
}

// Window is the startup window's length.
func (s *Summary) Window() time.Duration { return time.Duration(s.WindowEnd - s.WindowStart) }

// LaneShare is one lane's part of the window: Busy is when any of its spans was open, Own the sum
// of its spans' own time.
type LaneShare struct {
	Lane      *Lane
	Busy, Own time.Duration
}

// OwnTime is one span's own time inside the window.
type OwnTime struct {
	Span *Span
	Own  time.Duration
}

// Gap is one stretch of unattributed time. Before and After name what ends at its start and what
// starts at its end: a span's name, or "lane <name>" for a child lane; "" means the edge is the
// enclosing span's (or the window's) own start or end.
type Gap struct {
	Within        *Span
	Start, End    int64
	Before, After string
}

// Duration is the gap's length.
func (g Gap) Duration() time.Duration { return time.Duration(g.End - g.Start) }

// Overlap is another trace whose spans ran during this one's startup window, with its extent.
type Overlap struct {
	Trace      *Trace
	Start, End int64
}

// Summarize computes the summary of t (design §6).
func Summarize(t *Trace, opt Options) *Summary {
	if opt.Top <= 0 {
		opt.Top = DefaultTop
	}
	if opt.GapThreshold <= 0 {
		opt.GapThreshold = DefaultGapThreshold
	}
	s := &Summary{Trace: t, JailMissing: true}
	s.window(t)
	win := iv{s.WindowStart, s.WindowEnd}

	var top []iv
	var topLabels []labeled
	for _, lane := range t.Lanes {
		if lane.Jail {
			s.JailMissing = false
		}
		share := LaneShare{Lane: lane}
		var busy []iv
		for _, sp := range lane.Spans {
			busy = append(busy, iv{sp.Start, sp.End})
			own, children := ownIntervals(sp, win)
			ownTotal := total(own)
			share.Own += ownTotal
			if ownTotal > 0 {
				s.Top = append(s.Top, OwnTime{Span: sp, Own: ownTotal})
			}
			if len(children) > 0 {
				s.addGaps(sp, own, children, opt.GapThreshold)
			}
			if parentOf(sp) == nil {
				top = append(top, iv{sp.Start, sp.End})
				topLabels = append(topLabels, labeled{iv{sp.Start, sp.End}, sp.Name})
			}
			if !sp.Ended {
				s.NotEnded = append(s.NotEnded, sp)
			}
		}
		share.Busy = total(clip(union(busy), win))
		s.Lanes = append(s.Lanes, share)
	}
	s.addGaps(nil, subtract(win, union(top)), topLabels, opt.GapThreshold)

	sort.SliceStable(s.Top, func(i, j int) bool {
		if s.Top[i].Own != s.Top[j].Own {
			return s.Top[i].Own > s.Top[j].Own
		}
		return s.Top[i].Span.Start < s.Top[j].Span.Start
	})
	if len(s.Top) > opt.Top {
		s.Top = s.Top[:opt.Top]
	}
	sort.SliceStable(s.Gaps, func(i, j int) bool { return s.Gaps[i].Start < s.Gaps[j].Start })
	sort.SliceStable(s.NotEnded, func(i, j int) bool { return s.NotEnded[i].Start < s.NotEnded[j].Start })

	for _, o := range opt.Others {
		if o == nil || o.ID == t.ID {
			continue
		}
		for _, sp := range o.Spans {
			if sp.Start < s.WindowEnd && sp.End > s.WindowStart {
				s.Overlapping = append(s.Overlapping, Overlap{Trace: o, Start: o.Start, End: o.End})
				break
			}
		}
	}
	return s
}

// window sets the startup window. Its start is read from the host lanes only: the jail half is
// untrusted and cannot move the launch's beginning.
func (s *Summary) window(t *Trace) {
	s.WindowStart, s.WindowEnd = t.Start, t.End
	first := true
	for _, lane := range t.Lanes {
		if lane.Jail {
			continue
		}
		if first || lane.Start < s.WindowStart {
			s.WindowStart, first = lane.Start, false
		}
	}
	for _, lane := range t.Lanes {
		for _, e := range lane.Events {
			if e.Kind != KindMark || e.Name != SessionExecMark || e.T < s.WindowStart {
				continue
			}
			if !s.WindowEnded || e.T < s.WindowEnd {
				s.WindowEnd, s.WindowEnded = e.T, true
			}
		}
	}
	if s.WindowEnd < s.WindowStart {
		s.WindowEnd = s.WindowStart
	}
}

// addGaps records each own interval as unattributed time, and those over the threshold as gaps
// named by the children on either side.
func (s *Summary) addGaps(within *Span, own []iv, children []labeled, threshold time.Duration) {
	for _, g := range own {
		d := time.Duration(g.b - g.a)
		s.Unattributed += d
		if d <= threshold {
			continue
		}
		gap := Gap{Within: within, Start: g.a, End: g.b}
		var beforeStart int64
		for _, c := range children {
			if c.b == g.a && (gap.Before == "" || c.a >= beforeStart) {
				gap.Before, beforeStart = c.label, c.a
			}
			if c.a == g.b && gap.After == "" {
				gap.After = c.label
			}
		}
		s.Gaps = append(s.Gaps, gap)
	}
}

// labeled is a child's interval and the name a gap gives it.
type labeled struct {
	iv
	label string
}

// ownIntervals is the part of sp inside the window that none of its children cover, and the
// children with their labels. Children are listed in start order.
func ownIntervals(sp *Span, win iv) ([]iv, []labeled) {
	var children []labeled
	for _, c := range sp.Children {
		children = append(children, labeled{iv{c.Start, c.End}, c.Name})
	}
	for _, l := range sp.ChildLanes {
		children = append(children, labeled{iv{l.Start, l.End}, "lane " + l.Name})
	}
	sort.SliceStable(children, func(i, j int) bool { return children[i].a < children[j].a })
	cover := make([]iv, len(children))
	for i, c := range children {
		cover[i] = c.iv
	}
	base, ok := intersect(iv{sp.Start, sp.End}, win)
	if !ok {
		return nil, children
	}
	return subtract(base, union(cover)), children
}

// iv is a half-open interval of nanoseconds, [a, b).
type iv struct{ a, b int64 }

func intersect(x, y iv) (iv, bool) {
	r := iv{max(x.a, y.a), min(x.b, y.b)}
	return r, r.a < r.b
}

// union merges intervals into a sorted, disjoint list.
func union(in []iv) []iv {
	ivs := make([]iv, 0, len(in))
	for _, v := range in {
		if v.a < v.b {
			ivs = append(ivs, v)
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].a < ivs[j].a })
	var out []iv
	for _, v := range ivs {
		if n := len(out); n > 0 && v.a <= out[n-1].b {
			out[n-1].b = max(out[n-1].b, v.b)
			continue
		}
		out = append(out, v)
	}
	return out
}

// subtract is base less a sorted, disjoint cover.
func subtract(base iv, cover []iv) []iv {
	var out []iv
	at := base.a
	for _, c := range cover {
		if c.b <= at || c.a >= base.b {
			continue
		}
		if c.a > at {
			out = append(out, iv{at, c.a})
		}
		at = max(at, c.b)
	}
	if at < base.b {
		out = append(out, iv{at, base.b})
	}
	return out
}

func clip(ivs []iv, win iv) []iv {
	var out []iv
	for _, v := range ivs {
		if r, ok := intersect(v, win); ok {
			out = append(out, r)
		}
	}
	return out
}

func total(ivs []iv) time.Duration {
	var d int64
	for _, v := range ivs {
		d += v.b - v.a
	}
	return time.Duration(d)
}
