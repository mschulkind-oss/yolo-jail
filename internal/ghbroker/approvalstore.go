package ghbroker

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// approvalstore.go is the GitHub source's store as a host front end opens it
// (docs/design/boundary-broker.md §6.4, §7, BB-D79, BB-D80, BB-D92): its revalidation is
// ApprovalAuthority, every request, decision and spent grant is audited, and its per-resource
// answers are RequestEntries: `yolo approve <id> --add` puts a PR or issue in the workspace's
// working set, and `--for <duration>` allows the request's kind of write on its resource for
// that long, each a working-set record rather than a command grant.

// ApprovalStore is the GitHub source's host store as `yolo approve` opens it.
func ApprovalStore() *brokerstore.Store {
	return &brokerstore.Store{Dir: paths.BrokerSourceDir(Source), Source: Source, Revalidate: ApprovalAuthority,
		Entries: RequestEntries, Audit: StoreAudit(brokeraudit.Open(paths.BrokerAuditLog(), nil))}
}

// RequestEntries is the working-set entries `yolo approve <id> --for` (add false, timed) or
// `--add` (add true) records for GitHub request q: one, on the request's resource, of the
// request's kind (§5.7). The target is read again from the request's canonical argv, the same
// parse the broker filed it by.
//
// Add applies to a PR or issue write alone: a merge asks per PR (OQ-BB19), a lasting right to
// create is a user grant (BB-D90), and a repository-level write never joins the working set
// (BB-D95); each refuses Add, naming the answer that applies. A write naming no PR or issue
// number the broker can read takes Allow once alone, since a timed grant on it would cover the
// whole repository's writes.
func RequestEntries(q brokerstore.Request, add bool) ([]brokerstore.WorkingEntry, error) {
	if q.Set != SetReadWrite {
		return nil, errors.New("the request is not a GitHub write request")
	}
	t := targetOf(q.Argv, strings.Split(q.Repo, ","))
	e := brokerstore.WorkingEntry{Workspace: q.Workspace, Repo: t.storeRepo(), Resource: t.resource(), By: "human"}
	once := fmt.Sprintf("`yolo approve %s` to Allow once", q.ID)
	timed := fmt.Sprintf("`yolo approve %s --for 15m` to allow it for 15 minutes", q.ID)
	switch t.Kind {
	case targetPR:
		e.Kind = brokerstore.WorkingPR
	case targetIssue, targetIssueOrPR:
		// An issues/{n} write is a PR's or an issue's; the two share one number space and one
		// coverage, so the label is the only difference.
		e.Kind = brokerstore.WorkingIssue
	case targetMerge:
		if add {
			return nil, fmt.Errorf("--add is not available for a merge: a merge asks per PR, even for a PR in the "+
				"working set. Next: run %s, or %s", once, timed)
		}
		e.Kind = brokerstore.WorkingMerge
	case targetCreatePR, targetCreateIssue:
		if add {
			return nil, fmt.Errorf("--add is not available for %s: a lasting right to create is a %s grant in the "+
				"workspace config, allowed by your user config. Next: run %s (what it creates joins the working set), "+
				"or %s", t.describe(), createGrantKind(t), timed, once)
		}
		e.Kind = brokerstore.WorkingCreatePR
		if t.Kind == targetCreateIssue {
			e.Kind = brokerstore.WorkingCreateIssue
		}
	case targetRepo:
		if add || t.Repo == "" {
			if !add {
				return nil, fmt.Errorf("--for is not available for this request: it names no single repository. Next: run %s", once)
			}
			return nil, fmt.Errorf("--add is not available for %s: a repository-level write never joins the working set. "+
				"Next: run %s, or %s", t.describe(), timed, once)
		}
		e.Kind = brokerstore.WorkingRepo
	default:
		return nil, fmt.Errorf("--add and --for are not available for this request: it names no PR or issue by number, "+
			"so the broker cannot tell which one it writes. Next: run %s, or have the agent name the PR or issue by number "+
			"and run the command again", once)
	}
	return []brokerstore.WorkingEntry{e}, nil
}

// DescribeEntry is a GitHub working-set entry for a human: "PR github.com/o/r#32", "merges of
// github.com/o/r#32", "PR creation in github.com/o/r".
func DescribeEntry(e brokerstore.WorkingEntry) string {
	switch e.Kind {
	case brokerstore.WorkingPR:
		return "PR " + e.Repo + e.Resource
	case brokerstore.WorkingIssue:
		return "issue " + e.Repo + e.Resource
	case brokerstore.WorkingMerge:
		return "merges of " + e.Repo + e.Resource
	case brokerstore.WorkingCreatePR:
		return "PR creation in " + e.Repo
	case brokerstore.WorkingCreateIssue:
		return "issue creation in " + e.Repo
	case brokerstore.WorkingRepo:
		return "repository-level writes to " + e.Repo
	}
	return e.Kind + " " + e.Repo + " " + e.Resource
}
