package run

// patchedtrees_test.go pins the launch's half of a PATCHED EXTENSION (docs/design/patched-extensions.md
// §8.1, §9, §11; PPX-D7, PPX-D8, PPX-D18) at its call sites, from Run's own entry point: a fresh
// launch runs the tree arm in the fork slot with the copy root beside its pack tree, mounts the copy
// it gets back read-only at `into` and never the pack's own staged tree there, hands the jail
// YOLO_PATCHED_TREES, stops the owning agent's launchers when nothing serves, and does none of it in
// a capture or build jail; below Apple Container's floor it builds nothing and stops nothing; in a
// jail it names the host; the copies go with their pack tree; and an attach names what its jail mounts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

const (
	treeKey  = "treepack/tree-ext"
	treeInto = ".tool/ext/tree-ext"
)

// treeLaunchHome writes a user config selecting an agent pack whose `tool/settings` surface takes a
// packages list, and a pack contributing a patched extension at treeInto with its list entry there
// when listed.
func treeLaunchHome(t *testing.T, listed bool) string {
	t.Helper()
	return treeLaunchHomeAt(t, listed, treeInto, "")
}

// treeLaunchHomeAt is treeLaunchHome with the tree at into and agentExtra appended to the agent
// pack's contributions.
func treeLaunchHomeAt(t *testing.T, listed bool, into, agentExtra string) string {
	t.Helper()
	home := packHome(t)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_PACK_ROOT", "")
	packs := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(packs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agentpack/pack.json", `{"contributes":[{"kind":"program","bin":"tool","via":"npm","package":"tool"},
		{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}`+
		agentExtra+`]}`)
	list := ""
	if listed {
		list = `,{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/` + into + `"]}`
	}
	write("treepack/pack.json", `{"contributes":[{"kind":"files","into":"`+into+`",
		"source":"git+https://example.invalid/tree-ext?ref=main","patches":"patches"}`+list+`]}`)
	write("treepack/patches/0001-x.patch", "From 0123456789abcdef0123456789abcdef01234567 Mon Sep 17 00:00:00 2001\n"+
		"Subject: [PATCH] x\n\n---\n\nbase-commit: 0123456789abcdef0123456789abcdef01234567\n")
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "agentpack")+`","name":"agentpack"},`+
		`{"source":"file://`+filepath.Join(packs, "treepack")+`","name":"treepack"}]`)
	return home
}

// patchedTreesInArgv decodes the argv's PatchedTreesEnv pair, nil when there is none.
func patchedTreesInArgv(t *testing.T, argv []string) map[string]entrypoint.TreeDelivery {
	t.Helper()
	for _, e := range argvValues(argv, "-e") {
		if v, ok := strings.CutPrefix(e, entrypoint.PatchedTreesEnv+"="); ok {
			var d map[string]entrypoint.TreeDelivery
			if err := json.Unmarshal([]byte(v), &d); err != nil {
				t.Fatalf("%s is not JSON: %v", entrypoint.PatchedTreesEnv, err)
			}
			return d
		}
	}
	return nil
}

// mountsAt is every -v source the argv binds at /home/agent/<dest>.
func mountsAt(argv []string, dest string) []string {
	var out []string
	for _, v := range argvValues(argv, "-v") {
		parts := strings.Split(v, ":")
		if len(parts) >= 2 && parts[1] == "/home/agent/"+dest {
			out = append(out, v)
		}
	}
	return out
}

