package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// RENDER-MARK PARITY ON macos-user, ASKED OF A REAL SANDBOX
// (docs/design/notch-scoped-config-contributions.md §4.3, NS-D3;
// docs/plans/handoff-guest-notch-macos.md §4).
//
// THE RULE. A `readsHost` host file yolo has already rendered into the home is yolo's own
// output, not the user's, so a jail must compose that surface WITHOUT it: a key yolo wrote must
// not come back as the user's (OQ-CR6). The launcher decides it from the host-render mark
// (run.hostLayerIsRender → entrypoint.HostSurfaceRendered), and since `499a332f` the macos-user
// arm decides it with the same call (buildMacosCtxTree) and carries the label on the plan's
// wire. Unit tests drive a real Run() of that arm and read the wire back through the boot's
// reader; no Mac has booted on it.
//
// WHAT THIS ASKS. Two launches with the claude pack, each in a fresh workspace, the user's own
// ~/.claude/settings.json carrying a per-run key:
//
//   - the CONTROL, with no mark: the key must reach the sandbox's settings.json, which is
//     TestMacosUserDeliversHostBytesByCopy's assertion repeated so this test cannot pass by the
//     host layer not crossing at all;
//   - the MEASUREMENT, with this home's own host-render mark for claude/settings planted: the
//     sandbox's settings.json must still be rendered and must LACK the key.
//
// ⚠ NO LINE IS ASSERTED. The handoff row expected the boot log to say "baseline and not a
// layer". That note is written through Env.note, which writes only to Env.LogOnly, and when
// this test was written the macos-user bootstrap set no LogOnly, so the note was discarded.
// The bootstrap now keeps the container's boot log, <workspace>/.yolo/boot.log
// (entrypoint.attachDarwinBootLog; TestMacosUserBootstrapKeepsABootLog), so the note does land
// there. This test still asks only the bytes, which answer the rule itself; the line is
// commentary on it.
//
// THE MARK IS PLANTED IN A PRIVATE STATE DIR, never a shared one: privateHostProvenance first
// replaces the isolated home's link to the shared state dir (this run's own, or the machine's)
// with a private directory, so the mark written here is this test's alone and no other
// launch's render history is read or touched.
func TestMacosUserComposesARenderedHostFileAsABaseline(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["claude"]}`)
	home := os.Getenv("HOME")
	privateHostProvenance(t, home)
	if entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Fatal("this home already carries a host-render mark for claude/settings before the " +
			"test planted one, so the control below could not compose the user's layer")
	}
	nonce := acParityNonce()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"yoloItRenderMarkProbe": "`+nonce+`"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := strings.Join([]string{
		`echo "=== SETTINGS ==="; cat ~/.claude/settings.json 2>&1`,
		`echo "=== END ==="`,
	}, "\n")

	control := section(macosUserRunProbe(t, "render-mark control", macosUserWorkspace(t, `{}`), probe).stdout,
		"=== SETTINGS ===", "=== END ===")
	if !strings.Contains(control, nonce) {
		t.Fatalf("with NO render mark the sandbox's settings.json lacks the user's key, so the "+
			"host layer did not cross at all and the marked launch below would say nothing about "+
			"the mark (TestMacosUserDeliversHostBytesByCopy is the check for this half):\n%s", control)
	}

	plantHostRenderMark(t, home, "claude", "settings")
	if !entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Fatal("the planted mark does not read as rendered through entrypoint.HostSurfaceRendered, " +
			"the predicate the launcher asks, so nothing would be measured")
	}
	marked := section(macosUserRunProbe(t, "render-mark", macosUserWorkspace(t, `{}`), probe).stdout,
		"=== SETTINGS ===", "=== END ===")
	if !strings.Contains(marked, "{") {
		t.Fatalf("with the mark planted the sandbox has no rendered ~/.claude/settings.json at "+
			"all, so the key's absence below would prove nothing:\n%s", marked)
	}
	if strings.Contains(marked, nonce) {
		t.Errorf("the sandbox composed a host settings.json yolo has ALREADY RENDERED as the "+
			"user's layer: the per-run key from the host file reached it. Render-mark parity "+
			"(NS-D3) says that file is a baseline on this backend as on the container backends; "+
			"read buildMacosCtxTree's hostLayerIsRender call and the plan's HostLayerWire.\n%s", marked)
	}
}

// plantHostRenderMark writes an empty provenance record for agent/name under home's state dir:
// what a host render leaves, and all entrypoint.HostSurfaceRendered asks about (an empty record
// is a render that attributed no keys, as writeProvenanceRecord in internal/entrypoint/prism.go
// says). The path comes from hostRenderMarkPath, which refuses a state dir that is still a link.
func plantHostRenderMark(t *testing.T, home, agent, name string) {
	t.Helper()
	mark, err := hostRenderMarkPath(home, agent, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(mark), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// hostRenderMarkPath is where home's host-render mark for agent/name lives, or an error when
// home's state dir is still a LINK: isolateHome links it to a shared state dir (this run's own,
// or the machine's), and a mark written through that link would rewrite that dir's render history.
func hostRenderMarkPath(home, agent, name string) (string, error) {
	store := paths.GlobalStorageUnder(home)
	if fi, err := os.Lstat(store); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is still a link to another state dir; planting a mark there "+
			"would rewrite that dir's render history. Call privateHostProvenance first", store)
	}
	mark := render.Host(home, nil, render.OwnershipUnstated).ProvenancePath(agent, name)
	if mark == "" || !isUnder(mark, store) {
		return "", fmt.Errorf("the provenance path %q is not under this home's state dir %q", mark, store)
	}
	return mark, nil
}

// TestPlantHostRenderMarkReadsAsRendered is the -short check of the two helpers: the mark
// plantHostRenderMark writes into a private state dir is one the launcher's predicate reads as
// a render, and a state dir that links elsewhere is refused rather than written through.
func TestPlantHostRenderMarkReadsAsRendered(t *testing.T) {
	home := resolvedTempDir(t)
	if entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Fatal("precondition: a fresh home reads as rendered")
	}
	plantHostRenderMark(t, home, "claude", "settings")
	if !entrypoint.HostSurfaceRendered(home, claudeSettingsSurface) {
		t.Error("the planted mark does not read as rendered, so the macos-user test would " +
			"measure an unmarked launch twice")
	}

	machine := resolvedTempDir(t)
	linked := resolvedTempDir(t)
	store := paths.GlobalStorageUnder(linked)
	for _, d := range []string{filepath.Dir(store), paths.GlobalStorageUnder(machine)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(paths.GlobalStorageUnder(machine), store); err != nil {
		t.Fatal(err)
	}
	if _, err := hostRenderMarkPath(linked, "claude", "settings"); err == nil {
		t.Error("hostRenderMarkPath accepted a state dir that links to another home's, so a " +
			"planted mark would land in that home's render history")
	}
}
