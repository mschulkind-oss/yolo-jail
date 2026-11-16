package run

import (
	"fmt"
	"io"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

const approvalDiffUsage = `Usage: yolo config approval-diff

Show the stored and current workspace config approval identities. This command
is read-only and does not compare repository scope or loophole grants.

Flags:
  --help, -h               Show this help.`

// ConfigApprovalDiff implements the read-only `yolo config approval-diff` command.
func ConfigApprovalDiff(args []string, stdout, stderr io.Writer, color bool) int {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" || arg == "help" {
			_, _ = io.WriteString(stdout, approvalDiffUsage+"\n")
			return 0
		}
	}
	if len(args) != 0 {
		fmt.Fprintf(stderr, "yolo config approval-diff: unexpected argument %q\n\n%s\n", args[0], approvalDiffUsage)
		return 2
	}
	opts := NewDefaultOptions()
	opts.Color = color
	opts.Stdout, opts.Stderr = stdout, stderr
	fillDefaults(&opts)
	return opts.configApprovalDiff()
}

func (o *Options) configApprovalDiff() int {
	if o.inJail() && config.IsJailOwnWorkspace(o.Workspace) {
		o.pr(o.Stderr).print("[bold red]yolo config approval-diff is available on the host only; ask the user to run `yolo config approval-diff` on the host, in this workspace.[/bold red]")
		return 1
	}

	comparison, err := config.CompareApprovalRecord(o.Workspace)
	if err != nil {
		fmt.Fprintf(o.Stderr, "yolo config approval-diff: %s: %v\n", config.ApprovalSnapshotPath(o.Workspace), err)
		return 1
	}
	fmt.Fprintf(o.Stdout, "Approval record: %s\n", comparison.RecordPath)
	fmt.Fprintln(o.Stdout, "current: computed now, not saved")
	fmt.Fprintln(o.Stdout, "scope and loophole grants are not compared.")

	switch {
	case !comparison.HasRecord:
		fmt.Fprintln(o.Stdout, "No approval is recorded for this workspace yet.")
		fmt.Fprintln(o.Stdout, comparison.Current)
	case comparison.Malformed:
		fmt.Fprintln(o.Stdout, "The stored record is not valid JSON; comparing it as text.")
		writeApprovalDiffLines(o.Stdout, comparison.DiffLines)
	case comparison.Equal:
		fmt.Fprintln(o.Stdout, "No difference: no config approval is pending.")
	default:
		writeApprovalDiffLines(o.Stdout, comparison.DiffLines)
	}
	return 0
}

func writeApprovalDiffLines(w io.Writer, lines []string) {
	if len(lines) > 0 {
		_, _ = io.WriteString(w, strings.Join(lines, "\n")+"\n")
	}
}
