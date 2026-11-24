package callbroker

import (
	"fmt"
	"slices"
	"strings"
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
				// A frozen "ask" always holds: a true ask under an on_request ceiling would
				// run (brokerpolicy.Combine), which is what an absent request must not do.
				ask, ceiling = brokerpolicy.Ask, brokerpolicy.OnRequest
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

// groupDecision is one write that a withheld lookup left as candidate cells.
type groupDecision struct {
	Write int // index into the classification's writes
	// Keep are the candidates not refused, in classifier order: the cells a request binds.
	Keep []string
	// Fact are the withheld lookup's read subjects, carried only when that read asks.
	Fact []brokerpolicy.Operation
	// Resolve marks a candidate set with a refused member: its answer is spent at the
	// lookup's first send, and the write is sent only if the resolved cell is not refused.
	Resolve bool
}

// bundleDecision is one call's judgment: the bundle verdict, which reads asked, and
// how each candidate write group came out.
type bundleDecision struct {
	Verdict  Verdict
	AskReads []int // indices into the classification's operations
	Groups   []groupDecision
}

func (b bundleDecision) resolving() bool {
	return slices.ContainsFunc(b.Groups, func(g groupDecision) bool { return g.Resolve })
}

// timedCovers reports a live timed answer for the operation. A read is covered by its
// read role; a known write by a known or a candidate entry (the answer to a withheld
// request still covers the cell the fact later resolves to); a candidate by its own role.
func timedCovers(entry brokerstore.WorkingEntry, now time.Time, workspace, source string, op brokerpolicy.Operation, candidate bool) bool {
	if !entry.Timed() || !entry.LiveAt(now) || !brokerstore.SameWorkspace(entry.Workspace, workspace) || entry.Repo != source {
		return false
	}
	cell, roles := op.Cell, []string{brokerstore.RoleKnownWrite, brokerstore.RoleWithheldWrite}
	switch {
	case op.Kind == brokerpolicy.Read:
		roles = []string{brokerstore.RoleRead}
		if cell == "" {
			cell = "read"
		}
	case candidate:
		roles = []string{brokerstore.RoleWithheldWrite}
	}
	chain := make([]brokerstore.CallResource, len(op.Chain))
	for i, resource := range op.Chain {
		chain[i] = brokerstore.CallResource{Type: resource.Type, ID: resource.ID}
	}
	for _, role := range roles {
		if key, err := brokerstore.OperationResourceKey(role, brokerstore.CallResourceKey(chain), ""); err == nil && entry.Kind == brokerstore.WorkingCallPrefix+cell && entry.Resource == key {
			return true
		}
	}
	return false
}

func refusedWrite(next string) bundleDecision {
	return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "write is not permitted by its declared table", Next: next}}
}

