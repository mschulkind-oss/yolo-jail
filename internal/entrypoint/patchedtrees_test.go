package entrypoint

// patchedtrees_test.go pins the jail's half of a PATCHED EXTENSION (docs/design/patched-extensions.md
// §8.1, §9; PPX-D8, PPX-D18): GenerateAgentLaunchers reads YOLO_PATCHED_TREES once, and the launchers
// of the pack that owns a tree the host says has nothing to serve stop before exec, naming the
// extension and the host's reason — the npm launcher, the native one and a fork's source launcher alike — while
// every other pack's launchers, and the owner's once a build serves, start as before.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

const stoppedTree = `{"matt/pi-subagents":{"into":".pi/agent/yolo-patched/pi-subagents",` +
	`"reason":"extension matt/pi-subagents has no build on this machine yet — the next fresh launch builds it",` +
	`"owner":"pi","stop":true}}`

// TestTheOwningAgentsLauncherStopsBeforeExecWithNoBuild: the forked pi's source launcher, handed a
// build of pi itself, still stops before exec when a tree pi loads has none, and says why. Red if
// GenerateAgentLaunchers stops reading PatchedTreesEnv, stops setting the gate, or the template
// loses it.
func TestTheOwningAgentsLauncherStopsBeforeExecWithNoBuild(t *testing.T) {
	home, launcher, fakeBin, _ := forkLauncherWithTrees(t, `{"pi":{"key":"k1"}}`, stoppedTree)
	out, rc := runForkLauncher(t, home, launcher, fakeBin)
	if rc == 0 || strings.Contains(out, "FORK_BUILD_RAN") {
		t.Fatalf("rc=%d: the owner's launcher ran pi with no build of a tree it loads:\n%s", rc, out)
	}
	for _, w := range []string{"extension matt/pi-subagents", "~/.pi/agent/yolo-patched/pi-subagents",
		"has no build on this machine yet", "the shell is unaffected"} {
		if !strings.Contains(out, w) {
			t.Errorf("the stop does not name %q:\n%s", w, out)
		}
	}
	// A BUILD THAT SERVES starts pi.
	served := strings.Replace(strings.Replace(stoppedTree, `"stop":true`, `"stop":false`, 1),
		`"reason":"extension matt/pi-subagents has no build on this machine yet — the next fresh launch builds it",`,
		`"build":"e1",`, 1)
	home, launcher, fakeBin, _ = forkLauncherWithTrees(t, `{"pi":{"key":"k1"}}`, served)
	if out, rc := runForkLauncher(t, home, launcher, fakeBin); rc != 0 || !strings.Contains(out, "FORK_BUILD_RAN k1") {
		t.Errorf("rc=%d, with the tree served pi did not start:\n%s", rc, out)
	}
	// ANOTHER PACK'S TREE gates nothing of pi's.
	other := strings.Replace(stoppedTree, `"owner":"pi"`, `"owner":"claude"`, 1)
	home, launcher, fakeBin, _ = forkLauncherWithTrees(t, `{"pi":{"key":"k1"}}`, other)
	if out, rc := runForkLauncher(t, home, launcher, fakeBin); rc != 0 || !strings.Contains(out, "FORK_BUILD_RAN k1") {
		t.Errorf("rc=%d, another agent's tree stopped pi:\n%s", rc, out)
	}
}

