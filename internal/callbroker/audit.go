package callbroker

import (
	"context"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// StoreAudit records only identifiers and the digest of a classified pack call.
// Neither original/canonical argv nor resource facts, roots, stdin, host paths
// or environment belong in a pack's required authorization audit.
func StoreAudit(log *brokeraudit.Log, source string) func(context.Context, string, brokerstore.Request, brokerstore.Grant) error {
	return func(ctx context.Context, kind string, q brokerstore.Request, g brokerstore.Grant) error {
		outcome := "pending"
		if kind == "decision" {
			outcome = "denied"
			if g.ID != "" {
				outcome = "allowed"
			}
		}
		if kind == "grant-used" {
			outcome = "spent-before-dispatch"
		}
		jail := q.Jail
		if g.UsedBy != "" {
			jail = g.UsedBy
		}
		return log.RequiredContext(ctx, brokeraudit.Event{Event: kind, Service: source, Jail: jail,
			Set: "write", Outcome: outcome, CommandDigest: q.Digest, Request: q.ID, Grant: g.ID})
	}
}
