package run

// perfevents.go attributes Window A — the stretch of a shutdown that happens
// entirely INSIDE the podman child, after the container's PID 1 dies and
// before the podman process exits (conmon's exit file, netavark teardown, the
// unmount of every bind). No yolo code runs there, so no span can cover it;
// what yolo CAN do is ask podman's own event log afterwards when the two
// events bounded it, and print the gap (docs/reference/perf-logging.md, "Window A
// attribution"; D9).
//
// Best-effort by the package's standing rule, with three explicit bounds so
// the diagnosis can never become the delay: only the podman runtime (Apple
// Container has no events command), a 3s exec timeout, and --until on every
// invocation — `podman events --since` alone STREAMS on the journald backend,
// and a diagnostics query that never returns is the failure this feature
// exists to end.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// windowAEventsTimeout bounds the podman events query. Generous against a
// busy event log, short against a human watching a prompt.
const windowAEventsTimeout = 3 * time.Second

// attributeWindowA queries podman's event log for the container's die (and,
// if it exists, cleanup) timestamps and renders the die → podman-exit gap.
// podmanExited is when the proxy's child (the `podman run` process) was
// reaped — the collector's child.exited mark. ok=false renders nothing, for
// every failure mode: wrong runtime, missing binary, timeout, nonzero rc,
// unparsable or absent events (a rootless file backend can legitimately have
// none).
// The second return is the LINE; the third is WHY there is none. Silence was the
// original design and it was wrong in practice: two real-host launches produced no
// attribution at all and no way to tell whether the query failed, timed out, or
// simply found nothing — an observability feature that cannot explain its own
// blank is the failure it exists to remove. The reason is rendered once, dim,
// beneath the table.
func (o *Options) attributeWindowA(cname, rt string, since, podmanExited time.Time) (string, bool, string) {
	if rt != "podman" || o.Perf == nil {
		return "", false, "" // not applicable: say nothing at all
	}
	// --until IS NOW, AND MUST NEVER BE IN THE FUTURE. `podman events` treats a
	// future --until as an instruction to keep watching until that wall-clock
	// moment arrives, so it BLOCKS rather than returning what it already has.
	// This first shipped as podmanExited+5s — slack for a cleanup event landing
	// after the run process is gone — and that slack made every timed shutdown
	// stall for the full windowAEventsTimeout: measured 3.002s on every launch,
	// against 0.02s for an --until of now (podman 5.8.4, 2026-09-06). A timing
	// feature that adds three seconds to the thing it measures is worse than no
	// feature, and it is exactly the delay class this exists to find.
	//
	// No slack is needed: attribution runs AFTER the whole teardown chain, so
	// "now" is already later than the podman exit and later than any cleanup
	// event conmon has written. Pinned by TestWindowAUntilIsNeverInTheFuture.
	until := time.Now()
	if podmanExited.After(until) {
		until = podmanExited // clock skew only; still never a future wait
	}
	argv := []string{
		"podman", "events",
		"--since", since.UTC().Format(time.RFC3339),
		"--until", until.UTC().Format(time.RFC3339),
		"--filter", "container=" + cname,
		"--format", "{{.Time}} {{.Status}}",
	}
	sp := o.Perf.Span("shutdown.window_a_podman_events")
	res := o.Exec(argv, "", nil, windowAEventsTimeout)
	sp.End()
	switch {
	case res.Timeout:
		return "", false, fmt.Sprintf("Window A unattributed: `podman events` did not answer within %s", windowAEventsTimeout)
	case !res.Ran:
		return "", false, "Window A unattributed: `podman events` could not be run"
	case res.RC != 0:
		return "", false, fmt.Sprintf("Window A unattributed: `podman events` exited %d", res.RC)
	}
	dieAt, cleanupAt, ok := parseDieAndCleanup(res.Stdout)
	if !ok {
		// The ordinary case on a host whose events backend keeps nothing (the
		// rootless file backend expires them), so it is stated plainly rather
		// than as a fault.
		return "", false, "Window A unattributed: no container `die` event in podman's log for this jail"
	}
	line := fmt.Sprintf("Window A (container died → podman exit): %.3fs",
		podmanExited.Sub(dieAt).Seconds())
	if cleanupAt != nil {
		line += fmt.Sprintf(" — podman's own cleanup event landed %s after the die",
			cleanupAt.Sub(dieAt).Round(time.Millisecond))
	}
	return line, true, ""
}

// parseDieAndCleanup reads `podman events --format '{{.Time}} {{.Status}}'`
// output: one "<unix-seconds-float> <status>" line per event. The FIRST die
// wins (a stop-timeout escalation can produce several); the LAST cleanup
// wins (it is the terminal one). Unparsable lines are skipped, not fatal —
// best-effort means a partial answer beats none, and an empty log is the
// ordinary rootless-file-backend case.
func parseDieAndCleanup(out string) (dieAt time.Time, cleanupAt *time.Time, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		sec, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		at := time.Unix(int64(sec), int64((sec-float64(int64(sec)))*1e9)).UTC()
		switch fields[1] {
		case "die":
			if !ok {
				dieAt, ok = at, true
			}
		case "cleanup":
			t := at
			cleanupAt = &t
		}
	}
	return dieAt, cleanupAt, ok
}
