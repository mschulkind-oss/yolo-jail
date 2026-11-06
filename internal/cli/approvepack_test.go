package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/approvalwait"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/ghbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestApproveSelectsDeclaredSourcesWithoutNameBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")

	pack := filepath.Join(t.TempDir(), "pack")
	writeApprovePackFile(t, filepath.Join(pack, "pack.toml"), `name = "fixture"
[[loophole]]
from = "loopholes/tasks"
`)
	writeApprovePackFile(t, filepath.Join(pack, "loopholes", "tasks", "loophole.toml"), `name = "tasks"
description = "A local broker fixture"
version = 1
transport = "loopback-tls"
lifecycle = "spawned"
[brokered]
source = "local-tasks"
remote_host = "fixture.test"
[host_daemon]
cmd = ["fixture-broker", "{repository_scope}"]
`)
	userConfig := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	selection := `{"packs":[{"source":"file://` + filepath.ToSlash(pack) + `","name":"fixture"}]}`
	writeApprovePackFile(t, userConfig, selection)
	selected, complete := config.UserScopeSelectedPacks()
	defer packload.ReleaseEmbedded()
	if !complete {
		t.Fatal("the configured fixture pack did not resolve as a selected pack")
	}
	declared := false
	for _, pack := range selected {
		modules, _, _, err := packload.ReadableLoopholeModules(pack)
		if err != nil {
			t.Fatal(err)
		}
		for _, module := range modules {
			if module.Decl != nil && module.Decl.Brokered != nil && module.Decl.Brokered.Source == "local-tasks" {
				declared = true
			}
		}
	}
	if !declared {
		t.Fatal("the configured selected pack did not resolve brokered.source local-tasks")
	}
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(filepath.Join(workspace, ".yolo"), 0o700); err != nil {
		t.Fatal(err)
	}

	pending := func(source, command string) (*brokerstore.Store, brokerstore.Request) {
		t.Helper()
		s := &brokerstore.Store{Dir: paths.BrokerSourceDir(source), Source: source, PublishRecords: source == "local-tasks"}
		r, err := s.File(brokerstore.Request{Jail: "fixture-jail", Start: "fixture-start", Workspace: workspace,
			Argv: []string{command}, Repo: "fixture.test/team/project", StdinDigest: "empty", Set: "write",
			Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 1}})
		if err != nil {
			t.Fatal(err)
		}
		return s, r
	}
	custom, denied := pending("local-tasks", "update task-1")
	pendingRecordPath := filepath.Join(workspace, ".yolo", approvalwait.DirName, denied.ID+".json")
	pendingRecordBytes, err := os.ReadFile(pendingRecordPath)
	if err != nil {
		t.Fatalf("host fixture did not publish the actual pending workspace record: %v", err)
	}
	var publishedPending approvalwait.Record
	if err := json.Unmarshal(pendingRecordBytes, &publishedPending); err != nil || publishedPending.State != "pending" {
		t.Fatalf("workspace record was not pending before runApprove: record=%+v err=%v", publishedPending, err)
	}
	_, gitRequest := pending(gitbroker.Source, "push refs/heads/main")
	_, githubRequest := pending(ghbroker.Source, "pr merge 1")

	list := func() (int, string, string) {
		t.Helper()
		return captureStd(t, func() int { return runApprove([]string{"approve", "--json"}) })
	}
	rc, out, errs := list()
	if rc != 0 || !strings.Contains(out, denied.ID) || !strings.Contains(out, gitRequest.ID) || !strings.Contains(out, githubRequest.ID) {
		t.Fatalf("selected source and core requests were not listed: rc=%d stdout=%s stderr=%s", rc, out, errs)
	}
	var state brokerstore.StateView
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		t.Fatalf("decode approval listing: %v\n%s", err, out)
	}
	foundSource := false
	for _, request := range state.Requests {
		if request.ID == denied.ID && request.Source == "local-tasks" {
			foundSource = true
		}
	}
	if !foundSource {
		t.Fatalf("selected declaration did not register local-tasks in approval listing: %+v", state.Requests)
	}
	foundSelectedStatus := false
	for _, source := range state.Sources {
		if source.Source == "local-tasks" && source.Status == "selected" {
			foundSelectedStatus = true
		}
	}
	if !foundSelectedStatus || !strings.Contains(out, `"source_status":"selected"`) {
		t.Fatalf("selected source status missing from additive JSON labels: %+v", state.Sources)
	}
	foundDenyOnly := false
	for _, source := range state.Sources {
		if source.Source == "local-tasks" && source.Authority == "deny-only" && strings.Contains(source.SelectionBasis, "launch selection is not verified") {
			foundDenyOnly = true
		}
	}
	if !foundDenyOnly {
		t.Fatalf("pack source authority/basis was not labeled: %+v", state.Sources)
	}
	rc, human, errs := captureStd(t, func() int { return runApprove([]string{"approve"}) })
	if rc != 0 || !strings.Contains(human, "Source local-tasks — selected in current user-scope prediction") || !strings.Contains(human, "deny/revoke only") {
		t.Fatalf("human listing did not label selected deny-only source: rc=%d stdout=%s stderr=%s", rc, human, errs)
	}

	if rc, _, errs = captureStd(t, func() int { return runApprove([]string{"approve", denied.ID, "--deny"}) }); rc != 0 {
		t.Fatalf("deny selected source: rc=%d stderr=%s", rc, errs)
	}
	requests, _, err := custom.List()
	if err != nil || len(requests) != 1 || requests[0].State != "denied" {
		t.Fatalf("selected source denial was not stored: requests=%+v err=%v", requests, err)
	}
	var waitOut, waitErr bytes.Buffer
	if rc := approvalwait.Wait(approvalwait.Opts{Workspace: workspace, ID: denied.ID, Out: &waitOut, Err: &waitErr, Poll: time.Millisecond}); rc != approvalwait.ExitDenied || !strings.Contains(waitOut.String(), "denied") {
		t.Fatalf("wait did not observe denied record: rc=%d stdout=%q stderr=%q", rc, waitOut.String(), waitErr.String())
	}
	var historyOut, historyErr bytes.Buffer
	if rc := approvalwait.History(approvalwait.HistoryOpts{Workspace: workspace, ID: denied.ID, Out: &historyOut, Err: &historyErr}); rc != 0 || !strings.Contains(historyOut.String(), "denied") {
		t.Fatalf("history did not observe denied record: rc=%d stdout=%q stderr=%q", rc, historyOut.String(), historyErr.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, ".yolo", approvalwait.DirName, denied.ID+".json")); err != nil {
		t.Fatalf("actual workspace approval record was not published: %v", err)
	}
	audit, err := os.ReadFile(paths.BrokerAuditLog())
	if err != nil || !bytes.Contains(audit, []byte(`"event":"decision"`)) || !bytes.Contains(audit, []byte(`"service":"local-tasks"`)) || !bytes.Contains(audit, []byte(`"outcome":"denied"`)) {
		t.Fatalf("generic source decision was not audited: %s err=%v", audit, err)
	}

	_, pendingApproval := pending("local-tasks", "update task-2")
	for _, flags := range [][]string{{}, {"--for", "15m"}} {
		request := pendingApproval
		if len(flags) > 0 {
			_, request = pending("local-tasks", "update task-2")
		}
		args := append([]string{"approve", request.ID}, flags...)
		if rc, _, errs := captureStd(t, func() int { return runApprove(args) }); rc != approveExitNotConfirmed || !strings.Contains(errs, "no active core approval authority") {
			t.Fatalf("selected pack source was granted %v: rc=%d stderr=%s", flags, rc, errs)
		}
	}

	_, retiredRequest := pending("local-tasks", "update task-3")
	writeApprovePackFile(t, userConfig, `{"packs":[]}`)
	rc, out, errs = list()
	if rc != 0 || !strings.Contains(out, retiredRequest.ID) || !strings.Contains(out, "\"status\":\"removed\"") {
		t.Fatalf("retired source request/status was not safely listable: rc=%d stdout=%s stderr=%s", rc, out, errs)
	}
	rc, human, errs = captureStd(t, func() int { return runApprove([]string{"approve"}) })
	if rc != 0 || !strings.Contains(human, "Source local-tasks — removed from current user-scope selection") {
		t.Fatalf("human listing did not label retired source: rc=%d stdout=%s stderr=%s", rc, human, errs)
	}
	if rc, _, errs = captureStd(t, func() int { return runApprove([]string{"approve", retiredRequest.ID, "--deny"}) }); rc != 0 {
		t.Fatalf("deny retired request: rc=%d stderr=%s", rc, errs)
	}
	requests, _, err = custom.List()
	foundDenied := false
	for _, request := range requests {
		if request.ID == retiredRequest.ID && request.State == "denied" {
			foundDenied = true
		}
	}
	if err != nil || !foundDenied {
		t.Fatalf("retired request could not be denied: requests=%+v err=%v", requests, err)
	}
	var retiredWaitOut, retiredWaitErr bytes.Buffer
	if rc := approvalwait.Wait(approvalwait.Opts{Workspace: workspace, ID: retiredRequest.ID, Out: &retiredWaitOut, Err: &retiredWaitErr, Poll: time.Millisecond}); rc != approvalwait.ExitDenied {
		t.Fatalf("retired denial did not publish to wait: rc=%d stdout=%q stderr=%q", rc, retiredWaitOut.String(), retiredWaitErr.String())
	}
	audit, err = os.ReadFile(paths.BrokerAuditLog())
	if err != nil || bytes.Count(audit, []byte(`"event":"decision"`)) < 2 || bytes.Count(audit, []byte(`"service":"local-tasks"`)) < 2 {
		t.Fatalf("retired source decision was not audited: %s err=%v", audit, err)
	}
	if rc, _, errs := captureStd(t, func() int { return runApprove([]string{"approve", pendingApproval.ID}) }); rc != approveExitNotConfirmed || !strings.Contains(errs, "no active core approval authority") {
		t.Fatalf("removed pack request was approvable: rc=%d stderr=%s", rc, errs)
	}
}

