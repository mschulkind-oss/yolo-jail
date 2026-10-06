package cli

// stopmacosuser_test.go pins `yolo stop` at macos-user (docs/design/jail-lifetime-last-session-wins.md
// JL-D44): the backend has no container, and the stop still has something to end, the workspace's
// macos-user keeper and its sessions, which run.StopMacosUser ends (internal/cli/run's
// macosuserkeeper_test.go drives that half). Here: the CLI hands a macos-user stop to it, with the
// workspace, and asks no container runtime anything.

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Deleting stopJail's call of stopMacosUser fails this.
func TestAMacosUserStopEndsTheWorkspacesKeeperAndSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var got string
	orig := stopMacosUser
	stopMacosUser = func(stdout, _ io.Writer, ws string) int {
		got = ws
		fmt.Fprintln(stdout, "ended the macos-user sessions")
		return 3
	}
	t.Cleanup(func() { stopMacosUser = orig })
	var out, errOut bytes.Buffer
	s := &stopRun{}
	if rc := stopJail(&out, &errOut, "/ws", "macos-user", s.run, nil); rc != 3 {
		t.Fatalf("stopJail at macos-user = %d, want the macos-user stop's own status (3)\n%s%s", rc, out.String(), errOut.String())
	}
	if got != "/ws" {
		t.Errorf("the macos-user stop was handed %q, want the workspace /ws", got)
	}
	if len(s.calls) != 0 {
		t.Errorf("a macos-user stop asked a container runtime: %v", s.calls)
	}
	if !strings.Contains(out.String(), "ended the macos-user sessions") {
		t.Errorf("the stop's output is not the macos-user stop's:\n%s", out.String())
	}
}
