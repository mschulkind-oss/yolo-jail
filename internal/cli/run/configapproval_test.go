package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// configapproval_test.go covers the LAUNCHER half of docs/design/config-safety.md's
// two rulings: what a refused non-interactive launch actually prints (OQ-D2), and
// that the launcher never reaches into the workspace for the approval record
// (OQ-D1). The decision logic itself lives in internal/config and is tested there;
// what is only observable here is the rendering and the wiring of the flag.

// approvalOptions builds the minimal Options the config gate needs: an isolated
// $HOME (so the host-side approval record lands in a temp state dir), a captured
// stdout, and both tty seams pinned so the test never consults the real terminal.
func approvalOptions(t *testing.T, ws string, isTTY bool) (*Options, *bytes.Buffer) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	var buf bytes.Buffer
	return &Options{
		Workspace:   ws,
		Stdout:      &buf,
		Stderr:      &buf,
		IsTTYStdin:  func() bool { return isTTY },
		IsTTYStdout: func() bool { return false },
	}, &buf
}

func approvalConfig(t *testing.T, s string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("not an object: %T", v)
	}
	return m
}

// A REFUSED LAUNCH MUST SAY HOW TO PROCEED. The reader of this message is, by
// construction, someone who cannot be prompted — so the launcher has to print the
// flag, the files the merged config came from, and the change itself. Printing a
// bare "config changed" and exiting would leave a CI operator with no move.
func TestNonInteractiveConfigChangeRefusalIsActionable(t *testing.T) {
	ws := t.TempDir()
	o, buf := approvalOptions(t, ws, false /*non-tty*/)

	// Establish an approved baseline the same way an earlier launch would have.
	if ok, err := config.CheckConfigChanges(ws, approvalConfig(t, `{"packages": ["strace"]}`),
		false, true, nil); err != nil || !ok {
		t.Fatalf("seeding the baseline: ok=%v err=%v", ok, err)
	}

	if o.checkConfigChanges(approvalConfig(t, `{"packages": ["strace", "htop"]}`)) {
		t.Fatal("a changed config with no terminal to approve it on must refuse the launch")
	}

	out := buf.String()
	for _, want := range []string{
		config.AcceptConfigChangesFlag,
		filepath.Join(ws, config.WorkspaceConfigName),
		config.ApprovalSnapshotPath(ws),
		`+    "htop"`, // the diff, not just the fact of one
	} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal output does not mention %q:\n%s", want, out)
		}
	}
}

// THE SPLIT FILE MUST ACTUALLY BE DELIVERED. Moving the approval record host-side
// (OQ-D1) left the in-jail LoadConfig short-circuit without a source, so the launch
// now writes the merged config to <workspace>/.yolo/config-assembled.json for it.
// The writer lives here and the reader lives in internal/config, which is exactly
// the shape that rots quietly: drop this call and every unit test still passes
// while every jail silently falls back to a REDUCED re-assemble that has lost the
// host-only include_if_found overrides. So the round trip is pinned end to end.
func TestFreshLaunchDeliversTheAssembledConfigTheJailReadsBack(t *testing.T) {
	ws := t.TempDir()
	o, _ := approvalOptions(t, ws, false)

	// A merged config carrying a key that exists ONLY in the merge — the shape of
	// a host-side include_if_found override, which is the whole reason the jail
	// reads a delivered copy instead of re-assembling.
	merged := approvalConfig(t, `{"packages": ["ripgrep"], "mcp_servers": {"tavily": {"command": "npx"}}}`)
	o.writeLaunchConfigArtifacts(merged)

	// Read it back the way an in-jail LoadConfig does, for the jail's OWN workspace.
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	t.Setenv("YOLO_WORKSPACE", ws)
	got, err := config.LoadConfig(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Get("mcp_servers"); !ok {
		t.Errorf("the in-jail read did not get the delivered merge (keys %v) — a jail would "+
			"silently run on a reduced config", got.Keys())
	}
	// And it is NOT written to the retired approval location.
	if _, err := os.Stat(config.LegacyWorkspaceSnapshotPath(ws)); !os.IsNotExist(err) {
		t.Errorf("a launch must not write the old workspace-side snapshot (err=%v)", err)
	}
}

// The flag is the whole of Design Goal 5 after the ruling: non-interactive use
// still works, through an explicit approval rather than an implicit yes. Wiring it
// on the Options must both let the launch through AND record the approval, or the
// next scripted run refuses over the same unchanged change.
func TestAcceptConfigChangesFlagLetsANonInteractiveLaunchThrough(t *testing.T) {
	ws := t.TempDir()
	o, _ := approvalOptions(t, ws, false /*non-tty*/)
	o.AcceptConfigChanges = true

	if ok, err := config.CheckConfigChanges(ws, approvalConfig(t, `{"packages": ["strace"]}`),
		false, true, nil); err != nil || !ok {
		t.Fatalf("seeding the baseline: ok=%v err=%v", ok, err)
	}

	changed := approvalConfig(t, `{"packages": ["strace", "htop"]}`)
	if !o.checkConfigChanges(changed) {
		t.Fatal("--accept-config-changes must let a non-interactive launch proceed")
	}
	// Recorded: the same config now passes with the flag off.
	o.AcceptConfigChanges = false
	if !o.checkConfigChanges(changed) {
		t.Error("the flag must record the approval, or the next launch refuses again")
	}
}
