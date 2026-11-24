package callbroker

import (
	"fmt"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// workingMembers is the live, non-timed membership the one table selector reads.
func workingMembers(working []brokerstore.WorkingEntry, now time.Time) []brokerpolicy.Membership {
	members := make([]brokerpolicy.Membership, 0, len(working))
	for _, entry := range working {
		if entry.Timed() {
			continue
		}
		members = append(members, brokerpolicy.Membership{Workspace: brokerstore.ResolveWorkspace(entry.Workspace), Source: entry.Repo, Type: entry.Kind, ID: entry.Resource, Live: entry.LiveAt(now), Call: brokerstore.IsCallKind(entry.Kind)})
	}
	return members
}

// frozenOperationPolicy expresses the launch's frozen per-cell decisions and its
// read standing as the one pure decision leaf, so a read, a write and a mix are
// judged by the same code. A frozen "run" is an always ceiling with a true ask,
// "ask" an on_request ceiling with an ask, anything else a false ask: exactly
// the three outcomes the legacy table decision had. A read has the standing the
// launch was already refused without: always, or never when the host user
// config names it.
func frozenOperationPolicy(source string, cells []loopholedecl.CallBrokerCell, effective map[string]config.CallBrokerCellPolicy, readNever func(cell string) bool) (brokerpolicy.Policy, error) {
	matcher, err := brokerpolicy.NewScopeMatcher("exact", "", nil)
	if err != nil {
		return brokerpolicy.Policy{}, err
	}
	var declared []brokerpolicy.Cell
	var ceilings, asks []brokerpolicy.Rule
	readCells := []string{}
	for _, cell := range cells {
		if cell.IsRead() {
			declared = append(declared, brokerpolicy.Cell{Name: cell.Name, Kind: brokerpolicy.Read})
			readCells = append(readCells, cell.Name)
			continue
		}
		declared = append(declared, brokerpolicy.Cell{Name: cell.Name, Kind: brokerpolicy.Write, Tables: slices.Clone(cell.Tables)})
		for _, table := range cell.Tables {
			setting, ok := effective[cell.Name].Tables[table]
			ask, ceiling := brokerpolicy.False, brokerpolicy.Absent
			switch {
			case !ok || setting.Decision == "refuse" || setting.Request == "false":
			case setting.Decision == "run" && setting.Request != "ask":
				ask, ceiling = brokerpolicy.True, brokerpolicy.Always
			default:
				ask, ceiling = brokerpolicy.True, brokerpolicy.OnRequest
				if setting.Request == "ask" {
					ask = brokerpolicy.Ask
				}
			}
			origin := "launch input " + cell.Name + "." + table
			asks = append(asks, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Write, Cell: cell.Name, Table: table, Value: ask, Origin: origin})
			if ceiling != brokerpolicy.Absent {
				ceilings = append(ceilings, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Write, Cell: cell.Name, Table: table, Value: ceiling, Origin: origin})
			}
		}
	}
	if len(readCells) == 0 {
		readCells = []string{""}
	}
	for _, name := range readCells {
		value := brokerpolicy.Always
		if readNever(name) {
			value = brokerpolicy.Never
		}
		ceilings = append(ceilings, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Read, Cell: name, Value: value, Origin: "launch read standing"})
		asks = append(asks, brokerpolicy.Rule{Source: source, Kind: brokerpolicy.Read, Cell: name, Value: brokerpolicy.True, Origin: "launch read standing"})
	}
	return brokerpolicy.NewPolicy(source, matcher, declared, true, false, ceilings, asks, nil)
}

// operationPolicy is the decision leaf of this call. Tests substitute a scoped
// one through livePolicy; no launch yet publishes scoped asks to a daemon.
func (h *CLIHandler) operationPolicy(reads bool) (brokerpolicy.Policy, error) {
	if h.livePolicy != nil {
		return h.livePolicy()
	}
	cells, err := h.currentCellPolicies()
	if err != nil {
		return brokerpolicy.Policy{}, err
	}
	return frozenOperationPolicy(h.input.Source, h.input.Cells, cells, func(cell string) bool { return reads && readNeverNamed(h.input.Source, cell) })
}

// bundleDecision is one call's judgment: the bundle verdict and which
// operations asked, in classification order, for the request that names them.
type bundleDecision struct {
	Verdict  Verdict
	AskReads []int
}

