package callbroker

import (
	"context"
	"fmt"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// WithheldLookup is a declared lookup core did not send, because the read it is
// refuses or asks and no live answer covers it. It carries no provider data: only
// the bound read subject, its decision and the line that governs it.
type WithheldLookup struct {
	Name      string
	Arguments []string
	Decision  brokerpolicy.Decision
	Reads     []brokerpolicy.Operation
	Line      string
}

func (w *WithheldLookup) Error() string {
	return fmt.Sprintf("lookup %s is withheld by %s", w.Name, w.Line)
}

// WithheldFactError is a withheld lookup a /1-only classifier cannot be told about,
// so the call is refused naming the read line. Missing facts are never an empty result.
type WithheldFactError struct {
	Lookup WithheldLookup
	Source string
}

func (e *WithheldFactError) Error() string {
	return fmt.Sprintf("call broker %s classifier asked lookup %s, which read policy withholds (%s), and it cannot take a withheld fact; ask the human to change that line with `yolo allow` or `yolo request`, or update the pack's classifier", e.Source, e.Lookup.Name, e.Lookup.Line)
}

type workingStateKey struct{}

type workingState struct {
	working []brokerstore.WorkingEntry
	now     time.Time
}

// withWorkingState carries the store snapshot a caller already holds, so a lookup's
// read decision under the store's transaction never reacquires its lock.
func withWorkingState(ctx context.Context, working []brokerstore.WorkingEntry, now time.Time) context.Context {
	return context.WithValue(ctx, workingStateKey{}, workingState{working: working, now: now})
}

func (h *CLIHandler) workingFor(ctx context.Context) ([]brokerstore.WorkingEntry, time.Time, error) {
	if state, ok := ctx.Value(workingStateKey{}).(workingState); ok {
		return state.working, state.now, nil
	}
	_, _, working, now, err := h.store.InspectContext(ctx)
	return working, now, err
}

// lineOf names the first governing line of an operation that does not simply run.
func lineOf(decisions []brokerpolicy.OperationDecision) string {
	for _, d := range decisions {
		if d.Decision == brokerpolicy.Run {
			continue
		}
		if d.Ceiling.Value == brokerpolicy.Never || d.Ask.Origin == "" {
			if d.Ceiling.Origin != "" {
				return d.Ceiling.Origin
			}
		}
		if d.Ask.Origin != "" {
			return d.Ask.Origin
		}
	}
	return "no governing line"
}

// gateLookup decides a declared lookup's own read before any provider send. It
// returns nil when the read runs, or a withheld lookup naming why not.
func (h *CLIHandler) gateLookup(ctx context.Context, declared string, args []string, read Classification) (*WithheldLookup, error) {
	policy, err := h.operationPolicy(true)
	if err != nil {
		return nil, err
	}
	working, now, err := h.workingFor(ctx)
	if err != nil {
		return nil, err
	}
	if decided := decideBundle(policy, read, working, h.workspace, h.input.Source, now); decided.Verdict.Verdict == "runs" {
		return nil, nil
	}
	table := policy.Decide(read.Operations, brokerstore.ResolveWorkspace(h.workspace), workingMembers(working, now))
	held := &WithheldLookup{Name: declared, Arguments: append([]string(nil), args...), Decision: brokerpolicy.Hold, Reads: read.Operations, Line: lineOf(table.Operations)}
	for _, d := range table.Operations {
		if d.Decision == brokerpolicy.Refuse {
			held.Decision = brokerpolicy.Refuse
		}
	}
	return held, nil
}
