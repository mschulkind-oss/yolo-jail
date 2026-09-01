package run

// configapprovaldispatch_test.go pins the config-approval gate to the BACKEND
// DISPATCH rather than to one arm of it.
//
// The gate is a `runContainer` statement, and the `macos-user` arm of Run returns
// several lines above it. That is the same omission shape B-0 fixed for pack
// staging (packstagedispatch_test.go) and it failed the same silent way: nothing
// errored, the native backend simply launched a config no human had approved.
// Every existing approval test called config.CheckConfigChanges directly, so the
// question none of them asked was whether the pipeline reaches it at all.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// changedConfigWorkspace returns a workspace whose yolo-jail.jsonc has CHANGED
// since the last approved launch: an empty config is approved into the host-side
// record, then the file on disk grows an `mcp_servers` entry — a command line the
// agent's MCP client executes, which is the concrete reason this backend gating
// is not cosmetic.
func changedConfigWorkspace(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	if ok, err := config.CheckConfigChanges(ws, jsonx.NewOrderedMap(), false, true, nil); !ok || err != nil {
		t.Fatalf("establishing the approved baseline: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(filepath.Join(ws, config.WorkspaceConfigName),
		[]byte(`{"mcp_servers": {"x": {"command": "/bin/sh", "args": ["-c", "id"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return ws
}

// A macos-user launch with a changed config and nobody to prompt must REFUSE,
// exactly as the container path does. It is the OQ-D2 refusal reached through the
// pipeline: the native backend never runs, and the approval record is not rewritten
// (so the next interactive launch still has the same change to show).
func TestMacosUserLaunchGatesOnConfigApproval(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := changedConfigWorkspace(t)
	approved, _ := os.ReadFile(config.ApprovalSnapshotPath(ws))

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.IsTTYStdin = func() bool { return false }
	reached := false
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, bool, *jsonx.OrderedMap) int {
		reached = true
		return 0
	}

	rc := Run(*o)
	if reached {
		t.Error("macos-user launched a config that changed since the last approved launch, " +
			"with no terminal to approve it on and no " + config.AcceptConfigChangesFlag)
	}
	if rc != 1 {
		t.Errorf("Run() = %d, want 1 (a refused launch)\nstdout:\n%s", rc, stdout.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(config.AcceptConfigChangesFlag)) {
		t.Errorf("the refusal must name %s — its reader cannot be prompted\nstdout:\n%s",
			config.AcceptConfigChangesFlag, stdout.String())
	}
	if now, _ := os.ReadFile(config.ApprovalSnapshotPath(ws)); !bytes.Equal(approved, now) {
		t.Error("a refused macos-user launch rewrote the approval record")
	}
}

// The flag is the opt-in, and it must reach this arm too: with it the launch
// proceeds AND the new config is recorded, or the next scripted run refuses over
// the same change forever.
func TestMacosUserLaunchAcceptsWithTheFlagAndRecordsIt(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := changedConfigWorkspace(t)
	approved, _ := os.ReadFile(config.ApprovalSnapshotPath(ws))

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.IsTTYStdin = func() bool { return false }
	o.AcceptConfigChanges = true
	reached := false
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, bool, *jsonx.OrderedMap) int {
		reached = true
		return 0
	}

	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Fatal(config.AcceptConfigChangesFlag + " must let a macos-user launch through")
	}
	now, _ := os.ReadFile(config.ApprovalSnapshotPath(ws))
	if bytes.Equal(approved, now) {
		t.Error("the flag granted the approval but did not record it — the next launch refuses again")
	}
}

// --dry-run renders a plan and launches nothing, so there is nothing to approve.
// Gating it would refuse the very inspection a user reaches for when deciding
// whether to approve.
func TestMacosUserDryRunIsExemptFromTheApprovalGate(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := changedConfigWorkspace(t)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.IsTTYStdin = func() bool { return false }
	o.DryRun = true
	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, dryRun bool, _ *jsonx.OrderedMap) int {
		reached = true
		if !dryRun {
			t.Error("dry-run flag did not reach the handler")
		}
		return 0
	}

	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}
	if !reached {
		t.Error("--dry-run must render the plan without asking for approval")
	}
}
