package cli

// patchedtree_test.go drives a PATCHED EXTENSION's tree build and delivery
// (docs/design/patched-extensions.md §7, §8.1; PPX-D5 to PPX-D7) against a real local upstream, with
// the build jail substituted: a launch's tree arm runs the shared advance, builds the tree in a jail
// sealed to the contributing pack with the tree's final copy, admits it under a tree's recipe and a
// receipt naming the extension key, and copies it beside the launch's pack tree — reflink or copy,
// never the store's inode; the admit refuses strays, a missing `produces` and a reference to the
// build's home; a copy whose entry is reaped is made again once; below Apple Container's floor
// nothing is built and a good build is still copied; and a move reaps every other build at once.

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const treeKeyCLI = "treepack/tool-ext"

// treeFixture is the patched-fork fixture's upstream and series, contributed by a pack as a
// patched extension, with the build jail substituted.
type treeFixture struct {
	*patchedFixture
	treeDir string
	now     time.Time
	builds  []string // f.txt as each build saw it in its src/
	seen    []run.Options
	stray   bool // the fake build also leaves a path outside the reserved directory
	homeRef bool // the fake build's tree names the build home
	noFile  bool // the fake build leaves no f.txt in the tree
}

func newTreeFixture(t *testing.T, produces string) *treeFixture {
	t.Helper()
	fx := &treeFixture{patchedFixture: newPatchedFixture(t, ""), now: time.Unix(1_900_000_000, 0)}
	fx.treeDir = filepath.Join(fx.packs, "treepack")
	series, _ := filepath.Glob(filepath.Join(fx.forkDir, "patches", "*.patch"))
	for _, p := range series {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(fx.treeDir, "patches", filepath.Base(p)), string(data))
	}
	writeFile(t, filepath.Join(fx.treeDir, "pack.json"), `{"name":"treepack","contributes":[{"kind":"files",`+
		`"into":".tool/ext/tool-ext","source":"git+file://`+fx.repo+`?ref=main","patches":"patches",`+
		`"build":"true","produces":[`+produces+`]}]}`)
	writeFile(t, filepath.Join(fx.home, ".config", "yolo-jail", "config.jsonc"), `{"packs":[`+
		`{"source":"file://`+fx.treeDir+`","name":"treepack"}]}`)
	prevNow := patchedNow
	patchedNow = func() time.Time { return fx.now }
	t.Cleanup(func() { patchedNow = prevNow })
	withFakeCaptureJail(t, fx.buildJail(t))
	prevChild := forkBuildChild
	forkBuildChild = func(_ context.Context, _ time.Duration, staging string, b forkBuild, out, errw io.Writer,
		color bool) (int, bool) {
		return forkBuildRunJail(staging, b, out, errw, color), false
	}
	t.Cleanup(func() { forkBuildChild = prevChild })
	return fx
}

