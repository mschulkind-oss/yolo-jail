package callbroker

import (
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func selectedWriteTable(write Write, working []brokerstore.WorkingEntry, workspace, source string, now time.Time) string {
	op := brokerpolicy.Operation{Source: source, Kind: brokerpolicy.Write, Cell: write.Cell}
	for _, resource := range write.Chain {
		op.Chain = append(op.Chain, brokerpolicy.Resource{Type: resource.Type, ID: resource.ID})
	}
	members := make([]brokerpolicy.Membership, 0, len(working))
	for _, entry := range working {
		if entry.Timed() {
			continue
		}
		members = append(members, brokerpolicy.Membership{Workspace: brokerstore.ResolveWorkspace(entry.Workspace), Source: entry.Repo, Type: entry.Kind, ID: entry.Resource, Live: entry.LiveAt(now), Call: brokerstore.IsCallKind(entry.Kind)})
	}
	return brokerpolicy.SelectWriteTable(op, brokerstore.ResolveWorkspace(workspace), members)
}
