package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// audit.go is `yolo audit` (docs/design/boundary-broker.md §8, BB-D15): the host's view of
// every brokered call every broker on this machine made, read from the one audit log they
// append to. Host-only: the log is mounted into no jail, and an in-jail read would find an
// empty one and say nothing ran.

const auditUsage = `Usage: yolo audit [flags]

On the host: list the brokered calls — every command a jail sent a broker such as
the github-broker, whether it ran, was refused, was out of the workspace's
repository scope, or needed an approval. One line per call, oldest first, from
~/.local/share/yolo-jail/broker/audit.jsonl and its rotated archives.

Refuses inside a jail: the log is not mounted into any jail.

Flags:
  --since <when>      Only calls at or after <when>: a duration back from now
                      (30m, 2h, 7d) or an RFC 3339 time.
  --jail <id>         Only calls from this jail id.
  --workspace <path>  Only calls from this workspace.
  --set <name>        Only calls the broker assigned this set: read-only,
                      read-write, refused or out-of-scope.
  --grant <id>        Only calls run under this grant (none exist yet: this
                      version asks for no approvals).
  --json              Print each matching line as stored, one JSON object per line.
  --help, -h          Show this help.

Examples:
  yolo audit                          # everything the log holds
  yolo audit --since 1h --set refused # the last hour's refusals
  yolo audit --json | jq .argv        # the canonical argv of each call`

func runAudit(args []string) int {
	if answerHelp("audit", args, os.Stdout) {
		return 0
	}
	// A registry handler receives the verb itself first (dispatch.go).
	return auditMain(args[1:], config.InJail(), paths.BrokerAuditLog(), time.Now(), os.Stdout, os.Stderr)
}

type auditFilter struct {
	since                time.Time
	jail, workspace, set string
	grant                string
	json                 bool
}

func auditMain(args []string, inJail bool, logPath string, now time.Time, stdout, stderr io.Writer) int {
	if inJail {
		fmt.Fprintln(stderr, "yolo audit is host-only: the brokers' audit log is on the host and is mounted "+
			"into no jail. Run `yolo audit` in a terminal on the host.")
		return 2
	}
	var f auditFilter
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, val, hasEq := strings.Cut(a, "=")
		value := func() (string, bool) {
			if hasEq {
				return val, true
			}
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "yolo audit: %s needs a value\n", name)
				return "", false
			}
			i++
			return args[i], true
		}
		switch name {
		case "--since":
			v, ok := value()
			if !ok {
				return 2
			}
			t, err := parseSince(v, now)
			if err != nil {
				fmt.Fprintf(stderr, "yolo audit: --since %q: %v\n", v, err)
				return 2
			}
			f.since = t
		case "--jail", "--workspace", "--set", "--grant":
			v, ok := value()
			if !ok {
				return 2
			}
			switch name {
			case "--jail":
				f.jail = v
			case "--workspace":
				f.workspace = v
			case "--set":
				f.set = v
			case "--grant":
				f.grant = v
			}
		case "--json":
			f.json = true
		default:
			fmt.Fprintf(stderr, "yolo audit: unknown argument %q (see `yolo audit --help`)\n", a)
			return 2
		}
	}

	events, skipped, err := brokeraudit.Read(logPath)
	if err != nil {
		fmt.Fprintf(stderr, "yolo audit: reading %s: %v\n", logPath, err)
		return 1
	}
	var shown int
	for _, e := range events {
		if !f.match(e) {
			continue
		}
		shown++
		if f.json {
			line, _ := json.Marshal(e)
			fmt.Fprintln(stdout, string(line))
			continue
		}
		fmt.Fprintln(stdout, auditLine(e))
	}
	if !f.json {
		switch {
		case len(events) == 0:
			fmt.Fprintf(stdout, "No brokered calls recorded (%s).\n", logPath)
		case shown == 0:
			fmt.Fprintf(stdout, "No brokered call matches (%d recorded).\n", len(events))
		}
	}
	if skipped > 0 {
		fmt.Fprintf(stderr, "yolo audit: skipped %d line(s) that do not decode\n", skipped)
	}
	return 0
}

func (f auditFilter) match(e brokeraudit.Event) bool {
	if !f.since.IsZero() {
		t, err := time.Parse(time.RFC3339, e.Time)
		if err != nil || t.Before(f.since) {
			return false
		}
	}
	return (f.jail == "" || e.Jail == f.jail) &&
		(f.workspace == "" || e.Workspace == f.workspace) &&
		(f.set == "" || e.Set == f.set) &&
		(f.grant == "" || e.Grant == f.grant)
}

// auditLine is one call for a reader: when, which jail, the set, what became of it, the
// exit code and the command as the broker built it.
//
// THE ARGV IS THE JAIL'S WORDS, printed on the host's terminal, so it is quoted for DISPLAY
// (shquote.JoinDisplay): a word carrying an ESC or a carriage return would otherwise clear
// the screen, rewrite the lines above it, or retitle the window, and the audit would show
// something other than what ran. `--json` needs nothing: json.Marshal escapes them.
func auditLine(e brokeraudit.Event) string {
	exit := "-"
	if e.Exit != nil {
		exit = strconv.Itoa(*e.Exit)
	}
	line := fmt.Sprintf("%s  %-12s  %-12s  %-11s  %4s  %s: %s", termsafe.Visible(e.Time),
		termsafe.Visible(e.Jail), termsafe.Visible(e.Set), termsafe.Visible(e.Outcome), exit,
		termsafe.Visible(e.Service), shquote.JoinDisplay(e.Argv))
	if e.Workspace != "" {
		line += "  [" + termsafe.Visible(e.Workspace) + "]"
	}
	if e.Redactions > 0 {
		line += fmt.Sprintf("  (%d redaction(s))", e.Redactions)
	}
	return line
}

// parseSince reads a duration back from now (with a `d` for days) or an RFC 3339 time.
func parseSince(v string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 {
			return time.Time{}, fmt.Errorf("want a duration such as 30m, 2h or 7d, or an RFC 3339 time")
		}
		return now.Add(-time.Duration(n) * 24 * time.Hour), nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return time.Time{}, fmt.Errorf("want a duration such as 30m, 2h or 7d, or an RFC 3339 time")
	}
	return now.Add(-d), nil
}