// buildJail is the fake tree build jail: what the real one's final copy leaves, a tree at
// ~/.local/share/yolo-tree/tool-ext holding the patched f.txt the build saw.
func (fx *treeFixture) buildJail(t *testing.T) func(run.Options) int {
	return func(o run.Options) int {
		fx.seen = append(fx.seen, o)
		data, err := os.ReadFile(filepath.Join(o.Workspace, forkSourceLeaf, "f.txt"))
		if err != nil {
			t.Errorf("the patched source is not in the build's src/: %v", err)
			return 1
		}
		fx.builds = append(fx.builds, string(data))
		writeFile(t, filepath.Join(o.Workspace, forkToolchainLeaf), "image-identity node v24 npm 11\n")
		out := filepath.Join(o.Workspace, captureOutLeaf)
		reserved := packdecl.TreeReservedDir("tool-ext")
		entries := []capture.ManifestEntry{
			{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
			{Path: ".local/share", Kind: capture.KindDir, Mode: "0755"},
			{Path: packdecl.TreeReservedRoot, Kind: capture.KindDir, Mode: "0755"},
			{Path: reserved, Kind: capture.KindDir, Mode: "0755"},
		}
		if err := os.MkdirAll(filepath.Join(capture.TreeDir(out), filepath.FromSlash(reserved)), 0o755); err != nil {
			t.Fatal(err)
		}
		if !fx.noFile {
			writeFile(t, filepath.Join(capture.TreeDir(out), filepath.FromSlash(reserved), "f.txt"), string(data))
			entries = append(entries, capture.ManifestEntry{Path: reserved + "/f.txt", Kind: capture.KindFile,
				Mode: "0644", Size: int64(len(data))})
		}
		if fx.stray {
			writeFile(t, filepath.Join(capture.TreeDir(out), ".local", "state", "junk"), "x")
			entries = append(entries, capture.ManifestEntry{Path: ".local/state", Kind: capture.KindDir, Mode: "0755"},
				capture.ManifestEntry{Path: ".local/state/junk", Kind: capture.KindFile, Mode: "0644", Size: 1})
		}
		m := &capture.Manifest{Schema: capture.ManifestSchema, Home: "/home/agent", Platform: patchedTestPlatform,
			Surfaces: []string{".local"}, Excluded: capture.DefaultExcludes(), Entries: entries,
			RefScan: capture.RefScanFull, Relocatable: true}
		if fx.homeRef {
			m.AbsoluteRefs = []capture.AbsoluteRef{{Path: reserved + "/f.txt", Kind: capture.RefFileContent, Value: "/home/agent"}}
		}
		slices.SortFunc(m.Entries, func(a, b capture.ManifestEntry) int { return strings.Compare(a.Path, b.Path) })
		if err := capture.WriteManifest(out, m); err != nil {
			t.Fatal(err)
		}
		return 0
	}
}

func (fx *treeFixture) tree(t *testing.T) packload.Fork {
	t.Helper()
	for _, f := range packload.PatchedTrees(selectConfiguredHostPacks().packs) {
		if f.Key() == treeKeyCLI {
			return f
		}
	}
	t.Fatal("the fixture's selection carries no patched extension " + treeKeyCLI)
	return packload.Fork{}
}

// deliver runs one launch's tree arm for the fixture's extension.
func (fx *treeFixture) deliver(t *testing.T, build bool) (run.TreeDelivery, string) {
	t.Helper()
	var out, errw syncBuffer
	req := run.TreeBuildRequest{Trees: []packload.Fork{fx.tree(t)}, Platform: patchedTestPlatform, Runtime: "podman",
		Workspace: "/ws", Build: build, CopyRoot: filepath.Join(t.TempDir(), "tree.patched")}
	if !build {
		req.BuildFloor = "Apple Container 0.1 ignores read-only (:ro)"
	}
	got := deliverTreesForLaunch(req, &out, &errw, false)
	return got[treeKeyCLI], out.String() + errw.String()
}

func ino(t *testing.T, p string) uint64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(p, &st); err != nil {
		t.Fatal(err)
	}
	return st.Ino
}

