package cli

// hosttrees_test.go pins a PATCHED EXTENSION at the host notch (docs/design/patched-extensions.md
// §8.3, §11; PPX-D11, PPX-D18): the render links `~/<into>` to a versioned copy of the good build in
// the host-private trees directory, through an owned link swapped by rename, keeps the previous
// version until the next move, never takes a path the user owns, and says on a macOS host that it
// builds none; `yolo host apply` advances before the render in its acting posture only; `yolo host
// -- <bin>` advances only for its owning agent's programs; and the owning agent does not start at
// the host without the tree it loads.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// listTreeForAgent adds an agent pack declaring `tool` and its `tool/settings` surface to the
// fixture's selection, and the tree's list entry on it, so the agent pack owns the tree.
func (fx *treeFixture) listTreeForAgent(t *testing.T) {
	t.Helper()
	fx.listTreeForAgentWith(t, `{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/.tool/ext/tool-ext"]}`)
}

// listTreeForAgentWith is listTreeForAgent with entry, a contribution naming the tree, in place of
// the config-list.
func (fx *treeFixture) listTreeForAgentWith(t *testing.T, entry string) {
	t.Helper()
	agent := filepath.Join(fx.packs, "agentpack")
	writeFile(t, filepath.Join(agent, "pack.json"), `{"name":"agentpack","contributes":[`+
		`{"kind":"program","bin":"tool","via":"npm","package":"tool"},`+
		`{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}]}`)
	data, err := os.ReadFile(filepath.Join(fx.treeDir, "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := strings.Replace(string(data), `"produces":["f.txt"]}]}`, `"produces":["f.txt"]},`+entry+`]}`, 1)
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), manifest)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+agent+`","name":"agentpack"},{"source":"file://`+fx.treeDir+`","name":"treepack"}]}`)
}

// renderTrees runs the `files` render of the fixture's tree pack, in the posture asked for.
func (fx *treeFixture) renderTrees(t *testing.T, write bool) string {
	t.Helper()
	sel := selectConfiguredHostPacks()
	var p *packload.Pack
	for _, q := range sel.packs {
		if q.Name == "treepack" {
			p = q
		}
	}
	if p == nil {
		t.Fatal("no treepack in the selection")
	}
	var out bytes.Buffer
	applyHostFiles(richtext.Printer{W: &out}, &out, p, sel.packs, fx.home, "stamp", write, &hostApplySurvey{})
	return out.String()
}

func (fx *treeFixture) link() string { return filepath.Join(fx.home, ".tool", "ext", "tool-ext") }