func timedCovers(entry brokerstore.WorkingEntry, now time.Time, workspace, source string, op brokerpolicy.Operation) bool {
	if !entry.Timed() || !entry.LiveAt(now) || !brokerstore.SameWorkspace(entry.Workspace, workspace) || entry.Repo != source {
		return false
	}
	cell, role := op.Cell, brokerstore.RoleKnownWrite
	if op.Kind == brokerpolicy.Read {
		role = brokerstore.RoleRead
		if cell == "" {
			cell = "read"
		}
	}
	chain := make([]brokerstore.CallResource, len(op.Chain))
	for i, resource := range op.Chain {
		chain[i] = brokerstore.CallResource{Type: resource.Type, ID: resource.ID}
	}
	key, err := brokerstore.OperationResourceKey(role, brokerstore.CallResourceKey(chain), "")
	return err == nil && entry.Kind == brokerstore.WorkingCallPrefix+cell && entry.Resource == key
}

// decideBundle is pure. Any refused operation refuses the bundle; otherwise any
// asking operation without a live timed answer makes it ask. A timed answer
// covers only an ask and never overrides a refusal.
func decideBundle(policy brokerpolicy.Policy, call Classification, working []brokerstore.WorkingEntry, workspace, source string, now time.Time) bundleDecision {
	if len(call.Operations) == 0 {
		return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "empty classification", Next: "This is a bug in the selected pack's classifier, so running the call again fails the same way: report it to the pack's author"}}
	}
	decided := policy.Decide(call.Operations, brokerstore.ResolveWorkspace(workspace), workingMembers(working, now))
	out := bundleDecision{Verdict: Verdict{Verdict: "runs", Rule: "every write is covered"}}
	if len(call.Writes) == 0 {
		out.Verdict.Rule = "standing read"
	}
	asks := false
	for i, d := range decided.Operations {
		switch d.Decision {
		case brokerpolicy.Refuse:
			if d.Operation.Kind == brokerpolicy.Read {
				if d.Ceiling.Value == brokerpolicy.Never {
					return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "the host config says never for this read",
						Next: "Remove the `never` that names this read (" + d.Ceiling.Origin + ") with `yolo config unset`, or ask the human to"}}
				}
				return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "read is not permitted", Next: refusalNext(d)}}
			}
			return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "write is not permitted by its declared table", Next: "Change the request with `yolo request` and approve its scope at the gate"}}
		case brokerpolicy.Hold:
			if d.Operation.Kind == brokerpolicy.Read {
				out.AskReads = append(out.AskReads, i)
			}
			covered := slices.ContainsFunc(working, func(entry brokerstore.WorkingEntry) bool {
				return timedCovers(entry, now, workspace, source, d.Operation)
			})
			if !covered {
				asks = true
			}
		}
	}
	if asks {
		out.Verdict = Verdict{Verdict: "asks", Rule: "a write needs approval", Next: "Ask a human on the host to review pack broker access after its approval policy is settled"}
		if len(call.Writes) == 0 {
			out.Verdict.Rule = "a read needs approval"
		}
	}
	return out
}

// refusalNext names the governing line of a refused operation, so the stop
// names its next step.
func refusalNext(d brokerpolicy.OperationDecision) string {
	line := d.Ask.Origin
	if d.Ceiling.Value == brokerpolicy.Never || line == "" {
		line = d.Ceiling.Origin
	}
	if line == "" {
		line = "no governing line"
	}
	return fmt.Sprintf("This %s is refused by %s; ask the human to change that line with `yolo request` (a project's ask) or `yolo allow` (the host ceiling), then retry", d.Operation.Kind, line)
}

// bundleSubjects attaches the asking reads to a request, in classification
// order, so the one digest binds them beside every write. A read that already
// runs under standing authority is not a subject: it needs no answer, and a
// legacy call keeps its digest.
func bundleSubjects(q brokerstore.Request, call Classification, asking []int) brokerstore.Request {
	for _, index := range asking {
		op := call.Operations[index]
		read := brokerstore.CallRead{Cell: op.Cell}
		for _, resource := range op.Chain {
			read.Chain = append(read.Chain, brokerstore.CallResource{Type: resource.Type, ID: resource.ID})
		}
		q.CallReads = append(q.CallReads, read)
	}
	q.Digest = brokerstore.CommandDigest(q)
	return q
}
