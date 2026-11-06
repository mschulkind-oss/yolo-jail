package integration

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount is slice 12a on the hardware
// (docs/design/macos-user-model-plan.md, MMP-D25): while a session of workspace A runs as the
// sandbox account, a launch in workspace B runs too. Each session's $HOME is its own workspace's
// home state (<ws>/.yolo/setups/seatbelt-yolo-account/home), and nothing under the account's home is a link into either
// sidecar, so neither workspace's layout can repoint the other's.
//
// The pre-12a refusal ("Refusing the macos-user launch … shares one sandbox account") must not
// appear.
func TestMacosUserTwoWorkspacesRunAtOnceAsTheAccount(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": []}`)
	wsA := macosUserWorkspace(t, `{}`)
	wsB := macosUserWorkspace(t, `{}`)
	sync := macosUserSyncDir(t, wsA, ".yolo-it-account-home")
	a := holdSession(t, wsA, sync, heldSessionScript(sync, `printf '%s' "$HOME" > `+sync+`/home-a`, ""))
	a.awaitUp(t)

	homeA, err := os.ReadFile(filepath.Join(sync, "home-a"))
	if err != nil {
		t.Fatalf("A's session recorded no $HOME: %v", err)
	}
	if want := macosUserHome(wsA, "account"); string(homeA) != want {
		t.Errorf("A's $HOME is %q, want its own workspace home %q", homeA, want)
	}

	r := runMacosUser(t, wsB, `echo "B-RAN HOME=$HOME"`)
	out := r.combined()
	if r.rc != 0 || !strings.Contains(r.stdout, "B-RAN") {
		t.Fatalf("B did not launch beside A's session (rc %d):\n%s", r.rc, out)
	}
	if strings.Contains(out, "Refusing the macos-user launch") {
		t.Errorf("B's launch printed the pre-12a refusal:\n%s", out)
	}
	if want := "HOME=" + macosUserHome(wsB, "account"); !strings.Contains(r.stdout, want) {
		t.Errorf("B's session does not have its own workspace home (%s):\n%s", want, r.stdout)
	}

	// Nothing under the account's home points into either sidecar. A pre-12a account home may
	// still hold links into an OLD workspace's sidecar (nothing migrates, OQ-MM4); these two
	// workspaces are fresh, so any link into them was laid by this run.
	sidecars := []string{macosUserHome(wsA, "account"), macosUserHome(wsB, "account")}
	root := macosuser.SandboxHome()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // a dir the test user cannot read is not one it can judge
		}
		if d.IsDir() && strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)) >= 4 {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return nil
		}
		for _, sc := range sidecars {
			if target == sc || strings.HasPrefix(target, sc+string(filepath.Separator)) {
				t.Errorf("%s is a link into a workspace sidecar (%s)", p, target)
			}
		}
		return nil
	})

	if res := a.release(t); res.rc != 0 {
		t.Fatalf("A's session ended %d:\n%s\n%s", res.rc, res.stdout, res.stderr)
	}
}
