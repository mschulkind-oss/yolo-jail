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

// The two content pipelines that never reach macos-user: skills+briefings (composed
// host-side and delivered by MOUNTING, which this backend cannot do) and lsp_servers
// binaries (installed by a bootstrap script it deliberately does not run). Both warn
// rather than being fixed here — the first needs a delivery mechanism, the second needs
// a node precondition — but neither may be silent, because in both cases the agent is
// told the capability exists.
func TestMacosUserNotesContentGaps(t *testing.T) {
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
	if !strings.Contains(got, "briefings and skills are NOT delivered") {
		t.Errorf("a macos-user launch did not say the agent gets no briefing or skills.\n"+
			"The blocked-tool shims ARE generated on this backend, so a blocked command exits "+
			"127 with nothing explaining it.\noutput:\n%s", got)
	}
}

// Config-declared loopholes (loopholes.<name>.command) were invisible to the inert
// report, which walked packs only — so a user whose own config named a daemon got no
// line at all on a backend that starts none. Reporting one source and not the other made
// the silence look deliberate.
func TestConfigDeclaredLoopholesAreReportedInert(t *testing.T) {
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	entry.Set("command", []any{"/bin/true"})
	lp := jsonx.NewOrderedMap()
	lp.Set("acme-proxy", entry)

	var errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.notePackLoopholesInert("container", nil, newConfig("loopholes", lp))

	if got := errBuf.String(); !strings.Contains(got, "acme-proxy") {
		t.Errorf("a config-declared loophole is not reported inert on Apple Container:\n%s", got)
	}
}