// THE HOST RENDER (PPX-D11): `~/<into>` becomes a link the render owns, naming the good build's
// versioned copy in the host-private directory; an observe pass after it changes nothing; a move
// swaps the link and keeps the previous version, and the next move removes the one before it.
func TestTheHostRenderLinksTheGoodBuildAndKeepsThePreviousVersion(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	advanceHostTrees(io.Discard, false, "", nil)
	if out := fx.renderTrees(t, false); !strings.Contains(out, "would render") {
		t.Fatalf("an observe pass over a home with no link does not say it would render:\n%s", out)
	}
	if _, err := os.Lstat(fx.link()); err == nil {
		t.Fatal("the observe pass wrote the link")
	}
	out := fx.renderTrees(t, true)
	first, err := os.Readlink(fx.link())
	if err != nil || !strings.HasPrefix(first, paths.HostTreesDir()+string(filepath.Separator)) {
		t.Fatalf("~/.tool/ext/tool-ext = %q (%v), want a link into %s\n%s", first, err, paths.HostTreesDir(), out)
	}
	if got, err := os.ReadFile(filepath.Join(fx.link(), "f.txt")); err != nil || string(got) != lines30(map[int]string{10: "ten", 12: "twelve"}) {
		t.Errorf("the tree through the link reads %q (%v)", got, err)
	}
	man, _ := hostskills.LoadManifest(hostSkillsManifestPath())
	if !man.OwnedBy(fx.link(), "treepack") {
		t.Error("the link is not in the files ownership record")
	}
	if info, err := os.Stat(paths.HostTreesDir()); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the host trees directory is %v (%v), want 0700", info.Mode(), err)
	}
	if out := fx.renderTrees(t, false); !strings.Contains(out, "unchanged") && strings.Contains(out, "would render") {
		t.Errorf("an observe pass after the render would change it:\n%s", out)
	}
	// A MOVE swaps the link and keeps the version it named.
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	advanceHostTrees(io.Discard, false, "", nil)
	fx.renderTrees(t, true)
	second, _ := os.Readlink(fx.link())
	if second == first || !isDir(first) || !isDir(second) {
		t.Fatalf("after a move the link names %q, and the previous version %q is kept: %v", second, first, isDir(first))
	}
	// THE NEXT MOVE removes the one before the previous.
	fx.commit(t, "v1.2.0", map[int]string{14: "fourteen", 16: "sixteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	advanceHostTrees(io.Discard, false, "", nil)
	fx.renderTrees(t, true)
	third, _ := os.Readlink(fx.link())
	if third == second || isDir(first) || !isDir(second) {
		t.Errorf("after the second move: link %q, oldest kept %v, previous kept %v", third, isDir(first), isDir(second))
	}
}

// A PATH THE USER OWNS at `~/<into>` is never taken.
func TestTheHostRenderNeverTakesAUsersPath(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	advanceHostTrees(io.Discard, false, "", nil)
	if err := os.MkdirAll(fx.link(), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := fx.renderTrees(t, true); !strings.Contains(out, "yolo has no record of writing it") {
		t.Errorf("the render did not refuse the user's directory:\n%s", out)
	}
	if info, err := os.Lstat(fx.link()); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Error("the user's directory was replaced")
	}
}

// A MACOS HOST builds no tree, and the render says so naming a jail that has it (§11).
func TestAMacOSHostRendersNoTreeAndNamesAJail(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	prev := hostTreesBuild
	hostTreesBuild = func() bool { return false }
	t.Cleanup(func() { hostTreesBuild = prev })
	advanceHostTrees(io.Discard, false, "", nil)
	if len(fx.builds) != 0 {
		t.Error("a macOS host advanced a patched extension")
	}
	if out := fx.renderTrees(t, true); !strings.Contains(out, "YOLO_RUNTIME=podman yolo --") {
		t.Errorf("the macOS render does not name a jail:\n%s", out)
	}
}

// THE VERB'S POSTURE: `yolo host apply` checks and builds nothing in its dry run, and advances
// before the render with --assert. Red if hostApply stops calling advanceHostTrees.
func TestHostApplyAdvancesInItsActingPostureOnly(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	var errw bytes.Buffer
	hostApply(nil, io.Discard, &errw, false, nil)
	if len(fx.builds) != 0 {
		t.Fatalf("the dry run built the tree:\n%s", errw.String())
	}
	errw.Reset()
	hostApply([]string{"--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	if len(fx.builds) != 1 {
		t.Fatalf("--assert built %d trees:\n%s", len(fx.builds), errw.String())
	}
	if target, err := os.Readlink(fx.link()); err != nil || !isDir(target) {
		t.Errorf("--assert did not render the link (%q, %v)", target, err)
	}
}

// `yolo host -- <bin>` ADVANCES ONLY FOR THE OWNING AGENT'S PROGRAMS: a launch of any other bin
// waits on no extension's fetch or build.
func TestAHostLaunchAdvancesOnlyItsOwnersTrees(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	advanceHostTrees(io.Discard, false, "claude", nil)
	if len(fx.builds) != 0 {
		t.Fatal("`yolo host -- claude` advanced a tree its owner, agentpack, loads")
	}
	advanceHostTrees(io.Discard, false, "tool", nil)
	if len(fx.builds) != 1 {
		t.Errorf("`yolo host -- tool` built %d trees, want the one its owner loads", len(fx.builds))
	}
}

// PPX-D18 AT THE HOST: the owning agent does not start while `~/<into>` names no build, says why,
// and starts once the render put the link there; every other program starts regardless.
func TestTheOwningAgentStopsAtTheHostWithoutItsTree(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	var errw bytes.Buffer
	if hostTreeGate(&errw, "tool", fx.home) {
		t.Fatal("the owner started with no build of the tree it loads")
	}
	for _, w := range []string{"refusing to launch tool", "extension treepack/tool-ext", "yolo host apply --assert"} {
		if !strings.Contains(errw.String(), w) {
			t.Errorf("the stop does not name %q:\n%s", w, errw.String())
		}
	}
	if !hostTreeGate(io.Discard, "claude", fx.home) {
		t.Error("another program was stopped for the owner's tree")
	}
	advanceHostTrees(io.Discard, false, "tool", nil)
	fx.renderTrees(t, true)
	if !hostTreeGate(io.Discard, "tool", fx.home) {
		t.Error("the owner was stopped with the tree rendered")
	}
}

// writeHostConfig rewrites the fixture's user config, with the agent pack left out of the host
// floor (so no launch installs `tool`) and extra members after it.
func (fx *treeFixture) writeHostConfig(t *testing.T, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+filepath.Join(fx.packs, "agentpack")+`","name":"agentpack"},`+
		`{"source":"file://`+fx.treeDir+`","name":"treepack"}],"host_floor":{"agentpack":false}`+extra+`}`)
}

// THE STOP'S CALL SITE: `yolo host -- tool` refuses before exec while the tree its pack loads is not
// rendered. Red if hostExec stops calling hostTreeGate.
func TestAHostLaunchOfTheOwnerStopsWithoutItsTree(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, "")
	stubBins(t, "tool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 1 || got.execed {
		t.Fatalf("rc=%d, execed %v: the owner launched without its tree\n%s", rc, got.execed, errw.String())
	}
	if !strings.Contains(errw.String(), "extension treepack/tool-ext") {
		t.Errorf("the refusal does not name the extension:\n%s", errw.String())
	}
}

