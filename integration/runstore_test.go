package integration

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// runstore_test.go gives each `go test ./integration` process a yolo state directory of ITS
// OWN, which every isolated home in the run links to instead of the machine's — and links
// back, child by child, the few parts of the machine's that must stay shared.
//
// # Why the whole-store link was not enough
//
// isolateHome used to link $HOME/.local/share/yolo-jail straight to the machine's store
// (packHomeSharedStores). Inside one run that is harmless, because the run's launches take
// turns. Across two runs it is not: the store holds state no launch expects another user to
// be rewriting underneath it, and the suite is exactly that — every test is a different
// "user" with a different `packs`, and two runs put two of them on the machine at once.
//
// Measured 2026-09-27, twice, each time in a run that overlapped another:
//
//	Error: statfs …/.local/share/yolo-jail/state/openai-auth-broker/.mount-sentinel:
//	no such file or directory
//
// A launch that selects claude (so openai-auth, through `needs`) writes that sentinel and
// names it as a bind source (loopholes.Set.PrepareMountSentinels, which
// internal/cli/run/assemble_parts.go calls); a launch in the OTHER run
// that selects neither reads the machine-wide ownership record, sees openai-auth "leave
// `packs`", and RETIRES the loophole's state dir — moves it into state/.retired
// (internal/cli/run/loopholeretire.go) — between the first launch's write and podman's stat.
// The retirement is correct for one user; it is the suite that is two.
//
// # What is private, and what is shared
//
// PRIVATE by default: a fresh directory is what CI's first run sees anyway, so nothing here
// can depend on the machine's contents. Every child of the machine's store is private to the
// run EXCEPT runStoreShared, each of which is linked back because sharing it is load-bearing:
//
//   - build — the nix GC roots, the image-load sentinel, and the per-workspace current-image
//     pointers the post-launch image reaper keeps (internal/prune, autoreap.go). A private copy
//     would show the reaper only this run's workspaces, and it would reap every image the
//     machine's other jails run (the reason macArchiveEnv sets YOLO_NO_AUTO_IMAGE_REAP).
//   - locks — the image-copy lock is machine-wide ON PURPOSE (internal/image/copylock.go), and
//     TestImageCopyLockSerializesConcurrentLaunches is its test; per-workspace locks are keyed
//     by container name, which a temp workspace makes unique anyway.
//   - cache — the npm and mise download caches: warm across runs, never a correctness input.
//   - embedded-packs — one immutable tree per build, adopted under a shared lease
//     (internal/packload/embeddedcache.go): built for many processes, and a copy per run is
//     only a cost.
//   - stores — the install-capture store, machine-wide by design (autoCaptureEnvForSuite).
//
// The loophole state root and its ownership record are therefore private, which is the fix;
// so are the per-jail staging dirs, the home skeleton store and the approvals, which is
// isolation no test depended on the absence of. The retired-state archive this churn fills
// (state/.retired, one generation per retirement) now lands in the run store and is deleted
// with it, instead of accumulating in the developer's own.
//
// ⚠ THE HOST SINGLETONS ARE NOT IN HERE. Their sockets, PID files and adapter ports are fixed
// per machine (paths.HostSingletonSocket), so a run's launch may still adopt a singleton
// another run started. The tests that OWN a singleton take the cross-run lock exclusively
// instead (machinelock_test.go).
//
// # Which home gets it
//
// Only an isolated home whose "machine home" IS the machine's (runStore.machineHome). A
// fixture that hands seedPackHome a fake machine home of its own — TestPackHomeSharesHostStores,
// TestPlantMachineLoginLeavesTheRealStoreAlone — keeps the direct link it asserts on.

// runStoreShared are the machine-store children every run store links back. See the file
// header for why each one.
var runStoreShared = []string{"build", "locks", "cache", "embedded-packs", "stores"}

// runStore is this process's store and the machine home it stands in for. Both are empty
// under -short and until runSuite makes it.
var runStore struct{ machineHome, dir string }

