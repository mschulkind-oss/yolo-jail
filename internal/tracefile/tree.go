package tracefile

import (
	"fmt"
	"sort"
)

// assemble bounds the trace in time, closes what never ended at the trace's last instant, and
// places every span under its parent (design §5.4, TR-D7):
//
//   - a span whose line named a parent known to the trace is that parent's child, in whichever
//     lane it is (an explicit parent wins over enclosure);
//   - any other span goes under the shortest span of its own lane that was open for its whole
//     duration, or, when there is none, under the lane's root;
//   - a lane goes under the span its first line names, which is how one process's spans hang
//     under the span in another process that started it.
//
// An explicit or lane parent that would close a cycle is refused and the span is placed by
// enclosure instead; the refusal is recorded as an anomaly.
func (t *Trace) assemble() {
	t.bound()
	t.resolveLauncherRoot()
	index := map[string]*Span{}
	for _, lane := range t.Lanes {
		for _, s := range lane.Spans {
			if s.ID == "" {
				continue
			}
			if prev, dup := index[s.ID]; dup {
				t.Anomalies = append(t.Anomalies, fmt.Sprintf(
					"span id %s is used in both %s and %s; references resolve to the first",
					s.ID, prev.Lane.File, s.Lane.File))
				continue
			}
			index[s.ID] = s
		}
	}
	for _, lane := range t.Lanes {
		sortLane(lane)
		for _, s := range lane.Spans {
			if s.ExplicitParent != "" {
				if p, ok := index[s.ExplicitParent]; ok && p != s {
					s.Parent, s.Explicit = p, true
					continue
				}
				t.Anomalies = append(t.Anomalies, fmt.Sprintf(
					"span %s (%s) names parent %s, which is not in the trace; placed by enclosure",
					s.Name, s.ID, s.ExplicitParent))
			}
			s.Parent = enclosing(lane, s)
		}
		if lane.ParentID != "" {
			if p, ok := index[lane.ParentID]; ok {
				lane.ParentSpan = p
			} else {
				t.Anomalies = append(t.Anomalies, fmt.Sprintf(
					"lane %s (%s) names parent %s, which is not in the trace",
					lane.Name, lane.File, lane.ParentID))
			}
		}
	}
	for t.breakOneCycle() {
	}
	for _, lane := range t.Lanes {
		for _, s := range lane.Spans {
			if s.Parent != nil {
				s.Parent.Children = append(s.Parent.Children, s)
			}
			t.Spans = append(t.Spans, s)
		}
		if lane.ParentSpan != nil {
			lane.ParentSpan.ChildLanes = append(lane.ParentSpan.ChildLanes, lane)
		}
	}
	sort.SliceStable(t.Spans, func(i, j int) bool { return t.Spans[i].Start < t.Spans[j].Start })
}

// resolveLauncherRoot changes only the observed designated span's display name. Public-only
// copies remain readable; metadata start is not the event clock or a retiming instruction.
func (t *Trace) resolveLauncherRoot() {
	var root *Span
	candidates, diagnostics := 0, 0
	ids := map[string]int{}
	for _, lane := range t.Lanes {
		candidates += lane.rootCandidates
		diagnostics += lane.rootDiagnostics
		for _, s := range lane.Spans {
			ids[s.ID]++
			if s.launcherRoot {
				root = s
			}
		}
	}
	if diagnostics > 0 {
		t.Anomalies = append(t.Anomalies, "launcher root designation invalid; observed events preserved neutrally")
	}
	if candidates == 0 {
		return
	}
	if candidates != 1 || root == nil || root.Lane.rootConflicts[root.ID] || root.Recorded || !IsSpanID(root.ID) || root.observedParent != "" || root.ExplicitParent != "" || root.Lane.Name != "launcher" || root.Lane.Jail || root.Lane.ParentID != "" || root.Lane.traceID != t.ID || ids[root.ID] != 1 || t.Meta == nil || !t.Meta.rootValid || t.Meta.Trace != t.ID {
		t.Anomalies = append(t.Anomalies, "launcher root authority unavailable; observed invocation preserved")
		return
	}
	if t.Meta.Kind == "launch" || t.Meta.Kind == "attach" {
		root.Name = t.Meta.Kind
	}
}

