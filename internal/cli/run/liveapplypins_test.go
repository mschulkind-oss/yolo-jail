package run

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// callBrokerApplyFixture selects a call-broker source named tasks and returns the config
// a live apply plans from, with a private HOME.
func callBrokerApplyFixture(t *testing.T) *jsonx.OrderedMap {
	t.Helper()
	root := t.TempDir()
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"tasks","transport":"loopback-tls","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":["yolo","internal","call-broker-classify","tasks"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"update","tables":["working_set","all"]}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true}})
	t.Cleanup(func() { loopholes.SetPackModules(nil) })
	t.Setenv("HOME", t.TempDir())
	return mustPolicyJSON(t, `{"brokered":{"tasks":{"roots":["team:alpha"]}}}`)
}

func writeTasksScope(t *testing.T, workspace string, snapshot *brokerscope.PolicyReference) (string, brokerscope.File) {
	t.Helper()
	id, err := brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	sf := brokerscope.File{Source: "tasks", LaunchID: id, PID: os.Getpid(), Container: "fixture-container", Workspace: workspace,
		Form: brokerscope.FormCallRoot, Repos: []string{"team:alpha"}, LiveScope: true, PolicySnapshot: snapshot}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	read, err := brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, read
}

// snapshotTasksScope rewrites sf with a real policy snapshot, as a snapshot-consuming launch holds.
func snapshotTasksScope(t *testing.T, sf brokerscope.File) (string, brokerscope.File) {
	t.Helper()
	p := brokerscope.PolicySnapshot{Version: brokerscope.PolicySnapshotVersion,
		Binding: brokerscope.PolicyBinding{Source: sf.Source, Workspace: sf.Workspace, LaunchID: sf.LaunchID,
			DeclarationHash: strings.Repeat("a", 64), InputDigest: strings.Repeat("b", 64)},
		Sequence: 1, Model: "legacy/1", Roots: []string{"team:alpha"},
		Asks: brokerscope.WorkspaceAsks{SourceRead: "true", Requests: json.RawMessage(`{}`), Scopes: []brokerscope.WorkspaceAskScope{}}}
	err := brokerlock.WithTransaction(paths.BrokerSourceDir(sf.Source), func() error {
		ref, err := brokerscope.PreparePolicyLocked(sf, p)
		sf.PolicySnapshot = ref
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	read, err := brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, read
}

// Call-site pin for blockIncompleteSnapshotApply: a running source whose live scope file
// carries a policy snapshot must plan as snapshot-pending, never through the legacy writers.
// Deleting the call in applyPlan fails this.
func TestApplyPlanBlocksASnapshotSourceFromLegacyApply(t *testing.T) {
	cfg := callBrokerApplyFixture(t)
	workspace := t.TempDir()
	path, file := writeTasksScope(t, workspace, nil)
	path, file = snapshotTasksScope(t, file)
	swapSeam(t, &liveScopeFilesSeam, func(context.Context, string, string, string) ([]liveScopeFile, error) {
		return []liveScopeFile{{Path: path, File: file, LiveScope: true}}, nil
	})
	o := &Options{Workspace: workspace}
	run := &applyRun{cfg: cfg, rt: "macos-user", cname: "fixture-container", mode: applyMode{running: true}, current: cfg}
	for _, change := range o.applyPlan(run, nil) {
		if change.outcome == outcomeSnapshotPending {
			return
		}
	}
	t.Fatal("a snapshot-consuming source was not held back from the legacy apply")
}

// Call-site pin for olderSourcePrecheck: the scope replacement itself re-proves the delivered
// asks under the source lock. A scope file with no delivered lease or input is refused and left
// untouched; deleting the precheck in applyStep lets the replacement through.
func TestApplyStepReprovesDeliveredAsksUnderTheSourceLock(t *testing.T) {
	cfg := callBrokerApplyFixture(t)
	workspace := t.TempDir()
	path, file := writeTasksScope(t, workspace, nil)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	o := &Options{Workspace: workspace}
	o.recordApprovedScopes(map[string][]config.ScopeRepo{"tasks": {{Repo: "team:beta"}}}, nil, nil)
	delta := scopeDelta{label: "tasks", source: "tasks", desired: []string{"team:beta"}, files: []liveScopeFile{{Path: path, File: file, LiveScope: true}}}
	run := &applyRun{cfg: cfg, rt: "podman", cname: "fixture-container", mode: applyMode{running: true}}
	err = o.applyStep(run, &applyChange{kind: changeScope, scope: delta}, time.Now().Add(30*time.Second))
	if err == nil || !strings.Contains(err.Error(), "old policy not replaced") {
		t.Fatalf("err=%v, want the older-asks refusal", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != string(before) {
		t.Fatalf("a refused replacement changed the scope file: %v", readErr)
	}
}

// A running broker started before `never` existed ignores a source never, so an apply for its
// source refuses with the restart step rather than leave it running what the line forbids; a broker
// whose input carries the NeverAware marker is not refused for it.
func TestApplyRefusesASourceWhoseRunningBrokerPredatesNever(t *testing.T) {
	cfg := callBrokerApplyFixture(t)
	dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[allow.tasks]\nnever = [\"all.update\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		input   string
		refused bool
	}{
		"predates never": {`{"version":"yolo.call-broker-launch/2","source":"tasks"}`, true},
		"honors never":   {`{"version":"yolo.call-broker-launch/2","source":"tasks","never_aware":true}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			input := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(input, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			_, file := writeTasksScope(t, workspace, nil)
			file.CallInput = input
			err := brokerBlindToNever(file)
			if tc.refused != (err != nil) || tc.refused && (!strings.Contains(err.Error(), "yolo stop") || !strings.Contains(err.Error(), "predates `never`")) {
				t.Fatalf("refused=%v want %v: %v", err != nil, tc.refused, err)
			}
			if tc.refused {
				o := &Options{Workspace: workspace}
				o.recordApprovedScopes(map[string][]config.ScopeRepo{"tasks": {{Repo: "team:beta"}}}, nil, nil)
				path, written := writeTasksScope(t, workspace, nil)
				written.CallInput = input
				delta := scopeDelta{label: "tasks", source: "tasks", desired: []string{"team:beta"}, files: []liveScopeFile{{Path: path, File: written, LiveScope: true}}}
				run := &applyRun{cfg: cfg, rt: "podman", cname: "fixture-container", mode: applyMode{running: true}}
				err := o.applyStep(run, &applyChange{kind: changeScope, scope: delta}, time.Now().Add(30*time.Second))
				if err == nil || !strings.Contains(err.Error(), "predates `never`") {
					t.Fatalf("the apply did not reach the fence: %v", err)
				}
			}
		})
	}
}