// THE ADVANCE'S CALL SITE: under host_apply_on_launch, `yolo host -- tool` builds the tree its pack
// loads before the gate, the gate's apply renders it, and tool starts; `yolo host -- other` builds
// nothing. Red if hostExec stops calling advanceHostTrees.
func TestAHostLaunchAdvancesItsOwnersTreeBeforeTheGate(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, `,"host_apply_on_launch":true`)
	stubBins(t, "tool", "other")
	got := captureHostExec(t)
	var errw bytes.Buffer
	hostExec(nil, []string{"other"}, io.Discard, &errw, nil)
	if len(fx.builds) != 0 {
		t.Fatalf("`yolo host -- other` built a tree it does not load:\n%s", errw.String())
	}
	errw.Reset()
	rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil)
	if len(fx.builds) != 1 || rc != 0 || !got.execed {
		t.Fatalf("`yolo host -- tool`: %d builds, rc=%d, execed %v\n%s", len(fx.builds), rc, got.execed, errw.String())
	}
	if !isDir(fx.link()) {
		t.Error("the gate's apply did not render the tree's link")
	}
}

// A LAUNCH THE CAPABILITY GATE REFUSES BUILDS NOTHING (OQ-CAP2 before PPX-D11): the gate runs ahead
// of the advance, as it runs ahead of the render gate, so `yolo host -- tool` under a
// `required_capabilities` nothing satisfies stops without fetching or building the tree its pack
// loads. Red if the advance moves back above refuseHostUnmetCapabilities in hostLaunch.
func TestAHostLaunchTheCapabilityGateRefusesBuildsNoTree(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, `,"host_apply_on_launch":true,"required_capabilities":["web_search"]`)
	t.Setenv(config.AllowUnmetCapabilitiesEnv, "")
	stubBins(t, "tool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil)
	if rc != 1 || got.execed {
		t.Fatalf("rc=%d, execed %v: the capability gate did not refuse\n%s", rc, got.execed, errw.String())
	}
	if !strings.Contains(errw.String(), "declares 'web_search'") {
		t.Errorf("the refusal is not the capability gate's:\n%s", errw.String())
	}
	if len(fx.builds) != 0 {
		t.Errorf("a launch the capability gate refused built %d trees:\n%s", len(fx.builds), errw.String())
	}
}

