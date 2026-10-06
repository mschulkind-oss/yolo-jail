package run

// treenotch_test.go pins PPX-D35 and PPX-D36 (docs/design/patched-extensions.md) from Run's own entry
// point: a patched extension whose list entry reaches the host alone (a guarded posture list) is
// built and mounted in no jail and never stops the agent there, while the launch's block still names
// it; and a list entry under `~/<into>/` loads the tree, so the jail stops the owning agent when that
// tree has no build.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// treeLaunchHomeListing is treeLaunchHome with list, one contribution of the tree pack naming the tree
// (a config-list or an autonomy posture), in place of its config-list on `tool/settings`.
func treeLaunchHomeListing(t *testing.T, list string) {
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
		{"kind":"config","config":[{"agent":"tool","name":"settings","codec":"json","path":"~/.tool/settings.json"}]}]}`)
	write("treepack/pack.json", `{"contributes":[{"kind":"files","into":"`+treeInto+`",
		"source":"git+https://example.invalid/tree-ext?ref=main","patches":"patches"},`+list+`]}`)
	write("treepack/patches/0001-x.patch", "From 0123456789abcdef0123456789abcdef01234567 Mon Sep 17 00:00:00 2001\n"+
		"Subject: [PATCH] x\n\n---\n\ndiff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-a\n+b\n\n"+
		"base-commit: 0123456789abcdef0123456789abcdef01234567\n")
	writeUserPacks(t, home, `[{"source":"file://`+filepath.Join(packs, "agentpack")+`","name":"agentpack"},`+
		`{"source":"file://`+filepath.Join(packs, "treepack")+`","name":"treepack"}]`)
}

// postureListing is an autonomy contribution whose posture's list on `tool/settings` holds entry.
func postureListing(posture, entry string) string {
	return `{"kind":"autonomy","` + posture + `":{"lists":[{"surface":"tool/settings","path":"/packages","add":["` +
		entry + `"]}]}}`
}

// PPX-D35 AT ITS CALL SITE: a tree listed only in the guarded posture is handed to no tree arm, so it
// is never built for a jail, nothing is mounted at `into`, the jail is told nothing of it (so nothing
// stops), and the launch's block names it with where it goes instead. Red if notePatchedTrees stops
// returning only the trees a jail is delivered.
func TestAGuardedOnlyTreeIsNeitherBuiltNorMountedInAJail(t *testing.T) {
	asHostThatBuildsTrees(t, true)
	treeLaunchHomeListing(t, postureListing("guarded", "~/"+treeInto))
	called := false
	argv, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
			called = len(r.Trees) != 0
			return nil
		}
	})
	if argv == nil {
		t.Fatalf("the runtime was never run:\n%s", printed)
	}
	if called {
		t.Errorf("a tree no jail loads was handed to the tree arm:\n%s", printed)
	}
	if m := mountsAt(argv, treeInto); len(m) != 0 {
		t.Errorf("a tree no jail loads was mounted: %q", m)
	}
	if d, ok := patchedTreesInArgv(t, argv)[treeKey]; ok {
		t.Errorf("the jail was told of a tree it does not load: %+v", d)
	}
	if !strings.Contains(printed, "extension "+treeKey+": ~/"+treeInto) ||
		!strings.Contains(printed, packload.NotDeliveredInJailNote) {
		t.Errorf("the launch's block does not say the tree goes to the host alone:\n%s", printed)
	}
	if strings.Contains(printed, "is not mounted in this jail") {
		t.Errorf("the launch warns of a tree no jail loads:\n%s", printed)
	}
}

// The other two listings still reach a jail: an autonomous posture list and a config-list are handed
// to the tree arm, and with nothing to serve the owning agent stops (PPX-D18). PPX-D36: the
// config-list's entry is a path inside the tree, a monorepo's one package, and still loads it.
func TestATreeListedForJailsIsDeliveredAndAnEntryUnderItLoadsIt(t *testing.T) {
	for _, list := range []string{
		postureListing("autonomous", "~/"+treeInto),
		`{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/` + treeInto + `/packages/a"]}`,
		`{"kind":"config-list","surface":"tool/settings","path":"/packages","add":["~/` + treeInto + `/"]}`,
	} {
		treeLaunchHomeListing(t, list)
		var req TreeBuildRequest
		argv, printed := fakePodmanLaunch(t, func(o *Options) {
			allowMissingPrograms(o) // the launch goes on, to the launchers' stop (PPX-D40)
			o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
				req = r
				return deliverNoTree(r)
			}
		})
		if len(req.Trees) != 1 || req.Trees[0].Key() != treeKey || req.Trees[0].Owner != "agentpack" {
			t.Errorf("listing %s: the tree arm was handed %+v\n%s", list, req.Trees, printed)
			continue
		}
		if d := patchedTreesInArgv(t, argv)[treeKey]; !d.Stop || d.Owner != "agentpack" {
			t.Errorf("listing %s: the jail is handed %+v, want its owner stopped", list, d)
		}
		if strings.Contains(printed, "no agent loads it") {
			t.Errorf("listing %s: the launch lints a listed tree:\n%s", list, printed)
		}
	}
}

// MACOS-USER says nothing of a tree no jail loads: its agent would not load it on any backend. Red if
// the macos-user arm's line stops reading the trees a jail is delivered. macos-user runs on a macOS
// host, which builds no tree for itself, so the block's line names the step that works (PPX-D38).
func TestAMacosUserLaunchIsSilentOnAGuardedOnlyTree(t *testing.T) {
	asHostThatBuildsTrees(t, false)
	treeLaunchHomeListing(t, postureListing("guarded", "~/"+treeInto))
	out := launchToDispatch(t)
	if strings.Contains(out, "is not delivered on macos-user") {
		t.Errorf("the macos-user launch warns of a tree no jail loads:\n%s", out)
	}
	if !strings.Contains(out, packload.NotDeliveredAnywhereNote) {
		t.Errorf("the macos-user launch's block does not name the tree:\n%s", out)
	}
}

// ON A HOST THAT BUILDS NO TREE (macOS), a guarded-only tree reaches no notch that has it, so the
// block names moving the entry, never `yolo host apply --assert`, which refuses it there.
func TestAGuardedOnlyTreeOnAMacOSHostNamesAStepThatWorks(t *testing.T) {
	asHostThatBuildsTrees(t, false)
	treeLaunchHomeListing(t, postureListing("guarded", "~/"+treeInto))
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery { return nil }
	})
	if !strings.Contains(printed, "extension "+treeKey+": ~/"+treeInto) ||
		!strings.Contains(printed, packload.NotDeliveredAnywhereNote) || strings.Contains(printed, "yolo host apply --assert") {
		t.Errorf("the block on a macOS host:\n%s", printed)
	}
}

// asHostThatBuildsTrees makes this test's host one whose own render builds trees (Linux), or not.
func asHostThatBuildsTrees(t *testing.T, builds bool) {
	t.Helper()
	prev := hostBuildsOwnTrees
	hostBuildsOwnTrees = func() bool { return builds }
	t.Cleanup(func() { hostBuildsOwnTrees = prev })
}
