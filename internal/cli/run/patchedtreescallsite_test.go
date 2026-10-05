package run

// patchedtreescallsite_test.go pins the launch pipeline's patched-extension CALL SITES from Run's own
// entry point (docs/design/patched-extensions.md §8.1, §9), where patchedtrees_test.go pins most of
// the callees: the line a launch that delivers no tree says for each; the delivery record a fresh
// launch writes, read back by an attach to its jail; the delivered tree's mountpoint in the home
// skeleton and in the workspace state, retired once nothing is delivered there; and the staging
// refusal of a tree mounted over a config surface. Each test fails when its call site is deleted.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// deliverATreeCopy is a tree arm that delivers treeKey's per-launch copy at v1.0.0 + 1 patch.
func deliverATreeCopy(t *testing.T) func(TreeBuildRequest) map[string]TreeDelivery {
	return func(r TreeBuildRequest) map[string]TreeDelivery {
		dir := PatchedTreeCopyDir(r.CopyRoot, treeKey)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return map[string]TreeDelivery{treeKey: {Dir: dir, Entry: "e1", Commit: strings.Repeat("a", 40), Tag: "v1.0.0", Patches: 1}}
	}
}

// deliverNoTree is a tree arm with nothing to deliver.
func deliverNoTree(r TreeBuildRequest) map[string]TreeDelivery {
	return map[string]TreeDelivery{treeKey: {Reason: "extension " + treeKey + " has no build on this machine yet"}}
}

// THE UNDELIVERED LINE'S CALL SITE (§9): a nested launch through Run delivers no tree and asks the
// tree arm for nothing, and says so once for each extension, naming the host. Red if Run stops
// calling noteTreeDeliveries.
func TestANestedLaunchThroughRunSaysItMountsNoTree(t *testing.T) {
	treeLaunchHome(t, true)
	t.Setenv("YOLO_VERSION", "test")
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			t.Error("a nested launch ran the tree arm")
			return nil
		}
	})
	if !strings.Contains(printed, "Warning: extension "+treeKey+" is not mounted in this jail") ||
		!strings.Contains(printed, "built on the host") || !strings.Contains(printed, "the agent starts without it") {
		t.Errorf("a nested launch does not say it mounts no tree:\n%s", printed)
	}
}

