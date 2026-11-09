package callbroker

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// Verdict describes the same classified call in evaluate and act mode. It is not
// permission to dispatch: act must check live authority at its effect boundary.
type Verdict struct {
	Verdict   string `json:"verdict"`
	Rule      string `json:"rule"`
	Next      string `json:"next,omitempty"`
	Digest    string `json:"digest,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

func callRequest(source, workspace, jail, start, launch string, c Classification, stdin []byte) brokerstore.Request {
	q := brokerstore.Request{Workspace: workspace, Jail: jail, Start: start, LaunchID: launch,
		Set: source, Repo: source, Argv: slices.Clone(c.Argv), StdinDigest: fmt.Sprintf("%x", sha256.Sum256(stdin))}
	for _, write := range c.Writes {
		item := brokerstore.CallWrite{Cell: write.Cell}
		for _, resource := range write.Chain {
			item.Chain = append(item.Chain, brokerstore.CallResource{Type: resource.Type, ID: resource.ID})
		}
		q.CallWrites = append(q.CallWrites, item)
	}
	q.Digest = brokerstore.CommandDigest(q)
	return q
}

// decideCall is pure. Every classified write must run; one refused write
// refuses the whole call, otherwise any asking write makes the whole call ask.
// An absent policy is not an implicit grant. EffectiveCells must be the frozen
// gate result, never reconstructed from mutable workspace config at call time.
func decideCall(call Classification, cells map[string]config.CallBrokerCellPolicy, working []brokerstore.WorkingEntry, workspace, source string, now time.Time) Verdict {
	if len(call.Reads)+len(call.Writes) == 0 {
		return Verdict{Verdict: "refused", Rule: "empty classification", Next: "This is a bug in the selected pack's classifier, so running the call again fails the same way: report it to the pack's author"}
	}
	if len(call.Writes) == 0 {
		return Verdict{Verdict: "runs", Rule: "standing read"}
	}
	out := Verdict{Verdict: "runs", Rule: "every write is covered"}
	for _, write := range call.Writes {
		policy, ok := cells[write.Cell]
		if !ok || len(write.Chain) == 0 {
			return Verdict{Verdict: "refused", Rule: "undeclared write cell or resource", Next: "This is a bug in the selected pack, so running the call again fails the same way: report it to the pack's author"}
		}
		table := "all"
		timed := false
		chain := make([]brokerstore.CallResource, len(write.Chain))
		for i, resource := range write.Chain {
			chain[i] = brokerstore.CallResource{Type: resource.Type, ID: resource.ID}
		}
		for _, entry := range working {
			if !entry.LiveAt(now) || !brokerstore.SameWorkspace(entry.Workspace, workspace) || entry.Repo != source {
				continue
			}
			// A human's timed answer (BX-D17) covers exactly its cell on its whole chain; it
			// answers an ask and never overrides a table that refuses.
			if brokerstore.IsCallKind(entry.Kind) {
				if entry.Timed() && entry.Kind == brokerstore.WorkingCallPrefix+write.Cell && entry.Resource == brokerstore.CallResourceKey(chain) {
					timed = true
				}
				continue
			}
			for _, resource := range write.Chain {
				if entry.Kind == resource.Type && entry.Resource == resource.ID {
					table = "working_set"
				}
			}
		}
		setting, ok := policy.Tables[table]
		if !ok || setting.Decision == "refuse" || setting.Request == "false" {
			return Verdict{Verdict: "refused", Rule: "write is not permitted by its declared table", Next: "Change the request with `yolo request` and approve its scope at the gate"}
		}
		if (setting.Decision != "run" || setting.Request == "ask") && !timed {
			out = Verdict{Verdict: "asks", Rule: "a write needs approval", Next: "Ask a human on the host to review pack broker access after its approval policy is settled"}
		}
	}
	return out
}
