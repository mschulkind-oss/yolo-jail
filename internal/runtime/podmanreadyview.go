package runtime

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/progress"
)

// podmanreadyview.go is what the readiness gate SAYS, in one place because the launch and
// `yolo check` must say the same thing (PR-D6, PR-D9 of docs/design/podman-reboot-readiness.md):
// the progress line and its detail, one line above it per early exit, the line it closes with,
// the refusal, and the per-attempt timing note.

// PodmanReadyLabel is the gate's progress line.
const PodmanReadyLabel = "Checking that podman is running"

// WaitForPodmanShowing runs the gate for rt under line (nil draws nothing): the detail says
// how long of the budget has gone, each early exit is printed above the line as it happens,
// and note receives each attempt's `runtime.ready.attempt` detail (nil records nothing). The
// caller closes the line with the result's DoneText.
func WaitForPodmanShowing(line *progress.Line, rt string, seams ReadySeams, note func(detail string)) ReadyResult {
	stop := showReadyWait(line, PodmanReadyBudget)
	res := WaitForPodman(PodmanInfoArgv(rt), PodmanReadyBudget, seams, ReadyHooks{
		OnAttempt: func(n int, a Attempt, f Failure) {
			if note != nil {
				note(AttemptNote(n, a, f))
			}
		},
		OnRetry: func(_ int, a Attempt, f Failure, wait time.Duration) {
			line.Println(fmt.Sprintf("%s info: %s; retrying in %s", rt, Describe(a, f), wait))
		},
	})
	stop()
	line.Set("")
	return res
}

// showReadyWait keeps the line's detail current while the gate waits: "waiting for podman to
// answer (it may be finishing post-boot cleanup), 14s of 60s". Its own clock, the wall clock,
// because the line is a person's view of a real wait. The returned func stops it.
func showReadyWait(line *progress.Line, budget time.Duration) func() {
	if line == nil {
		return func() {}
	}
	start := time.Now()
	set := func() {
		line.Set(fmt.Sprintf("waiting for podman to answer (it may be finishing post-boot cleanup), %ds of %ds",
			int(time.Since(start).Seconds()), int(budget.Seconds())))
	}
	set()
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				set()
			}
		}
	}()
	return func() {
		close(stop)
		<-stopped
	}
}

// DoneText is what the progress line closes with: "done", "done after 3 attempts" when the
// answer took more than one, or "failed".
func (r ReadyResult) DoneText() string {
	switch {
	case r.Outcome != PodmanReady:
		return "failed"
	case len(r.Attempts) > 1:
		return fmt.Sprintf("done after %d attempts", len(r.Attempts))
	}
	return "done"
}

// Refusal is the body of the refusal a gate that did not answer ends in: the attempt count,
// the elapsed time, podman's last reason line, the fix when there is one, and any podman left
// running (PR-D9). "" for a ready result.
func (r ReadyResult) Refusal(rt string) string {
	if r.Outcome == PodmanReady {
		return ""
	}
	last := r.Last()
	attempts := fmt.Sprintf("%d attempt", len(r.Attempts))
	if len(r.Attempts) != 1 {
		attempts += "s"
	}
	span := fmt.Sprintf("%s, %.1fs", attempts, r.Elapsed.Seconds())
	var b strings.Builder
	switch r.Outcome {
	case PodmanNotStarted:
		fmt.Fprintf(&b, "%s info could not run: %s.", rt, r.Failure.Line)
		if r.Failure.Fix != "" {
			fmt.Fprintf(&b, "\nFix: %s.", r.Failure.Fix)
		}
	case PodmanRefused:
		fmt.Fprintf(&b, "%s info failed with an error that does not clear on its own (%s): %s\nFix: %s.",
			rt, span, Describe(last, r.Failure), r.Failure.Fix)
	case PodmanInterrupted:
		fmt.Fprintf(&b, "Interrupted while waiting for %s info to answer (%s).", rt, span)
	default:
		fmt.Fprintf(&b, "%s info did not answer within %ds (%s)", rt, int(PodmanReadyBudget.Seconds()), span)
		// Podman's own reason whenever it gave one: the last attempt's, or — when the last
		// attempt was still running at the end of the budget — the last error an earlier one
		// gave, which is otherwise dropped for a line that only says a podman is running.
		if answered, ok := r.lastAnswered(); ok && r.Failure.Line != "" {
			if answered == len(r.Attempts)-1 {
				fmt.Fprintf(&b, "; the last attempt: %s", Describe(last, r.Failure))
			} else {
				fmt.Fprintf(&b, "; the last error it gave: %s", Describe(r.Attempts[answered], r.Failure))
			}
		}
		b.WriteString(".")
	}
	if r.Running > 0 {
		fmt.Fprintf(&b, "\n%s (pid %d) is still running; yolo left it to finish.", rt, r.Running)
	}
	return b.String()
}

// lastAnswered is the index of the last attempt that ended with an answer — it exited, or it
// could not start — the one r.Failure classifies; false when none did.
func (r ReadyResult) lastAnswered() (int, bool) {
	for i := len(r.Attempts) - 1; i >= 0; i-- {
		if r.Attempts[i].Exited || r.Attempts[i].StartErr != nil {
			return i, true
		}
	}
	return 0, false
}

// AttemptNote is one `runtime.ready.attempt` note: the attempt's number, how long the gate
// waited on it, what it did, and podman's reason line, truncated.
func AttemptNote(n int, a Attempt, f Failure) string {
	outcome := fmt.Sprintf("exit=%d", a.RC)
	switch {
	case a.StartErr != nil:
		outcome = "not-started"
	case a.Interrupted:
		outcome = "interrupted"
	case !a.Exited:
		outcome = "still-running"
	}
	note := fmt.Sprintf("n=%d dur=%.3fs outcome=%s", n, a.Duration.Seconds(), outcome)
	if f.Line != "" {
		note += " stderr=" + clipRunes(f.Line, 160)
	}
	return note
}

// clipRunes cuts s to n runes, marking the cut.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
