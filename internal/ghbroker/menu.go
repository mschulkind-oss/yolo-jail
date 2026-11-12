package ghbroker

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
)

// menu.go is the menu a pending write prints (approvalwait.Menu, the one renderer the
// git-broker and the jail briefing share): every answer the host can give request id for the
// write to t, each command alone on its own line, and the project request that would make it
// last, when one exists.

// retryCommand is what the agent runs again once a write's request is allowed.
const retryCommand = "run the same gh command again"

// pendingMenu is request id's menu for the write argv to t.
func pendingMenu(t writeTarget, argv []string, id string) approvalwait.Menu {
	m := approvalwait.Menu{ID: id, Retry: retryCommand}
	// `yolo approve --add`, which put a PR or issue in the working set for good, is removed
	// (github-write-scopes.md OQ-GW2): a write to an item the agent did not create takes Allow
	// once or `--for`.
	switch t.Kind {
	case targetCreatePR, targetCreateIssue:
		m.Project = []approvalwait.ProjectStep{{Command: requestCreateCommand(t)}}
	}
	if a, ok := runsOf(argv, t); ok && a.Op == runsDispatch {
		m.Project = append(m.Project, approvalwait.ProjectStep{Command: "yolo request " + Source + " runs " + a.Dispatch.Workflow})
	}
	return m
}

// originOnlyRequest is the sentence a pending message adds when its menu holds the unqualified
// project request for capability on repo. That command is for the workspace's origin alone
// (GW-D79), and the scope file does not say which repository the origin is, so the message states
// the condition and names the named-table form for any other repository.
func originOnlyRequest(repo, capability, value string) string {
	unqualified := "yolo request " + Source + " " + capability
	if value != "" {
		unqualified += " " + value
	}
	return " The project request in the menu below (`" + unqualified + "`) covers this workspace's origin alone, " +
		"so use it only when " + repo + " is that origin" + otherRepoRequest(capability, value) + "."
}

// pendingMessage is the refusal of the write argv to t while request id waits for the host,
// note being the notifier's failure clause, if any.
func pendingMessage(t writeTarget, argv []string, id, note string) string {
	lead := id + " remains pending; nothing ran. Its Allow once outlasts a broker or jail restart."
	if t.Kind == targetMerge {
		lead += " A merge always asks, per PR."
	}
	switch t.Kind {
	case targetCreatePR:
		lead += originOnlyRequest(t.Repo, "create_pr", "")
	case targetCreateIssue:
		lead += originOnlyRequest(t.Repo, "create_issue", "")
	}
	if a, ok := runsOf(argv, t); ok {
		switch a.Op {
		case runsDispatch:
			lead += " A runs grant covers a run on the default branch only; a run on any other ref still asks." +
				originOnlyRequest(t.Repo, "runs", a.Dispatch.Workflow)
		case runsRerun:
			lead += " A runs grant naming the run's workflow file (`yolo request " + Source + " runs <workflow file>`" + otherRepoRequest("runs", "<workflow file>") + ", then " +
				"`yolo config apply` on the host) covers re-running a run of the default branch of " + t.Repo + " itself; " +
				"a run of any other branch, or of a fork, still asks."
		case runsCancel:
			lead += " Any runs grant on " + t.Repo + " (`yolo request " + Source + " runs <workflow file>`" + otherRepoRequest("runs", "<workflow file>") + ", then " +
				"`yolo config apply` on the host) covers cancelling its runs."
		}
	}
	return lead + approveRunHint(argv, t) + note + "\n\n" + strings.TrimRight(pendingMenu(t, argv, id).Render(), "\n")
}

// lapsedMessage is the refusal of a retry after request id's Allow once lapsed unused: the
// store refuses it once, and the next run files a new request, so the human is asked again
// deliberately rather than by a retry nobody relayed.
func lapsedMessage(id string) string {
	return "the host allowed " + id + ", but its 15-minute window closed before this command ran, so it covers " +
		"nothing now and no new request was filed; nothing ran. Tell the human, and only if they still want it, run " +
		"the same command again: that files a new request for them to answer"
}

// PendingMessageSamples is the pending message of every write target that has its own menu,
// for a sample request, so the CLI's tests can check that each command they name exists.
func PendingMessageSamples() []string {
	const id = "r-00000000000000000000000000000000"
	var out []string
	for _, t := range []writeTarget{
		{Kind: targetPR, Repo: "o/r", Number: 7}, {Kind: targetMerge, Repo: "o/r", Number: 7},
		{Kind: targetCreatePR, Repo: "o/r"}, {Kind: targetCreateIssue, Repo: "o/r"}, {Kind: targetRepo, Repo: "o/r"},
	} {
		out = append(out, pendingMessage(t, nil, id, NotifierFailed))
	}
	return out
}

// requestCreateCommand is the project command that declares the create-pr or create-issue
// grant a creation target t needs, "" for any other target.
func requestCreateCommand(t writeTarget) string {
	switch t.Kind {
	case targetCreatePR:
		return "yolo request " + Source + " create_pr"
	case targetCreateIssue:
		return "yolo request " + Source + " create_issue"
	}
	return ""
}
