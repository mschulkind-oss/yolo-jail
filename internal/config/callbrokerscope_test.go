package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestCallBrokerRootsDefaultsAndReadFloorReachGate(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"local-tasks","front":"http","classifier":["yolo","internal","call-broker-classify","circleci"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"rerun_workflows","tables":["working_set","all"],"default":{"working_set":true}}],"read_over_post":["/graphql"]}}`
	m, err := loopholedecl.Decode([]byte(manifest), "/staged/packs/tasks/loopholes/tasks")
	if err != nil {
		t.Fatal(err)
	}
	read := &WorkspaceRead{Config: decode(t, `{"brokered":{"local-tasks":{"roots":["team:alpha"]}}}`)}
	check := WithCallBrokerDeclarations(nil, ws, []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: m.CallBroker}}, read)
	report, err := ScopeChanges(ws, check)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(report.ScopeBlock, "\n")
	for _, want := range []string{"team:alpha", "rerun_workflows", "working_set=true,all=ask", "read_over_post=/graphql (method-floor exception)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("gate does not disclose %q:\n%s", want, joined)
		}
	}
}
