package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserASecondWorkspaceIsRefusedWhileASessionHoldsTheAccountHome is the account-home hold
// on the hardware (internal/cli/run's accounthomehold.go;
// docs/reference/macos-user-home-tiers.md#ht-d15): while a session of workspace A runs, a launch in
// workspace B is refused — not made to wait — before its nix build and before any sudo (the hold is
// asked ahead of the context-mount preflight, the first step that may run one), naming A and the
// next steps, and the account home's links still point into A's sidecar; once A's session ends, B
// launches.
//
// The hazard the hold closes is measured on Linux (entrypoint's
// TestASecondWorkspaceLayoutRepointsTheFirstsLinks: B's layout takes five of A's core links); its
// consequence for a live session on a Mac is reasoned, not observed.
func TestMacosUserASecondWorkspaceIsRefusedWhileASessionHoldsTheAccountHome(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": []}`)
	wsA := macosUserWorkspace(t, `{}`)
	wsB := macosUserWorkspace(t, `{}`)
	sync := macosUserSyncDir(t, wsA, ".yolo-it-account-home")
	a := holdSession(t, wsA, sync, heldSessionScript(sync, "", ""))
	a.awaitUp(t)

	binLink := filepath.Join(macosuser.SandboxHome(), ".yolo", "bin")
	targetA, err := os.Readlink(binLink)
	if err != nil {
		t.Fatalf("A's session laid no %s: %v", binLink, err)
	}
	if !strings.HasPrefix(targetA, filepath.Join(wsA, ".yolo", "home")) {
		t.Fatalf("%s points at %s during A's session, not into A's sidecar", binLink, targetA)
	}

	r := runMacosUser(t, wsB, "echo B-RAN")
	out := r.combined()
	if r.rc != 1 || strings.Contains(out, "B-RAN") {
		t.Fatalf("B's launch ran beside A's session (rc %d):\n%s", r.rc, out)
	}
	for _, want := range []string{"Refusing the macos-user launch", wsA, "Quit that session", `"runtime": "container"`} {
		if !strings.Contains(out, want) {
			t.Errorf("B's refusal does not say %q:\n%s", want, out)
		}
	}
	for _, before := range []string{"Building the sandbox's tools with nix", "Setting up the sandbox"} {
		if strings.Contains(out, before) {
			t.Errorf("B was refused only after %q:\n%s", before, out)
		}
	}
	if got, _ := os.Readlink(binLink); got != targetA {
		t.Errorf("B's refused launch repointed %s from %s to %s", binLink, targetA, got)
	}

	if res := a.release(t); res.rc != 0 {
		t.Fatalf("A's session ended %d:\n%s\n%s", res.rc, res.stdout, res.stderr)
	}
	r = runMacosUser(t, wsB, "echo B-RAN")
	if r.rc != 0 || !strings.Contains(r.stdout, "B-RAN") {
		t.Errorf("B did not launch once A's session ended (rc %d):\n%s", r.rc, r.combined())
	}
}