// THE FIRST LAUNCH builds the tree through the shared advance, in a jail sealed to the contributing
// pack whose command ends in the tree's final copy, admits it under a tree's recipe with a receipt
// naming the extension key, and copies it for the jail — never the store's inode.
func TestAPatchedExtensionsLaunchBuildsItsTreeAndCopiesIt(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	d, out := fx.deliver(t, true)
	if d.Dir == "" {
		t.Fatalf("no copy was delivered: %+v\n%s", d, out)
	}
	want := lines30(map[int]string{10: "ten", 12: "twelve"})
	if got, err := os.ReadFile(filepath.Join(d.Dir, "f.txt")); err != nil || string(got) != want {
		t.Errorf("the copy's f.txt = %q (%v), want the base with the series applied", got, err)
	}
	if len(fx.seen) != 1 || !slices.Equal(fx.seen[0].OnlyPacks, []string{"treepack"}) || !fx.seen[0].Sealed {
		t.Fatalf("the build jail ran %d times, sealed to %v", len(fx.seen), fx.seen[0].OnlyPacks)
	}
	script := strings.Join(fx.seen[0].Args, " ")
	if !strings.Contains(script, "cp -a") || !strings.Contains(script, packdecl.TreeReservedDir("tool-ext")) ||
		!strings.Contains(script, "export PATH=/bin:/usr/bin:") {
		t.Errorf("the build jail's command is not the tree's: %q", script)
	}
	rec := patchedRecordOf(t, treeKeyCLI)
	series, _ := fx.tree(t).ReadSeries()
	if rec.Good == nil || rec.Good.Entry != d.Entry ||
		rec.Good.Recipe != packdecl.TreeSourceRecipe(fx.tree(t).Source, "true", []string{"f.txt"}, series.Digest) {
		t.Fatalf("the good build = %+v, want the admitted tree under a tree's recipe", rec.Good)
	}
	entry, err := (&capture.Store{Dir: paths.CapturesDir()}).Resolve(d.Entry)
	if err != nil {
		t.Fatal(err)
	}
	recs, _ := entrypoint.ReadBuildReceipts(capture.ReceiptsPath(entry.Root))
	if len(recs) != 1 || recs[0].Fork != treeKeyCLI || recs[0].Bin != "tool-ext" || recs[0].Series != series.Digest ||
		!strings.Contains(recs[0].Toolchain, "node") {
		t.Errorf("the receipt = %+v", recs)
	}
	stored := filepath.Join(entry.Tree, filepath.FromSlash(packdecl.TreeReservedDir("tool-ext")), "f.txt")
	if ino(t, stored) == ino(t, filepath.Join(d.Dir, "f.txt")) {
		t.Error("the per-launch copy is the store's own inode")
	}
	if !strings.Contains(out, "built extension "+treeKeyCLI+": ") {
		t.Errorf("the move line does not name the extension:\n%s", out)
	}
	// A SECOND LAUNCH inside the hour builds nothing and copies the same build.
	again, _ := fx.deliver(t, true)
	if again.Entry != d.Entry || len(fx.builds) != 1 {
		t.Errorf("the second launch built again (%d builds) or answered %+v", len(fx.builds), again)
	}
}

// THE ADMIT'S THREE CHECKS (PPX-D5): a path left outside the reserved directory, a `produces` the
// tree lacks and a reference to the build's home each fail the build, naming what, and nothing is
// delivered.
func TestATreesAdmitRefusesStraysMissingProducesAndHomeReferences(t *testing.T) {
	for _, tc := range []struct {
		name   string
		edit   func(*treeFixture)
		reason string
	}{
		{"a stray", func(fx *treeFixture) { fx.stray = true }, ".local/state/junk"},
		{"a missing produces", func(fx *treeFixture) { fx.noFile = true }, "none of f.txt"},
		{"a home reference", func(fx *treeFixture) { fx.homeRef = true }, "names the build jail's home /home/agent in f.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTreeFixture(t, `"f.txt"`)
			tc.edit(fx)
			d, out := fx.deliver(t, true)
			if d.Dir != "" || !strings.Contains(out, tc.reason) {
				t.Errorf("delivered %+v; want a failed build naming %q:\n%s", d, tc.reason, out)
			}
			if rec := patchedRecordOf(t, treeKeyCLI); rec.Good != nil {
				t.Errorf("a refused tree became the good build: %+v", rec.Good)
			}
		})
	}
}

