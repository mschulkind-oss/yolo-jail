package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
	"github.com/mschulkind-oss/yolo-jail/internal/tracefile"
)

const traceUsage = `Usage: yolo trace [list|show [id]|export [id] [--format chrome|otlp] [-o <file>]]

Read already collected startup traces from the machine-wide host log store.
Bare trace (or list) lists every retained workspace and trace kind, oldest first.
Show and export default to this workspace's latest launch, not a newer preparation
trace. An explicit id can select any retained trace.

Show summarizes the startup window, lane shares, top own-time spans, gaps,
unfinished spans, missing jail half and overlaps. Export writes an offline Chrome
trace-event or OTLP JSON file; without -o, JSON is written to stdout. No viewer
or network is opened, and -o names a literal file in an existing parent directory.

This command does not refresh a running jail's source. Production startup trace
collection is not yet wired in this build; list, show and export read only traces
already present in the host log store.

Performance recording is opt-in and user-only, on the host:
  yolo init-user-config  # first, only if no user config exists
  yolo config set perf_logging true --user

  --help, -h  Show this help.

Examples:
  yolo trace list
  yolo trace show
  yolo trace export --format chrome -o startup-trace.json`

type traceEnv struct {
	root, workspaceCode string
	inJail              bool
}

type traceExportOptions struct {
	id, format, output string
	outputSet          bool
}

func parseTraceExportArgs(args []string) (traceExportOptions, error) {
	options := traceExportOptions{format: "chrome"}
	formatSet := false
	positional := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--format":
			if formatSet {
				return traceExportOptions{}, fmt.Errorf("--format may be used only once")
			}
			if i+1 >= len(args) {
				return traceExportOptions{}, fmt.Errorf("--format needs chrome or otlp")
			}
			i++
			options.format = args[i]
			formatSet = true
		case "-o":
			if options.outputSet {
				return traceExportOptions{}, fmt.Errorf("-o may be used only once")
			}
			if i+1 >= len(args) {
				return traceExportOptions{}, fmt.Errorf("-o needs a literal output filename")
			}
			i++
			options.output = args[i]
			if options.output == "" {
				return traceExportOptions{}, fmt.Errorf("-o needs a non-empty literal output filename")
			}
			options.outputSet = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return traceExportOptions{}, fmt.Errorf("unknown export flag %s", termsafe.Visible(args[i]))
			}
			if positional {
				return traceExportOptions{}, fmt.Errorf("export accepts at most one trace id")
			}
			if !tracefile.IsTraceID(args[i]) {
				return traceExportOptions{}, fmt.Errorf("trace id must be 32-character lowercase hex")
			}
			options.id = args[i]
			positional = true
		}
	}
	if options.format != "chrome" && options.format != "otlp" {
		return traceExportOptions{}, fmt.Errorf("--format must be chrome or otlp")
	}
	return options, nil
}

func traceExportTargetParts(path string) (string, string, error) {
	parent, name := filepath.Split(path)
	if name == "" || name == "." || name == ".." {
		return "", "", fmt.Errorf("output target is not a regular filename")
	}
	if parent == "" {
		parent = "."
	}
	return parent, name, nil
}

func validateTraceExportTarget(root *os.Root, name string) error {
	targetInfo, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot inspect output target")
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("output target is a symlink")
	}
	if !targetInfo.Mode().IsRegular() {
		return fmt.Errorf("output target is not a regular file")
	}
	return nil
}

func traceExportParentStillBound(root *os.Root, parent string) error {
	anchored, err := root.Stat(".")
	if err != nil {
		return fmt.Errorf("cannot verify output parent")
	}
	current, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("output parent changed during export")
	}
	if !os.SameFile(anchored, current) {
		return fmt.Errorf("output parent changed during export")
	}
	return nil
}

func createTraceExportTemp(root *os.Root) (string, *os.File, fs.FileInfo, error) {
	for attempt := 0; attempt < 8; attempt++ {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return "", nil, nil, fmt.Errorf("cannot create output temporary")
		}
		name := ".yolo-trace-export-" + hex.EncodeToString(nonce[:])
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, nil, fmt.Errorf("cannot create output temporary")
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			_ = file.Close()
			_ = root.Remove(name)
			return "", nil, nil, fmt.Errorf("cannot identify output temporary")
		}
		return name, file, info, nil
	}
	return "", nil, nil, fmt.Errorf("cannot reserve output temporary")
}

func traceExportTempIdentity(root *os.Root, name string, original fs.FileInfo) error {
	current, err := root.Lstat(name)
	if err != nil || !os.SameFile(original, current) {
		return fmt.Errorf("output temporary identity changed")
	}
	return nil
}

func removeTraceExportTemp(root *os.Root, name string, original fs.FileInfo) error {
	current, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot inspect unpublished output temporary")
	}
	if !os.SameFile(original, current) {
		return fmt.Errorf("unpublished output temporary identity changed")
	}
	if err := root.Remove(name); err != nil {
		return fmt.Errorf("cannot remove unpublished output temporary")
	}
	return nil
}

type traceExportEncoder func(io.Writer, *tracefile.Trace, string) error

func writeTraceExport(w io.Writer, tr *tracefile.Trace, format string) error {
	switch format {
	case "chrome":
		return tracefile.WriteChrome(w, tr)
	case "otlp":
		return tracefile.WriteOTLP(w, tr)
	default:
		return fmt.Errorf("unsupported trace export format")
	}
}

