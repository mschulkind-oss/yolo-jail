package run

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// brokerLaunch is one launch of a workspace with a GitHub remote, through Run itself, with the
// github pack selected in the user config and nothing else there unless userExtra says so. The
// macos-user arm is the one whose handler a test can stand in for, and its call site hands the
// gate the MERGED config, which is where the per-workspace file's switch lands. Handed anything
// less, the gate finds no broker in play, asks nothing about the remotes, and the broker starts
// with an empty scope: every in-scope read answers 64, silently.
type brokerLaunch struct {
	rc      int
	reached bool
	out     string
}

// launchWithGitHub prepares HOME and the workspace and returns them before the launch, so a test
// can write the per-workspace file in between; run launches.
func launchWithGitHub(t *testing.T, userExtra string) (home, ws string, run func() brokerLaunch) {
	t.Helper()
	home = packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"),
		[]byte("{\n  \"packs\": [\"github\"]"+userExtra+"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, ws, func() brokerLaunch {
		t.Helper()
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		var l brokerLaunch
		o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ macosuser.HomeOverlay, _ macosuser.HostContext, _ bool, _ *jsonx.OrderedMap, _ []packload.BlockedTool, _ macosuser.JailDaemons) int {
			l.reached = true
			return 0
		}
		l.rc = Run(*o)
		l.out = stdout.String() + stderr.String()
		return l
	}
}

// writeWorkspaceSwitch writes the per-workspace file by hand, at the name its documented rule
// gives (docs/design/boundary-broker.md BB-D53), so this test reads the launch's behavior and not
// the writer's: the command that writes it is cli.TestLoopholesEnableWritesThePerWorkspaceFileAndNothingElse.
func writeWorkspaceSwitch(t *testing.T, home, ws, body string) {
	t.Helper()
	sum := sha256.Sum256([]byte(ws))
	path := filepath.Join(home, ".config", "yolo-jail", "workspaces",
		filepath.Base(ws)+"-"+hex.EncodeToString(sum[:])[:12]+".jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestOnlyAWorkspaceSwitchedOnStartsTheBroker is MANUAL-ONLY at the launch
// (docs/design/boundary-broker.md OQ-BB13, BB-D55): with the github pack selected, the
// github-broker is in play — its repository scope asked about at the gate — for a workspace
// whose per-workspace file turns it on, and for no other; a switch in the user config, or in
// the workspace's own config, refuses the launch naming `yolo loopholes enable`.
func TestOnlyAWorkspaceSwitchedOnStartsTheBroker(t *testing.T) {
	t.Run("switched on for this workspace", func(t *testing.T) {
		home, ws, run := launchWithGitHub(t, "")
		writeWorkspaceSwitch(t, home, ws, `{"workspace": "`+ws+`", "loopholes": {"github-broker": {"enabled": true}}}`)
		l := run()
		if l.rc == 0 || l.reached {
			t.Fatalf("a launch with no terminal ran past an unapproved repository scope: rc %d, "+
				"reached=%v\n%s", l.rc, l.reached, l.out)
		}
		for _, want := range []string{"github-broker repository scope, read from", `+ o/r  remote "origin"  added`,
			"--accept-config-changes", "`yolo loopholes disable github-broker`"} {
			if !strings.Contains(l.out, want) {
				t.Errorf("the refusal does not show %q:\n%s", want, l.out)
			}
		}
	})
	t.Run("no switch", func(t *testing.T) {
		_, _, run := launchWithGitHub(t, "")
		l := run()
		if l.rc != 0 || !l.reached {
			t.Fatalf("a launch with the pack selected and no switch did not launch: rc %d\n%s", l.rc, l.out)
		}
		if strings.Contains(l.out, "repository scope") {
			t.Errorf("a workspace that never turned the broker on was asked about its repositories:\n%s", l.out)
		}
	})
	t.Run("switched on for another workspace", func(t *testing.T) {
		home, ws, run := launchWithGitHub(t, "")
		other := filepath.Join(filepath.Dir(ws), "elsewhere")
		writeWorkspaceSwitch(t, home, ws, `{"workspace": "`+other+`", "loopholes": {"github-broker": {"enabled": true}}}`)
		l := run()
		if l.rc != 0 || !l.reached || strings.Contains(l.out, "repository scope") {
			t.Fatalf("a file naming another workspace turned the broker on here: rc %d reached=%v\n%s",
				l.rc, l.reached, l.out)
		}
		if !strings.Contains(l.out, "nothing in it applies") {
			t.Errorf("the launch did not warn about the file naming another workspace:\n%s", l.out)
		}
	})
	t.Run("switched in the user config", func(t *testing.T) {
		_, _, run := launchWithGitHub(t, `,
  "loopholes": {"github-broker": {"enabled": true}}`)
		l := run()
		if l.rc == 0 || l.reached {
			t.Fatalf("a user-config switch of a brokered loophole launched: rc %d\n%s", l.rc, l.out)
		}
		if !strings.Contains(l.out, "`yolo loopholes enable github-broker`") {
			t.Errorf("the refusal does not name the command that switches it per project:\n%s", l.out)
		}
	})
	t.Run("switched in the workspace config", func(t *testing.T) {
		_, ws, run := launchWithGitHub(t, "")
		if err := os.WriteFile(filepath.Join(ws, "yolo-jail.local.jsonc"),
			[]byte(`{"loopholes": {"github-broker": {"enabled": true}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		l := run()
		if l.rc == 0 || l.reached {
			t.Fatalf("a workspace switch of a brokered loophole launched: rc %d\n%s", l.rc, l.out)
		}
		if !strings.Contains(l.out, "yolo-jail.local.jsonc") ||
			!strings.Contains(l.out, "`yolo loopholes enable github-broker`") {
			t.Errorf("the refusal does not name the file and the command:\n%s", l.out)
		}
	})
}
