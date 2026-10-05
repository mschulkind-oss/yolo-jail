package integration

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packbinaryseed_test.go is the integration harness's half of docs/design/broker-as-a-pack.md
// §14.4 step 7 (BP-D15, OQ-BP7 ruled 2026-10-05: main pins its own build). The suite builds
// the `yolo` under test from this tree, so the manifests that yolo embeds pin this tree's build
// of each official pack program, and no release publishes those bytes. A launch never fetches,
// so without this step every test of an official loophole that runs a program would find its
// build "not fetched" and the loophole off.
//
// So, once per run, the pin tool's `seed` verb builds this machine's builds of every official
// program from the tree and admits each whose digest is the pin to the pack-binary cache the
// run's isolated homes read — the same step `just install` takes for a from-source host. It runs
// WITHOUT --repin: a test run never writes the tree's manifests. A pin the tree no longer
// reproduces is not seeded, and the run says so as DEGRADED rather than failing: `just check-ci`
// is the gate that refuses it, naming the fix.
//
// With no official binary declared (the tree today), the tool says so, builds nothing and fetches
// no toolchain.

// seedPackBinaries seeds the run's pack-binary cache with this tree's official builds. Never
// fatal: a test that needs a build reports its own diagnosis against its own assertions.
func seedPackBinaries() {
	home := os.Getenv("HOME")
	if home == "" {
		degraded("HOME is unset, so the run has no pack-binary cache to seed; a test of an " +
			"official loophole's program will find its build not fetched")
		return
	}
	dir := packBinarySeedDir(home)
	out, err := packBinarySeedCmd(repoRoot, dir).CombinedOutput()
	if err != nil {
		degraded("seeding this tree's official pack programs into %s: %v\n%s\n— a test of an "+
			"official loophole's program will find its build not fetched; `just check-ci` names "+
			"the fix for a stale pin", dir, err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("[integration] %s", strings.TrimSpace(string(out)))
}

// packBinarySeedDir is the pack-binary cache an isolated home of this run reads, for the machine
// home it stands in for: under the run's own store when there is one (sharedStoreTarget), which
// every isolated home's state dir links to, and the machine's otherwise.
func packBinarySeedDir(machineHome string) string {
	rel, err := filepath.Rel(paths.GlobalStorageUnder(machineHome), paths.PackBinariesDirUnder(machineHome))
	if err != nil || strings.HasPrefix(rel, "..") {
		// The cache is not inside the store, so no link reaches it: seed where yolo reads it.
		return paths.PackBinariesDirUnder(machineHome)
	}
	return filepath.Join(sharedStoreTarget(machineHome, yoloStoreRel), rel)
}

// packBinarySeedCmd is the pin tool's seed into dir, run from the checkout at root.
func packBinarySeedCmd(root, dir string) *exec.Cmd {
	cmd := exec.Command("go", "run", "./tools/pack-binaries", "seed", dir)
	cmd.Dir = root
	return cmd
}

// The seed lands where an isolated home of this run looks: the run store's cache when the run
// has a store, the machine's when it fell back. Under -short, no container.
func TestPackBinarySeedTargetsTheCacheTheRunsHomesRead(t *testing.T) {
	machineHome := resolvedTempDir(t)
	saved := runStore
	t.Cleanup(func() { runStore = saved })

	runStore.machineHome, runStore.dir = "", ""
	if got, want := packBinarySeedDir(machineHome), paths.PackBinariesDirUnder(machineHome); got != want {
		t.Errorf("with no run store, the seed goes to %s, want the machine's cache %s", got, want)
	}

	runStore.machineHome, runStore.dir = machineHome, filepath.Join(resolvedTempDir(t), "yolo-jail")
	want := filepath.Join(runStore.dir, filepath.Base(paths.PackBinariesDirUnder(machineHome)))
	if got := packBinarySeedDir(machineHome); got != want {
		t.Errorf("with a run store, the seed goes to %s, want the run store's cache %s", got, want)
	}
	// What an isolated home reads through its link is that same directory.
	home := resolvedTempDir(t)
	if err := seedPackHome(home, machineHome, "{}"); err != nil {
		t.Fatal(err)
	}
	if got, err := filepath.EvalSymlinks(filepath.Dir(paths.PackBinariesDirUnder(home))); err != nil ||
		got != filepath.Dir(want) {
		t.Errorf("an isolated home's store resolves to %s (%v), and the seed went under %s", got, err,
			filepath.Dir(want))
	}

	cmd := packBinarySeedCmd("/the/checkout", want)
	if strings.Join(cmd.Args, " ") != "go run ./tools/pack-binaries seed "+want || cmd.Dir != "/the/checkout" {
		t.Errorf("the seed runs %q in %s", cmd.Args, cmd.Dir)
	}
	for _, a := range cmd.Args {
		if a == "--repin" {
			t.Error("the harness re-pins, which writes the tree's manifests during a test run")
		}
	}
}