// makeRunStore creates a run store for machineHome and returns it: a fresh directory holding
// a link to each of machineHome's runStoreShared children, created first when absent (a
// dangling link is what MkdirAll refuses — seedPackHome's comment tells that story).
func makeRunStore(machineHome string) (string, error) {
	machine := paths.GlobalStorageUnder(machineHome)
	parent, err := os.MkdirTemp("", "yolo-integration-store-")
	if err != nil {
		return "", err
	}
	// Resolved where it is minted (resolvedTempDir states the rule): a symlinked TMPDIR is
	// what macOS hands out, and a store path goes into mount sources and nix arguments.
	if r, err := filepath.EvalSymlinks(parent); err == nil {
		parent = r
	}
	// NAMED `yolo-jail`, like the store it stands in for, inside the temp dir: a bind source
	// under the store reads `…/yolo-jail/agents/<cname>/home/…` either way, and that is what
	// TestPodmanHomeIsAPerJailReadOnlySkeleton reads a jail's mountinfo for.
	dir := filepath.Join(parent, filepath.Base(machine))
	if err := os.Mkdir(dir, 0o755); err != nil {
		_ = os.RemoveAll(parent)
		return "", err
	}
	for _, name := range runStoreShared {
		target := filepath.Join(machine, name)
		if err := os.MkdirAll(target, 0o755); err != nil {
			_ = os.RemoveAll(parent)
			return "", fmt.Errorf("creating the machine store's %s: %w", name, err)
		}
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			_ = os.RemoveAll(parent)
			return "", err
		}
	}
	return dir, nil
}

// setUpRunStore makes this process's run store for the machine's home, or reports why the run
// falls back to the machine's own store (DEGRADED, never fatal: the suite still runs, it is
// only as exposed to an overlapping run as it always was).
func setUpRunStore() {
	home := os.Getenv("HOME")
	if home == "" {
		degraded("HOME is unset, so the run has no store of its own; isolated homes link the " +
			"machine's")
		return
	}
	dir, err := makeRunStore(home)
	if err != nil {
		degraded("creating the run's own yolo state dir: %v — isolated homes link the "+
			"machine's instead, and an overlapping run can retire this one's loophole state", err)
		return
	}
	runStore.machineHome, runStore.dir = home, dir
	log.Printf("[integration] this run's yolo state dir: %s (shares %s with %s)", dir,
		strings.Join(runStoreShared, ", "), paths.GlobalStorageUnder(home))
}

// tearDownRunStore removes the run store. Its shared children are links, which RemoveAll
// removes without following; what a launch left read-only or foreign-owned is handled the
// way a workspace is (removeWorkspaceTree's two steps).
func tearDownRunStore() {
	if runStore.dir == "" {
		return
	}
	dir := filepath.Dir(runStore.dir) // makeRunStore's temp dir, which holds the store
	runStore.machineHome, runStore.dir = "", ""
	makeTreeRemovable(dir)
	if err := removeAllFn(dir); err == nil {
		return
	}
	_ = unshareRemoveAll(detectRuntime(), dir)
	if err := removeAllFn(dir); err != nil {
		log.Printf("[integration] could not remove this run's yolo state dir %s: %v", dir, err)
	}
}

// yoloStoreRel is the store packHomeSharedStores links, HOME-relative.
var yoloStoreRel = filepath.FromSlash(paths.GlobalStorageRel())

// sharedStoreTarget is where an isolated home's link for the HOME-relative store rel points:
// the run store for yolo's own store when realHome is the machine home it stands in for, and
// realHome's own store in every other case.
func sharedStoreTarget(realHome, rel string) string {
	if rel == yoloStoreRel && runStore.dir != "" && realHome == runStore.machineHome {
		return runStore.dir
	}
	return filepath.Join(realHome, rel)
}