// THE OTHER SPELLING, `yolo apply --at host`, advances in its acting posture too: one operation.
func TestApplyAtHostAdvancesInItsActingPostureOnly(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	var errw bytes.Buffer
	applyMain([]string{"--at", "host"}, io.Discard, &errw, false, nil)
	if len(fx.builds) != 0 {
		t.Fatalf("the dry run built the tree:\n%s", errw.String())
	}
	applyMain([]string{"--at", "host", "--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	if len(fx.builds) != 1 {
		t.Fatalf("--assert built %d trees:\n%s", len(fx.builds), errw.String())
	}
}

// A DROPPED EXTENSION's host copies go once nothing yolo recorded links into them, and stay while
// a recorded link still does.
func TestADroppedExtensionsHostCopiesAreSwept(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	advanceHostTrees(io.Discard, false, "", nil)
	fx.renderTrees(t, true)
	target, err := os.Readlink(fx.link())
	if err != nil {
		t.Fatal(err)
	}
	slugDir := filepath.Dir(target)
	sweepDroppedHostTrees(nil) // the selection no longer carries it, and its link is still there
	if !isDir(target) {
		t.Fatal("a copy a recorded link still names was swept")
	}
	if err := os.Remove(fx.link()); err != nil {
		t.Fatal(err)
	}
	sweepDroppedHostTrees(nil)
	if isDir(slugDir) {
		t.Error("a dropped extension's copies outlived its link")
	}
}

// THE SWEEP'S CALL SITE: an apply that retires a dropped pack's link removes its copies too. Red if
// the apply stops calling sweepDroppedHostTrees.
func TestAnApplyThatDropsTheExtensionRemovesItsHostCopies(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	var errw bytes.Buffer
	hostApply([]string{"--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	target, err := os.Readlink(fx.link())
	if err != nil {
		t.Fatalf("the first apply rendered no link: %v\n%s", err, errw.String())
	}
	other := filepath.Join(fx.packs, "otherpack")
	writeFile(t, filepath.Join(other, "pack.json"), `{"name":"otherpack","contributes":[]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+other+`","name":"otherpack"}]}`)
	var out bytes.Buffer
	hostApply([]string{"--assert"}, &out, &errw, false, strings.NewReader("y\n"))
	if _, err := os.Lstat(fx.link()); err == nil {
		t.Fatalf("the dropped pack's link was not retired:\n%s", out.String())
	}
	if isDir(filepath.Dir(target)) {
		t.Errorf("the dropped extension's host copies outlived its retired link:\n%s", out.String())
	}
}

// UNDER host_management: none the host renders nothing, so no link is ever there, and the owner is
// never stopped for one.
func TestTheHostStopIsOffUnderHostManagementNone(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, `,"host_management":"none"`)
	if !hostTreeGate(io.Discard, "tool", fx.home) {
		t.Error("under host_management none the owner was stopped for a tree the host never renders")
	}
}

// AT THE HOST THE ADVANCE'S LINES NAME THE HOST (PPX-D28), never "this jail": the wait line of a build
// a good build serves beside, and the line of a Ctrl-C that ends it, whose next step is the host's.
func TestAHostAdvancesLinesNameTheHost(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	advanceHostTrees(io.Discard, false, "", nil)
	if len(fx.builds) != 1 {
		t.Fatalf("the first host advance built %d trees", len(fx.builds))
	}
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	prev := forkBuildChild
	forkBuildChild = func(ctx context.Context, _ time.Duration, _ string, _ forkBuild, _, _ io.Writer, _ bool) (int, bool) {
		_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
			t.Error("the Ctrl-C did not reach the advance")
		}
		return 130, false
	}
	t.Cleanup(func() { forkBuildChild = prev })
	var errw syncBuffer
	advanceHostTrees(&errw, false, "", nil)
	out := errw.String()
	if strings.Contains(out, "this jail") {
		t.Errorf("a host advance's lines name a jail:\n%s", out)
	}
	for _, w := range []string{"a Ctrl-C keeps the host on the good build", "the advance was interrupted — the host keeps " +
		"the good build", "the next `yolo host apply --assert` tries again"} {
		if !strings.Contains(out, w) {
			t.Errorf("the host advance lacks %q:\n%s", w, out)
		}
	}
}

// THE GOOD-BUILD LINE'S CALL SITE (PPX-D26): `yolo host -- tool` names the build its link names,
// which is what the agent loads; once the machine's good build has moved past it with no render,
// the line names the linked build and that `yolo host apply --assert` renders the good one. Red if
// hostExec stops calling noteHostTreeLines.
func TestAHostLaunchNamesTheBuildItsLinkNames(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, `,"host_apply_on_launch":true`)
	stubBins(t, "tool")
	captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errw.String())
	}
	good := patchedRecordOf(t, treeKeyCLI).Good
	first := "yolo host: extension " + treeKeyCLI + " at " + run.GoodBuildLabel(good) + " + " + run.PatchCount(good.Patches)
	if !strings.Contains(errw.String(), first) {
		t.Errorf("the launch does not name the build its link names (%q):\n%s", first, errw.String())
	}
	// THE GOOD BUILD MOVES (a jail launch's advance), and the host's link stays where it was.
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	if d, out := fx.deliver(t, true); d.Dir == "" {
		t.Fatalf("the jail launch's advance did not move the good build:\n%s", out)
	}
	moved := patchedRecordOf(t, treeKeyCLI).Good
	fx.writeHostConfig(t, "")
	errw.Reset()
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errw.String())
	}
	if strings.Contains(errw.String(), "at "+run.GoodBuildLabel(moved)) ||
		!strings.Contains(errw.String(), run.GoodBuildLabel(moved)+" + "+run.PatchCount(moved.Patches)+" is built, and "+
			"`yolo host apply --assert` renders it") {
		t.Errorf("the line names the good build where the link names an earlier one:\n%s", errw.String())
	}
	// UNDER host_management: none yolo writes no link, and says nothing of one.
	fx.writeHostConfig(t, `,"host_management":"none"`)
	errw.Reset()
	hostExec(nil, []string{"tool"}, io.Discard, &errw, nil)
	if strings.Contains(errw.String(), "yolo host: extension") {
		t.Errorf("under host_management none the launch names an extension build:\n%s", errw.String())
	}
}

