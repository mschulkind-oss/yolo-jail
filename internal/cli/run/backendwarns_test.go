package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
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
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	got := stdout.String() + stderr.String()
	// This asserted "briefings and skills are NOT delivered" until 2026-09-03, when
	// they started being delivered (by copy rather than by mount). The claim that
	// survives is about the DIFFERENCE from every other backend, not about absence:
	// the copy is writable where a bind is `:ro`, and the one machine-wide home means
	// a concurrent second workspace replaces what this one delivered.
	if !strings.Contains(got, "delivered by COPY on macos-user") {
		t.Errorf("a macos-user launch did not say how content is delivered here.\n"+
			"Every other backend mounts it read-only; this one copies into a home it "+
			"shares with every other workspace, and both differences change what the "+
			"agent can rely on.\noutput:\n%s", got)
	}
	// And it must not still claim the gap it no longer has: a warning describing a
	// closed gap teaches the reader to distrust the ones that are still true.
	if strings.Contains(got, "NOT delivered") {
		t.Errorf("the launch still reports skills/briefings as undelivered:\n%s", got)
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

// The other half of the same silence, found by the release-notes pass rather than by
// the sweep: the two channels that carry HOST BYTES into a config surface both fail
// open on macos-user, so the user's own file is replaced by a default and nothing says
// which one they got.
//
//   - a pack's `reads-host` grant is read from its /ctx mount (hostSurfaceBytes), and
//     that read is deliberately fail-open — an absent mount means "the user never
//     configured this tool", which must not refuse a launch. On a backend with no /ctx
//     at all, every grant takes that path.
//   - a source-bearing `host_files` entry is FILTERED OUT of the wire before the
//     bootstrap sees it (runplan.go), which is the more honest of the two — it renders
//     nothing rather than a default — but is equally quiet.
//
// Neither is fixed here and neither can be cheaply: both need a delivery mechanism this
// backend does not have. The requirement is only that a user who declared the file
// learns their file was not used, since the failure otherwise looks like the tool
// ignoring its own config.
func TestMacosUserNotesHostByteGaps(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The claude pack declares `reads-host .claude/settings.json`; the config adds a
	// source-bearing host_files entry. Both grants exist on paper, neither can arrive.
	body := `{"packs": ["claude"], "host_files": [{"path": ".npmrc", "source": "~/.npmrc"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, t.TempDir(), "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	got := stdout.String() + stderr.String()

	if !strings.Contains(got, "reads-host") {
		t.Errorf("a pack's reads-host grant cannot cross on macos-user and the launch did not "+
			"say so — the surface renders from its DEFAULTS layer, so the agent runs on a "+
			"config the user did not write.\noutput:\n%s", got)
	}
	if !strings.Contains(got, ".npmrc") {
		t.Errorf("a source-bearing host_files entry is dropped from the wire on macos-user and "+
			"the launch did not name it. The user declared a file by path; the jail has no "+
			"file at that path and no reason given.\noutput:\n%s", got)
	}
}

// mise_tools had the same defect as lsp_servers and none of the same warning: the
// shims dir is on the sandbox PATH, so it LOOKS provisioned, while nothing provides
// a `mise` binary the sandbox can reach and nothing runs `mise install` (that step
// is in the CONTAINER command wrapper). A config declaring tools got silence and a
// jail without them.
//
// Verified on a Mac 2026-09-04: no mise data dir in the sandbox home, no mise on any
// path it can read.
func TestMacosUserWarnsAboutMiseTools(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"mise_tools": {"neovim": "nightly"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	// A workspace config that did not exist before is a CHANGE, and this arm gates on
	// approval with no terminal to prompt on. The flag is the non-interactive grant.
	o.AcceptConfigChanges = true
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string,
		bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}

	got := stdout.String() + stderr.String()
	if !strings.Contains(got, "mise_tools are NOT installed on macos-user") {
		t.Errorf("a config declaring mise tools was told nothing:\n%s", got)
	}
	if !strings.Contains(got, "neovim") {
		t.Errorf("the warning does not name the tools that will be missing:\n%s", got)
	}
	// It must point at the mechanism that DOES work here, or the user is left with a
	// problem and no route out.
	if !strings.Contains(got, "packages:") {
		t.Errorf("the warning does not name the working alternative:\n%s", got)
	}
}
