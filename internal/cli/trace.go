package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
	"github.com/mschulkind-oss/yolo-jail/internal/tracefile"
)

const traceUsage = `Usage: yolo trace [list|show [id]]

Read already collected startup traces from the machine-wide host log store.
Bare trace (or list) lists every retained workspace and trace kind, oldest first.
Show defaults to this workspace's latest launch, not a newer preparation trace.
An explicit id can select any retained trace.

The summary shows the startup window, lane shares, the top fifteen own-time
spans, gaps over 100 ms, unfinished spans, missing jail half and overlaps.
Incomplete windows stop at the last collected event, not the current time.

This offline command does not refresh a running jail's source. Production
startup trace collection is not yet wired. Export, -o, format selection and
collector/network flags are not implemented; no viewer or network is opened.

Performance recording is opt-in and user-only, on the host:
  yolo init-user-config  # first, only if no user config exists
  yolo config set perf_logging true --user

  --help, -h  Show this help.`

type traceEnv struct {
	root, workspaceCode string
	inJail              bool
}

func runTrace(args []string) int {
	if answerHelp("trace", args, os.Stdout) {
		return 0
	}
	env := traceEnv{root: filepath.Join(paths.LogsDir(), "traces"), inJail: config.InJail()}
	ws := ""
	if env.inJail {
		ws = config.JailWorkspace()
	} else if root, ok := resolveWorkspaceRoot(); ok {
		ws = root
	}
	if ws != "" {
		env.workspaceCode = paths.JailShortHash(runtime.FromWorkspace(ws))
	}
	return traceMain(args, env, os.Stdout, os.Stderr)
}

func traceMain(args []string, env traceEnv, out, errw io.Writer) int {
	if answerHelp("trace", args, out) {
		return 0
	}
	rest := args
	if len(rest) > 0 && rest[0] == "trace" {
		rest = rest[1:]
	}
	sub := "list"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	id := ""
	switch sub {
	case "list":
		if len(rest) != 0 {
			fmt.Fprintln(errw, "yolo trace list takes no flags or arguments; use `yolo trace --help` for the offline commands.")
			return 2
		}
	case "show":
		if len(rest) > 1 || (len(rest) == 1 && !tracefile.IsTraceID(rest[0])) {
			fmt.Fprintln(errw, "yolo trace show takes one 32-character lowercase hex id, or no id for this workspace's latest launch; use `yolo trace` to list ids.")
			return 2
		}
		if len(rest) == 1 {
			id = rest[0]
		}
	default:
		fmt.Fprintf(errw, "yolo trace: %q is not implemented; use `yolo trace` to list or `yolo trace show` to summarize collected traces. Export and its flags are not implemented.\n", termsafe.Visible(sub))
		return 2
	}

	traces, skipped, err := tracefile.ReadRoot(env.root)
	traceSkipped(errw, skipped)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(errw, "yolo trace: cannot read collected traces: %s.\n", termsafe.Visible(err.Error()))
		if env.inJail {
			fmt.Fprintln(errw, "Run `yolo trace` on the host; this jail may not have the host log store mounted.")
		} else {
			fmt.Fprintln(errw, "Ask the host operator to restore read access to the host log store, then run `yolo trace` again.")
		}
		return 1
	}
	if len(traces) == 0 {
		traceEmpty(out, env)
		if sub == "list" {
			return 0
		}
	}
	if sub == "list" {
		fmt.Fprintln(out, "Collected traces (oldest first):")
		for _, tr := range traces {
			workspace, jail := "unknown", "unknown"
			if tr.Meta != nil {
				workspace, jail = tr.Meta.Workspace, tr.Meta.Jail
			}
			fmt.Fprintf(out, "  %s  %s  workspace %s  jail %s  %s\n", tr.ID, traceText(tr.Kind()), traceText(workspace), traceText(jail), traceInstant(tr.Start))
			traceDiagnostics(errw, tr)
		}
		fmt.Fprintln(out, "Next: `yolo trace show <id>` summarizes a collected trace.")
		return 0
	}
	var selected *tracefile.Trace
	for _, tr := range traces {
		if id != "" {
			if tr.ID == id {
				selected = tr
			}
		} else if env.workspaceCode != "" && tr.Kind() == "launch" && tr.Meta.Workspace == env.workspaceCode {
			selected = tr // ReadRoot orders oldest first.
		}
	}
	if selected == nil {
		if id != "" {
			fmt.Fprintf(errw, "yolo trace: no collected trace %s; use `yolo trace` to list retained ids.\n", id)
		} else {
			fmt.Fprintln(errw, "yolo trace: no collected launch for this workspace; use `yolo trace` to list all workspaces and kinds.")
		}
		if env.inJail {
			fmt.Fprintln(errw, "Run `yolo trace show` on the host from the workspace to see its host log store.")
		}
		return 1
	}
	traceDiagnostics(errw, selected)
	traceSummary(out, tracefile.Summarize(selected, tracefile.Options{Others: traces}))
	return 0
}