func TestApproveIncompleteSelectionIsUnknownButRetiredRecordsRemainManageable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	configPath := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeApprovePackFile(t, configPath, `{"packs":[`)

	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &brokerstore.Store{Dir: paths.BrokerSourceDir("unknown-source"), Source: "unknown-source"}
	request, err := store.File(brokerstore.Request{Jail: "jail", Start: "start", Workspace: workspace, Argv: []string{"write"}, StdinDigest: "empty",
		Repo: "host/project", Set: "write", Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 1}})
	if err != nil {
		t.Fatal(err)
	}
	rc, out, errs := captureStd(t, func() int { return runApprove([]string{"approve", "--json"}) })
	if rc != 0 || !strings.Contains(out, `"status":"unknown"`) || !strings.Contains(out, `"diagnostics":["INCOMPLETE:`) || !strings.Contains(out, "run `yolo check`") {
		t.Fatalf("incomplete discovery was not explicit/actionable: rc=%d stdout=%s stderr=%s", rc, out, errs)
	}
	if rc, _, errs = captureStd(t, func() int { return runApprove([]string{"approve", request.ID}) }); rc != approveExitNotConfirmed || !strings.Contains(errs, "selection unknown") {
		t.Fatalf("unknown source received authority: rc=%d stderr=%s", rc, errs)
	}
	if rc, _, errs = captureStd(t, func() int { return runApprove([]string{"approve", request.ID, "--deny"}) }); rc != 0 {
		t.Fatalf("host could not deny unknown source: rc=%d stderr=%s", rc, errs)
	}
}

