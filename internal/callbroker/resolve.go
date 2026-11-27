package callbroker

import (
	"context"
	"errors"
	"reflect"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// resolveAndRun is the dispatch of an answered request whose candidate set has a refused
// member. The answer was spent before this runs, at the lookup's first send. It sends the
// lookup the request names, lets the classifier resolve the one cell the fact decides, and
// sends the write only when that cell is not refused: a refused cell returns its refusal,
// and the answer is not refunded.
func (h *CLIHandler) resolveAndRun(ctx context.Context, q brokerstore.Request, bound Classification, stdin []byte) (CLIResult, *Verdict, error) {
	// This runs inside the source transaction, so it reads the snapshot the caller holds.
	working, now, _ := q.WorkingState()
	ctx = snapshotTransactionContext(withWorkingState(ctx, working, now))
	classifier := h.classifier
	if h.start != "" {
		sf, _, err := h.currentAuthority()
		if err != nil {
			return CLIResult{}, nil, err
		}
		classifier.Roots = slices.Clone(sf.Repos)
	}
	answer, err := classifier.ClassifyCLIDigest(ctx, q.OriginalArgv, q.StdinDigest, h.lookupReader(q.Jail, classifier, true))
	if err != nil {
		return CLIResult{}, nil, err
	}
	resolved := answer.Classification
	if resolved == nil || len(resolved.Withheld) != 0 || len(resolved.Writes) != len(bound.Writes) || !reflect.DeepEqual(resolved.Argv, bound.Argv) {
		return CLIResult{}, nil, errors.New("the resolved call differs from the one answered; retry the original call")
	}
	for i, write := range resolved.Writes {
		if len(bound.Writes[i].Candidates) == 0 {
			continue
		}
		if write.Cell == "" || !slices.Contains(bound.Writes[i].Candidates, write.Cell) || !reflect.DeepEqual(write.Chain, bound.Writes[i].Chain) {
			return CLIResult{}, nil, errors.New("the resolved write is not one of the answered candidates; retry the original call")
		}
	}
	policy, err := h.operationPolicy(len(resolved.Reads) != 0)
	if err != nil {
		return CLIResult{}, nil, err
	}
	// The answer covers the fact and any asking candidate, never a refusal. It was filed
	// under this request's execution context, which the resolved call runs in too (ctx).
	resolved.contextDigest = q.ContextDigest
	covered := *resolved
	covered.Operations = nil
	for _, op := range resolved.Operations {
		if op.Kind == brokerpolicy.Write {
			covered.Operations = append(covered.Operations, op)
		}
	}
	covered.Reads = nil
	if verdict := decideBundle(policy, covered, working, h.workspace, h.input.Source, now).Verdict; verdict.Verdict == "refused" {
		verdict.Next = "The lookup was sent and the answer is spent; the cell it chose is refused: " + verdict.Next
		return CLIResult{}, &verdict, nil
	}
	result, err := h.program.run(ctx, *resolved, stdin)
	return result, nil, err
}
