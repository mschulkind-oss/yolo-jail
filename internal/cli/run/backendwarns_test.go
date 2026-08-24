package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// EXPLICIT host networking on Apple Container is worse than the default and used to say
// nothing: no --net is emitted, AND both port keys are bridge-gated, so asking for host
// mode also drops every published port. Warned now.
func TestAppleContainerWarnsOnExplicitHostNetworking(t *testing.T) {
	if got := acNetOutput(t, "host"); !strings.Contains(got, "network.mode") {
		t.Errorf("no warning for explicit host networking on Apple Container:\n%s", got)
	}
}

// bridge is GENUINELY honored on that backend (-p ungated, forward_host_ports via
// --publish-socket, its own vmnet netns), so warning on it would be noise on every
// launch. This is the half that keeps the warning worth reading.
func TestAppleContainerSilentOnBridge(t *testing.T) {
	if got := acNetOutput(t, "bridge"); strings.Contains(got, "network.mode") {
		t.Errorf("bridge is honored on Apple Container and must not warn:\n%s", got)
	}
}

func acNetOutput(t *testing.T, mode string) string {
	t.Helper()
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	o := goldenOptions(ws, home)
	o.IsMacOS = true
	o.IsLinux = false
	var out bytes.Buffer
	o.Stdout = &out

	net := jsonx.NewOrderedMap()
	net.Set("mode", mode)
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	cfg := newConfig("security", sec)
	cfg.Set("network", net)

	o.assembleRunCmd(&assembleInput{
		cfg: cfg, rt: "container", cname: "yolo-ws-abcd1234",
		packs: claudePackFixture(t), agentsPath: ws,
		wsState: ws, miseStore: "/mise-store", yoloVersion: "9.9.9-test",
		mountTargets: map[string]struct{}{},
	})
	return out.String()
}

// The macos-user tier collapse is #39's mirror image: that backend's home is a constant,
// so every pack `state` dir at scope:workspace is shared by every workspace. Warned,
// not fixed — splitting the home would break the machine tier to repair the workspace
// tier, since the single home IS the shared-credentials mechanism there.
func TestMacosUserNotesMachineWideWorkspaceState(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, bool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	got := stdout.String() + stderr.String()
	if !strings.Contains(got, "shared across ALL workspaces") || !strings.Contains(got, ".claude") {
		t.Errorf("a macos-user launch did not say that pack state dirs are machine-wide.\n"+
			"The sandbox denies reading a sibling workspace's files and then leaks the same\n"+
			"content through ~/.claude/projects/<other>/*.jsonl, which is worth one line.\n"+
			"output:\n%s", got)
	}
}
