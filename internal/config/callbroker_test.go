package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func callBrokerCellFixture(t *testing.T) *loopholedecl.CallBroker {
	t.Helper()
	body := `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"local-tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"rerun_workflows","tables":["working_set","all"],"default":{"working_set":true}}]}}`
	m, err := loopholedecl.Decode([]byte(body), "/packs/tasks/loopholes/tasks")
	if err != nil {
		t.Fatal(err)
	}
	return m.CallBroker
}
func testConfigMap(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	m, err := jsonx.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	o, ok := m.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("config=%T", m)
	}
	return o
}

func TestValidateConfigRefusesCallBrokerRequestForUndeclaredTableCell(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, WorkspaceConfigName), []byte(`{"requests":{"local-tasks":{"all":{"rerun_workflows":true}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack := selectedCallBrokerFixturePack(t, t.TempDir(), "", []string{"working_set"})
	resolver := explicitPackLoopholeResolver{packs: []*packload.Pack{pack}}
	errs, _ := ValidateConfig(decode(t, `{"requests":{"local-tasks":{"all":{"rerun_workflows":true}}}}`), workspace, resolver)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "not declared by the call-broker cell") {
		t.Fatalf("request to undeclared table/cell pair was not refused: %v", errs)
	}

	emptyUnknownTable := decode(t, `{"requests":{"local-tasks":{"not_a_table":{}}}}`)
	errs, _ = ValidateConfig(emptyUnknownTable, workspace, resolver)
	if len(errs) == 0 || !strings.Contains(strings.Join(errs, "\n"), "not a declared call-broker table") {
		t.Fatalf("empty request table not declared by the call-broker was not refused: %v", errs)
	}

	emptyDeclaredTable := decode(t, `{"requests":{"local-tasks":{"working_set":{}}}}`)
	errs, _ = ValidateConfig(emptyDeclaredTable, workspace, resolver)
	if strings.Contains(strings.Join(errs, "\n"), "not a declared call-broker table") {
		t.Fatalf("empty declared request table was refused: %v", errs)
	}
}

func TestCallBrokerConfigDefersOnlyTheMissingPreStageRootIdentity(t *testing.T) {
	resolver := callBrokerFenceResolver{
		"tasks": {Name: "tasks", CallBroker: callBrokerCellFixture(t)},
	}
	broker := resolver["tasks"].CallBroker
	broker.ProgramIdentityError = "selected staged pack root is unavailable"
	broker.ProgramIdentityPending = true
	if _, problems := callBrokerInfoBySource(resolver); len(problems) != 0 {
		t.Fatalf("the pre-stage-only missing root should be deferred to staged launch validation: %v", problems)
	}
	broker.ProgramIdentityPending = false
	if _, problems := callBrokerInfoBySource(resolver); len(problems) != 1 || !strings.Contains(problems[0], "program identity") {
		t.Fatalf("a non-pending program identity error must remain fatal: %v", problems)
	}
}

func TestDeclaredCellsRespectCeilingAndInvariant(t *testing.T) {
	decl := callBrokerCellFixture(t)
	user := testConfigMap(t, `{"allow":{"local-tasks":{"always":[]}}}`)
	ws := testConfigMap(t, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":true}}}}`)
	var errs, warns []string
	validateCallBrokerCells("local-tasks", decl, user, ws, &errs, &warns)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "outside the user ceiling") {
		t.Fatalf("ceiling warnings=%v", warns)
	}

	ws = testConfigMap(t, `{"requests":{"local-tasks":{"working_set":{"rerun_workflows":false},"all":{"rerun_workflows":true}}}}`)
	errs, warns = nil, nil
	validateCallBrokerCells("local-tasks", decl, user, ws, &errs, &warns)
	if len(errs) != 1 || !strings.Contains(errs[0], "working_set at least as permissive") {
		t.Fatalf("invariant errors=%v", errs)
	}

	ws = testConfigMap(t, `{"requests":{"local-tasks":{"working_set":{"unknown_cell":true}}}}`)
	errs, warns = nil, nil
	validateCallBrokerCells("local-tasks", decl, user, ws, &errs, &warns)
	if len(errs) != 1 || !strings.Contains(errs[0], "unknown call-broker cell") {
		t.Fatalf("unknown-cell errors=%v", errs)
	}
}

// A `<table>.<cell>` ceiling token is skipped by the grant reader, so the call-broker reader
// must name one that matches no declared table/cell pair instead of ignoring a typo.
func TestUndeclaredCallBrokerCeilingTokenIsNamed(t *testing.T) {
	decl := callBrokerCellFixture(t)
	ws := testConfigMap(t, `{}`)
	user := testConfigMap(t, `{"allow":{"local-tasks":{"on_request":["all.rerun_workflow","working_set.rerun_workflows"]}}}`)
	var errs, warns []string
	validateCallBrokerCells("local-tasks", decl, user, ws, &errs, &warns)
	if len(errs) != 0 || len(warns) != 1 || !strings.Contains(warns[0], "all.rerun_workflow names no declared") {
		t.Fatalf("errs=%v warns=%v", errs, warns)
	}
}
