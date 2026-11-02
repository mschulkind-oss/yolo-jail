package cli

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// approveoutcome.go is the one shape every `yolo approve <id>` answer prints on the host: a
// one-line headline, the facts indented beneath it (what it covers, for whom, how long), and
// the command that ends it alone on its own indented line, which the human copies rather
// than reads.

// approveFact is one indented fact under an outcome's headline.
type approveFact struct{ label, text string }

// writeApproveOutcome prints headline, its facts and the revoke command for each of ids.
func writeApproveOutcome(out io.Writer, headline string, facts []approveFact, ids ...string) {
	var b strings.Builder
	b.WriteString(headline + "\n")
	width := 0
	for _, f := range facts {
		width = max(width, len(f.label))
	}
	for _, f := range facts {
		fmt.Fprintf(&b, "  %-*s  %s\n", width+1, f.label+":", f.text)
	}
	b.WriteString("\n  To end it early, on the host:\n\n")
	for _, id := range ids {
		b.WriteString("      yolo approve --revoke " + id + "\n")
	}
	b.WriteString("\n")
	b.WriteString("  `yolo approve` lists every request, grant and working-set entry.\n")
	_, _ = io.WriteString(out, b.String())
}

// approveWhen is an expiry as the human reads it: the local time, then the RFC 3339 instant.
func approveWhen(t time.Time) string {
	return t.Local().Format("15:04 MST, Mon 2 Jan") + " (" + t.UTC().Format(time.RFC3339) + ")"
}

// whoFor is the jails an answer covers.
func whoFor(workspace string) string {
	return "every jail of " + termsafe.Visible(workspace) + ", across restarts"
}

// approveOnceOutcome is an Allow once (or a legacy set-wide grant) answering request id.
func approveOnceOutcome(out io.Writer, id string, g brokerstore.Grant) {
	coverage := "this exact command, once"
	if g.Uses != 1 {
		coverage = fmt.Sprintf("this exact command, %d uses", g.Uses)
	}
	if g.Digest == "" {
		coverage = fmt.Sprintf("the whole set %s, %d use(s)", termsafe.Visible(g.Set), g.Uses)
	}
	writeApproveOutcome(out, "Allowed "+termsafe.Visible(id)+": "+coverage+".", []approveFact{
		{"For", whoFor(g.Workspace)},
		{"Lasts", "until it is used, or it expires at " + approveWhen(g.Expires)},
	}, g.ID)
}

// githubEntryName is a GitHub working-set entry as the human names it: the repository
// without its github.com host.
func githubEntryName(e brokerstore.WorkingEntry) string {
	e.Repo = strings.TrimPrefix(e.Repo, ghbroker.SourceHost+"/")
	return termsafe.Visible(ghbroker.DescribeEntry(e))
}

// approveGitHubAddOutcome is a GitHub request answered with --add: its PR or issue joined
// the working set. An issue is never merged, so only a PR's entry ends by itself.
func approveGitHubAddOutcome(out io.Writer, id string, e brokerstore.WorkingEntry) {
	covers, lasts := "every write to it, unasked", "until revoked"
	if e.Kind == brokerstore.WorkingPR {
		covers, lasts = "every write to it but a merge, unasked", "until the PR is merged, or revoked"
	}
	writeApproveOutcome(out, "Allowed "+termsafe.Visible(id)+": "+githubEntryName(e)+" joined this workspace's working set.",
		[]approveFact{{"Covers", covers}, {"For", whoFor(e.Workspace)}, {"Lasts", lasts}}, e.ID)
}

// approveTimedOutcome is a request answered with --for: what (already made safe for the
// terminal) is allowed until e expires.
func approveTimedOutcome(out io.Writer, id, what string, entries []brokerstore.WorkingEntry) {
	e := entries[0]
	writeApproveOutcome(out, "Allowed "+termsafe.Visible(id)+": "+what+", unasked, for a while.",
		[]approveFact{{"For", whoFor(e.Workspace)}, {"Lasts", "until " + approveWhen(e.Expires)}},
		entryIDs(entries)...)
}

// entryIDs is the ids of entries, in order.
func entryIDs(entries []brokerstore.WorkingEntry) []string {
	var ids []string
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

// approveGitAddOutcome is a git push request answered with --add: its branches joined the
// working set.
func approveGitAddOutcome(out io.Writer, id string, refs []string, entries []brokerstore.WorkingEntry) {
	e := entries[0]
	writeApproveOutcome(out, "Allowed "+termsafe.Visible(id)+": "+strings.Join(refs, ", ")+" joined this workspace's working set.",
		[]approveFact{{"Covers", "every push to them, unasked: update, force-push and delete"}, {"For", whoFor(e.Workspace)},
			{"Lasts", "until the branch is deleted, or revoked"}}, entryIDs(entries)...)
}
