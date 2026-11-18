package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func scopedBrokerGateFixture(t *testing.T, roots bool) (string, *WorkspaceRead, *loopholedecl.CallBroker) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	body := ""
	if roots {
		body = "[call_roots]\nlocal-tasks = [\"team:alpha\"]\n"
	}
	write(t, filepath.Join(workspace, "yolo-jail.toml"), body)
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loopholedecl.Decode([]byte(`{"name":"tasks-loop","transport":"loopback-tls","call_broker":{"source":"local-tasks","front":"cli","program":"fixture-task","classifier":["yolo","internal","call-broker-classify","circleci"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["all"]}]}}`), "/selected/task-pack/loopholes/tasks-loop")
	if err != nil {
		t.Fatal(err)
	}
	return workspace, read, manifest.CallBroker
}

func scopedBrokerGateCheck(workspace string, read *WorkspaceRead, declaration *loopholedecl.CallBroker) *ScopeCheck {
	return WithCallBrokerDeclarations(nil, workspace, []SelectedCallBrokerScope{{Source: "local-tasks", Label: "tasks-loop", Pack: "task-pack", Declaration: declaration}}, read)
}

func TestScopedBrokerGateInactiveDeclarationRecordsNothing(t *testing.T) {
	workspace, read, declaration := scopedBrokerGateFixture(t, false)
	check := scopedBrokerGateCheck(workspace, read, declaration)
	report, err := ScopeChanges(workspace, check)
	if err != nil || report.ScopeChanged || len(report.ScopeBlock)+len(report.ScopeCounts) != 0 {
		t.Fatalf("rootless declaration prompted: %+v %v", report, err)
	}
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	record, _, _, err := readScopeRecords(workspace)
	if err != nil || len(record) != 0 {
		t.Fatalf("inactive source acquired record: %+v %v", record, err)
	}
	if _, err := os.Stat(ApprovalScopePath(workspace)); !os.IsNotExist(err) {
		t.Fatalf("inactive source created scope record: %v", err)
	}
}

func TestScopedBrokerGateDeclaredFirstRootAndDeclarationOnly(t *testing.T) {
	workspace, read, declaration := scopedBrokerGateFixture(t, true)
	check := scopedBrokerGateCheck(workspace, read, declaration)
	report, err := ScopeChanges(workspace, check)
	block := strings.Join(report.ScopeBlock, "\n")
	if err != nil || !report.ScopeChanged || !strings.Contains(block, "team:alpha") || !strings.Contains(block, "yolo-jail.toml") || strings.Contains(block, ".git/config") || !strings.Contains(block, "selected call-broker declaration") {
		t.Fatalf("first declared root disclosure: %+v %v", report, err)
	}
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ApprovalScopePath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("initial admitted record: %s", before)
	declaration.Cells[0].MaxFor = "1h"
	changed := scopedBrokerGateCheck(workspace, read, declaration)
	report, err = ScopeChanges(workspace, changed)
	block = strings.Join(report.ScopeBlock, "\n")
	if err != nil || !report.ScopeChanged || len(report.ScopeCounts) != 0 || strings.Contains(block, "read from") || !strings.Contains(block, "removed") || !strings.Contains(block, "added") {
		t.Fatalf("declaration-only showed scope/origin/count or hid diff: %+v %v", report, err)
	}
	ok, err := CheckConfigAndScopeChanges(workspace, read.Config, changed, false, false, nil)
	if ok || err == nil {
		t.Fatalf("unapproved declaration passed: %v %v", ok, err)
	}
	after, _ := os.ReadFile(ApprovalScopePath(workspace))
	if !bytes.Equal(before, after) {
		t.Fatal("decline changed admitted record")
	}
	if err := RecordApproval(workspace, read.Config, changed); err != nil {
		t.Fatal(err)
	}
	report, err = ScopeChanges(workspace, changed)
	if err != nil || report.ScopeChanged {
		t.Fatalf("approved declaration asked again: %+v %v", report, err)
	}
}

type scopedBrokerModelPrompter struct {
	accept bool
	report ChangeReport
}

