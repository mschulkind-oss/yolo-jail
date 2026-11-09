package callbroker

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/callbrokerinput"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// timed.go is `yolo approve <id> --for <duration>` for a pack call (pack-brokers.md BX-D17,
// BX-D18, OQ-BX7): one timed working-set entry per classified write, kind "call:<cell>" on the
// write's whole chain, stored for every write or none by brokerstore.DecideWorking. A pack
// gets the answers yolo's own packs get: Allow once, a timed answer and Deny, never a
// permanent Add.

// defaultCellMaxFor caps a timed answer for a cell that declares no max_for: the common
// 15-minute timed offer (boundary-broker.md BB-D80).
const defaultCellMaxFor = brokerstore.TimedGrantDefault

// RequestEntries is a pack store's Entries hook: the timed entries a --for answer to q records,
// one per classified write. Its call:<cell> kinds never join the working set for good (the
// store refuses a lasting one), so no pack answer is permanent.
func RequestEntries(q brokerstore.Request) ([]brokerstore.WorkingEntry, error) {
	if len(q.CallWrites) == 0 || q.Repo == "" || q.Workspace == "" {
		return nil, fmt.Errorf("request %s names no classified write a timed answer can cover. Next: run `yolo approve %s` to Allow once", q.ID, q.ID)
	}
	var out []brokerstore.WorkingEntry
	for _, w := range q.CallWrites {
		kind := brokerstore.WorkingCallPrefix + w.Cell
		if !brokerstore.IsCallKind(kind) || len(w.Chain) == 0 {
			return nil, fmt.Errorf("request %s has a write with no valid cell or resource. Next: deny it with `yolo approve %s --deny`", q.ID, q.ID)
		}
		e := brokerstore.WorkingEntry{Workspace: q.Workspace, Repo: q.Repo, Kind: kind,
			Resource: brokerstore.CallResourceKey(w.Chain), By: "human"}
		if !slices.Contains(out, e) {
			out = append(out, e)
		}
	}
	return out, nil
}

// ApprovalTimedLimit is a pack store's TimedLimit hook: the longest timed answer q may take,
// the smallest max_for among the cells its writes touch (BX-D17). It reads the cells of q's
// own launch input, which the store's Revalidate (ApprovalAuthority) has just checked against
// the current selected declaration in the same transaction.
func ApprovalTimedLimit(q brokerstore.Request) (time.Duration, error) {
	if !loopholedecl.ValidBrokerSourceName(q.Set) || q.LaunchID == "" {
		return 0, errors.New("call broker request has no live host launch; retry the original call")
	}
	sf, err := brokerscope.ReadFile(paths.BrokerScopeFile(q.Set, q.LaunchID))
	if err != nil || sf.Form != brokerscope.FormCallRoot || sf.CallInput == "" || sf.Source != q.Set {
		return 0, errors.New("selected call broker scope is unavailable; relaunch and retry")
	}
	data, err := readPrivateInput(sf.CallInput)
	if err != nil {
		return 0, err
	}
	var in callbrokerinput.Launch
	if json.Unmarshal(data, &in) != nil || in.Source != q.Set {
		return 0, errors.New("selected call broker input changed; relaunch and retry")
	}
	return cellsTimedLimit(in.Cells, q.CallWrites)
}

// cellsTimedLimit is the smallest max_for among the cells writes touch.
func cellsTimedLimit(cells []loopholedecl.CallBrokerCell, writes []brokerstore.CallWrite) (time.Duration, error) {
	if len(writes) == 0 {
		return 0, errors.New("the request names no classified write")
	}
	limit := time.Duration(0)
	for _, w := range writes {
		i := slices.IndexFunc(cells, func(c loopholedecl.CallBrokerCell) bool { return c.Name == w.Cell })
		if i < 0 {
			return 0, fmt.Errorf("cell %s is not declared by the selected pack; relaunch and retry", w.Cell)
		}
		cap := defaultCellMaxFor
		if cells[i].MaxFor != "" {
			d, err := time.ParseDuration(cells[i].MaxFor)
			if err != nil || d <= 0 {
				return 0, fmt.Errorf("cell %s declares an invalid max_for; check the selected pack", w.Cell)
			}
			cap = d
		}
		if limit == 0 || cap < limit {
			limit = cap
		}
	}
	return limit, nil
}