// THE RECORD'S WRITER AT ITS CALL SITE (§8.1 step 4): what a fresh launch hands its jail for a
// patched extension is what an attach to that jail names, from the record that launch wrote and no
// record written by hand. Red if treeDeliveriesFor stops recording what it handed.
func TestAnAttachNamesTheTreeAFreshLaunchHanded(t *testing.T) {
	treeLaunchHome(t, true)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	saved := t.TempDir()
	// The delivery record goes with its pack tree once the launch ends, so the fake podman keeps a
	// copy at its `run`, while the jail it starts would hold it.
	onRun := "cp " + shquote.Quote(paths.PackTreeRoot(cname)) + "/*" + handedForksSuffix + " " + shquote.Quote(saved) +
		"/ 2>/dev/null || true"
	fakePodmanLaunchIn(t, ws, onRun, func(o *Options) { o.BuildTrees = deliverATreeCopy(t) })
	records, _ := filepath.Glob(filepath.Join(saved, "*"+handedForksSuffix))
	if len(records) != 1 {
		t.Fatalf("the fresh launch's jail started with %d delivery records, want 1", len(records))
	}
	record, err := os.ReadFile(records[0])
	if err != nil {
		t.Fatal(err)
	}
	// THE ATTACH, to the jail that launch started: its pack tree, and the record the launch wrote.
	_, printed := fakePodmanLaunchIn(t, ws, "", func(o *Options) {
		tree, err := newPackTree(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeLivePackTree(cname, tree); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(handedForksPath(tree), record, 0o644); err != nil {
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
	if want := "this jail mounts extension " + treeKey + " at v1.0.0 (aaaaaaaa) + 1 patch"; !strings.Contains(printed, want) {
		t.Errorf("the attach does not name the build the fresh launch handed:\n%s", printed)
	}
}

// THE SKELETON'S CALL SITE (§9): a fresh podman launch's home skeleton holds a directory mountpoint
// for the tree it delivers, where `into` lies outside every writable dir, and none for a tree it
// does not deliver. Red if Run stops handing buildHomeSkeleton the delivered trees.
func TestAFreshLaunchsSkeletonHoldsTheDeliveredTreesMountpoint(t *testing.T) {
	treeLaunchHome(t, true)
	for _, delivered := range []bool{true, false} {
		marker := filepath.Join(t.TempDir(), "skeleton-has-tree")
		onRun := `for a in "$@"; do case "$a" in *:/home/agent:ro) [ -d "${a%:/home/agent:ro}"/` + shquote.Quote(treeInto) +
			` ] && : > ` + shquote.Quote(marker) + `;; esac; done`
		fakePodmanLaunchIn(t, t.TempDir(), onRun, func(o *Options) {
			o.BuildTrees = deliverNoTree
			if delivered {
				o.BuildTrees = deliverATreeCopy(t)
			}
		})
		if _, err := os.Stat(marker); (err == nil) != delivered {
			t.Errorf("delivered %v: the skeleton holds a mountpoint at ~/%s = %v", delivered, treeInto, err == nil)
		}
	}
}

// THE WORKSPACE OVERLAY'S CALL SITE (§9's empty-directory hazard): a delivered tree whose `into` lies
// under a pack-declared workspace state dir gets its mountpoint in the workspace state, recorded as
// yolo's; a later launch with no copy to mount retires it, so the agent never meets an empty
// directory at `into`. Red if Run stops handing preparePackFiles the delivered trees.
func TestADeliveredTreesWorkspaceMountpointIsMadeAndRetired(t *testing.T) {
	treeLaunchHomeAt(t, true, treeInto, `,{"kind":"state","at":".tool","scope":"workspace"}`)
	ws := t.TempDir()
	mountpoint := filepath.Join(paths.WorkspaceHomeState(ws), "tool", "ext", "tree-ext")
	manifest := filepath.Join(paths.WorkspaceStateDir(ws), packFilesMountpointManifestName)
	_, printed := fakePodmanLaunchIn(t, ws, "", func(o *Options) { o.BuildTrees = deliverATreeCopy(t) })
	if info, err := os.Stat(mountpoint); err != nil || !info.IsDir() {
		t.Fatalf("the delivered tree has no mountpoint in the workspace state (%v):\n%s", err, printed)
	}
	if _, ok := loadPackFilesMountpointManifest(manifest).Entries[filepath.Join("tool", "ext", "tree-ext")]; !ok {
		t.Errorf("the mountpoint is not recorded as yolo's in %s", manifest)
	}
	_, printed = fakePodmanLaunchIn(t, ws, "", func(o *Options) { o.BuildTrees = deliverNoTree })
	if _, err := os.Lstat(mountpoint); err == nil {
		t.Errorf("a launch with no copy to mount left the empty mountpoint at ~/%s:\n%s", treeInto, printed)
	}
}

// A PATCHED EXTENSION OVER A CONFIG SURFACE is refused at staging, as any `files` tree is: its
// `into` is mounted read-only by any launch that delivers it, over a surface an agent writes. Red if
// packFilesShadowedSurfaces stops counting patched extensions, or staging stops calling it.
func TestAPatchedExtensionOverAConfigSurfaceIsRefused(t *testing.T) {
	treeLaunchHomeAt(t, false, ".tool", "")
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(TreeBuildRequest) map[string]TreeDelivery {
			t.Error("a launch refused at staging ran the tree arm")
			return nil
		}
	})
	if !strings.Contains(printed, "pack treepack claims ~/.tool as a `files` tree") ||
		!strings.Contains(printed, "tool/settings at ~/.tool/settings.json") {
		t.Errorf("a patched extension over a config surface was not refused:\n%s", printed)
	}
}

// AN UNREADABLE DELIVERY RECORD is said ONCE by an attach, for the forks and the patched extensions
// it holds alike, with what follows — never once per half, and never with no next step.
func TestAnAttachSaysAnUnreadableDeliveryRecordOnce(t *testing.T) {
	treeLaunchHome(t, true)
	ws := t.TempDir()
	cname := yoloruntime.FromWorkspace(ws)
	_, printed := fakePodmanLaunchIn(t, ws, "", func(o *Options) {
		tree, err := newPackTree(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeLivePackTree(cname, tree); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(handedForksPath(tree), []byte("{not json"), 0o644); err != nil {
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
	})
	if !strings.Contains(printed, "Attaching to existing jail") {
		t.Fatalf("the fixture did not attach:\n%s", printed)
	}
	if n := strings.Count(printed, "could not read what this jail was handed"); n != 1 {
		t.Errorf("the unreadable record is said %d times, want once:\n%s", n, printed)
	}
	if !strings.Contains(printed, "the next fresh launch, once this jail stops, writes a new one") {
		t.Errorf("the unreadable record's line names no next step:\n%s", printed)
	}
}

// A PATCHED EXTENSION IS DISCLOSED AT EVERY LAUNCH (PPX-D15): its review-marked claim's sentence —
// the upstream's new code arriving unreviewed, which the agent loading the tree runs — prints with
// the launch's disclosure, as a patched fork's program claim does, and is classified per claim.
func TestAPatchedExtensionsClaimIsDisclosedAtLaunch(t *testing.T) {
	treeLaunchHome(t, true)
	_, printed := fakePodmanLaunch(t, func(o *Options) { o.BuildTrees = deliverATreeCopy(t) })
	if !strings.Contains(printed, "DELIVERS a tree built from source at ~/"+treeInto) ||
		!strings.Contains(printed, "UPSTREAM'S NEW CODE ARRIVES UNREVIEWED") {
		t.Errorf("the launch does not disclose its patched extension's claim:\n%s", printed)
	}
}