// decideBundle is pure. Any refused operation refuses the bundle; otherwise any
// asking operation without a live timed answer makes it ask. A timed answer
// covers only an ask and never overrides a refusal. A write left as candidates by a
// withheld lookup follows the bound candidate and fact matrix.
func decideBundle(policy brokerpolicy.Policy, call Classification, working []brokerstore.WorkingEntry, workspace, source string, now time.Time) bundleDecision {
	if len(call.Operations) == 0 {
		return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "empty classification", Next: "This is a bug in the selected pack's classifier, so running the call again fails the same way: report it to the pack's author"}}
	}
	decided := policy.Decide(call.Operations, brokerstore.ResolveWorkspace(workspace), workingMembers(working, now))
	covered := func(op brokerpolicy.Operation, candidate bool) bool {
		return slices.ContainsFunc(working, func(entry brokerstore.WorkingEntry) bool {
			return timedCovers(entry, now, workspace, source, op, candidate)
		})
	}
	out := bundleDecision{Verdict: Verdict{Verdict: "runs", Rule: "every write is covered"}}
	if len(call.Writes) == 0 {
		out.Verdict.Rule = "standing read"
	}
	asks := false
	next := 0
	for range call.Reads {
		d := decided.Operations[next]
		switch d.Decision {
		case brokerpolicy.Refuse:
			if d.Ceiling.Value == brokerpolicy.Never {
				return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "the host config says never for this read",
					Next: "Remove the `never` that names this read (" + d.Ceiling.Origin + ") with `yolo config unset`, or ask the human to"}}
			}
			return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "read is not permitted", Next: refusalNext(d)}}
		case brokerpolicy.Hold:
			out.AskReads = append(out.AskReads, next)
			asks = asks || !covered(d.Operation, false)
		}
		next++
	}
	for index, write := range call.Writes {
		if len(write.Candidates) == 0 {
			d := decided.Operations[next]
			next++
			switch d.Decision {
			case brokerpolicy.Refuse:
				return refusedWrite("This write is refused by " + lineOf([]brokerpolicy.OperationDecision{d}) + "; change the request with `yolo request` and approve its scope at the gate")
			case brokerpolicy.Hold:
				asks = asks || !covered(d.Operation, false)
			}
			continue
		}
		group := decided.Operations[next : next+len(write.Candidates)]
		next += len(write.Candidates)
		g := groupDecision{Write: index}
		uncovered := false
		var refused []string
		for _, d := range group {
			if d.Decision == brokerpolicy.Refuse {
				refused = append(refused, d.Operation.Cell+" by "+lineOf([]brokerpolicy.OperationDecision{d}))
				continue
			}
			g.Keep = append(g.Keep, d.Operation.Cell)
			uncovered = uncovered || d.Decision == brokerpolicy.Hold && !covered(d.Operation, true)
		}
		factRefused, factLines := false, []string{}
		for _, fact := range call.Withheld {
			factRefused = factRefused || fact.Decision == brokerpolicy.Refuse
			factLines = append(factLines, fact.Line)
		}
		switch {
		case len(g.Keep) == 0:
			return refusedWrite("Every cell this call might write is refused (" + strings.Join(refused, "; ") + "); ask the human to change one of those lines with `yolo request` or `yolo allow`, then retry")
		case len(refused) != 0 && factRefused:
			return bundleDecision{Verdict: Verdict{Verdict: "refused", Rule: "a refused lookup decides between refused and allowed writes",
				Next: "This call needs a lookup that read policy refuses (" + strings.Join(factLines, "; ") + ") and some cells it might write are refused (" + strings.Join(refused, "; ") + "); ask the human to change one of those lines with `yolo request` or `yolo allow`, then retry"}}
		}
		g.Resolve = len(refused) != 0
		if !factRefused {
			for _, fact := range call.Withheld {
				g.Fact = append(g.Fact, fact.Reads...)
			}
		}
		asks = asks || uncovered || g.Resolve
		out.Groups = append(out.Groups, g)
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

// bundleSubjects attaches the asking reads, then a withheld lookup's read, to a request
// in classification order, and narrows each candidate write to the cells not refused, so
// the one digest binds them beside every write. A read that already runs under standing
// authority is not a subject: it needs no answer, and a legacy call keeps its digest.
func bundleSubjects(q brokerstore.Request, call Classification, decided bundleDecision) brokerstore.Request {
	chainOf := func(op brokerpolicy.Operation) []brokerstore.CallResource {
		var chain []brokerstore.CallResource
		for _, resource := range op.Chain {
			chain = append(chain, brokerstore.CallResource{Type: resource.Type, ID: resource.ID})
		}
		return chain
	}
	seen := map[string]bool{}
	add := func(op brokerpolicy.Operation) {
		chain := chainOf(op)
		if key := op.Cell + "\x00" + brokerstore.CallResourceKey(chain); !seen[key] {
			seen[key] = true
			q.CallReads = append(q.CallReads, brokerstore.CallRead{Cell: op.Cell, Chain: chain})
		}
	}
	for _, index := range decided.AskReads {
		add(call.Operations[index])
	}
	for _, g := range decided.Groups {
		for _, op := range g.Fact {
			add(op)
		}
		q.CallWrites = slices.Clone(q.CallWrites)
		q.CallWrites[g.Write] = brokerstore.CallWrite{Cell: g.Keep[0], Chain: q.CallWrites[g.Write].Chain, Role: brokerstore.RoleWithheldWrite, Candidates: slices.Clone(g.Keep)}
	}
	q.Digest = brokerstore.CommandDigest(q)
	return q
}