func (p *scopedBrokerModelPrompter) Prompt([]string) bool { return p.accept }
func (p *scopedBrokerModelPrompter) PromptReport(report ChangeReport) bool {
	p.report = report
	return p.accept
}

func TestScopedBrokerGateLegacyAbsentReadRequiresExplicitWriteUpgrade(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n url = git@github.com:o/r.git\n")
	if err := os.Remove(h.userCfg); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	write(t, filepath.Join(filepath.Dir(h.userCfg), "config.toml"), "[allow.git]\non_request = [\"push\"]\n")
	write(t, filepath.Join(h.ws, "yolo-jail.toml"), "[requests.git]\npush = [\"main\"]\n")
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordApproval(h.ws, read.Config, nil); err != nil {
		t.Fatal(err)
	}
	legacy, err := scopeRecordJSON(scopeRecord{"git": {"git@github.com:o/r.git"}})
	if err != nil {
		t.Fatal(err)
	}
	legacy += "\n"
	write(t, ApprovalScopePath(h.ws), legacy)
	metadata := loopholedecl.Brokered{Source: "git", RemoteHost: "*", Grants: []string{"branch"}, ReadCapability: "fetch", KindCapabilities: map[string]string{"branch": "push"}}
	declaration := readableBrokerDeclaration("git-pack", "git-broker", metadata, nil)
	broker := ScopeBroker{Source: "git", Label: "git-broker", Pack: "git-pack", RemoteHost: "*", GrantKinds: metadata.Grants, Brokered: metadata, Readable: &declaration}
	check := NewScopeCheck(h.ws, []ScopeBroker{broker}, read)
	report, err := ScopeChanges(h.ws, check)
	if err != nil || report.ScopeChanged || len(check.ApprovedGrants()["git-broker"]) != 0 {
		t.Fatalf("legacy absent-read acquired authority/prompt: %+v %v", report, err)
	}
	ok, err := CheckConfigAndScopeChanges(h.ws, read.Config, check, false, false, nil)
	before, _ := os.ReadFile(ApprovalScopePath(h.ws))
	if !ok || err != nil || string(before) != legacy {
		t.Fatalf("unchanged gate rewrote legacy record: %s %v %v", before, ok, err)
	}
	write(t, filepath.Join(h.ws, "yolo-jail.toml"), "[requests.git]\npush = [\"main\"]\n[network]\nmode = \"host\"\n")
	read, err = ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	check = NewScopeCheck(h.ws, []ScopeBroker{broker}, read)
	prompt := &scopedBrokerModelPrompter{}
	ok, err = CheckConfigAndScopeChanges(h.ws, read.Config, check, true, false, prompt)
	block := strings.Join(prompt.report.ScopeBlock, "\n")
	after, _ := os.ReadFile(ApprovalScopePath(h.ws))
	if ok || err != nil || !bytes.Equal(before, after) || !strings.Contains(block, "+ grant branch") || !strings.Contains(block, "+ write request requests.git.push") || !strings.Contains(block, "added") {
		t.Fatalf("pending upgrade omitted added writes or recorded cancellation: %+v %v %v", prompt.report, ok, err)
	}
	prompt.accept = true
	ok, err = CheckConfigAndScopeChanges(h.ws, read.Config, check, true, false, prompt)
	if !ok || err != nil {
		t.Fatalf("explicit upgrade failed: %v %v", ok, err)
	}
	record, _, _, err := readScopeRecords(h.ws)
	if err != nil || len(record["git.policy-model"]) != 1 || len(ApprovedGrants(h.ws, "git")) != 1 || recordHeldRead(record, "git") {
		t.Fatalf("marked write-only approval projected incorrectly: %+v %v", record, err)
	}
	projected := NewScopeCheck(h.ws, []ScopeBroker{broker}, read)
	if len(projected.Sources[0].Repos()) != 1 || len(projected.ApprovedGrants()["git-broker"]) != 1 {
		t.Fatalf("marked projection lost independent write: %+v", projected)
	}
}

