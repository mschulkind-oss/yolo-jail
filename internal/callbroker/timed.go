package callbroker

import (
	"fmt"
	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// defaultCellMaxFor is the shared default for a cell with no selected max_for.
const defaultCellMaxFor = brokerstore.TimedGrantDefault

// RequestEntries expands a revalidated request into timed, role/context-bound
// call:<cell> entries. It cannot grant permanent membership.
func RequestEntries(q brokerstore.Request) ([]brokerstore.WorkingEntry, error) {
	return brokerstore.OperationEntries(q)
}

func timedApprovalNext(q brokerstore.Request, retry string) string {
	bound, err := ApprovalTimedLimit(q)
	if err != nil {
		return "Ask the human to run `yolo approve " + q.ID + "` to Allow once; run yolo check before offering a timed answer, then " + retry
	}
	example := min(brokerstore.TimedGrantDefault, bound.Maximum)
	return fmt.Sprintf("Ask the human to run `yolo approve %s` (Allow once) or `yolo approve %s --for %s` (maximum %s, cell %s role %s) on the host, then %s", q.ID, q.ID, brokerstore.FormatTimedDuration(example), brokerstore.FormatTimedDuration(bound.Maximum), bound.Cell, bound.Role, retry)
}

// ApprovalTimedLimit is shared by the current host offer and the answer check.
// Declaration caps are reread, never copied from the launch input or request.
func ApprovalTimedLimit(q brokerstore.Request) (brokerstore.TimedBound, error) {
	return brokercore.SelectedTimedLimit(q.Set, q)
}
