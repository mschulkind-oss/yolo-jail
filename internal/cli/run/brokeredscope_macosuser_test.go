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
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
	// OUT OF A JAIL, pinned rather than inherited: this is a HOST launch, and in a jail
	// (YOLO_VERSION set) a workspace file's switch of a brokered loophole is a WARNING, the
	// workspace being live-mounted, so the launch went on to put the broker in play. Unset, not
	// emptied, because internal/loopholes reads the variable's presence.
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
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

// TestASelectedBrokerPackPutsTheBrokerInPlay: a broker has no switch
// (docs/design/boundary-broker.md BB-D104, BB-D106, BB-D109). With the github pack selected, the
// github-broker runs in every workspace and grants nothing, and selecting it is "basically a
// no-op option" (BB-D110): with no read grant the gate asks nothing about its remotes, and a
// launch with no terminal goes on. Once the user config holds the read grant the gate shows it
// on its own line, marked as the user config's, together with the remotes it reaches, so neither
// takes effect unseen (BB-D105, BB-D107). A switch a previous yolo wrote into the per-workspace file is
// ignored, in either direction, with a note naming the grant and the command that takes it out;
// a switch in the user config, or in the workspace's own config, refuses the launch naming the
// grant that gives access instead.
func TestASelectedBrokerPackPutsTheBrokerInPlay(t *testing.T) {
	// asked is the launch refused at the gate for want of a terminal, having shown the scope.
	asked := func(t *testing.T, l brokerLaunch) {
		t.Helper()
		if l.rc == 0 || l.reached {
			t.Fatalf("a launch with no terminal ran past an unapproved repository scope: rc %d, "+
				"reached=%v\n%s", l.rc, l.reached, l.out)
		}
		for _, want := range []string{"github-broker repository scope, read from", `+ o/r  remote "origin"  added`,
			"--accept-config-changes", "take the pack that ships it out of `packs`"} {
			if !strings.Contains(l.out, want) {
				t.Errorf("the refusal does not show %q:\n%s", want, l.out)
			}
		}
	}
	// notAsked is the launch that went on with no terminal, having asked nothing about the
	// broker's scope, and said the broker grants nothing.
	notAsked := func(t *testing.T, l brokerLaunch) {
		t.Helper()
		if l.rc != 0 || !l.reached {
			t.Fatalf("a launch with no terminal and no read grant was stopped: rc %d, reached=%v\n%s",
				l.rc, l.reached, l.out)
		}
		for _, bad := range []string{"repository scope", `+ o/r`, "--accept-config-changes"} {
			if strings.Contains(l.out, bad) {
				t.Errorf("a broker with no read grant was put to the gate (%q):\n%s", bad, l.out)
			}
		}
		if !strings.Contains(l.out, "github-broker: grants nothing in this workspace") {
			t.Errorf("the launch does not say the broker grants nothing:\n%s", l.out)
		}
	}
	t.Run("no grant", func(t *testing.T) {
		_, _, run := launchWithGitHub(t, "")
		notAsked(t, run())
	})
	t.Run("the user config's read grant", func(t *testing.T) {
		_, _, run := launchWithGitHub(t, `,
  "brokered": {"github": {"grants": [{"kind": "read", "repo": "*"}]}}`)
		l := run()
		asked(t, l)
		if !strings.Contains(l.out, "+ grant read *  added (from your user config, for every workspace)") {
			t.Errorf("the gate does not show the user config's read grant:\n%s", l.out)
		}
	})
	for _, v := range []string{"true", "false"} {
		t.Run("an old per-workspace switch, "+v, func(t *testing.T) {
			home, ws, run := launchWithGitHub(t, "")
			writeWorkspaceSwitch(t, home, ws, `{"workspace": "`+ws+`", "loopholes": {"github-broker": {"enabled": `+v+`}}}`)
			l := run()
			notAsked(t, l)
			for _, want := range []string{"loopholes.github-broker.enabled", "is ignored", "`yolo allow github read`",
				"`yolo loopholes disable github-broker --workspace " + shquote.QuoteDisplay(ws) + "`"} {
				if !strings.Contains(l.out, want) {
					t.Errorf("the launch does not note the ignored switch with %q:\n%s", want, l.out)
				}
			}
		})
	}
	t.Run("switched in the user config", func(t *testing.T) {
		_, _, run := launchWithGitHub(t, `,
  "loopholes": {"github-broker": {"enabled": true}}`)
		l := run()
		if l.rc == 0 || l.reached {
			t.Fatalf("a user-config switch of a brokered loophole launched: rc %d\n%s", l.rc, l.out)
		}
		if !strings.Contains(l.out, "has no switch") || !strings.Contains(l.out, "`yolo allow github read`") {
			t.Errorf("the refusal does not name the grant that gives access:\n%s", l.out)
		}
	})
	// Every name the loader reads a workspace config under: the `.json` spellings are read
	// when no `.jsonc` of the name exists, and were once merged without being refused.
	for _, name := range []string{"yolo-jail.jsonc", "yolo-jail.local.jsonc", "yolo-jail.json", "yolo-jail.local.json"} {
		t.Run("switched in "+name, func(t *testing.T) {
			_, ws, run := launchWithGitHub(t, "")
			if err := os.WriteFile(filepath.Join(ws, name),
				[]byte(`{"loopholes": {"github-broker": {"enabled": true}}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			l := run()
			if l.rc == 0 || l.reached {
				t.Fatalf("a workspace switch of a brokered loophole launched: rc %d\n%s", l.rc, l.out)
			}
			if !strings.Contains(l.out, "never "+filepath.Join(ws, name)+", a file an agent can edit") ||
				!strings.Contains(l.out, "`yolo allow github read`") {
				t.Errorf("the refusal does not name the file and the grant:\n%s", l.out)
			}
		})
	}
	t.Run("an install in yolo-jail.local.json", func(t *testing.T) {
		_, ws, run := launchWithGitHub(t, "")
		marker := filepath.Join(t.TempDir(), "ran")
		if err := os.WriteFile(filepath.Join(ws, "yolo-jail.local.json"),
			[]byte(`{"loopholes": {"pwn": {"command": ["/bin/sh", "-c", "touch `+marker+`"]}}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		l := run()
		if l.rc == 0 || l.reached {
			t.Fatalf("a workspace install launched: rc %d\n%s", l.rc, l.out)
		}
		if !strings.Contains(l.out, "installing is user-scope only") ||
			!strings.Contains(l.out, filepath.Join(ws, "yolo-jail.local.json")+" is agent-editable") {
			t.Errorf("the refusal does not say the install is user-scope only, naming the file:\n%s", l.out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("the workspace's host command ran")
		}
	})
}
