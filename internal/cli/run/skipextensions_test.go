package run

// skipextensions_test.go pins YOLO_SKIP_EXTENSIONS and cached-good for extensions at a launch's call
// sites (docs/reference/pi-extensions.md §9.1, patched-forks.md §8.5), from Run's own entry point: a fresh
// launch hands the tree arm no skipped extension, mounts nothing for it, marks it skipped on the
// wire with no stop and says so in one line; a name covering nothing warns; an attach and a nested
// launch each say the hatch does nothing there (a macos-user launch acts on it:
// macosusertreeslot_test.go); and YOLO_USE_CACHED_GOOD
// naming an extension reaches the tree arm.

import (
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/patchrecovery"
)

func TestASkippedExtensionIsNotBuiltMountedOrStopped(t *testing.T) {
	for _, name := range []string{"1", treeKey, "treepack", "tree-ext"} {
		t.Run(name, func(t *testing.T) {
			treeLaunchHome(t, true)
			argv, printed := fakePodmanLaunch(t, func(o *Options) {
				withEnv(o, patchrecovery.SkipExtensionsEnv, name)
				// The missing-build refusal is not this test's: it is set aside here.
				allowMissingPrograms(o)
				o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
					for _, f := range r.Trees {
						if f.Key() == treeKey {
							t.Error("the tree arm was handed a skipped extension")
						}
					}
					return nil
				}
			})
			if argv == nil {
				t.Fatalf("the launch did not reach the container:\n%s", printed)
			}
			if m := mountsAt(argv, treeInto); len(m) != 0 {
				t.Errorf("a skipped extension mounted %q", m)
			}
			d := patchedTreesInArgv(t, argv)[treeKey]
			if !d.Skipped || d.Stop || d.Build != "" {
				t.Errorf("the jail is handed %+v, want it skipped with no stop", d)
			}
			want := "extension " + treeKey + ": skipped (YOLO_SKIP_EXTENSIONS); agentpack starts without it"
			if strings.Count(printed, want) != 1 {
				t.Errorf("the launch does not say %q once:\n%s", want, printed)
			}
		})
	}
}

// A NAME THAT COVERS NOTHING warns, and the extension is delivered as ever.
func TestASkipNameCoveringNothingWarnsAndSkipsNothing(t *testing.T) {
	treeLaunchHome(t, true)
	called := false
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		withEnv(o, patchrecovery.SkipExtensionsEnv, "nosuch")
		allowMissingPrograms(o)
		o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
			called = len(r.Trees) == 1 && r.Trees[0].Key() == treeKey
			return nil
		}
	})
	if !called {
		t.Errorf("the extension was not handed to the tree arm:\n%s", printed)
	}
	if !strings.Contains(printed, "Warning: YOLO_SKIP_EXTENSIONS names nosuch, which matches no patched build here "+
		"(patched builds: "+treeKey+")") {
		t.Errorf("no unmatched-name warning:\n%s", printed)
	}
}