// bound sets the trace's extent, ends every unended span at the trace's last instant, and sets
// each lane's extent.
func (t *Trace) bound() {
	first := true
	see := func(v int64) {
		if first {
			t.Start, t.End, first = v, v, false
			return
		}
		t.Start, t.End = min(t.Start, v), max(t.End, v)
	}
	for _, lane := range t.Lanes {
		for _, s := range lane.Spans {
			see(s.Start)
			if s.Ended {
				see(s.End)
			}
		}
		for _, e := range lane.Events {
			see(e.T)
		}
	}
	for _, lane := range t.Lanes {
		lfirst := true
		lsee := func(a, b int64) {
			if lfirst {
				lane.Start, lane.End, lfirst = a, b, false
				return
			}
			lane.Start, lane.End = min(lane.Start, a), max(lane.End, b)
		}
		for _, s := range lane.Spans {
			if !s.Ended {
				s.End = t.End
			}
			lsee(s.Start, s.End)
		}
		for _, e := range lane.Events {
			lsee(e.T, e.T)
		}
	}
}

// sortLane orders a lane's spans so a span that could enclose another always sorts before it:
// by start, then the longer first, then the order the lines were written.
func sortLane(lane *Lane) {
	sort.SliceStable(lane.Spans, func(i, j int) bool {
		a, b := lane.Spans[i], lane.Spans[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.End != b.End {
			return a.End > b.End
		}
		return a.seq < b.seq
	})
	for i, s := range lane.Spans {
		s.order = i
	}
}

// enclosing is the shortest span of s's lane, sorted before it, that was open for the whole of
// s; the latest such when two are equally short. nil means the lane's root. Because a parent
// always sorts before its child, enclosure alone can never make a cycle.
func enclosing(lane *Lane, s *Span) *Span {
	var best *Span
	for _, p := range lane.Spans[:s.order] {
		if p.Start > s.Start || p.End < s.End {
			continue
		}
		if best == nil || p.Duration() <= best.Duration() {
			best = p
		}
	}
	return best
}

// parentOf is the next node up from s: its placed parent, or for a span at its lane's root the
// span the lane hangs under.
func parentOf(s *Span) *Span {
	if s.Parent != nil {
		return s.Parent
	}
	return s.Lane.ParentSpan
}

// breakOneCycle finds one cycle among the parent edges, refuses the first explicit or lane edge
// on it, and reports whether it found one. Enclosure edges alone are acyclic, so every cycle
// holds an edge it can refuse.
func (t *Trace) breakOneCycle() bool {
	const (
		unseen = iota
		onPath
		done
	)
	state := map[*Span]int{}
	for _, lane := range t.Lanes {
		for _, start := range lane.Spans {
			var path []*Span
			cur := start
			for cur != nil && state[cur] == unseen {
				state[cur] = onPath
				path = append(path, cur)
				cur = parentOf(cur)
			}
			if cur != nil && state[cur] == onPath {
				i := 0
				for path[i] != cur {
					i++
				}
				for _, s := range path[i:] {
					switch {
					case s.Explicit:
						t.Anomalies = append(t.Anomalies, fmt.Sprintf(
							"span %s (%s) names parent %s, which would make a cycle; placed by enclosure",
							s.Name, s.ID, s.ExplicitParent))
						s.Explicit = false
						s.Parent = enclosing(s.Lane, s)
						return true
					case s.Parent == nil && s.Lane.ParentSpan != nil:
						t.Anomalies = append(t.Anomalies, fmt.Sprintf(
							"lane %s (%s) names parent %s, which would make a cycle; placed at the trace's root",
							s.Lane.Name, s.Lane.File, s.Lane.ParentID))
						s.Lane.ParentSpan = nil
						return true
					}
				}
			}
			for _, s := range path {
				state[s] = done
			}
		}
	}
	return false
}
