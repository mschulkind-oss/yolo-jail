package run

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// The host daemon compares the launch input's cells with the selected declaration by deep
// equality (callbroker.currentAuthority). A read cell declares no default table, so the input
// must keep that absence through its JSON round trip, or the first call after a fresh launch is
// refused as "selected pack declaration changed".
func TestLaunchInputKeepsAReadCellEqualToItsDeclarationAfterJSON(t *testing.T) {
	decl := &loopholedecl.CallBroker{Source: "aws", Front: "cli", Program: "aws", Cells: []loopholedecl.CallBrokerCell{
		{Name: "put", Tables: []string{"all"}, Default: map[string]bool{"all": false}},
		{Name: "list", Kind: "read"},
	}}
	lp := &loopholes.Loophole{Name: "aws", Pack: "aws", Source: loopholes.SourcePack, Enabled: true, CallBroker: decl}
	policy := map[string]map[string]config.CallBrokerCellPolicy{"aws": {
		"put":  {Tables: map[string]config.CallBrokerTablePolicy{"all": {Decision: "ask", Request: "absent", Ceiling: "on_request"}}},
		"list": {},
	}}
	in, err := selectedCallBrokerLaunchInput(lp, map[string][]config.ScopeRepo{"aws": nil}, policy, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var back CallBrokerLaunchInput
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decl.Cells, back.Cells) {
		t.Fatalf("cells differ after the JSON round trip:\n decl %#v\n sent %#v", decl.Cells, back.Cells)
	}
}