// A COPY WHOSE ENTRY IS REAPED WHILE IT RUNS (§8.1): the marker gone when the copy ends, the copy is
// removed and the record re-read once — the advance again, which rebuilds the build that went.
func TestATreeCopyWhoseEntryIsReapedIsMadeAgainOnce(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	reaped := 0
	prev := treeCopied
	treeCopied = func(key string) {
		if reaped == 0 {
			reaped++
			if err := (&capture.Store{Dir: paths.CapturesDir()}).ReapEntry(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(func() { treeCopied = prev })
	d, out := fx.deliver(t, true)
	if d.Dir == "" || len(fx.builds) != 2 || !strings.Contains(out, "was reaped while this launch copied it") {
		t.Fatalf("delivered %+v after %d builds:\n%s", d, len(fx.builds), out)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, "f.txt")); err != nil {
		t.Errorf("the second copy is not whole: %v", err)
	}
}

// BELOW APPLE CONTAINER'S READ-ONLY FLOOR nothing is checked or built, a good build already on this
// machine is still copied, and with none the reason names the floor.
func TestBelowTheFloorAGoodBuildIsCopiedAndNothingIsBuilt(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	if d, out := fx.deliver(t, false); d.Dir != "" || !strings.Contains(d.Reason, "this runtime builds none") ||
		len(fx.builds) != 0 {
		t.Fatalf("with no good build below the floor: %+v, %d builds\n%s", d, len(fx.builds), out)
	}
	first, _ := fx.deliver(t, true)
	prev := treeAdvance
	treeAdvance = func(packload.Fork, advanceOptions) advanceResult {
		t.Error("below the floor the advance ran")
		return advanceResult{}
	}
	t.Cleanup(func() { treeAdvance = prev })
	d, out := fx.deliver(t, false)
	if d.Dir == "" || d.Entry != first.Entry {
		t.Errorf("below the floor the good build was not copied: %+v\n%s", d, out)
	}
}

// A MOVE REAPS EVERY OTHER BUILD OF A TREE AT ONCE (PPX-D7), even one a delivery record names: jails
// hold copies, never the store's entry, as a fork's jail would.
func TestATreesMoveReapsTheOldBuildEvenWhenARecordNamesIt(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	first, _ := fx.deliver(t, true)
	tree := filepath.Join(paths.PackTreeRoot("yolo-other"), "20260101T000000Z-1")
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, tree+".forks.json", `{"schema":1,"forks":{"tool-ext":{"key":"`+first.Entry+`"}}}`)
	fx.commit(t, "v1.1.0", map[int]string{14: "fourteen"})
	fx.now = fx.now.Add(2 * time.Hour)
	moved, out := fx.deliver(t, true)
	if moved.Entry == "" || moved.Entry == first.Entry {
		t.Fatalf("the good build did not move: %+v\n%s", moved, out)
	}
	if storeEntryExists(first.Entry) {
		t.Error("a tree's move kept the old build a delivery record named")
	}
}

// THE CHILD BUILD JAIL carries a tree's arm: the final copy, the seal narrowed to the contributing
// pack, and an empty build line accepted.
func TestTheChildBuildJailRunsATreesBuild(t *testing.T) {
	f := packload.Fork{Pack: "treepack", Bin: "tool-ext", Into: ".tool/ext/tool-ext", Patches: "patches"}
	argv := forkBuildChildArgv("/staging", forkBuild{Fork: f}, false)
	if !slices.Contains(argv, "--tree=tool-ext") || !slices.Contains(argv, "--only=treepack") ||
		slices.ContainsFunc(argv, func(a string) bool { return a == "--only=" }) {
		t.Fatalf("the child's argv = %q", argv)
	}
	var seen run.Options
	withFakeCaptureJail(t, func(o run.Options) int { seen = o; return 0 })
	if rc := runForkBuildJail(argv[2:], io.Discard, io.Discard); rc != 0 {
		t.Fatalf("the child refused a tree's build with no build line: rc %d", rc)
	}
	if !slices.Equal(seen.OnlyPacks, []string{"treepack"}) ||
		!strings.Contains(strings.Join(seen.Args, " "), packdecl.TreeReservedDir("tool-ext")) {
		t.Errorf("the child ran %q sealed to %v", seen.Args, seen.OnlyPacks)
	}
}

// THE TREE'S JAIL SCRIPT is valid shell, quotes what it pastes, and runs the build line in a subshell
// only when there is one.
func TestATreesBuildJailScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	for _, build := range []string{"", "npm ci && npm run build"} {
		argv := treeBuildJailArgv(build, "tool ext")
		script := argv[len(argv)-1]
		if out, err := exec.Command("bash", "-n", "-c", script).CombinedOutput(); err != nil {
			t.Errorf("build %q: the script does not parse: %v\n%s\n%s", build, err, out, script)
		}
		if strings.Contains(script, "( )") || (build != "" && !strings.Contains(script, "( "+build+" )")) {
			t.Errorf("build %q: the script's build line is %q", build, script)
		}
		if !strings.Contains(script, "'"+packdecl.TreeReservedDir("tool ext")+"'") {
			t.Errorf("the reserved directory is not quoted: %s", script)
		}
	}
}