// AN ATTACH keeps what its jail booted with, and says when the hatch applies.
func TestAnAttachSaysTheSkipAppliesAtTheNextFreshLaunch(t *testing.T) {
	treeLaunchHome(t, true)
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		withEnv(o, patchrecovery.SkipExtensionsEnv, "1")
		cname := o.jailName()
		tree, err := newPackTree(cname)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeLivePackTree(cname, tree); err != nil {
			t.Fatal(err)
		}
		o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
			joined := strings.Join(argv, " ")
			switch {
			case len(argv) >= 2 && argv[1] == "ps" && strings.Contains(joined, "name=^/"+cname+"$"):
				return ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}
			case len(argv) >= 2 && argv[1] == "inspect":
				return ExecResult{Ran: true, RC: 0, Stdout: podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n" + entrypointContractTagsLine() + "\n"}
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
	if !strings.Contains(printed, "YOLO_SKIP_EXTENSIONS: this jail keeps its extensions; it applies at the next fresh launch") {
		t.Errorf("the attach does not say the hatch waits for a fresh launch:\n%s", printed)
	}
}

// IN A JAIL the tree arm delivers nothing, so the hatch does nothing, and says so.
func TestANestedLaunchSaysTheSkipHasNoEffect(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	o := goldenOptions("/ws", t.TempDir())
	o.CapturesDir = func() string { return "/store" }
	o.Getenv = func(k string) string {
		if k == patchrecovery.SkipExtensionsEnv {
			return "1"
		}
		return ""
	}
	var stderr strings.Builder
	o.Stderr = &stderr
	o.patchedTrees = []packload.Fork{{Pack: "treepack", Bin: "tree-ext", Into: treeInto, Owner: "agentpack", ListedInJail: true}}
	o.treeDelivered = o.treeDeliveriesFor("podman")
	if !strings.Contains(stderr.String(), "YOLO_SKIP_EXTENSIONS: no effect in a jail") || o.treeDelivered[treeKey].Skipped {
		t.Errorf("a nested launch: %q, delivered %+v", stderr.String(), o.treeDelivered)
	}
}

// CACHED GOOD FOR AN EXTENSION (§6.7): YOLO_USE_CACHED_GOOD naming the extension — by key, by pack,
// or `1` — reaches the tree arm's request, and no fork is asked for.
func TestCachedGoodNamingAnExtensionReachesTheTreeArm(t *testing.T) {
	for _, name := range []string{treeKey, "treepack", "1"} {
		t.Run(name, func(t *testing.T) {
			treeLaunchHome(t, true)
			var got []string
			_, printed := fakePodmanLaunch(t, func(o *Options) {
				o.Getenv = cachedGoodEnv(name)
				o.BuildTrees = func(r TreeBuildRequest) map[string]TreeDelivery {
					got = r.CachedGood
					return nil
				}
			})
			if len(got) != 1 || got[0] != treeKey {
				t.Errorf("the tree arm's cached-good keys = %q, want [%s]:\n%s", got, treeKey, printed)
			}
			if strings.Contains(printed, "Refusing cached-good recovery") {
				t.Errorf("an extension key was refused:\n%s", printed)
			}
		})
	}
}

// A SINGLE `<pack>/<name>` CACHED-GOOD KEY COVERING NOTHING SELECTED warns and goes on, as every
// hatch's unmatched name does (§8.5): a hatch left in a shell profile never stops a workspace that
// does not select the pack it names. Red with selectCachedGood refusing an exact-key typo.
func TestASingleCachedGoodKeyCoveringNothingWarnsAndLaunches(t *testing.T) {
	treeLaunchHome(t, true)
	var got []string
	slotRan := false
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Getenv = cachedGoodEnv("otherpack/other")
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			slotRan = true
			if req.Trees != nil {
				got = req.Trees.CachedGood
			}
			return nil, nil
		}
	})
	if strings.Contains(printed, "Refusing cached-good recovery") || !slotRan || len(got) != 0 {
		t.Errorf("an unmatched single key refused or selected something (slot ran %v, keys %q):\n%s", slotRan, got, printed)
	}
	if !strings.Contains(printed, "Warning: YOLO_USE_CACHED_GOOD names otherpack/other, which matches no patched build here") {
		t.Errorf("no unmatched-name warning:\n%s", printed)
	}
}

// A CACHED-GOOD NAME COVERING NOTHING, among names that cover something, warns and goes on.
func TestCachedGoodWarnsForANameCoveringNothing(t *testing.T) {
	treeLaunchHome(t, true)
	var got []string
	_, printed := fakePodmanLaunch(t, func(o *Options) {
		o.Getenv = cachedGoodEnv(treeKey + ",nosuch")
		o.BuildSlot = func(req BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]TreeDelivery) {
			if req.Trees != nil {
				got = req.Trees.CachedGood
			}
			return nil, nil
		}
	})
	if len(got) != 1 || got[0] != treeKey {
		t.Errorf("the slot's cached-good keys = %q:\n%s", got, printed)
	}
	if !strings.Contains(printed, "Warning: YOLO_USE_CACHED_GOOD names nosuch, which matches no patched build here") {
		t.Errorf("no unmatched-name warning:\n%s", printed)
	}
}