func writeTraceExportFile(path string, tr *tracefile.Trace, format string, encode traceExportEncoder) (resultErr error) {
	parent, target, err := traceExportTargetParts(path)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return fmt.Errorf("cannot open output parent")
	}
	defer root.Close()
	if err := traceExportParentStillBound(root, parent); err != nil {
		return err
	}
	if err := validateTraceExportTarget(root, target); err != nil {
		return err
	}
	tempName, temp, tempInfo, err := createTraceExportTemp(root)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			if temp != nil {
				_ = temp.Close()
			}
			if err := removeTraceExportTemp(root, tempName, tempInfo); err != nil {
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	if encode == nil {
		encode = traceExportEncoder(writeTraceExport)
	}
	encodeErr := encode(temp, tr, format)
	closeErr := temp.Close()
	temp = nil
	if encodeErr != nil {
		return fmt.Errorf("cannot encode or write selected trace")
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close output temporary")
	}
	if err := traceExportParentStillBound(root, parent); err != nil {
		return err
	}
	if err := validateTraceExportTarget(root, target); err != nil {
		return err
	}
	if err := traceExportTempIdentity(root, tempName, tempInfo); err != nil {
		return err
	}
	if err := root.Rename(tempName, target); err != nil {
		return fmt.Errorf("cannot publish output file")
	}
	published = true
	return nil
}

func traceAnswerHelp(args []string, out io.Writer) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--":
			return false
		case "--help", "-h", "help":
			fmt.Fprintln(out, traceUsage)
			return true
		case "--format", "-o":
			i++ // These options consume the next token, even when it spells a help flag.
		}
	}
	return false
}

func runTrace(args []string) int {
	if traceAnswerHelp(args, os.Stdout) {
		return 0
	}
	env := traceEnv{root: filepath.Join(paths.LogsDir(), "traces"), inJail: config.InJail()}
	if env.inJail {
		// The existing host-origin label is not a path to resolve in the guest.
		if origin := os.Getenv("YOLO_HOST_DIR"); filepath.IsAbs(origin) {
			env.workspaceCode = paths.JailShortHash(runtime.FromResolved(origin))
		}
	} else if ws, ok := resolveWorkspaceRoot(); ok {
		env.workspaceCode = paths.JailShortHash(runtime.FromWorkspace(ws))
	}
	return traceMain(args, env, os.Stdout, os.Stderr)
}

func traceMain(args []string, env traceEnv, out, errw io.Writer, testEncoder ...traceExportEncoder) int {
	if traceAnswerHelp(args, out) {
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
	exportEncoder := traceExportEncoder(writeTraceExport)
	// The callback lets tests pause the actual command at the temporary-file boundary.
	if len(testEncoder) > 0 && testEncoder[0] != nil {
		exportEncoder = testEncoder[0]
	}
	var exportOptions traceExportOptions
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
	case "export":
		var err error
		exportOptions, err = parseTraceExportArgs(rest)
		if err != nil {
			fmt.Fprintf(errw, "yolo trace export: %s; use `yolo trace --help` for the supported offline formats and flags.\n", termsafe.Visible(err.Error()))
			return 2
		}
		id = exportOptions.id
	default:
		fmt.Fprintf(errw, "yolo trace: %q is not a trace command; use `yolo trace` to list, `yolo trace show` to summarize, or `yolo trace export` to write offline JSON.\n", termsafe.Visible(sub))
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
		if sub != "export" {
			traceEmpty(out, env)
		}
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
			// Observation order need not match launch order (a new trace may have no lanes).
			// Equal metadata starts choose the lexicographically greater trace id.
			if selected == nil || tr.Meta.Start > selected.Meta.Start ||
				(tr.Meta.Start == selected.Meta.Start && tr.ID > selected.ID) {
				selected = tr
			}
		}
	}
	if selected == nil {
		if id != "" {
			fmt.Fprintf(errw, "yolo trace: no collected trace %s; use `yolo trace` to list retained ids.\n", id)
		} else if env.inJail && env.workspaceCode == "" {
			fmt.Fprintln(errw, "yolo trace: host workspace identity is unavailable here; use `yolo trace` to list retained traces, then `yolo trace show <id>` to select one explicitly.")
		} else {
			fmt.Fprintln(errw, "yolo trace: no collected launch for this workspace; use `yolo trace` to list all workspaces and kinds.")
		}
		if env.inJail {
			fmt.Fprintln(errw, "Run `yolo trace show` on the host from the workspace to see its host log store.")
		}
		return 1
	}
	traceDiagnostics(errw, selected)
	if sub == "export" {
		var exportErr error
		if exportOptions.outputSet {
			exportErr = writeTraceExportFile(exportOptions.output, selected, exportOptions.format, exportEncoder)
		} else if err := exportEncoder(out, selected, exportOptions.format); err != nil {
			exportErr = fmt.Errorf("cannot encode or write selected trace")
		}
		if exportErr != nil {
			fmt.Fprintf(errw, "yolo trace: cannot write offline %s export: %s.\n", exportOptions.format, termsafe.Visible(exportErr.Error()))
			fmt.Fprintf(errw, "Next: inspect the selected trace with `yolo trace show %s`; then retry `yolo trace export %s -o <file>` using a regular file in an existing writable directory. If collected spans are malformed, ask the host operator to repair the trace before retrying.\n", selected.ID, selected.ID)
			return 1
		}
		return 0
	}
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
