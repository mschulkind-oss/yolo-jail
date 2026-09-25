package entrypoint

// nodefloor_test.go covers the interpreter resolution for a `program` declaring a `node_floor`
// (docs/reference/agent-program-runtimes.md, "Resolution" and OQ-AR1).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// fakeMiseStore builds a store shaped like a real one: alias dirs beside real version dirs, each
// with a runnable bin/node.
func fakeMiseStore(t *testing.T, versions ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, v := range versions {
		bin := filepath.Join(root, v, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := miseNodeStore
	miseNodeStore = root
	t.Cleanup(func() { miseNodeStore = old })
	return root
}

func stubImageNode(t *testing.T, version string) {
	t.Helper()
	old := imageNodeVersion
	imageNodeVersion = func() string { return version }
	t.Cleanup(func() { imageNodeVersion = old })
}

// THE IMAGE'S NODE WINS when it satisfies — self-contained, no install, and the node the MCP
// wrappers already target.
func TestImageNodeWinsWhenItSatisfies(t *testing.T) {
	stubImageNode(t, "24.19.0")
	fakeMiseStore(t, "22.23.2")
	if got := ResolveNodeForFloor("22.19"); got != imageNodePath {
		t.Errorf("ResolveNodeForFloor = %q, want the image's %q", got, imageNodePath)
	}
}

// When the image's node is too old, the store's NEWEST satisfying version is taken — and this is the
// case a lexical compare gets wrong, so the fixture is built to catch it.
func TestFallsBackToTheNewestSatisfyingMiseNode(t *testing.T) {
	stubImageNode(t, "20.20.2") // too old, and lexically ">" the floor
	root := fakeMiseStore(t, "20.20.2", "22.20.0", "22.23.2", "24.19.0")

	got := ResolveNodeForFloor("22.19")
	if want := filepath.Join(root, "24.19.0", "bin", "node"); got != want {
		t.Errorf("ResolveNodeForFloor = %q, want the newest satisfying %q", got, want)
	}
}

// An ALIAS dir is usable, and a more specific spelling wins a tie — the baked path is read by a human
// debugging a launcher, and "24.19.0" tells them what they got where "24" does not.
func TestAMoreSpecificSpellingWinsATie(t *testing.T) {
	stubImageNode(t, "")
	root := fakeMiseStore(t, "24", "24.19.0")
	got := ResolveNodeForFloor("22.19")
	if want := filepath.Join(root, "24.19.0", "bin", "node"); got != want {
		t.Errorf("ResolveNodeForFloor = %q, want the specific spelling %q", got, want)
	}
}

// An alias ALONE still resolves — it is a real directory whose bin/node runs.
func TestAnAliasAloneResolves(t *testing.T) {
	stubImageNode(t, "")
	root := fakeMiseStore(t, "24")
	if got := ResolveNodeForFloor("22.19"); got != filepath.Join(root, "24", "bin", "node") {
		t.Errorf("an alias dir must resolve; got %q", got)
	}
}

// ⚠ A directory with NO runnable bin/node is skipped, not returned. mise leaves one behind for a
// failed or partial install, and baking a path to a binary that is not there turns a resolution
// success into an exec failure at the worst moment.
func TestAPartialInstallIsSkipped(t *testing.T) {
	stubImageNode(t, "")
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "24.19.0"), 0o755); err != nil { // no bin/node
		t.Fatal(err)
	}
	bin := filepath.Join(root, "22.23.2", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := miseNodeStore
	miseNodeStore = root
	t.Cleanup(func() { miseNodeStore = old })

	if got := ResolveNodeForFloor("22.19"); got != filepath.Join(bin, "node") {
		t.Errorf("a dir with no runnable bin/node must be skipped; got %q", got)
	}
}

// Nothing satisfies -> "". The REFUSAL for that case is the launch's, not this resolver's: a content
// generator that refused would refuse during `yolo check`, which is an observe verb.
func TestNothingSatisfyingResolvesToEmpty(t *testing.T) {
	stubImageNode(t, "20.20.2")
	fakeMiseStore(t, "20.20.2", "21.0.0")
	if got := ResolveNodeForFloor("22.19"); got != "" {
		t.Errorf("want \"\" when nothing satisfies, got %q", got)
	}
}

// No floor -> "" without consulting anything. A program that declares nothing keeps today's exec.
func TestNoFloorResolvesNothing(t *testing.T) {
	// RESTORE the original, never nil. Setting the seam to nil on cleanup left every later test in
	// the package with a nil probe — the same unscoped-package-state class that
	// TestEveryPerLaunchPackRecordIsScoped exists to catch one layer up, reproduced here by hand.
	old := imageNodeVersion
	imageNodeVersion = func() string {
		t.Fatal("an undeclared floor must not probe the image's node")
		return ""
	}
	t.Cleanup(func() { imageNodeVersion = old })
	if got := ResolveNodeForFloor(""); got != "" {
		t.Errorf("want \"\", got %q", got)
	}
}

// THE BYTE-IDENTITY REQUIREMENT, which docs/reference/agent-program-runtimes.md's "The launcher" makes a test rather than an intention: a program with no
// declared floor must render the launcher it rendered before this feature existed.
func TestNoFloorRendersAByteIdenticalLauncher(t *testing.T) {
	stubImageNode(t, "24.19.0")
	fakeMiseStore(t, "24.19.0")
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing"}

	got := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	// EVERY exec path, not just the tail: npmLauncherTemplate has two — the main one and the
	// re-entry guard's — and a prefix applied to only one leaves a re-entrant launch unwrapped.
	for _, line := range execLinesOf(got) {
		if !strings.Contains(line, `exec "$REAL_BIN" `) {
			t.Errorf("a program with no floor must exec $REAL_BIN directly; got: %s", line)
		}
	}
	if strings.Contains(got, "__YOLO_EXEC_PREFIX__") {
		t.Error("the placeholder was not substituted")
	}
}

// And with a floor, the resolved interpreter is baked in, shell-quoted, ahead of $REAL_BIN.
func TestAFloorBakesTheResolvedInterpreter(t *testing.T) {
	stubImageNode(t, "24.19.0")
	fakeMiseStore(t)
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing", NodeFloor: "22.19"}

	got := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	lines := execLinesOf(got)
	if len(lines) < 2 {
		t.Fatalf("expected both exec paths (main + re-entry guard), found %d: %v", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.Contains(line, "exec "+shquote.Quote(imageNodePath)+" \"$REAL_BIN\" ") {
			t.Errorf("EVERY exec path must carry the interpreter — one left unwrapped means a "+
				"re-entrant launch runs under the workspace's node; got: %s", line)
		}
	}
}

// A declared floor that resolves to NOTHING still renders the plain exec — the refusal is the
// launch's job, and a half-written launcher would be worse than an unwrapped one.
func TestAnUnsatisfiableFloorStillRendersAPlainExec(t *testing.T) {
	stubImageNode(t, "20.20.2")
	fakeMiseStore(t, "20.20.2")
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing", NodeFloor: "99.0"}

	got := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	for _, line := range execLinesOf(got) {
		if !strings.Contains(line, `exec "$REAL_BIN" `) {
			t.Errorf("want a plain exec when nothing satisfies; got: %s", line)
		}
	}
}

// THE STAGE-INSTALL GAP (docs/design/agent-program-runtimes.md, OQ-AR7): the interpreter is
// resolved ONCE, when the launcher is generated, and generation runs at boot BEFORE the
// provisioning stage. So a node the stage installs satisfies the floor CHECK (which asks the
// resolver again) but is not in the launcher of the launch that installed it; the next
// generation, i.e. the next boot, bakes it. This pins that documented behavior, so the reference's
// Resolution table cannot drift back into claiming the launcher execs a stage-installed node on
// the launch that installs it. A fix for OQ-AR7 is expected to change this test.
func TestAStageInstalledNodeReachesTheLauncherOnlyAtTheNextGeneration(t *testing.T) {
	stubImageNode(t, "20.20.2")
	root := fakeMiseStore(t)
	inst := &packdecl.Install{Kind: "npm", Bin: "thing", Package: "thing", NodeFloor: "22.19"}

	// Boot: nothing satisfies yet, so the launcher is baked with a plain exec.
	atBoot := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)

	// The stage installs node@22.19 into the store; the floor check's resolver now sees it.
	bin := filepath.Join(root, "22.19.0", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(bin, "node")
	if got := ResolveNodeForFloor("22.19"); got != installed {
		t.Fatalf("after the install the check's resolver must see %q, got %q", installed, got)
	}

	for _, line := range execLinesOf(atBoot) {
		if !strings.Contains(line, `exec "$REAL_BIN" `) {
			t.Errorf("the launcher generated before the install must not name an interpreter; got: %s", line)
		}
	}

	// Next boot: generation runs again against the now-populated store and bakes the interpreter.
	nextBoot := npmAgentLauncher(inst, "/stamps", "/receipts", false, launcherServers{}, nil)
	for _, line := range execLinesOf(nextBoot) {
		if !strings.Contains(line, "exec "+shquote.Quote(installed)+" \"$REAL_BIN\" ") {
			t.Errorf("the next generation must bake the stage-installed interpreter; got: %s", line)
		}
	}
}

// execLinesOf returns EVERY exec-the-program line. Plural on purpose: the first draft of this file
// checked only the first match and passed while the re-entry guard's exec was still unwrapped.
func execLinesOf(launcher string) []string {
	var out []string
	for _, l := range strings.Split(launcher, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "exec ") && strings.Contains(t, "REAL_BIN") {
			out = append(out, t)
		}
	}
	return out
}

// TestBootstrapSubstitutesTheFloorPlaceholders pins OQ-AR2's eager half at the GENERATOR: the
// checks must reach the script that runs them. A placeholder left unsubstituted is the failure mode
// — the script would run a line named after a placeholder, or `exit` with a non-number.
//
// Asserted on the rendered script rather than on declaredNodeFloors alone, because the wiring is the
// half that can be lost: the projection could be perfect and the replacer entry deleted. What the
// checks DO is TestTheBootstrapRefusesAnUnsatisfiableFloor's job (nodefloorrefusal_test.go), which
// runs them.
func TestBootstrapSubstitutesTheFloorPlaceholders(t *testing.T) {
	e := NewEnv(map[string]string{"HOME": t.TempDir()})
	script := BootstrapScript(e)

	for _, ph := range []string{"__YOLO_NODE_FLOOR_CHECKS__", "__YOLO_REFUSED_STATUS__", "__YOLO_NODE_FLOORS__"} {
		if strings.Contains(script, ph) {
			t.Errorf("the placeholder %s was not substituted", ph)
		}
	}
	// The refusal's exit is the wrapper's reserved status, rendered from the constant.
	if !strings.Contains(script, "exit "+strconv.Itoa(provision.RefusedStatus)+"\n") {
		t.Errorf("the bootstrap does not exit provision.RefusedStatus (%d) on a refused floor — the "+
			"stage's wrapper would degrade it like any other failure and the target would run",
			provision.RefusedStatus)
	}
}

// TestFloorsAreDistinctSortedAndKeepEveryDeclarer is the projection, over a REAL staged pack tree:
// two packs declaring one floor is one install, a stable order keeps two bootstrap scripts
// diffable, and BOTH declarers survive the merge — a refusal naming only the first would send the
// user to drop one pack and meet the same refusal again.
func TestFloorsAreDistinctSortedAndKeepEveryDeclarer(t *testing.T) {
	e := NewEnv(map[string]string{"JAIL_HOME": t.TempDir(), "YOLO_PACK_ROOT": stageFloorPacks(t, map[string]string{
		"zeta":  floorProgram("zed", "22.19"),
		"alpha": floorProgram("ay", "24"),
		"mid":   floorProgram("em", "22.19"),
		"plain": `{"name": "plain", "contributes": [{"kind": "program", "bin": "pl", "via": "npm", "package": "pl"}]}`,
	})})
	got := declaredNodeFloors(e)
	want := []nodeFloorDecl{
		{Floor: "22.19", DeclaredBy: []string{"program em (pack mid)", "program zed (pack zeta)"}},
		{Floor: "24", DeclaredBy: []string{"program ay (pack alpha)"}},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("declaredNodeFloors =\n  %v\nwant\n  %v", got, want)
	}
	// And the rendered calls quote what they bake: the declarer list is one shell word.
	checks := nodeFloorChecks(e)
	if want := "_yolo_node_floor 22.19 'program em (pack mid) and program zed (pack zeta)'\n" +
		"_yolo_node_floor 24 'program ay (pack alpha)'"; checks != want {
		t.Errorf("nodeFloorChecks =\n%s\nwant\n%s", checks, want)
	}
}

// fakePackageFloor puts an executable `node` printing `v<version>` in a fresh dir, and sets
// $YOLO_DARWIN_LOGIN_PATH to a sandbox-shaped PATH: a mise shims dir UNDER $HOME first (whose
// node is the workspace's pin, and must never be read), then the floor dir, then an empty stand-in
// for the system dirs (the test host's own /bin may hold a node; a Mac's does not). It returns the
// floor's node.
func fakePackageFloor(t *testing.T, version string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	shims := filepath.Join(home, ".yolo", "mise", "shims")
	floorBin := filepath.Join(t.TempDir(), "profile", "bin")
	for _, d := range []string{shims, floorBin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The shim reports a floor-satisfying version on purpose: if the home filter were lost, the
	// resolution would pick THIS, and the assertions below name which one it picked.
	if err := os.WriteFile(filepath.Join(shims, "node"), []byte("#!/bin/sh\necho v99.0.0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(floorBin, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\necho v"+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(DarwinLoginPathEnv, shims+":"+floorBin+":"+t.TempDir())
	return node
}

// macos-user HAS NO IMAGE, so candidate 1 is the package floor's node on the sandbox PATH. The
// reviewer's scenario: no /bin/node, an empty mise store (the stage's `mise install node@22.19`
// failed offline), and nodejs_24 on the floor. Before packageFloorNodes this resolved to "" — and
// since the floor check became fatal, that refused a launch the floor's node would have served.
func TestMacosUserResolvesThePackageFloorNode(t *testing.T) {
	stubImageNode(t, "")
	fakeMiseStore(t)
	node := fakePackageFloor(t, "24.19.0")
	if got := ResolveNodeForFloor("22.19"); got != node {
		t.Errorf("ResolveNodeForFloor = %q, want the package floor's %q (never the mise shim under $HOME)", got, node)
	}
	if got := DescribeAvailableNodes(); got != "24.19.0 at "+node {
		t.Errorf("DescribeAvailableNodes = %q, want %q", got, "24.19.0 at "+node)
	}
}

// A floor node too old for the floor is listed but not chosen, and the mise store still answers.
func TestATooOldPackageFloorNodeFallsThroughToTheStore(t *testing.T) {
	stubImageNode(t, "")
	store := fakeMiseStore(t, "22.23.2")
	node := fakePackageFloor(t, "20.20.2")
	want := filepath.Join(store, "22.23.2", "bin", "node")
	if got := ResolveNodeForFloor("22.19"); got != want {
		t.Errorf("ResolveNodeForFloor = %q, want the store's %q", got, want)
	}
	if got, w := DescribeAvailableNodes(), "20.20.2 at "+node+"; 22.23.2 at "+want; got != w {
		t.Errorf("DescribeAvailableNodes = %q, want %q", got, w)
	}
}

// Nothing anywhere on macos-user: the refusal's "none" names all three places it looked.
func TestMacosUserNoneNamesTheLoginPath(t *testing.T) {
	stubImageNode(t, "")
	store := fakeMiseStore(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(DarwinLoginPathEnv, t.TempDir())
	want := "none (no readable " + imageNodePath + ", no node outside $HOME on $" + DarwinLoginPathEnv +
		", and no node in " + store + ")"
	if got := DescribeAvailableNodes(); got != want {
		t.Errorf("DescribeAvailableNodes = %q, want %q", got, want)
	}
}