// TestRunStoreIsPrivateExceptTheSharedChildren pins the run store's shape and seedPackHome's
// use of it, under -short (no container): a machine home stands in for the developer's.
func TestRunStoreIsPrivateExceptTheSharedChildren(t *testing.T) {
	machineHome := resolvedTempDir(t)
	machine := paths.GlobalStorageUnder(machineHome)
	// The machine already has a loophole state dir and an ownership record — the two things
	// an overlapping run must not be able to retire from under this one.
	if err := os.MkdirAll(filepath.Join(machine, "state", "openai-auth-broker"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(machine, "pack-loophole-owners.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, err := makeRunStore(machineHome)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != filepath.Base(machine) {
		t.Errorf("run store %s is not named like the store it stands in for (%s)", dir,
			filepath.Base(machine))
	}
	saved := runStore
	runStore.machineHome, runStore.dir = machineHome, dir
	t.Cleanup(func() {
		tearDownRunStore()
		runStore = saved
	})

	for _, name := range runStoreShared {
		got, err := os.Readlink(filepath.Join(dir, name))
		if err != nil || got != filepath.Join(machine, name) {
			t.Errorf("run store's %s -> %q (%v), want a link to the machine's %s", name, got, err,
				filepath.Join(machine, name))
		}
		if fi, err := os.Stat(filepath.Join(machine, name)); err != nil || !fi.IsDir() {
			t.Errorf("the machine's %s was not created before it was linked (%v): the link "+
				"would dangle", name, err)
		}
	}
	for _, private := range []string{"state", "pack-loophole-owners.json"} {
		if _, err := os.Lstat(filepath.Join(dir, private)); !os.IsNotExist(err) {
			t.Errorf("run store's %s exists (%v) — it must be the run's own, not the machine's", private, err)
		}
	}

	// seedPackHome, for a home standing in for THIS machine home, links the run store; for
	// any other "machine" it keeps the direct link.
	home := resolvedTempDir(t)
	if err := seedPackHome(home, machineHome, `{}`); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(home, yoloStoreRel)); err != nil || got != dir {
		t.Errorf("an isolated home's yolo store -> %q (%v), want the run store %s", got, err, dir)
	}
	other := resolvedTempDir(t)
	home2 := resolvedTempDir(t)
	if err := seedPackHome(home2, other, `{}`); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(home2, yoloStoreRel)); err != nil ||
		got != filepath.Join(other, yoloStoreRel) {
		t.Errorf("a home for a different machine home links %q (%v), want its own store", got, err)
	}

	// Removing the run store leaves the machine's store whole.
	tearDownRunStore()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the run store survived its teardown: %v", err)
	}
	for _, keep := range []string{"state/openai-auth-broker", "pack-loophole-owners.json", "build"} {
		if _, err := os.Stat(filepath.Join(machine, filepath.FromSlash(keep))); err != nil {
			t.Errorf("tearing the run store down touched the machine's %s: %v", keep, err)
		}
	}
}

// TestIsolatedHomeUsesTheRunStore pins the CALL SITES the -short test above cannot see:
// TestMain made a run store, and a container test's isolated home links to it. It runs in the
// container suite for the reason TestRequireJailIsolatesHomeByDefault does, and launches
// nothing.
func TestIsolatedHomeUsesTheRunStore(t *testing.T) {
	requireJail(t)
	if runStore.dir == "" {
		t.Fatal("this run has no store of its own (TestMain's setUpRunStore did not run, or " +
			"degraded — see the suite log), so an overlapping run can retire its loophole state")
	}
	link := filepath.Join(os.Getenv("HOME"), yoloStoreRel)
	if got, err := os.Readlink(link); err != nil || got != runStore.dir {
		t.Errorf("the isolated home's %s -> %q (%v), want this run's store %s", link, got, err,
			runStore.dir)
	}
	if got, want := paths.GlobalStorage(), link; got != want {
		t.Errorf("paths.GlobalStorage() = %s under the isolated home, want %s", got, want)
	}
}
