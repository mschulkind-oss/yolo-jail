package run

import (
	"fmt"
	"strconv"
	"strings"
)

// goneObservations records confirmGone's probe answers, one entry per run of identical answers, so
// the summary it logs when it gives up stays short however many probes it made.
type goneObservations struct {
	lines []string
	count []int
}

func (g *goneObservations) add(line string) {
	if n := len(g.lines); n > 0 && g.lines[n-1] == line {
		g.count[n-1]++
		return
	}
	g.lines = append(g.lines, line)
	g.count = append(g.count, 1)
}

// goneObservationRuns bounds how many distinct runs the summary names; the rest are counted.
const goneObservationRuns = 6

func (g *goneObservations) String() string {
	var parts []string
	for i, l := range g.lines {
		if i == goneObservationRuns {
			parts = append(parts, fmt.Sprintf("... and %d more changes", len(g.lines)-i))
			break
		}
		parts = append(parts, fmt.Sprintf("%dx [%s]", g.count[i], l))
	}
	return strings.Join(parts, ", then ")
}

func idOrNone(id string) string {
	if id == "" {
		return "none"
	}
	return id
}

// describeExec renders one runtime command's answer compactly: its exit code, or why it has none.
func describeExec(what string, r ExecResult) string {
	switch {
	case !r.Ran:
		return what + ": did not run"
	case r.Timeout:
		return what + ": timed out"
	}
	out := what + ": rc=" + strconv.Itoa(r.RC)
	if e := strings.TrimSpace(r.Stderr); e != "" {
		if len(e) > 160 {
			e = e[:160] + "..."
		}
		out += fmt.Sprintf(" stderr=%q", e)
	}
	return out
}
