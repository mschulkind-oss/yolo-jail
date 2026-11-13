package cli

import (
	"context"
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"io"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

func printPreparationStatus(out, errw io.Writer, color bool, key string) int {
	state, err := loadPreparationState()
	if err != nil {
		fmt.Fprintf(errw, "preparation status: %v\n", err)
		return 1
	}
	pr := richtext.Printer{W: out, Color: color}
	if len(state.Subscriptions) == 0 {
		if key == "" {
			pr.Printf("[dim]No built-tree preparations are registered.[/dim]")
		}
		return 0
	}
	record, live, probeErr := probePreparationWorker(preparationRoot())
	switch live {
	case preparationAlive:
		pr.Printf("Tree preparation worker: running (authenticated incarnation %s; pid %d is diagnostic)", record.Identity, record.PID)
	case preparationUnknown:
		pr.Printf("Tree preparation worker: unknown (%v); state retained — retry `yolo pack prepare status`", probeErr)
	default:
		// A new incarnation rewrites the worker record, so a recorded stop request is the last
		// worker's: it stopped because it was asked to, and stays stopped until resumed.
		if state.Worker.StopRequested && !state.Worker.StoppedAt.IsZero() {
			pr.Printf("Tree preparation worker: not running; stopped by `yolo pack prepare stop` at %s; run `yolo pack prepare start` to reauthorize and resume", state.Worker.StoppedAt.Local().Format(time.RFC3339))
			break
		}
		pr.Printf("Tree preparation worker: not running; run `yolo pack prepare start` to reauthorize and resume")
	}

	records := append([]preparationRecord(nil), state.Subscriptions...)
	sortPreparationRecords(records)
	found := false
	for _, r := range records {
		if key != "" && (r.Key != key || r.Runtime != captureRuntime() || r.Platform != capture.Platform()) {
			continue
		}
		found = true
		phase := r.Phase
		if phase == "" {
			phase = "pending"
		}
		pr.Printf("%s [%s/%s] generation %d (diagnostic only; not admission): %s%s", r.Key, r.Runtime, r.Platform, r.Generation, phase,
			map[bool]string{true: " (inactive)", false: ""}[!r.Active])
		if r.Retained != nil && (r.Retained.Generation != r.Generation || r.Retained.InputIdentity != r.InputIdentity) {
			pr.Printf("  retained diagnostic generation %d (previous declaration, not current admission): %s", r.Retained.Generation, richtext.Escape(r.Retained.Phase))
			if r.Retained.Reason != "" {
				pr.Printf("  retained reason: %s", richtext.Escape(r.Retained.Reason))
			}
			if r.Retained.Log != "" {
				pr.Printf("  retained log: %s", r.Retained.Log)
			}
		}
		pr.Printf("  input: %s", r.InputIdentity)
		pr.Printf("  worker: %s", state.Worker.Identity)
		if r.ServingEntry != "" {
			pr.Printf("  admitted entry: %s (diagnostic only; not admission)", r.ServingEntry)
		}
		if !r.At.IsZero() {
			pr.Printf("  updated: %s", r.At.Format(time.RFC3339))
		}
		if !r.RetryAt.IsZero() {
			pr.Printf("  retry after: %s", r.RetryAt.Format(time.RFC3339))
		}
		if r.Reason != "" {
			pr.Printf("  reason: %s", richtext.Escape(r.Reason))
		}
		if r.Log != "" {
			pr.Printf("  log: %s", r.Log)
		}
	}
	if key != "" && !found {
		pr.Printf("[dim]No preparation subscription is recorded for %s.[/dim]", richtext.Escape(key))
	}
	return 0
}

func packPrepare(args []string, out, errw io.Writer, color bool) int {
	if len(args) != 1 {
		fmt.Fprintln(errw, "yolo pack prepare: use `yolo pack prepare start`, `stop` or `status`")
		return 2
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return 2
		}
		return printPreparationStatus(out, errw, color, "")
	case "start":
		state, err := preparationSubscriptions()
		if err != nil {
			fmt.Fprintf(errw, "yolo pack prepare start: %v\n", err)
			return 1
		}
		current, err := currentPreparationTrees(state)
		if err != nil {
			fmt.Fprintf(errw, "yolo pack prepare start: %v — repair %s, then run `yolo pack prepare start` again\n", err, paths.UserConfigDisplayPath())
			return 1
		}
		state = refreshPreparationSubscriptions(state, current)
		eligible := false
		for _, r := range state {
			eligible = eligible || r.Active
		}
		if !eligible {
			fmt.Fprintln(out, "No selected next-launch tree preparations are registered; nothing was started.")
			return 0
		}
		if err := ensurePreparationScheduler(); err != nil {
			fmt.Fprintf(errw, "yolo pack prepare start: could not start the owned host worker (%v) — retry `yolo pack prepare start`\n", err)
			return 1
		}
		fmt.Fprintln(out, "The host tree-preparation worker is running; inspect progress with `yolo pack prepare status`.")
		return 0
	case "stop":
		record, live, err := probePreparationWorker(preparationRoot())
		if live == preparationGone {
			fmt.Fprintln(out, "No live preparation worker owns the scheduler lease; recorded state was retained.")
			return 0
		}
		if live == preparationUnknown {
			fmt.Fprintf(errw, "yolo pack prepare stop: owner unknown (%v) — retry status; state retained and no process was signalled\n", err)
			return 1
		}
		if err := controlPreparationWorker(context.Background(), preparationRoot(), record.Identity, "stop"); err != nil {
			fmt.Fprintf(errw, "yolo pack prepare stop: %v — retry status\n", err)
			return 1
		}

		fmt.Fprintln(out, "Stop requested for the proven preparation worker; its owned child work will drain and state will be retained.")
		return 0
	default:
		fmt.Fprintf(errw, "yolo pack prepare: unknown action %q — use start, stop or status\n", args[0])
		return 2
	}
}
