package callbroker

import (
	"context"
	"errors"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// boundWrite keeps Evaluate read-only and puts filing, standing coverage,
// exact-once spending and the original host CLI invocation behind one store
// transaction. There is no external CLI rollback guarantee after dispatch.
func (h *CLIHandler) boundWrite(ctx context.Context, op string, q brokerstore.Request, call Classification, predicted Verdict, stdin []byte) (Verdict, CLIResult, error) {
	refused := func(err error) (Verdict, CLIResult, error) {
		if executionCompleted(err) {
			return Verdict{Verdict: "refused", Rule: "host CLI response rejected after completed write",
					Next: "The host write may already have executed; inspect host state before retrying", Digest: q.Digest},
				CLIResult{}, completedWriteResponseError{err: err}
		}
		next := "Run yolo check on the host, then retry this selected launch"
		if _, lapsed := brokerstore.IsLapsedAllow(err); lapsed {
			next = "The Allow once expired; retry the same call to file a new request for host approval"
		}
		return Verdict{Verdict: "refused", Rule: "current host authority unavailable", Next: next, Digest: q.Digest}, CLIResult{}, err
	}
	if predicted.Verdict == "refused" {
		return predicted, CLIResult{}, nil
	}
	if _, err := h.revalidate(q); err != nil {
		return refused(err)
	}
	// The exact grant's stored parent is not the invocation. Revalidate both,
	// even when the digest matches but the authenticated caller changed.
	gate := func(held brokerstore.Request) (brokerstore.Policy, error) {
		if _, err := h.revalidate(q); err != nil {
			return brokerstore.Policy{}, err
		}
		return h.revalidate(held)
	}
	inspection, err := h.store.InspectExactGrantContext(ctx, q, gate)
	if err != nil {
		return refused(err)
	}
	if inspection.EligibilityErr != nil {
		return refused(inspection.EligibilityErr)
	}
	if inspection.Eligible {
		predicted.Verdict, predicted.Rule = "runs", "exact Allow once is available"
	}
	if op == "evaluate" {
		return predicted, CLIResult{}, nil
	}
	var result CLIResult
	didRun := false
	run := func() error {
		if err := h.audit.RequiredContext(ctx, brokeraudit.Event{Event: "call-authorization", Service: h.input.Source, Jail: q.Jail,
			Set: "write", Outcome: "authorized-before-dispatch", CommandDigest: q.Digest}); err != nil {
			return err
		}
		var err error
		result, err = h.program.run(ctx, call, stdin)
		if err == nil {
			didRun = true
		}
		return err
	}
	coverage := func(working []brokerstore.WorkingEntry, now time.Time) (bool, error) {
		current := decideCall(call, h.input.EffectiveCells, working, q.Workspace, h.input.Source, now)
		if current.Verdict == "refused" {
			return false, errors.New("one write was refused by the current working-set table; change the request and retry")
		}
		if current.Verdict != "runs" {
			return false, nil
		}
		if _, err := h.revalidate(q); err != nil {
			return false, err
		}
		if op == "act" {
			if err := run(); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	filed, _, covered, err := h.store.FileIfStillAsksWithCoverageContext(ctx, q, h.revalidate, coverage)
	if err != nil {
		return refused(err)
	}
	if didRun {
		return Verdict{Verdict: "runs", Rule: "standing coverage", Digest: q.Digest}, result, nil
	}
	if covered {
		if op == "ask" {
			return Verdict{Verdict: "runs", Rule: "covered without spending", Digest: q.Digest}, CLIResult{}, nil
		}
		_, spent, next, err := h.store.SpendExactOrFileContext(ctx, q, gate, run)
		if err != nil {
			return refused(err)
		}
		if spent && didRun {
			return Verdict{Verdict: "runs", Rule: "exact Allow once spent", Digest: q.Digest}, result, nil
		}
		filed = next
	}
	if filed.ID == "" {
		return refused(errors.New("call broker neither filed nor covered the call; retry"))
	}
	return Verdict{Verdict: "asks", Rule: "host approval required", Digest: q.Digest,
		Next: "Ask the human to run `yolo approve " + filed.ID + "` on the host, then retry the same call"}, CLIResult{}, nil
}
