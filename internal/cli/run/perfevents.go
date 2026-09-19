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
// Container has no events command), a 3s exec timeout, and `--stream=false` on
// every invocation — `podman events --since` alone STREAMS, and a diagnostics
// query that never returns is the failure this feature exists to end.

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// windowAEventsTimeout bounds the podman events query. Generous against a
// busy event log, short against a human watching a prompt.
const windowAEventsTimeout = 3 * time.Second

// windowAResult is one attribution attempt's whole answer, in the four forms
// its two consumers need: the measured gap (recorded in the timing log), the
// line the report prints, the long reason a reader sees when there is no gap,
// and the short stable token the log records in its place.
//
// The token exists because the two consumers sit on different gates. The long
// reason is prose for a human reading a table; a quietly-recording launch
// prints nothing at all, and "unattributed, and here is the class" still has to
// survive into the file — greppable, and stable across rewordings of the prose.
type windowAResult struct {
	dur    time.Duration // die → podman exit; meaningful only when ok
	line   string        // the report's line; "" when unattributed
	reason string        // why there is no line; "" when attributed
	token  string        // short failure class for the log; "" = not applicable
	ok     bool
}

// attributeWindowA queries podman's event log for the container's die (and,
// if it exists, cleanup) timestamps and measures the die → podman-exit gap.
// podmanExited is when the proxy's child (the `podman run` process) was
// reaped — the collector's child.exited mark. ok=false measures nothing, for
// every failure mode: wrong runtime, missing binary, timeout, nonzero rc,
// unparsable or absent events (a rootless file backend can legitimately have
// none).
//
// A result carries the LINE and, when there is none, WHY. Silence was the
// original design and it was wrong in practice: two real-host launches produced no
// attribution at all and no way to tell whether the query failed, timed out, or
// simply found nothing — an observability feature that cannot explain its own
// blank is the failure it exists to remove. The reason is rendered once, dim,
// beneath the table; recordWindowA puts the token in the file for the launches
// that render nothing.
//
// This is the CALLEE. It is never called directly by the pipeline — the arms
// call recordWindowA, which is what gates and records it.
func (o *Options) attributeWindowA(cname, rt string, since, podmanExited time.Time) windowAResult {
	if rt != "podman" || o.Perf == nil {
		return windowAResult{} // not applicable: say nothing, record nothing
	}
	// `--stream=false` IS THE BOUND, AND `--until` MUST NOT BE HERE AT ALL.
	//
	// This query carried `--until` for its whole life, on the model that a bound
	// was needed to stop `podman events` streaming and that the bound had to be a
	// TIMESTAMP. Two fixes chased that model — the first because `--until
	// podmanExited+5s` made podman wait for a wall-clock moment that had not
	// arrived, stalling every timed shutdown for the full windowAEventsTimeout
	// (measured 3.002s, 2026-09-06); the second because RFC3339 second-truncation
	// then put `--until` just BEFORE the die it was hunting (2026-09-13). Both
	// were real. Neither made the query work.
	//
	// MEASURED, podman 5.8.6, file backend, 2026-09-19: `--until <now>` returns
	// NOTHING — 3 runs of the shipped argv against a container whose die was in
	// the log, 3 empty answers, while the same argv with `--stream=false` and no
	// `--until` returned all six of that container's events in 0.012s, 3 for 3.
	// An `--until` at or before now makes podman stop at EOF IMMEDIATELY, so what
	// comes back is a racy prefix of the log — usually empty, sometimes one line.
	// That is what `no_die` had been reporting: 8 of 8 recorded shutdowns in the
	// maintainer's host perf log, every one of them after Window A shipped, so the
	// feature built to price this window had never once priced it.
	//
	// `--stream=false` is what the bound always meant — "return what you have and
	// exit" — and it says so to both event backends rather than encoding it as a
	// timestamp the reader has to interpret. It also removes the need for any
	// slack: attribution runs after the whole teardown chain, so every event
	// conmon wrote is already in the log.
	//
	// `--since` stays, at second granularity: truncating the START of a window
	// downward only widens it, which can never hide an event.
	//
	// {{.TimeNano}}, NOT {{.Time}}: `{{.Time}}` renders integer Unix SECONDS, so
	// it truncated the die downward and inflated every measured window by up to a
	// second — invisible on a 10s Window A and the whole of a fast one.
	argv := []string{
		"podman", "events",
		"--since", since.UTC().Format(time.RFC3339),
		"--stream=false",
		"--filter", "container=" + cname,
		"--format", "{{.TimeNano}} {{.Status}}",
	}
	sp := o.Perf.Span("shutdown.window_a_podman_events")
	res := o.Exec(argv, "", nil, windowAEventsTimeout)
	sp.End()
	switch {
	case res.Timeout:
		return windowAResult{token: "timeout", reason: fmt.Sprintf(
			"Window A unattributed: `podman events` did not answer within %s", windowAEventsTimeout)}
	case !res.Ran:
		return windowAResult{token: "not_run",
			reason: "Window A unattributed: `podman events` could not be run"}
	case res.RC != 0:
		return windowAResult{token: "rc", reason: fmt.Sprintf(
			"Window A unattributed: `podman events` exited %d", res.RC)}
	}
	dieAt, cleanupAt, ok := parseDieAndCleanup(res.Stdout)
	if !ok {
		// A host whose events backend keeps nothing (the rootless file backend
		// expires them), so it is stated plainly rather than as a fault.
		//
		// ⚠ SUSPECT THE QUERY BEFORE THE BACKEND. This token was 8-for-8 on the
		// maintainer's host and the backend was never the reason: twice it was
		// `--until` (see the argv note above) and once it was this parser looking
		// for Docker's spelling of a status podman does not emit. The backend
		// explanation is the one that sounds right and has been wrong every time.
		return windowAResult{token: "no_die",
			reason: "Window A unattributed: no container `died` event in podman's log for this jail"}
	}
	dur := podmanExited.Sub(dieAt)
	line := fmt.Sprintf("Window A (container died → podman exit): %.3fs", dur.Seconds())
	if cleanupAt != nil {
		line += fmt.Sprintf(" — podman's own last teardown event landed %s after the death",
			cleanupAt.Sub(dieAt).Round(time.Millisecond))
	}
	return windowAResult{dur: dur, line: line, ok: true}
}