// THE TREE ARM AT ITS CALL SITE: a fresh launch hands the act its patched extension, the jail's
// platform and a copy root beside its pack tree; the copy it gets back is mounted :ro at `into`, and
// the jail is told what it got. Red if Run stops calling treeDeliveriesFor, if the `files` emitter
// stops mounting the copy, or if assembly stops emitting YOLO_PATCHED_TREES.
func TestALaunchRunsTheTreeArmAndMountsItsCopy(t *testing.T) {
	treeLaunchHome(t, true)
	var req TreeBuildRequest
	var copyDir string
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
			req = r
			copyDir = PatchedTreeCopyDir(r.CopyRoot, treeKey)
			if err := os.MkdirAll(copyDir, 0o755); err != nil {
				t.Fatal(err)
			}
			return map[string]TreeDelivery{treeKey: {Dir: copyDir, Entry: "e1", Commit: strings.Repeat("a", 40), Patches: 1}}
		}
	})
	if len(req.Trees) != 1 || req.Trees[0].Key() != treeKey || !req.Build || req.Trees[0].Owner != "agentpack" {
		t.Fatalf("the tree arm was handed %+v\n%s", req, printed)
	}
	if !strings.HasSuffix(req.CopyRoot, patchedCopiesSuffix) ||
		filepath.Dir(req.CopyRoot) != paths.PackTreeRoot(yoloruntime.FromWorkspace(req.Workspace)) {
		t.Errorf("the copy root %q is not beside the launch's pack tree", req.CopyRoot)
	}
	mounts := mountsAt(argv, treeInto)
	if len(mounts) != 1 || mounts[0] != copyDir+":/home/agent/"+treeInto+":ro" {
		t.Errorf("the mounts at ~/%s are %q, want the per-launch copy, read-only, alone", treeInto, mounts)
	}
	d := patchedTreesInArgv(t, argv)[treeKey]
	if d.Into != treeInto || d.Build != "e1" || d.Stop || d.Owner != "agentpack" {
		t.Errorf("the jail is handed %+v", d)
	}
	if !strings.Contains(printed, "Patched extensions this launch:") || !strings.Contains(printed, "extension "+treeKey) {
		t.Errorf("the launch does not name its patched extension:\n%s", printed)
	}
}

// NOTHING SERVES (PPX-D18): nothing is mounted at `into` — never the pack's whole staged tree, which
// a `from` of "" joined onto the root would name — and the owning agent's launchers are told to stop.
func TestATreeWithNoBuildMountsNothingAndStopsItsOwner(t *testing.T) {
	treeLaunchHome(t, true)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "extension " + treeKey + " has no build on this machine yet"}}
		}
	})
	if m := mountsAt(argv, treeInto); len(m) != 0 {
		t.Errorf("a tree with no build mounted %q", m)
	}
	d := patchedTreesInArgv(t, argv)[treeKey]
	if !d.Stop || d.Owner != "agentpack" || !strings.Contains(d.Reason, "no build on this machine yet") {
		t.Errorf("the jail is handed %+v, want its owner stopped with the reason\n%s", d, printed)
	}
}

// NO LIST ENTRY, NO OWNER: the lint says so once at launch, and nothing is stopped.
func TestAnUnlistedTreeIsLintedAndStopsNothing(t *testing.T) {
	treeLaunchHome(t, false)
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
			return map[string]TreeDelivery{treeKey: {Reason: "none"}}
		}
	})
	if d := patchedTreesInArgv(t, argv)[treeKey]; d.Stop || d.Owner != "" {
		t.Errorf("an unlisted tree stopped %q", d.Owner)
	}
	if !strings.Contains(printed, "no agent loads it") || !strings.Contains(printed, "~/"+treeInto) {
		t.Errorf("the launch does not lint the unlisted tree:\n%s", printed)
	}
}

// NEVER IN A CAPTURE OR BUILD JAIL: the switch that suppresses the store mount suppresses the arm,
// so a tree's build jail, which selects its pack, cannot start another.
func TestACaptureJailRunsNoTreeArm(t *testing.T) {
	treeLaunchHome(t, true)
	called := false
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.CapturesDir = func() string { return "" }
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery { called = true; return nil }
	})
	if called || patchedTreesInArgv(t, argv) != nil || strings.Contains(printed, "Patched extensions this launch") {
		t.Errorf("a capture jail ran the tree arm (called %v) or said its block:\n%s", called, printed)
	}
}

