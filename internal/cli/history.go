package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// history.go is `yolo history`, the verb that answers "what just happened in this jail"
// (docs/design/jail-history.md §5.3, JH-D7). This yolo builds its thin first slice (JH-D16):
// the `requests` and `r-<id>` subjects, read from the workspace's approval records
// (.yolo/approvals, internal/approvalwait), the same in a jail and on the host. The other
// subjects need the workspace event record, which is not built yet, and say so naming what
// does answer today. MAIN ROUTES IT BEFORE THE FRONT DOOR (cli.go), as it does `yolo wait`:
// an agent reads its output, and the banner or an update notice would be noise in it.

const historyUsage = `Usage: yolo history [-C DIR] [--since DURATION|TIME] [--json] SUBJECT

Report what happened to this workspace's broker requests: how each was
answered, when it ran, and what to do next. Runs in a jail and on the host.

Subjects:
  requests   Every request of this workspace, oldest first: filed, answered
             how (once, for a time, added, remembered, denied) and by whom,
             spent, expired or closed, each with its next step.
  r-<id>     One request, end to end.

Flags:
  -C DIR           Find the workspace from DIR instead of this directory.
  --since VALUE    Only requests filed since then: a duration such as 30m, 2h
                   or 7d, or an RFC 3339 time.
  --json           The records as stored, one JSON object per line.
  --help, -h       Show this help.

Examples:
  yolo history requests
  yolo history r-0123456789abcdef0123456789abcdef
  yolo history --since 2h requests

` + "`yolo wait`" + ` lists only what still waits on you; this lists everything.`

func runHistoryVerb(args []string) int { return historyMain(args[1:]) }

// historyMain is `yolo history` after the verb.
func historyMain(args []string) int {
	if answerHelp("history", args, os.Stdout) {
		return 0
	}
	fail := func(format string, v ...any) int {
		fmt.Fprintf(os.Stderr, "yolo history: "+format+"; see `yolo history --help`.\n", v...)
		return approvalwait.ExitUsage
	}
	var dir, subject string
	o := approvalwait.HistoryOpts{InJail: config.InJail()}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, val, glued := strings.Cut(arg, "=")
		switch name {
		case "-C", "--since":
			if !glued {
				if i+1 >= len(args) {
					return fail("%s needs a value", name)
				}
				i++
				val = args[i]
			}
			if name == "-C" {
				dir = val
				continue
			}
			t, err := parseSince(val, time.Now())
			if err != nil {
				return fail("the since value %s: %v", shquote.QuoteDisplay(val), err)
			}
			o.Since = t
		case "--json":
			if glued {
				return fail("the json flag takes no value")
			}
			o.JSON = true
		default:
			if subject != "" || strings.HasPrefix(arg, "-") {
				return fail("yolo history does not take %s", shquote.QuoteDisplay(arg))
			}
			subject = arg
		}
	}
	switch {
	case subject == "requests":
	case approvalwait.LooksLikeID(subject):
		o.ID = subject
	default:
		// The bare form and the other subjects read the workspace event record, which this
		// yolo does not write yet (jail-history.md OQ-JH1).
		what := "with no subject"
		if subject != "" {
			what = "for " + shquote.QuoteDisplay(subject)
		}
		fmt.Fprintf(os.Stderr, "yolo history: this yolo records broker requests only, so it has nothing to report %s yet. "+
			"Next: run `yolo history requests` for this workspace's requests, or `yolo history r-<id>` for one; "+
			"for a launch or a config change, read .yolo/logs/launch.log and .yolo/logs/boot.log in the workspace (directly in .yolo for a launch by an older yolo).\n", what)
		return approvalwait.ExitUsage
	}
	ws, ok := historyWorkspace(dir)
	if !ok {
		return approvalwait.ExitUsage
	}
	o.Workspace = ws
	return approvalwait.History(o)
}

// historyWorkspace is the workspace at or above dir (the working directory when empty), as
// `yolo wait` finds it.
func historyWorkspace(dir string) (string, bool) {
	start := dir
	if start == "" {
		start, _ = os.Getwd()
	}
	if abs, err := filepath.Abs(start); err == nil {
		start = abs
	}
	if r, err := filepath.EvalSymlinks(start); err == nil {
		start = r
	}
	ws, ok := resolveWorkspaceRootFrom(start)
	if !ok {
		fmt.Fprintf(os.Stderr, "yolo history: no yolo workspace at or above %s; run it from the workspace or add `-C <workspace>`.\n", termsafe.Visible(start))
	}
	return ws, ok
}
