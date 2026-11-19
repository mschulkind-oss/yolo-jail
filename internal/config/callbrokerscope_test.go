package config

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func TestSameCallBrokerDisclosurePreservesExactBytesAndMultiplicity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous []string
		current  []string
		equal    bool
	}{
		{"display reordered", []string{"source aws", "classifier=x"}, []string{"classifier=x", "source aws"}, true},
		{"changed case", []string{"source AWS"}, []string{"source aws"}, false},
		{"changed whitespace", []string{"cell change"}, []string{"cell change "}, false},
		{"duplicate removed", []string{"cell", "cell"}, []string{"cell"}, false},
		{"duplicate reordered", []string{"b", "a", "a"}, []string{"a", "b", "a"}, true},
		{"empty", nil, []string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			beforePrevious := append([]string(nil), tc.previous...)
			beforeCurrent := append([]string(nil), tc.current...)
			if got := sameCallBrokerDisclosure(tc.previous, tc.current); got != tc.equal {
				t.Fatalf("disclosure equality=%t, want %t", got, tc.equal)
			}
			if len(tc.previous) > 0 && !reflect.DeepEqual(tc.previous, beforePrevious) || len(tc.current) > 0 && !reflect.DeepEqual(tc.current, beforeCurrent) {
				t.Fatal("disclosure comparison mutated caller input")
			}
		})
	}
}

func TestCallBrokerLegacyDeclarationWithoutCLIArgvDoesNotPrompt(t *testing.T) {
	workspace := approvalWorkspace(t)
	declaration := callBrokerCellFixture(t)
	declaration.Classifier = nil
	read := &WorkspaceRead{Config: decode(t, `{"brokered":{"local-tasks":{"roots":["team:alpha"]}}}`)}
	selected := []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}
	if err := RecordApproval(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read)); err != nil {
		t.Fatal(err)
	}
	ok, err := CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read), false, false, nil)
	if !ok || err != nil {
		t.Fatalf("legacy declaration without CLI argv was re-approved: %v", err)
	}
}

func TestCallBrokerLegacyCLIApprovalRequiresOneTimeRenewal(t *testing.T) {
	workspace := approvalWorkspace(t)
	declaration := callBrokerCellFixture(t)
	read := &WorkspaceRead{Config: decode(t, `{"brokered":{"local-tasks":{"roots":["team:alpha"]}}}`)}
	selected := []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}
	check := WithCallBrokerDeclarations(nil, workspace, selected, read)
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	record, _, sources, err := readScopeRecords(workspace)
	if err != nil {
		t.Fatal(err)
	}
	key := callBrokerScopeRecordKey("local-tasks")
	var oldDisclosure []string
	for _, line := range record[key] {
		if strings.Contains(line, "_argv_json=") {
			continue
		}
		oldDisclosure = append(oldDisclosure, line)
	}
	record[key] = oldDisclosure
	if err := writeScopeRecords(workspace, scopeOutcome{next: record, nextSources: sources}); err != nil {
		t.Fatal(err)
	}
	before, err := scopeRecordJSON(record)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read), false, false, nil)
	var refusal *ChangedNonInteractiveError
	if ok || !errors.As(err, &refusal) || !refusal.ScopeChanged {
		t.Fatalf("hashless legacy CLI approval was silently accepted: %v", err)
	}
	afterRecord, _, _, err := readScopeRecords(workspace)
	if err != nil {
		t.Fatal(err)
	}
	after, err := scopeRecordJSON(afterRecord)
	if err != nil || before != after {
		t.Fatalf("legacy refusal changed its record: %v", err)
	}
	if err := RecordApproval(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read)); err != nil {
		t.Fatal(err)
	}
	ok, err = CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read), false, false, nil)
	if !ok || err != nil {
		t.Fatalf("explicit legacy renewal was not stable: %v", err)
	}
}

func TestCallBrokerHTTPFingerprintPreservesArgvBindingAndReapproval(t *testing.T) {
	workspace := approvalWorkspace(t)
	declaration := callBrokerCellFixture(t)
	declaration.Classifier = []string{"{program:classifier}", "--mode", "safe allow"}
	declaration.HTTP = &loopholedecl.CallBrokerHTTP{Origin: "https://fixture.example"}
	read := &WorkspaceRead{Config: decode(t, `{"brokered":{"local-tasks":{"roots":["team:alpha"]}}}`)}
	selected := []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}
	check := WithCallBrokerDeclarations(nil, workspace, selected, read)
	for _, line := range check.Sources[0].Disclosure {
		if strings.Contains(line, "_argv_json=") {
			t.Fatalf("full-hash HTTP declaration gained redundant argv marker: %s", line)
		}
	}
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	ok, err := CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read), false, false, nil)
	if !ok || err != nil {
		t.Fatalf("unchanged full-hash HTTP declaration was re-approved: %v", err)
	}
	if strings.Join(declaration.Classifier, " ") != strings.Join([]string{"{program:classifier}", "--mode", "safe", "allow"}, " ") {
		t.Fatal("HTTP fixture no longer reproduces the flattened classifier collision")
	}
	oldHash := loopholedecl.CallBrokerDeclarationHash(declaration)
	declaration.Classifier = []string{"{program:classifier}", "--mode", "safe", "allow"}
	if oldHash == loopholedecl.CallBrokerDeclarationHash(declaration) {
		t.Fatal("HTTP canonical hash did not change with argv boundaries")
	}
	ok, err = CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read), false, false, nil)
	var refusal *ChangedNonInteractiveError
	if ok || !errors.As(err, &refusal) || !refusal.ScopeChanged {
		t.Fatalf("HTTP full hash failed to gate changed classifier argv: %v", err)
	}
}