// THE FRONT DOOR WIRES THE TREE ARM. Red if `yolo run` stops setting Options.BuildTrees.
func TestALaunchWiresTheTreeArm(t *testing.T) {
	newTreeFixture(t, `"f.txt"`)
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	if rc := runRun([]string{"run", "--", "true"}); rc != 0 {
		t.Fatalf("runRun = %d with the pipeline stubbed", rc)
	}
	if seen.BuildTrees == nil {
		t.Fatal("`yolo run` did not wire Options.BuildTrees: no patched extension is ever built")
	}
}

// patchedRecordOf is the check record of owner key, which must be there.
func patchedRecordOf(t *testing.T, owner string) *packsrc.CheckRecord {
	t.Helper()
	r, err := (&packsrc.Store{Dir: paths.PacksDir()}).LoadCheckRecord(owner)
	if err != nil {
		t.Fatalf("the check record of %s: %v", owner, err)
	}
	return r
}

// THE EXPLICIT ACTS ON AN EXTENSION KEY (PF-D12 generalized): `yolo pack update` checks the upstream
// and replays the series, building nothing; `yolo pack status` reports it offline; `yolo capture
// <pack>/<name>` builds it now through the swap, and refuses a key nothing declares.
func TestTheExplicitActsTakeAnExtensionKey(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	rc, out, errw := packVerb(t, "update")
	if !strings.Contains(out+errw, "extension "+treeKeyCLI) || len(fx.builds) != 0 {
		t.Fatalf("`yolo pack update` rc=%d did not check the extension, or built it (%d):\n%s%s", rc,
			len(fx.builds), out, errw)
	}
	if rec := patchedRecordOf(t, treeKeyCLI); rec.Check == nil {
		t.Error("`yolo pack update` recorded no check of the extension")
	}
	_, out, errw = packVerb(t, "status")
	if !strings.Contains(out+errw, treeKeyCLI) || !strings.Contains(out+errw, "patched extension at ~/.tool/ext/tool-ext") {
		t.Errorf("`yolo pack status` does not report the extension:\n%s%s", out, errw)
	}
	var cout, cerr bytes.Buffer
	if rc := captureHost([]string{treeKeyCLI}, &cout, &cerr, false); rc != 0 || len(fx.builds) != 1 {
		t.Fatalf("`yolo capture %s` rc=%d after %d builds:\n%s%s", treeKeyCLI, rc, len(fx.builds), cout.String(), cerr.String())
	}
	if rec := patchedRecordOf(t, treeKeyCLI); rec.Good == nil {
		t.Error("the capture's build did not become the good build")
	}
	cerr.Reset()
	if rc := captureHost([]string{"treepack/nope"}, &cout, &cerr, false); rc == 0 || !strings.Contains(cerr.String(), treeKeyCLI) {
		t.Errorf("a key nothing declares: rc=%d\n%s", rc, cerr.String())
	}
}
