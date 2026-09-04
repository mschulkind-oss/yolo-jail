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

// An unshared workspace must be caught BEFORE the sudo prompt, the nix build,
// the staging and the bootstrap. Measured 2026-09-03 on the first real
// end-to-end launch: without this the launch reached config generation and died
// with `mkdir <ws>/.yolo/prism: permission denied` six times over, naming
// neither ACLs nor `yolo macos-fix-permissions`.
//
// Pins the CALL SITE: MaterializeDarwin must never be reached, so deleting the
// check fails this rather than merely changing a message.
func TestRunOffersToShareUnsharedWorkspace(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := grantDeps(&rec, &buf)
	materialized := false
	d.MaterializeDarwin = func(string, []any) (*Darwin, bool, error) {
		materialized = true
		return nil, true, nil
	}
	asked := ""
	d.Confirm = func(prompt string) bool { asked = prompt; return false }

	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/old-checkout"))

	if rc != 1 {
		t.Errorf("rc = %d, want 1 (declined)", rc)
	}
	if asked == "" {
		t.Error("never asked — a bare refusal is a gate that protects nothing here")
	}
	if materialized {
		t.Error("reached MaterializeDarwin — the check must run before the nix build")
	}
	if strings.Contains(strings.Join(rec, "\n"), "proxy:") {
		t.Error("launched a sandbox that cannot write the workspace")
	}
	got := buf.String()
	// Declining must leave the user able to do it themselves.
	if !strings.Contains(got, "macos-fix-permissions /Users/Shared/yolo/old-checkout") {
		t.Errorf("declining does not name the explicit command:\n%s", got)
	}
	// WHY, not just what: without this a user cannot tell an unshared workspace
	// from a yolo bug.
	if !strings.Contains(got, "CREATED") {
		t.Errorf("message does not explain that the ACL is granted at creation:\n%s", got)
	}
	// It is not an error state, and saying so avoids a bug report.
	if !strings.Contains(got, "Nothing is broken") {
		t.Errorf("message reads as a failure rather than as unfinished setup:\n%s", got)
	}
}

// Accepting applies the retrofit and CONTINUES into the launch — the whole point
// of asking rather than refusing.
func TestRunSharesWorkspaceOnConfirm(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := grantDeps(&rec, &buf)
	d.Confirm = func(string) bool { return true }

	rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/old-checkout"))

	if rc != 42 {
		t.Errorf("rc = %d, want 42 (the mock proxy's exit code) — accepting must continue\n%s",
			rc, buf.String())
	}
	joined := strings.Join(rec, "\n")
	// The retrofit must be the SAME script the standalone command runs, or the two
	// paths could drift into applying different ACEs.
	// Match the retrofit's own shape, not the bare word "find": the bootstrap argv
	// carries YOLO_BLOCK_CONFIG, which names the blocked `find` tool and matches a
	// loose substring test on every launch.
	if !strings.Contains(joined, "-exec chmod -h +a") {
		t.Errorf("the confirm branch did not run FixPermissionsScript:\n%s", joined)
	}
	if !strings.Contains(joined, "proxy:") {
		t.Errorf("did not launch after sharing:\n%s", buf.String())
	}
}

// A nil Confirm is NON-INTERACTIVE, and must read as "no". A launch with no
// terminal to ask on must never rewrite a tree's permissions because a pipe
// happened to be empty.
func TestRunRefusesUnsharedWorkspaceNonInteractively(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := grantDeps(&rec, &buf)
	d.Confirm = nil

	if rc := RunMacosUser(d, newOpts("/Users/Shared/yolo/old-checkout")); rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if strings.Contains(strings.Join(rec, "\n"), "-exec chmod -h +a") {
		t.Error("applied ACLs with no way to ask — silence must not consent")
	}
	if !strings.Contains(buf.String(), "macos-fix-permissions") {
		t.Errorf("non-interactive refusal does not name the command:\n%s", buf.String())
	}
}

// A shared workspace is never asked about and never walked: the hot path does
// ZERO ACL work, which is the property 84c55268 bought by moving to inheritance.
func TestRunDoesNoACLWorkOnASharedWorkspace(t *testing.T) {
	var rec []string
	var buf bytes.Buffer
	d := mockDeps(&rec) // probe returns 0 — already shared
	d.Out = &buf
	d.Confirm = func(string) bool { t.Error("asked about a workspace that is already shared"); return false }

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