// BELOW APPLE CONTAINER'S READ-ONLY FLOOR the arm checks and builds nothing but still asks for a
// copy of what serves (§11), and no launcher is stopped there: that notch builds no tree.
func TestBelowTheAppleContainerFloorTheTreeArmBuildsNothingAndStopsNothing(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	o.CapturesDir = func() string { return "/store" }
	var req TreeBuildRequest
	o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
		req = r
		return map[string]TreeDelivery{treeKey: {Reason: "no build"}}
	}
	o.patchedTrees = []packload.Fork{{Pack: "treepack", Bin: "tree-ext", Into: treeInto, Owner: "agentpack", ListedInJail: true}}
	o.treeDelivered = o.treeDeliveriesFor("container")
	if req.Build || req.BuildFloor == "" {
		t.Errorf("below the floor the arm was asked to build (%+v)", req)
	}
	if d := o.patchedTreesWire("container")[treeKey]; d.Stop {
		t.Error("below the floor a tree with nothing to serve stopped its owner")
	}
	// Above it (podman), the same answer stops the owner.
	o.treeDelivered = map[string]TreeDelivery{treeKey: {Reason: "no build"}}
	if d := o.patchedTreesWire("podman")[treeKey]; !d.Stop {
		t.Error("on podman a tree with nothing to serve did not stop its owner")
	}
	// A guarded-only list entry reaches no jail, so nothing stops there either.
	o.patchedTrees[0].ListedInJail = false
	if d := o.patchedTreesWire("podman")[treeKey]; d.Stop {
		t.Error("a tree whose list entry reaches no jail stopped its owner")
	}
}

// IN A JAIL a nested launch delivers no tree and names the host, and stops nothing.
func TestANestedLaunchDeliversNoTreeAndNamesTheHost(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	o := goldenOptions("/ws", t.TempDir())
	o.CapturesDir = func() string { return "/store" }
	called := false
	o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery { called = true; return nil }
	o.patchedTrees = []packload.Fork{{Pack: "treepack", Bin: "tree-ext", Into: treeInto, Owner: "agentpack", ListedInJail: true}}
	o.treeDelivered = o.treeDeliveriesFor("podman")
	if called || !strings.Contains(o.treeDelivered[treeKey].Reason, "built on the host") {
		t.Errorf("in a jail: called %v, delivered %+v", called, o.treeDelivered)
	}
	var stderr strings.Builder
	o.Stderr = &stderr
	o.noteTreeDeliveries("podman")
	if !strings.Contains(stderr.String(), "is not mounted in this jail") || strings.Contains(stderr.String(), "YOLO_RUNTIME=podman") {
		t.Errorf("a nested launch's line = %q, want the host named and no runtime to switch to", stderr.String())
	}
	if o.patchedTreesWire("podman")[treeKey].Stop {
		t.Error("a nested launch, which builds no tree, stopped the owner")
	}
}

// THE COPIES GO WITH THEIR TREE, as its delivery record does.
func TestThePerLaunchCopiesGoWithTheirPackTree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree, err := newPackTree("yolo-tree-copies")
	if err != nil {
		t.Fatal(err)
	}
	copyDir := patchedTreeCopyDir(patchedCopiesDir(tree), treeKey)
	if err := os.MkdirAll(copyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	discardPackTree("yolo-tree-copies", tree)
	if _, err := os.Stat(patchedCopiesDir(tree)); err == nil {
		t.Error("the per-launch copies outlived their pack tree")
	}
}

// AN ATTACH names the build its jail mounts, from the record its launch left, and that a newer good
// build waits for the next fresh launch.
func TestAnAttachNamesTheTreeItsJailMounts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tree, err := newPackTree("yolo-tree-attach")
	if err != nil {
		t.Fatal(err)
	}
	if err := recordHandedTree(tree, treeKey, HandedTree{Entry: "e1", Into: treeInto, Commit: strings.Repeat("b", 40),
		Tag: "v1.0.0", Patches: 2}); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	o := &Options{Stderr: &stderr}
	o.noteAttachHandedBuilds(attachPackView{staged: stagedPacks{root: tree}})
	if !strings.Contains(stderr.String(), "this jail mounts extension "+treeKey+" at v1.0.0") ||
		!strings.Contains(stderr.String(), "2 patches") {
		t.Errorf("the attach line = %q", stderr.String())
	}
	// The fork half of the record is untouched by the tree half.
	if forks, err := readHandedForks(tree); err != nil || len(forks) != 0 {
		t.Errorf("the record's forks = %v (%v)", forks, err)
	}
}

