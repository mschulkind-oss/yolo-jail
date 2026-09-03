package macosuser

import (
	"bytes"
	"strings"
	"testing"
)

// The launch must REFUSE a workspace the sandbox group has no ACL entry for, and
// refuse BEFORE spending a sudo prompt, a nix build, the staging and the whole
// bootstrap. Measured 2026-09-03 on the first real end-to-end launch: without
// this, it reached config generation and died with
// `mkdir <ws>/.yolo/prism: permission denied` six times over — a message naming
// neither ACLs nor `yolo macos-fix-permissions`, the remedy that already existed.
//
// Pins the CALL SITE: the assertion is that MaterializeDarwin is never reached
// and nothing launches, so deleting the check in RunMacosUser fails this test
// rather than merely changing a message.
func TestRunRefusesWorkspaceWithoutSandboxACL(t *testing.T) {
	var rec []string
	d := mockDeps(&rec)
	materialized := false
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		materialized = true
		return nil, true, nil
	}
	// The ACL probe is the ONLY RunBash that fails; every other bash step still
	// succeeds, so a refusal here cannot be an artifact of a blanket failure.
	d.RunBash = func(s string) int {
		rec = append(rec, "bash:"+s)
		if strings.Contains(s, "group:"+SandboxGroup+" allow") {
			return 1
		}
		return 0
	}
	var buf bytes.Buffer
	d.Out = &buf

	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/old-checkout"))

	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if materialized {
		t.Error("reached MaterializeDarwin — the ACL check must refuse before the nix build")
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Error("launched a sandbox it cannot write to")
	}
	got := buf.String()
	// The remedy is the whole value of this refusal: the failure it replaces was
	// already loud, just undiagnosable.
	if !strings.Contains(got, "macos-fix-permissions") {
		t.Errorf("refusal does not name the remedy command:\n%s", got)
	}
	if !strings.Contains(got, "/Users/Shared/yolo/old-checkout") {
		t.Errorf("refusal does not name the workspace:\n%s", got)
	}
	// WHY, not just what: a user who does not know macOS applies inheriting ACLs
	// at creation time only cannot tell this from a yolo bug.
	if !strings.Contains(got, "creation time") {
		t.Errorf("refusal does not explain why an existing dir lacks the ACE:\n%s", got)
	}
}

// The converse, or a check that refused unconditionally would pass the test
// above: a granted workspace proceeds into the launch.
func TestRunAcceptsWorkspaceWithSandboxACL(t *testing.T) {
	var rec []string
	d := mockDeps(&rec) // RunBash returns 0 for everything, including the probe
	var buf bytes.Buffer
	d.Out = &buf

	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/ok")); rc != 42 {
		t.Errorf("rc = %d, want 42 (the mock proxy's exit code)\n%s", rc, buf.String())
	}
	if !strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Errorf("a granted workspace never launched:\n%s", buf.String())
	}
}

// The probe must ask for the ACE provisioning actually applies. A hardcoded
// spelling here that drifted from WorkspaceACLAces would report a problem that
// running the remedy could never clear — the worst shape for a preflight.
func TestWorkspaceGrantedScriptMatchesTheProvisionedACE(t *testing.T) {
	script := WorkspaceGrantedScript("/Users/Shared/yolo/ws", "")
	if !strings.Contains(script, "group:"+SandboxGroup+" allow") {
		t.Errorf("probe does not look for the granted ACE: %s", script)
	}
	if !strings.Contains(script, "/Users/Shared/yolo/ws") {
		t.Errorf("probe does not name the workspace: %s", script)
	}
	if !strings.HasPrefix(WorkspaceACLAces("")["dir"], "group:"+SandboxGroup+" allow") {
		t.Error("the probed ACE prefix and WorkspaceACLAces have drifted apart")
	}
}