// THE NPM LAUNCHER carries the same gate, with the host's reason baked into it, and an owner with
// no stopped tree carries an empty one.
func TestTheNpmLauncherCarriesTheTreeGate(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pi"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pi", "pack.json"),
		[]byte(`{"name":"pi","contributes":[{"kind":"program","bin":"pi","via":"npm","package":"pi-coding-agent"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		trees string
		gated bool
	}{{stoppedTree, true}, {"", false}} {
		home := t.TempDir()
		e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": root, PatchedTreesEnv: tc.trees})
		e.Stderr = &bytes.Buffer{}
		if err := GenerateAgentLaunchers(e); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(e.LaunchDir(), "pi"))
		if err != nil {
			t.Fatal(err)
		}
		has := strings.Contains(string(body), "has no build on this machine yet")
		if has != tc.gated || !strings.Contains(string(body), `if [ -n "$TREE_GATE" ]; then`) {
			t.Errorf("trees %q: the npm launcher's gate carries the reason = %v, want %v", tc.trees, has, tc.gated)
		}
		if !tc.gated && !strings.Contains(string(body), "TREE_GATE=''") {
			t.Error("an ungated launcher's TREE_GATE is not empty")
		}
	}
}

// forkLauncherWithTrees is forkLauncher with the host's patched-extension decisions too.
func forkLauncherWithTrees(t *testing.T, deliveries, trees string) (home, launcher, fakeBin, argvLog string) {
	t.Helper()
	t.Setenv(PatchedTreesEnv, "") // the env a test runs under is never the jail's
	home, _, fakeBin, argvLog = forkLauncher(t, deliveries)
	e := NewEnv(map[string]string{
		"JAIL_HOME": home, "YOLO_WORKSPACE": filepath.Join(home, "ws"), "YOLO_PACK_ROOT": forkPackTree(t),
		CapturesDirEnv: t.TempDir(), ForkBuildsEnv: deliveries, PatchedTreesEnv: trees,
	})
	e.Stderr = &bytes.Buffer{}
	if err := GenerateAgentLaunchers(e); err != nil {
		t.Fatal(err)
	}
	return home, filepath.Join(e.LaunchDir(), "pi"), fakeBin, argvLog
}

// THE NATIVE LAUNCHER carries the gate too (PPX-D24): an owner whose program installs through a
// vendor installer stops before exec, naming the extension and the host's reason, while a tree it
// loads has nothing to serve, and runs its program with no tree stopped. Red if nativeAgentLauncher
// stops splicing the install's gate.
func TestTheNativeLauncherStopsBeforeExecWithNoBuild(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	url := installerURL(t, "#!/bin/bash\nexit 0\n")
	owned := strings.Replace(stoppedTree, `"owner":"pi"`, `"owner":"acme"`, 1)
	for _, tc := range []struct {
		trees string
		stop  bool
	}{{owned, true}, {"", false}} {
		home := t.TempDir()
		packRoot := filepath.Join(t.TempDir(), "packs")
		writeTestFile(t, filepath.Join(packRoot, "acme", "pack.json"),
			`{"name":"acme","contributes":[{"kind":"program","bin":"nat","via":"installer","url":"`+url+`"}]}`)
		e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot,
			"YOLO_WORKSPACE": filepath.Join(home, "ws"), PatchedTreesEnv: tc.trees})
		e.Stderr = &bytes.Buffer{}
		if err := GenerateAgentLaunchers(e); err != nil {
			t.Fatal(err)
		}
		realBin := filepath.Join(home, ".local", "bin", "nat")
		writeTestFile(t, realBin, "#!/bin/sh\necho NATIVE_RAN\n")
		if err := os.Chmod(realBin, 0o755); err != nil {
			t.Fatal(err)
		}
		out, rc := runGeneratedLauncher(t, e, os.Getenv("PATH"), "nat")
		ran := strings.Contains(out, "NATIVE_RAN")
		if tc.stop {
			if rc == 0 || ran {
				t.Fatalf("rc=%d: the native launcher ran its program with no build of a tree it loads:\n%s", rc, out)
			}
			for _, w := range []string{"extension matt/pi-subagents", "has no build on this machine yet"} {
				if !strings.Contains(out, w) {
					t.Errorf("the native launcher's stop does not name %q:\n%s", w, out)
				}
			}
		} else if rc != 0 || !ran {
			t.Errorf("rc=%d: with no tree stopped the native launcher did not run its program:\n%s", rc, out)
		}
	}
}

// A TREE'S BUILD JAIL SAYS NO ORPHANED LIST (TreeBuildEnv): its seal selects the contributing pack
// alone (PPX-D5), so the pack's own entry naming `~/<into>` has no owner there by construction, and
// telling the user to check a correct identity is wrong. Every other jail still names the orphan.
func TestATreesBuildJailNamesNoOrphanedList(t *testing.T) {
	overlays := &packoverlay.OverlaySet{Orphans: []packoverlay.OrphanOverlay{{Kind: packdecl.KindConfigList,
		Pack: "treepack", Target: "tool/settings"}}}
	for _, tc := range []struct {
		tree string
		said bool
	}{{"tool-ext", false}, {"", true}} {
		var stderr bytes.Buffer
		e := NewEnv(map[string]string{TreeBuildEnv: tc.tree})
		e.Stderr = &stderr
		reportOverlayResolution(e, overlays)
		if said := strings.Contains(stderr.String(), "no effect"); said != tc.said {
			t.Errorf("%s=%q: the orphan is said %v, want %v:\n%s", TreeBuildEnv, tc.tree, said, tc.said, stderr.String())
		}
	}
}
