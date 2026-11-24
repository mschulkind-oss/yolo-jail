package callbroker

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// A launch's frozen "ask" must hold the write, whatever the workspace's request was. An absent
// request under an on_request ceiling is the shipped aws pack's put with the user's ceiling and
// no workspace line: the cell asks. A true ask under that ceiling would run it unasked.
func TestFrozenAskHoldsEveryWriteItDecided(t *testing.T) {
	cells := []loopholedecl.CallBrokerCell{{Name: "put_objects", Tables: []string{"all"}}}
	for _, request := range []string{"absent", "true", "ask"} {
		effective := map[string]config.CallBrokerCellPolicy{"put_objects": {Tables: map[string]config.CallBrokerTablePolicy{
			"all": {Request: request, Ceiling: "on_request", Decision: "ask"}}}}
		policy, err := frozenOperationPolicy("aws", cells, effective, func(string) bool { return false })
		if err != nil {
			t.Fatal(err)
		}
		op := brokerpolicy.Operation{Source: "aws", Cell: "put_objects", Kind: brokerpolicy.Write,
			Chain: []brokerpolicy.Resource{{Type: "object", ID: "s3://bkt/team/new.txt"}}}
		if got := policy.Decide([]brokerpolicy.Operation{op}, "ws", nil).Decision; got != brokerpolicy.Hold {
			t.Errorf("request %q: a frozen ask decided %q, want %q", request, got, brokerpolicy.Hold)
		}
	}
}