func TestApprovalDeclarationRefusalsAndWarningsMarkSourceDiscoveryIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name        string
		makeProblem func(*testing.T, string)
	}{
		{name: "refusal", makeProblem: func(t *testing.T, pack string) {}},
		{name: "warning", makeProblem: func(t *testing.T, pack string) {
			writeApprovePackFile(t, filepath.Join(pack, "loopholes", "bad", "loophole.toml"), "name = [\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_USER_LAYER", "")
			pack := filepath.Join(t.TempDir(), "pack")
			writeApprovePackFile(t, filepath.Join(pack, "pack.toml"), `name = "fixture"
[[loophole]]
from = "loopholes/bad"
`)
			tc.makeProblem(t, pack)
			writeApprovePackFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[{"source":"file://`+filepath.ToSlash(pack)+`","name":"fixture"}]}`)
			workspace := filepath.Join(t.TempDir(), "workspace")
			if err := os.MkdirAll(workspace, 0o755); err != nil {
				t.Fatal(err)
			}
			store := &brokerstore.Store{Dir: paths.BrokerSourceDir("legacy-source"), Source: "legacy-source"}
			if _, err := store.File(brokerstore.Request{Jail: "jail", Start: "start", Workspace: workspace, Argv: []string{"write"}, StdinDigest: "empty",
				Repo: "host/project", Set: "write", Policy: brokerstore.Policy{Window: time.Minute, MaxWindow: time.Hour, Uses: 1}}); err != nil {
				t.Fatal(err)
			}
			rc, out, errs := captureStd(t, func() int { return runApprove([]string{"approve", "--json"}) })
			if rc != 0 || !strings.Contains(out, `"status":"unknown"`) || !strings.Contains(out, `"diagnostics":["INCOMPLETE:`) || !strings.Contains(out, "run `yolo check`") {
				t.Fatalf("%s declaration problem was not an actionable incomplete state: rc=%d stdout=%s stderr=%s", tc.name, rc, out, errs)
			}
		})
	}
}