func TestScopedBrokerGateLegacyBroadReadDoesNotPromptOrGainMarker(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n url = git@github.com:o/r.git\n")
	write(t, h.userCfg, `{"brokered":{"git":{"grants":[{"kind":"read","repo":"*"}]}}}`)
	write(t, filepath.Join(h.ws, "yolo-jail.toml"), "")
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordApproval(h.ws, read.Config, nil); err != nil {
		t.Fatal(err)
	}
	legacy, err := scopeRecordJSON(scopeRecord{"git": {"git@github.com:o/r.git"}, "git.grants": {"read *"}})
	if err != nil {
		t.Fatal(err)
	}
	legacy += "\n"
	write(t, ApprovalScopePath(h.ws), legacy)
	check := NewScopeCheck(h.ws, []ScopeBroker{{Source: "git", Label: "git-broker", RemoteHost: "*", Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "*"}}}, read)
	prompt := &scopedBrokerModelPrompter{}
	ok, err := CheckConfigAndScopeChanges(h.ws, read.Config, check, true, false, prompt)
	after, _ := os.ReadFile(ApprovalScopePath(h.ws))
	if !ok || err != nil || prompt.report.ScopeChanged || string(after) != legacy {
		t.Fatalf("legacy broad read prompted/rewritten: %v %v %s %+v", ok, err, after, prompt.report)
	}
}

func TestScopedBrokerGateLegacyCallDeclarationHashDoesNotPrompt(t *testing.T) {
	workspace, read, declaration := scopedBrokerGateFixture(t, true)
	check := scopedBrokerGateCheck(workspace, read, declaration)
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	record, _, sources, err := readScopeRecords(workspace)
	if err != nil {
		t.Fatal(err)
	}
	delete(record, policyModelRecordKey("local-tasks"))
	key := callBrokerScopeRecordKey("local-tasks")
	var legacy []string
	for _, line := range record[key] {
		if !strings.HasPrefix(line, "declaration_sha256=") {
			legacy = append(legacy, line)
		}
	}
	record[key] = legacy
	if err := writeScopeRecords(workspace, scopeOutcome{next: record, nextSources: sources}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(ApprovalScopePath(workspace))
	ok, err := CheckConfigAndScopeChanges(workspace, read.Config, check, false, false, nil)
	after, _ := os.ReadFile(ApprovalScopePath(workspace))
	if !ok || err != nil || !bytes.Equal(before, after) {
		t.Fatalf("legacy body disclosure acquired hash/model or prompt: %v %v %s", ok, err, after)
	}
}

func TestScopedBrokerGateMatchingAndRouteHashMutations(t *testing.T) {
	for _, field := range []string{"mode", "separator", "anchors", "route_anchors"} {
		t.Run(field, func(t *testing.T) {
			workspace, read, declaration := scopedBrokerGateFixture(t, true)
			declaration.Scope.Nesting = "segment"
			declaration.Scope.Separator = "/"
			declaration.Scope.Anchors = []string{"team:"}
			declaration.Context = &loopholedecl.CallBrokerContext{Fields: map[string]string{"account": "FIXTURE_ACCOUNT"}, RouteAnchors: []string{"team:"}}
			if err := RecordApproval(workspace, read.Config, scopedBrokerGateCheck(workspace, read, declaration)); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "mode":
				declaration.Scope.Nesting = "exact"
				declaration.Scope.Separator, declaration.Scope.Anchors = "", nil
			case "separator":
				declaration.Scope.Separator = ":"
			case "anchors":
				declaration.Scope.Anchors = []string{"team:", "task:"}
			case "route_anchors":
				declaration.Context.RouteAnchors = nil
			}
			report, err := ScopeChanges(workspace, scopedBrokerGateCheck(workspace, read, declaration))
			block := strings.Join(report.ScopeBlock, "\n")
			if err != nil || !report.ScopeChanged || len(report.ScopeCounts) != 0 || strings.Contains(block, "read from") || !strings.Contains(block, loopholedecl.CallBrokerDeclarationHash(declaration)) || !strings.Contains(block, "removed") || !strings.Contains(block, "added") {
				t.Fatalf("selected %s hash mutation escaped declaration gate: %+v %v", field, report, err)
			}
		})
	}
}