// recordWindowA runs the attribution ONCE per launch and puts the answer in the
// timing log. It is the piece that moved gates: attribution used to live inside
// emitTimingReportLocked, so the only launches that ever learned Window A's cost
// were the ones that typed --timing — and Window A is precisely the stretch a
// user cannot predict wanting to have measured, because they find out it was
// slow by waiting through it. The query now rides the RECORDING gate with every
// other event, and the table stays on the reporting gate (D15; D12 is about
// what PRINTS, and never said the file should be missing a number yolo can get).
//
// Both teardown arms call it, each after its own chain has finished, so every
// event conmon wrote about this container is already in the log when it asks (D9).
// The Once is what makes the ordinary signal-path interleaving — both arms
// running — one query rather than two, and it is also what makes the read in
// emitTimingReportLocked safe from the other arm's goroutine: whichever arm
// gets there first writes o.windowA inside Do, and every later Do returns only
// after that write is visible.
//
// A launch on a non-podman runtime, or an arm with no child.exited mark (the
// attach arm: `podman exec` has no container death to attribute), records
// nothing at all — there was no window, so there is no blank to explain.
func (o *Options) recordWindowA(cname, rt string) {
	if o.perfWindowAOnce == nil {
		return // no collector: this launch recorded nothing (see initPerf)
	}
	o.perfWindowAOnce.Do(func() {
		child, ok := o.Perf.LastEvent("child.exited")
		if !ok {
			return
		}
		res := o.attributeWindowA(cname, rt, o.Perf.StartTime(), child.At)
		o.windowA = res
		switch {
		case res.ok:
			o.Perf.Record("shutdown.window_a", res.dur)
		case res.token != "":
			o.Perf.Mark("shutdown.window_a_unattributed." + res.token)
		}
	})
}

// parseDieAndCleanup reads `podman events --format '{{.TimeNano}} {{.Status}}'`
// output: one "<unix-nanoseconds> <status>" line per event. The FIRST death
// wins (a stop-timeout escalation can produce several); the LAST teardown event
// wins (it is the terminal one). Unparsable lines are skipped, not fatal —
// best-effort means a partial answer beats none, and an empty log is the
// ordinary rootless-file-backend case.
//
// ⚠ THE STATUS IS `died`, NOT `die`. `die` is DOCKER's spelling; podman's event
// for a container whose process exited is `died`, on every backend, because the
// status string is one enum serialized by all of them. This parser looked for
// `die` from its first commit, so it could never have matched — measured against
// podman 5.8.6's own events.log on 2026-09-19, which writes
// `"Status":"died"`.
//
// ⚠ AND THERE IS NO `cleanup` EVENT FOR A `--rm` CONTAINER. The terminal event
// in that measurement is `remove`. `cleanup` is a real podman status and is kept
// for the configurations that emit one, but a run that only ever saw `cleanup`
// would report the offset on approximately no launches, `--rm` being how every
// jail runs.
func parseDieAndCleanup(out string) (dieAt time.Time, cleanupAt *time.Time, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		nanos, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		at := time.Unix(0, nanos).UTC()
		switch fields[1] {
		case "died":
			if !ok {
				dieAt, ok = at, true
			}
		case "cleanup", "remove":
			t := at
			cleanupAt = &t
		}
	}
	return dieAt, cleanupAt, ok
}