func assertSelectedAliasIsVisibleDenyOnly(t *testing.T, source string) {
	t.Helper()
	rc, out, errs := captureStd(t, func() int { return runApprove([]string{"approve", "--json"}) })
	if rc != 0 {
		t.Fatalf("list selected source %s: rc=%d stdout=%s stderr=%s", source, rc, out, errs)
	}
	var state brokerstore.StateView
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		t.Fatalf("decode selected source listing: %v\n%s", err, out)
	}
	for _, listed := range state.Sources {
		if listed.Source == source && listed.Status == "selected" && listed.Authority == "deny-only" &&
			strings.Contains(listed.SelectionBasis, "launch selection is not verified") {
			return
		}
	}
	t.Fatalf("selected alias %s was not visible as selected and deny-only: %+v; diagnostics=%v", source, state.Sources, state.Diagnostics)
}

func selectApprovalAliasPack(t *testing.T, home, source, name string) string {
	t.Helper()
	pack := filepath.Join(t.TempDir(), "pack")
	writeApprovePackFile(t, filepath.Join(pack, "pack.toml"), `name = "fixture"
[[loophole]]
from = "loopholes/broker"
`)
	remoteHost := "*"
	if source == ghbroker.Source {
		remoteHost = "github.com"
	}
	writeApprovePackFile(t, filepath.Join(pack, "loopholes", "broker", "loophole.toml"), `name = "broker"
description = "A custom broker alias"
version = 1
transport = "loopback-tls"
lifecycle = "spawned"
[brokered]
source = "`+source+`"
remote_host = "`+remoteHost+`"
[host_daemon]
cmd = ["fixture-broker", "{repository_scope}"]
publishes = "socket"
`)
	selection, err := json.Marshal(map[string]any{"packs": []any{map[string]any{
		"source": "file://" + filepath.ToSlash(pack), "name": name,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeApprovePackFile(t, path, string(selection))
	return path
}

func selectApprovePacks(t *testing.T, home string, names ...string) string {
	t.Helper()
	selection, err := json.Marshal(map[string]any{"packs": names})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeApprovePackFile(t, path, string(selection))
	return path
}

func writeApprovePackFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
