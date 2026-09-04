package macosuser

import (
	"bytes"
	"strings"
	"testing"
)

// grantDeps returns mockDeps whose ACL probe FAILS — i.e. the workspace has not
// been shared with the sandbox group — while every other bash step succeeds, so
// nothing below can be an artifact of a blanket RunBash failure.
func grantDeps(rec *[]string, buf *bytes.Buffer) Deps {
	d := mockDeps(rec)
	d.RunBash = func(s string) int {
		*rec = append(*rec, "bash:"+s)
		if strings.Contains(s, "ls -lde") {
			return 1 // the probe: no sandbox-group ACE
		}
		return 0
	}
	d.Out = buf
	return d
}

// An unshared workspace must be caught BEFORE the sudo prompt, the nix build, the
// staging and the bootstrap. Measured 2026-09-03 on the first real end-to-end
// launch: without this, it reached config generation and died with
// `mkdir <ws>/.yolo/prism: permission denied` six times over, naming neither ACLs
// nor `yolo macos-fix-permissions`.
//
// It REFUSES and names the command rather than offering to fix inline: one
// idempotent command the user has already met (macos-setup names it, so does
// `yolo --help` and the diagnosing-the-jail skill) beats a prompt in every path,
// and it keeps the O(files) walk off the launch.
//
// Pins the CALL SITE: MaterializeDarwin must never be reached, so deleting the
// check fails this rather than merely changing a message.
func TestRunRefusesUnsharedWorkspace(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := grantDeps(&rec, &buf)
	materialized := false
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		materialized = true
		return nil, true, nil
	}

	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/moved-in"))

	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if materialized {
		t.Error("reached MaterializeDarwin — the check must refuse before the nix build")
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Error("launched a sandbox that cannot write the workspace")
	}
	// The launch must NOT walk the tree: that cost is why the check exists rather
	// than an unconditional retrofit.
	if strings.Contains(strings.Join(rec, "\n"), "-exec chmod -h +a") {
		t.Error("applied ACLs on the launch path — the walk belongs in macos-fix-permissions")
	}
	got := buf.String()
	if !strings.Contains(got, "macos-fix-permissions /Users/Shared/yolo/moved-in") {
		t.Errorf("refusal does not name the remedy with the workspace path:\n%s", got)
	}
	// After macos-setup shares everything under the root, a MOVED-in project is the
	// only route left — so that is what the message should lead with.
	if !strings.Contains(got, "MOVED") {
		t.Errorf("refusal does not name the likely cause:\n%s", got)
	}
	// It is not breakage, and saying so avoids a bug report.
	if !strings.Contains(got, "idempotent") {
		t.Errorf("refusal does not say the remedy is safe to re-run:\n%s", got)
	}
}

// A shared workspace launches with no ACL work at all — the hot path stays free,
// which is the property 84c55268 bought by moving to an inheriting entry.
func TestRunDoesNoACLWorkOnASharedWorkspace(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec) // probe returns 0 — already shared
	d.Out = &buf

	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/ok")); rc != 42 {
		t.Errorf("rc = %d, want 42\n%s", rc, buf.String())
	}
	if strings.Contains(strings.Join(rec, "\n"), "-exec chmod -h +a") {
		t.Error("walked the tree on a shared workspace — the hot path must do no ACL work")
	}
}

// The probe must ask for the ACE provisioning actually applies. A spelling that
// drifted from WorkspaceACLAces would report a problem the remedy cannot clear —
// the worst shape for a preflight.
func TestWorkspaceGrantedScriptMatchesTheProvisionedACE(t *testing.T) {
	script := WorkspaceGrantedScript("/Users/Shared/yolo/ws", "")
	// The optional "inherited " is the whole point: ls renders a directly-applied
	// ACE and an inherited one differently, and matching only the first
	// false-negatives every correctly-provisioned workspace. Behaviour is pinned
	// against real `ls` output in workspacegrantreal_test.go; this pins that the
	// script keeps naming the right principal.
	if !strings.Contains(script, "group:"+SandboxGroup+" (inherited )?allow") {
		t.Errorf("probe does not look for the granted ACE in both spellings: %s", script)
	}
	if !strings.Contains(script, "/Users/Shared/yolo/ws") {
		t.Errorf("probe does not name the workspace: %s", script)
	}
	if !strings.HasPrefix(WorkspaceACLAces("")["dir"], "group:"+SandboxGroup+" allow") {
		t.Error("the probed ACE prefix and WorkspaceACLAces have drifted apart")
	}
}
