package lingerprobe

import (
	"fmt"
	"sort"
	"strings"
)

// tally accumulates samples into the one-phrase answer the stderr line gives:
// which blocked state best explains the client still being alive.
type tally struct {
	samples int
	entries map[string]*tallyEntry
	// timed counts the Go runtime's own timed waits by call+timeout, for the
	// all-parked case, where the timeout is the only thing that tells a poll
	// loop from one long wait.
	timed map[string]int
}

type tallyEntry struct {
	key      string
	rank     int
	count    int // samples in which this state appeared
	order    int // first-seen order, for a stable tie-break
	timeouts []string
}

func (ty *tally) add(snap Snapshot) {
	if ty.entries == nil {
		ty.entries = map[string]*tallyEntry{}
		ty.timed = map[string]int{}
	}
	ty.samples++
	comms := map[int]string{}
	for _, p := range snap.Procs {
		comms[p.PID] = p.Comm
	}
	seen := map[string]bool{}
	for _, p := range snap.Procs {
		for _, t := range p.Threads {
			r := rank(p, t, comms)
			if r < 0 {
				continue
			}
			key := t.Call
			switch {
			case r == 5:
				key = t.Call + " in uninterruptible sleep"
				if t.Wchan != "" {
					key += " (" + t.Wchan + ")"
				}
			case r == 2:
				key = "running on CPU"
			case r == 1:
				key = t.Syscall + " on " + childList(p, comms)
			}
			e, ok := ty.entries[key]
			if !ok {
				e = &tallyEntry{key: key, rank: r, order: len(ty.entries)}
				ty.entries[key] = e
			}
			if !seen[key] {
				seen[key] = true
				e.count++
			}
			if t.Timeout != "" && !contains(e.timeouts, t.Timeout) && len(e.timeouts) < 6 {
				e.timeouts = append(e.timeouts, t.Timeout)
			}
			if r == 0 && strings.HasPrefix(t.Timeout, "timeout=") && t.Timeout != "timeout=none" {
				ty.timed[t.Syscall+" "+t.Timeout]++
			}
		}
	}
}

func childList(p Proc, comms map[int]string) string {
	var out []string
	for _, c := range p.Children {
		name := comms[c]
		if name == "" {
			name = "?"
		}
		out = append(out, fmt.Sprintf("child %s (pid %d)", name, c))
	}
	if len(out) == 0 {
		return "a child"
	}
	return strings.Join(out, ", ")
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// dominant is the phrase that completes "podman stayed Ns after …, <phrase>".
// "" when nothing was sampled.
func (ty *tally) dominant() string {
	if ty.samples == 0 || len(ty.entries) == 0 {
		return ""
	}
	var best *tallyEntry
	for _, e := range ty.entries {
		if best == nil || e.rank > best.rank ||
			(e.rank == best.rank && (e.count > best.count || (e.count == best.count && e.order < best.order))) {
			best = e
		}
	}
	switch {
	case best.rank >= 3:
		return "blocked in " + best.key + timeoutsPhrase(best.timeouts)
	case best.rank == 2:
		return "with a thread running on CPU"
	case best.rank == 1:
		return "waiting in " + best.key
	}
	phrase := "with every thread parked in the Go runtime (futex/epoll/nanosleep): a timer or retry loop, not one blocked syscall"
	if len(ty.timed) > 0 {
		keys := make([]string, 0, len(ty.timed))
		for k := range ty.timed {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			if ty.timed[keys[i]] != ty.timed[keys[j]] {
				return ty.timed[keys[i]] > ty.timed[keys[j]]
			}
			return keys[i] < keys[j]
		})
		if len(keys) > 3 {
			keys = keys[:3]
		}
		for i, k := range keys {
			keys[i] = fmt.Sprintf("%s ×%d", k, ty.timed[k])
		}
		phrase += "; timed waits seen: " + strings.Join(keys, ", ")
	}
	return phrase
}

func timeoutsPhrase(ts []string) string {
	if len(ts) == 0 {
		return ""
	}
	return " (" + strings.Join(ts, ", ") + ")"
}
