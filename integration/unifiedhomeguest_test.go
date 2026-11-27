package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The guest joins the unified workspace home (docs/design/unified-workspace-home-guest-plan.md, UG5):
// the cases only real Seatbelt can show. Every test starts TestMacosUser (requireMacosUser) so the
// macOS nightly selects it and a Linux run skips it; no host is faked here.

// guestHomeWorkspace is a workspace under the shared root with the claude pack selected for the run.
func guestHomeWorkspace(t *testing.T) string {
	t.Helper()
	packHome(t, `{"packs": ["claude"]}`)
	return macosUserWorkspace(t, `{}`)
}

// TestMacosUserGuestSessionsReachTheUnifiedHome: a session the agent wrote in the guest's own
// ~/.claude is adopted into the workspace's unified home by the next launch, which leaves a link.
func TestMacosUserGuestSessionsReachTheUnifiedHome(t *testing.T) {
	requireMacosUser(t)
	ws := guestHomeWorkspace(t)
	r := runMacosUserAs(t, "self", ws, `mkdir -p ~/.claude/projects/-g && echo s > ~/.claude/projects/-g/s.jsonl`)
	if r.rc != 0 {
		t.Fatalf("the first launch failed (rc %d):\n%s", r.rc, r.combined())
	}
	r = runMacosUserAs(t, "self", ws, `true`)
	if r.rc != 0 {
		t.Fatalf("the second launch failed (rc %d):\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Merged 1 files") {
		t.Errorf("the second launch did not say it merged the session:\n%s", r.combined())
	}
	unified := filepath.Join(ws, ".yolo", "unified-home", ".claude", "projects", "-g", "s.jsonl")
	if b, err := os.ReadFile(unified); err != nil || strings.TrimSpace(string(b)) != "s" {
		t.Errorf("the session is not in the unified home: %q, %v", b, err)
	}
	link := filepath.Join(macosUserHome(ws, "self"), ".claude", "projects")
	if tgt, err := os.Readlink(link); err != nil || !strings.HasPrefix(tgt, "../") {
		t.Errorf("%s is not a relative link: %q, %v", link, tgt, err)
	}
}

// TestMacosUserAWorkspaceMoveKeepsTheLinksWorking (UHG-D2): the links are relative, so renaming the
// workspace directory leaves them resolving.
func TestMacosUserAWorkspaceMoveKeepsTheLinksWorking(t *testing.T) {
	requireMacosUser(t)
	ws := guestHomeWorkspace(t)
	for _, script := range []string{
		`mkdir -p ~/.claude/projects/-g && echo s > ~/.claude/projects/-g/s.jsonl`, `true`,
	} {
		if r := runMacosUserAs(t, "self", ws, script); r.rc != 0 {
			t.Fatalf("launch failed (rc %d):\n%s", r.rc, r.combined())
		}
	}
	moved := ws + "-moved"
	if err := os.Rename(ws, moved); err != nil {
		t.Fatalf("moving the workspace: %v", err)
	}
	t.Cleanup(func() { _ = os.Rename(moved, ws) })
	r := runMacosUserAs(t, "self", moved, `cat ~/.claude/projects/-g/s.jsonl`)
	if r.rc != 0 || !strings.Contains(r.stdout, "s") {
		t.Fatalf("the link did not survive the move (rc %d):\n%s", r.rc, r.combined())
	}
}

// TestMacosUserASecondGuestIsRefused: a running session of the account's setup refuses a launch of
// seatbelt-self, naming the stop. The held session is the event; nothing sleeps. (holdSession
// launches the account, so the roles are the plan's, reversed.)
func TestMacosUserASecondGuestIsRefused(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": []}`)
	ws := macosUserWorkspace(t, `{}`)
	sync := macosUserSyncDir(t, ws, ".yolo-it-second-guest")
	held := holdSession(t, ws, sync, heldSessionScript(sync, `true`, ""))
	held.awaitUp(t)

	r := runMacosUserAs(t, "self", ws, `echo SHOULD-NOT-RUN`)
	if r.rc == 0 || strings.Contains(r.stdout, "SHOULD-NOT-RUN") {
		t.Fatalf("a second guest launched beside a running one (rc %d):\n%s", r.rc, r.combined())
	}
	want := "yolo stop --setup " + macosUserSetupFor("account")
	if !strings.Contains(r.combined(), want) {
		t.Errorf("the refusal does not name %q:\n%s", want, r.combined())
	}
}