// THE ATTACH'S CALL SITE: an attach through Run names what its jail mounts and that a newer good build
// waits, and runs no tree arm. Red when the attach stops calling noteAttachTreeBuilds.
func TestAnAttachThroughRunNamesTheTreeItsJailMounts(t *testing.T) {
	treeLaunchHome(t, true)
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		cname := yoloruntime.FromWorkspace(o.Workspace)
		tree, err := newPackTree(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeLivePackTree(cname, tree); err != nil {
			t.Fatal(err)
		}
		if err := recordHandedTree(tree, treeKey, HandedTree{Entry: "e-old", Into: treeInto,
			Commit: strings.Repeat("a", 40), Tag: "v1.0.0", Patches: 1}); err != nil {
			t.Fatal(err)
		}
		if err := patchedPacksStore().WithCheckRecord(treeKey, nil, func(r *packsrc.CheckRecord, _ error, _ func() error) (bool, error) {
			r.Good = &packsrc.GoodBuild{Commit: strings.Repeat("b", 40), Tag: "v1.1.0", Patches: 1, Entry: "e-new"}
			return true, nil
		}); err != nil {
			t.Fatal(err)
		}
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
			}
			return ExecResult{Ran: true, RC: 0}
		}
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			t.Error("an attach ran the tree arm")
			return nil
		}
	})
	if !strings.Contains(printed, "Attaching to existing jail") {
		t.Fatalf("the fixture did not attach:\n%s", printed)
	}
	want := "this jail mounts extension " + treeKey + " at v1.0.0 (aaaaaaaa) + 1 patch; v1.1.0 (bbbbbbbb) + 1 patch is " +
		"built, and the next fresh launch, once this jail stops, mounts it"
	if !strings.Contains(printed, want) {
		t.Errorf("the attach does not name the build its jail mounts:\n%s", printed)
	}
}

// MACOS-USER builds no tree (§11): its launch says so, naming a container backend. Red when the
// macos-user arm stops calling noteMacosUserTrees.
func TestAMacosUserLaunchSaysItDeliversNoTree(t *testing.T) {
	treeLaunchHome(t, true)
	out := launchToDispatch(t)
	if !strings.Contains(out, "extension "+treeKey+" is not delivered on macos-user") ||
		!strings.Contains(out, "YOLO_RUNTIME=podman") {
		t.Errorf("the macos-user launch does not name the undelivered extension:\n%s", out)
	}
}

// A DELIVERED TREE GETS A DIRECTORY MOUNTPOINT, in the skeleton or in the workspace overlay, and one
// with no copy gets none: an empty directory at `into` is one an agent may try to load (§9).
func TestOnlyADeliveredTreeGetsAMountpoint(t *testing.T) {
	p := &packload.Pack{Name: "treepack", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Into: treeInto, Source: "git+https://example.invalid/x?ref=main", Patches: "patches"}}}}
	copyDir := t.TempDir()
	dirs, files := packFilesSkeletonEntries([]*packload.Pack{p}, map[string]string{treeKey: copyDir})
	if len(dirs) != 1 || dirs[0] != treeInto || len(files) != 0 {
		t.Errorf("a delivered tree's skeleton entries = %v, %v; want the directory %s", dirs, files, treeInto)
	}
	if dirs, files := packFilesSkeletonEntries([]*packload.Pack{p}, nil); len(dirs)+len(files) != 0 {
		t.Errorf("an undelivered tree has skeleton entries %v, %v", dirs, files)
	}
	targets := packFilesTargets([]*packload.Pack{p}, map[string]string{treeKey: copyDir})
	if len(targets) != 1 || targets[0].Src != copyDir || targets[0].Tree != treeKey {
		t.Errorf("targets = %+v, want the copy at %s, never the pack's root", targets, copyDir)
	}
}

// THE SKELETON BUILDER takes the delivered trees: the mountpoint is made for one, never for none.
func TestTheHomeSkeletonMakesADeliveredTreesMountpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	p := &packload.Pack{Name: "treepack", Root: t.TempDir(), Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindFiles, Into: treeInto, Source: "git+https://example.invalid/x?ref=main", Patches: "patches"}}}}
	sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot("yolo-tree-skel"), []*packload.Pack{p}, nil, nil,
		map[string]string{treeKey: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(sk.dir, filepath.FromSlash(treeInto))); err != nil || !info.IsDir() {
		t.Errorf("the skeleton has no directory mountpoint for the delivered tree: %v", err)
	}
	none, err := buildHomeSkeleton(paths.HomeSkeletonRoot("yolo-tree-skel-none"), []*packload.Pack{p}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(none.dir, filepath.FromSlash(treeInto))); err == nil {
		t.Error("the skeleton made a mountpoint for a tree with no copy")
	}
}