// UNDER host_management: none `yolo host -- tool` advances nothing even with host_apply_on_launch
// (PPX-D25): that contract renders nothing, so a build would serve no one. Red if hostExec's advance
// stops reading host_management.
func TestAHostLaunchUnderHostManagementNoneAdvancesNothing(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, `,"host_apply_on_launch":true,"host_management":"none"`)
	stubBins(t, "tool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d, execed %v\n%s", rc, got.execed, errw.String())
	}
	if len(fx.builds) != 0 {
		t.Errorf("under host_management none the launch built %d trees:\n%s", len(fx.builds), errw.String())
	}
}

// A HOST COPY WHOSE ENTRY IS REAPED WHILE IT RUNS (PPX-D25) is never renamed in or linked: the
// copy is checked against the entry's completion marker once it ends.
func TestAHostCopyWhoseEntryIsReapedIsNeverLinked(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	advanceHostTrees(io.Discard, false, "", nil)
	prev := treeCopied
	treeCopied = func(key string) {
		if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(key); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { treeCopied = prev })
	out := fx.renderTrees(t, true)
	if !strings.Contains(out, "was reaped while it was copied") {
		t.Errorf("the render does not say the build went while it was copied:\n%s", out)
	}
	if _, err := os.Lstat(fx.link()); err == nil {
		t.Error("a copy of a reaped build was linked")
	}
	if versions, _ := os.ReadDir(hostTreeVersionsDir(fx.tree(t))); len(versions) != 0 {
		t.Errorf("a copy of a reaped build was renamed in: %v", versions)
	}
}

// A TREE LISTED ONLY IN AN AUTONOMOUS POSTURE LIST reaches no host, so it never stops the owner's host
// launch (PPX-D26), though nothing renders it there. Red if the stop stops reading where the entry
// reaches.
func TestATreeListedOnlyForJailsNeverStopsAHostLaunch(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgentWith(t, `{"kind":"autonomy","autonomous":{"lists":[{"surface":"tool/settings",`+
		`"path":"/packages","add":["~/.tool/ext/tool-ext"]}]}}`)
	fx.writeHostConfig(t, "")
	if f := fx.tree(t); f.Owner != "agentpack" || f.ListedAtHost {
		t.Fatalf("the fixture's tree: owner %q, listed at the host %v", f.Owner, f.ListedAtHost)
	}
	stubBins(t, "tool")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Errorf("rc=%d, execed %v: a tree no host loads stopped the owner's host launch\n%s", rc, got.execed, errw.String())
	}
}

