package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
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
	// Launch notices go to stderr (the jailed command owns stdout), so that is
	// where the warning this pair pins is captured.
	var out bytes.Buffer
	o.Stderr = &out

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

// The macos-user tier collapse was #39's mirror image, and it is FIXED: every pack `state`
// dir at scope:workspace is a symlink into <workspace>/.yolo/home now
// (entrypoint.InstallDarwinHomeLayout, pinned in internal/entrypoint against a real boot).
//
// So what a launch must no longer say is that those dirs are machine-wide. This is the
// negative half of the rule the content-gaps test below states positively: a warning that
// describes a closed gap teaches the reader to distrust the ones that are still true, and
// this one would be read by someone deciding whether to keep two projects apart.
func TestMacosUserNoLongerClaimsMachineWideWorkspaceState(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	got := stdout.String() + stderr.String()
	if strings.Contains(got, "shared across ALL workspaces") {
		t.Errorf("a macos-user launch still reports pack state dirs as machine-wide, which "+
			"they have not been since the home-tier layout:\n%s", got)
	}
}

// The content pipeline that reached macos-user last: skills+briefings, composed host-side
// and delivered by MOUNTING everywhere else, which this backend cannot do. (This comment
// also named lsp_servers binaries, "installed by a bootstrap script it deliberately does
// not run"; that gap closed on 2026-09-12, and since 2026-09-25 no backend installs a
// language server at all — docs/reference/mcp-configuration.md#oq-lsp1.)
func TestMacosUserNotesContentGaps(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstderr:\n%s", rc, stderr.String())
	}
	got := stdout.String() + stderr.String()
	// This asserted "briefings and skills are NOT delivered" until 2026-09-03, when
	// they started being delivered (by copy rather than by mount). The claim that
	// survives is about the DIFFERENCE from every other backend, not about absence:
	// the copy is writable where a bind is `:ro`. Its second half — a concurrent second
	// workspace replacing what this one delivered — went with the home-tier layout, which
	// gave the destination a per-workspace one.
	if !strings.Contains(got, "delivered by COPY on macos-user") {
		t.Errorf("a macos-user launch did not say how content is delivered here.\n"+
			"Every other backend mounts it read-only; this one copies, so the agent can "+
			"edit what it was given, which changes what it can rely on.\noutput:\n%s", got)
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

// ⚠ TestMacosUserNotesHostByteGaps STOOD HERE AND IS GONE (2026-09-13), because the two
// gaps it asserted are CLOSED rather than because it became inconvenient. It required the
// launch to say that a pack `reads-host` grant "cannot cross on macos-user" and that a
// source-bearing `host_files` entry "is dropped from the wire": both were true while the
// bytes crossed on a /ctx mount this backend does not have, and both are false now that
// they cross by COPY into a root-owned tree (DP-L1, macosctxtree.go).
//
// Its replacement is not a deletion. TestMacosUserLaunchNoLongerWarnsThatHostBytesCannotCross
// is the same requirement inverted, and it sits beside the two tests that read the bytes
// out of the composed tree — because absence of a warning is not evidence of a feature,
// which is the rule TestMacosUserNoLongerWarnsThatToolsAreUninstallable below already
// states and the reason all three live together.

// mise_tools and lsp_servers BOTH warned that they install nothing on macos-user, and
// both gaps are CLOSED as of 2026-09-12: the floor puts mise and node on the sandbox
// PATH, and the confined provisioning stage runs `mise install` and the generated
// bootstrap script before the agent starts (docs/design/macos-user-provisioning.md).
//
// So the requirement inverted, and this test inverted with it — the same move
// TestMacosUserNoLongerClaimsMachineWideWorkspaceState made, for the same reason: a
// warning describing a closed gap teaches the reader to distrust the ones still true,
// and these two named a specific mechanism ("nothing runs `mise install`") that a reader
// would act on by rewriting their config around a limitation that is gone.
//
// ⚠ THIS IS THE NEGATIVE HALF ONLY. That the stage actually runs is pinned where it can
// be observed rather than inferred from silence — macosuser.TestProvisioningStageRuns…,
// which fails if the orchestrator's call site is deleted. Absence of a warning is not
// evidence of a feature.
//
// The lsp_servers half now stays retired for a different reason: since the LSP recipe
// table's deletion (docs/reference/mcp-configuration.md#oq-lsp1) NO backend installs a
// language server — the user brings the binary — so "the binaries are not installed" is
// the ruled design everywhere rather than a gap of this backend's, and a warning here
// would single out macos-user for a property every backend shares.
func TestMacosUserNoLongerWarnsThatToolsAreUninstallable(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"),
		[]byte(`{"mise_tools": {"neovim": "nightly"}, "lsp_servers": {"pyright": `+
			`{"command": "pyright-langserver --stdio", "fileExtensions": {"py": "python"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	// A workspace config that did not exist before is a CHANGE, and this arm gates on
	// approval with no terminal to prompt on. The flag is the non-interactive grant.
	o.AcceptConfigChanges = true
	o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, string,
		macosuser.HostContext,
		bool, *jsonx.OrderedMap, []packload.BlockedTool) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	}

	got := stdout.String() + stderr.String()
	if strings.Contains(got, "mise_tools are NOT installed on macos-user") {
		t.Errorf("the launch still says mise_tools are not installed here, but the "+
			"provisioning stage runs `mise install` before the agent:\n%s", got)
	}
	if strings.Contains(got, "lsp_servers CONFIG renders but the binaries are not installed") {
		t.Errorf("the launch still says lsp_servers never install here, but no backend "+
			"installs a language server; the user brings the binary:\n%s", got)
	}
}
