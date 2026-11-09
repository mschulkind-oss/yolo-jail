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
// answers are RequestEntries: `yolo approve <id> --for <duration>` allows the request's kind of
// write on its resource for that long, a working-set record rather than a command grant.

// ApprovalStore is the GitHub source's host store as `yolo approve` opens it.
func ApprovalStore() *brokerstore.Store {
	return &brokerstore.Store{Dir: paths.BrokerSourceDir(Source), Source: Source, Revalidate: ApprovalAuthority, PublishRecords: true,
		Entries: RequestEntries, Audit: StoreAudit(brokeraudit.Open(paths.BrokerAuditLog(), nil))}
}

// RequestEntries is the working-set entries `yolo approve <id> --for <duration>` records for
// GitHub request q: one timed entry, on the request's resource, of the request's kind (§5.7).
// The target is read again from the request's canonical argv, the same parse the broker filed
// it by. A write naming no PR or issue number the broker can read takes Allow once alone, since
// a timed grant on it would cover the whole repository's writes.
func RequestEntries(q brokerstore.Request) ([]brokerstore.WorkingEntry, error) {
	if q.Set != SetReadWrite {
		return nil, errors.New("the request is not a GitHub write request")
	}
	t := targetOf(q.Argv, strings.Split(q.Repo, ","))
	e := brokerstore.WorkingEntry{Workspace: q.Workspace, Repo: t.storeRepo(), Resource: t.resource(), By: "human"}
	once := fmt.Sprintf("`yolo approve %s` to Allow once", q.ID)
	switch t.Kind {
	case targetPR:
		e.Kind = brokerstore.WorkingPR
	case targetIssue, targetIssueOrPR:
		// An issues/{n} write is a PR's or an issue's; the two share one number space and one
		// coverage, so the label is the only difference.
		e.Kind = brokerstore.WorkingIssue
	case targetMerge:
		e.Kind = brokerstore.WorkingMerge
	case targetCreatePR, targetCreateIssue:
		e.Kind = brokerstore.WorkingCreatePR
		if t.Kind == targetCreateIssue {
			e.Kind = brokerstore.WorkingCreateIssue
		}
	case targetRepo:
		if t.Repo == "" {
			return nil, fmt.Errorf("--for is not available for this request: it names no single repository. Next: run %s", once)
		}
		e.Kind = brokerstore.WorkingRepo
	default:
		return nil, fmt.Errorf("--for is not available for this request: it names no PR or issue by number, "+
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