func TestScopedBrokerGateCombinedAndRootWithdrawal(t *testing.T) {
	workspace, read, declaration := scopedBrokerGateFixture(t, true)
	if err := RecordApproval(workspace, read.Config, scopedBrokerGateCheck(workspace, read, declaration)); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(workspace, "yolo-jail.toml"), "[call_roots]\nlocal-tasks = [\"team:beta\"]\n")
	read, err := ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	declaration.Cells[0].MaxFor = "1h"
	report, err := ScopeChanges(workspace, scopedBrokerGateCheck(workspace, read, declaration))
	block := strings.Join(report.ScopeBlock, "\n")
	if err != nil || !report.ScopeChanged || len(report.ScopeCounts) != 1 || !strings.Contains(block, "read from yolo-jail.toml") || !strings.Contains(block, "team:beta") || !strings.Contains(block, "declaration_sha256=") {
		t.Fatalf("combined gate lost scope/declaration: %+v %v", report, err)
	}
	write(t, filepath.Join(workspace, "yolo-jail.toml"), "")
	read, err = ReadWorkspaceForGate(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := RecordApproval(workspace, read.Config, scopedBrokerGateCheck(workspace, read, declaration)); err != nil {
		t.Fatal(err)
	}
	record, _, _, err := readScopeRecords(workspace)
	if err != nil || len(record) != 0 {
		t.Fatalf("withdrawn root retained source declaration/model: %+v %v", record, err)
	}
	report, err = ScopeChanges(workspace, scopedBrokerGateCheck(workspace, read, declaration))
	if err != nil || report.ScopeChanged || len(report.ScopeBlock) != 0 {
		t.Fatalf("inactive declaration re-prompted after withdrawal: %+v %v", report, err)
	}
}

func TestScopedBrokerGateRemotesOriginNamesGitConfig(t *testing.T) {
	workspace, read, declaration := scopedBrokerGateFixture(t, false)
	if err := os.MkdirAll(filepath.Join(workspace, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(workspace, ".git", "config"), "[remote \"origin\"]\n url = git@github.com:o/r.git\n")
	declaration.Scope.Roots = "remotes"
	declaration.Scope.RemoteHost = "github.com"
	declaration.Scope.RootTemplate = "{owner}/{repo}"
	check := scopedBrokerGateCheck(workspace, read, declaration)
	if err := RecordApproval(workspace, read.Config, check); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(workspace, ".git", "config"), "[remote \"origin\"]\n url = git@github.com:o/changed.git\n")
	report, err := ScopeChanges(workspace, scopedBrokerGateCheck(workspace, read, declaration))
	block := strings.Join(report.ScopeBlock, "\n")
	if err != nil || !report.ScopeChanged || !strings.Contains(block, ".git/config") || !strings.Contains(block, "o/changed") {
		t.Fatalf("remote source origin misleading: %+v %v", report, err)
	}
}

func TestScopedBrokerGateNativeReadCapChangesDeclaration(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n url = git@github.com:o/r.git\n")
	write(t, filepath.Join(h.ws, "yolo-jail.toml"), "[requests.git]\nfetch = true\n")
	write(t, h.userCfg, `{"brokered":{"git":{"grants":[{"kind":"read","repo":"*"}]}}}`)
	broker := ScopeBroker{Source: "git", Label: "git-broker", RemoteHost: "*", Brokered: loopholedecl.Brokered{Source: "git", RemoteHost: "*"}}
	read, err := ReadWorkspaceForGate(h.ws)
	if err != nil {
		t.Fatal(err)
	}
	check := NewScopeCheck(h.ws, []ScopeBroker{broker}, read)
	if err := RecordApproval(h.ws, read.Config, check); err != nil {
		t.Fatal(err)
	}
	broker.Brokered.ReadMaxFor = "1h"
	report, err := ScopeChanges(h.ws, NewScopeCheck(h.ws, []ScopeBroker{broker}, read))
	block := strings.Join(report.ScopeBlock, "\n")
	if err != nil || !report.ScopeChanged || len(report.ScopeCounts) != 0 || strings.Contains(block, "read from") || !strings.Contains(block, loopholedecl.BrokeredDeclarationHash(&broker.Brokered)) {
		t.Fatalf("selected native hash mutation did not gate: %+v %v", report, err)
	}
}