// ON A MACOS HOST `yolo host -- tool` starts the owner without the tree, and says so once, naming a
// jail that has it (§9, §11). Red if hostExec stops calling noteHostTreeLines.
func TestAMacOSHostLaunchSaysTheOwnerStartsWithoutItsTree(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgent(t)
	fx.writeHostConfig(t, "")
	prev := hostTreesBuild
	hostTreesBuild = func() bool { return false }
	t.Cleanup(func() { hostTreesBuild = prev })
	stubBins(t, "tool", "other")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d, execed %v\n%s", rc, got.execed, errw.String())
	}
	for _, w := range []string{"extension " + treeKeyCLI + " is not delivered on this host", "tool starts without it",
		"YOLO_RUNTIME=podman yolo -- tool"} {
		if n := strings.Count(errw.String(), w); n != 1 {
			t.Errorf("the macOS launch says %q %d times, want once:\n%s", w, n, errw.String())
		}
	}
	errw.Reset()
	hostExec(nil, []string{"other"}, io.Discard, &errw, nil)
	if strings.Contains(errw.String(), "is not delivered on this host") {
		t.Errorf("a program that loads no tree was told of one:\n%s", errw.String())
	}
}

// A REVERT REMOVES THE LINK (§8.3), and the host's versioned copies with it, as it withdraws every
// key yolo wrote: its dry run names the link and writes nothing, and --assert removes the link, the
// record's entry for it and the copies. Red if the revert stops calling revertHostTreeLinks.
func TestARevertRemovesAPatchedExtensionsLinkAndItsCopies(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	var out, errw bytes.Buffer
	hostApply([]string{"--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	target, err := os.Readlink(fx.link())
	if err != nil {
		t.Fatalf("the apply rendered no link: %v\n%s", err, errw.String())
	}
	if rc := hostMain([]string{"apply", "--revert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("the dry revert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "would remove") || !strings.Contains(out.String(), fx.link()) {
		t.Errorf("the dry revert does not name the link:\n%s", out.String())
	}
	if _, err := os.Lstat(fx.link()); err != nil {
		t.Fatal("the dry revert removed the link")
	}
	out.Reset()
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("the revert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	if _, err := os.Lstat(fx.link()); err == nil {
		t.Errorf("the revert left the link:\n%s", out.String())
	}
	if isDir(filepath.Dir(target)) {
		t.Errorf("the revert left the host's versioned copies:\n%s", out.String())
	}
	if man, _ := hostskills.LoadManifest(hostSkillsManifestPath()); man.OwnedBy(fx.link(), "treepack") {
		t.Error("the revert left the link in the files ownership record")
	}
	if !strings.Contains(out.String(), "1 patched-extension link") {
		t.Errorf("the revert's summary does not count the link:\n%s", out.String())
	}
}

// AN UNREADABLE FILES RECORD fails no revert: the keys are still withdrawn, nothing the record
// cannot prove is yolo's is removed, and the line names the record and what to do.
func TestARevertOverAnUnreadableFilesRecordRemovesNoLinkAndSaysSo(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	var out, errw bytes.Buffer
	hostApply([]string{"--assert"}, io.Discard, &errw, false, strings.NewReader(""))
	if _, err := os.Readlink(fx.link()); err != nil {
		t.Fatalf("the apply rendered no link: %v\n%s", err, errw.String())
	}
	writeFile(t, hostSkillsManifestPath(), "{not json")
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("the revert rc=%d over an unreadable files record\n%s%s", rc, out.String(), errw.String())
	}
	if _, err := os.Lstat(fx.link()); err != nil {
		t.Error("the revert removed a link an unreadable record cannot prove is yolo's")
	}
	if !strings.Contains(out.String()+errw.String(), hostSkillsManifestPath()) ||
		!strings.Contains(out.String()+errw.String(), "no patched extension's link is removed") {
		t.Errorf("the revert does not say why it left the link, naming the record:\n%s%s", out.String(), errw.String())
	}
}