func traceEmpty(out io.Writer, env traceEnv) {
	fmt.Fprintln(out, "No collected startup traces in this view.")
	if env.inJail {
		fmt.Fprintln(out, "Run `yolo trace` on the host; this jail may not have the host log store mounted.")
	}
	fmt.Fprintln(out, "For performance recording on the host, if no user config exists, first run `yolo init-user-config`; then run `yolo config set perf_logging true --user`.")
	fmt.Fprintln(out, "Startup trace collection is not yet wired in this build; this command reads already collected traces only.")
}

func traceText(s string) string {
	if s == "" {
		return "unknown"
	}
	return termsafe.Visible(s)
}

func traceInstant(t int64) string {
	return time.Unix(0, t).UTC().Format(time.RFC3339Nano)
}

func traceSkipped(errw io.Writer, skipped []tracefile.Skipped) {
	for _, s := range skipped {
		fmt.Fprintf(errw, "Skipped %s: %s; ask the host operator to inspect the collected file, then retry `yolo trace`.\n", termsafe.Visible(s.Path), termsafe.Visible(s.Reason))
	}
}

func traceDiagnostics(errw io.Writer, tr *tracefile.Trace) {
	traceSkipped(errw, tr.Skipped)
	for _, a := range tr.Anomalies {
		fmt.Fprintf(errw, "Trace %s anomaly: %s; ask the host operator to inspect the collected lanes.\n", tr.ID, termsafe.Visible(a))
	}
	for _, lane := range tr.Lanes {
		if lane.Dropped > 0 || lane.Truncated {
			fmt.Fprintf(errw, "Trace %s lane %s: %d dropped rows, truncated=%t; ask the host operator to inspect the collected lane.\n", tr.ID, traceText(lane.Name), lane.Dropped, lane.Truncated)
		}
	}
}

func traceSummary(out io.Writer, s *tracefile.Summary) {
	fmt.Fprintf(out, "Trace %s (%s)\n", s.Trace.ID, traceText(s.Trace.Kind()))
	state := "complete (session.exec)"
	if !s.WindowEnded {
		state = "incomplete (last collected event; session.exec missing)"
	}
	fmt.Fprintf(out, "Startup window: %s to %s (%s), %s\n", traceInstant(s.WindowStart), traceInstant(s.WindowEnd), s.Window(), state)
	fmt.Fprintln(out, "Lane shares (busy / own):")
	for _, lane := range s.Lanes {
		fmt.Fprintf(out, "  %s pid %d: %s / %s\n", traceText(lane.Lane.Name), lane.Lane.PID, lane.Busy, lane.Own)
	}
	fmt.Fprintln(out, "Top own-time spans (up to 15):")
	for _, sp := range s.Top {
		fmt.Fprintf(out, "  %s  %s  lane %s\n", sp.Own, traceText(sp.Span.Name), traceText(sp.Span.Lane.Name))
	}
	fmt.Fprintf(out, "Unattributed time: %s\nGaps over 100 ms:\n", s.Unattributed)
	for _, gap := range s.Gaps {
		within := "startup window"
		if gap.Within != nil {
			within = traceText(gap.Within.Name)
		}
		fmt.Fprintf(out, "  %s to %s: %s within %s (%s -> %s)\n", traceInstant(gap.Start), traceInstant(gap.End), gap.Duration(), within, traceText(gap.Before), traceText(gap.After))
	}
	fmt.Fprintf(out, "Unfinished spans: %d\n", len(s.NotEnded))
	for _, sp := range s.NotEnded {
		fmt.Fprintf(out, "  %s  lane %s  since %s\n", traceText(sp.Name), traceText(sp.Lane.Name), traceInstant(sp.Start))
	}
	if s.JailMissing {
		fmt.Fprintln(out, "Missing jail half: no collected jail lane; ask the host operator to check trace collection.")
	}
	fmt.Fprintf(out, "Overlapping traces: %d\n", len(s.Overlapping))
	for _, overlap := range s.Overlapping {
		fmt.Fprintf(out, "  %s (%s) %s to %s\n", overlap.Trace.ID, traceText(overlap.Trace.Kind()), traceInstant(overlap.Start), traceInstant(overlap.End))
	}
	fmt.Fprintln(out, "Offline snapshot only; run `yolo trace` to list other retained traces.")
}