func TestCallBrokerDisclosureDisplayOrderDoesNotReapprove(t *testing.T) {
	workspace := approvalWorkspace(t)
	declaration := callBrokerCellFixture(t)
	read := &WorkspaceRead{Config: decode(t, `{"brokered":{"local-tasks":{"roots":["team:alpha"]}}}`)}
	selected := []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}
	check := WithCallBrokerDeclarations(nil, workspace, selected, read)
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	ok, err := CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, selected, read), false, false, nil)
	if !ok || err != nil {
		t.Fatalf("repeat launch re-approved an unchanged declaration: %v", err)
	}
}

func TestCallBrokerArgvRepartitionRequiresReapproval(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv [][]string
		set  func(*loopholedecl.CallBroker, []string)
	}{
		{
			name: "classifier",
			argv: [][]string{{"{program:classifier}", "--mode", "safe allow"}, {"{program:classifier}", "--mode", "safe", "allow"}},
			set:  func(b *loopholedecl.CallBroker, argv []string) { b.Classifier = argv },
		},
		{
			name: "lookup",
			argv: [][]string{{"{program:lookup}", "--mode", "safe allow"}, {"{program:lookup}", "--mode", "safe", "allow"}},
			set: func(b *loopholedecl.CallBroker, argv []string) {
				b.Lookups = []loopholedecl.CallBrokerLookup{{Name: "find", CLI: argv}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := approvalWorkspace(t)
			declaration := callBrokerCellFixture(t)
			if tc.name == "lookup" {
				declaration.Classifier = nil
			}
			tc.set(declaration, tc.argv[0])
			read := &WorkspaceRead{Config: decode(t, `{"brokered":{"local-tasks":{"roots":["team:alpha"]}}}`)}
			check := WithCallBrokerDeclarations(nil, workspace, []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}, read)
			if err := RecordApproval(workspace, read.Config, check); err != nil {
				t.Fatal(err)
			}
			unchanged := WithCallBrokerDeclarations(nil, workspace, []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}, read)
			ok, err := CheckConfigAndScopeChanges(workspace, read.Config, unchanged, false, false, nil)
			if !ok || err != nil {
				t.Fatalf("unchanged current-writer argv approval did not pass: %v", err)
			}
			record, _, _, err := readScopeRecords(workspace)
			if err != nil {
				t.Fatal(err)
			}
			key := callBrokerScopeRecordKey("local-tasks")
			binding := "classifier_argv_json="
			if tc.name == "lookup" {
				binding = "lookup=find cli_argv_json="
			}
			if !strings.Contains(strings.Join(record[key], "\n"), binding) {
				t.Fatalf("current-writer approval omitted its %s argv binding", tc.name)
			}
			before, err := scopeRecordJSON(record)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(tc.argv[0], " ") != strings.Join(tc.argv[1], " ") {
				t.Fatal("fixture does not reproduce the flattened disclosure collision")
			}
			tc.set(declaration, tc.argv[1])
			changed := WithCallBrokerDeclarations(nil, workspace, []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}, read)
			ok, err = CheckConfigAndScopeChanges(workspace, read.Config, changed, false, false, nil)
			var refusal *ChangedNonInteractiveError
			if ok || !errors.As(err, &refusal) || !refusal.ScopeChanged {
				t.Fatalf("non-TTY did not refuse repartitioned %s argv through the scope gate: %v", tc.name, err)
			}
			afterRecord, _, _, err := readScopeRecords(workspace)
			if err != nil {
				t.Fatal(err)
			}
			after, err := scopeRecordJSON(afterRecord)
			if err != nil || before != after {
				t.Fatalf("refusal changed prior approval: %v", err)
			}
			if err := RecordApproval(workspace, read.Config, changed); err != nil {
				t.Fatal(err)
			}
			ok, err = CheckConfigAndScopeChanges(workspace, read.Config, WithCallBrokerDeclarations(nil, workspace, []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks", Declaration: declaration}}, read), false, false, nil)
			if !ok || err != nil {
				t.Fatalf("explicit approval did not make stable declaration pass: %v", err)
			}
		})
	}
}

func TestCallBrokerRootsDefaultsAndReadFloorReachGate(t *testing.T) {
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"local-tasks","front":"http","http":{"origin":"https://fixture.example","credential":{"source":"host_env","key":"TASKS_TOKEN","header":"Authorization","prefix":"Bearer "},"request_headers":["Content-Type"]},"env_from_host":["TASKS_TOKEN"],"classifier":["yolo","internal","call-broker-classify","circleci"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"rerun_workflows","tables":["working_set","all"],"default":{"working_set":true}}],"read_over_post":["/graphql"]}}`
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
