package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The unified workspace home end to end (docs/design/unified-workspace-home.md §5.2, §7.1, §9;
// unified-workspace-home-plan.md U7): the cases the unit suites cannot see because they cross a real
// launch. Podman only. A nested jail proves nothing about reachability or rootless-only paths, so
// none of these claims one.

// unifiedHomeLaunch is a `podman-self` launch of script in dir, the jail removed afterwards so the
// next launch composes again instead of attaching.
func unifiedHomeLaunch(t *testing.T, dir, script string) result {
	t.Helper()
	r := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc", script), withEnv("YOLO_SETUP=podman-self"))
	forceRemoveContainer(dir)
	return r
}

func requireUnifiedHomeRuntime(t *testing.T) {
	t.Helper()
	requireJail(t)
	if rt := detectRuntime(); rt != "podman" {
		t.Skipf("the unified home integration cases launch podman-self; the runtime here is %q", rt)
	}
}

// holdFlock takes an exclusive flock on path and keeps it until the test ends: a fake keeper. The
// held lock is the event the launch reads; nothing waits on a clock. It returns the release, which
// a test calls as soon as its launch has answered: the suite's cleanup waits for the keepers of
// every setup of the workspace (awaitDetachedWriters), and would wait on this one for two minutes.
func holdFlock(t *testing.T, path string) (release func()) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("could not hold %s: %v", path, err)
	}
	release = func() { _ = f.Close() } // closing the descriptor drops the flock
	t.Cleanup(release)
	return release
}

func fakePeerSetup(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(paths.SetupStateDir(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// keeperLockPath is where a keeper of cname's workspace at key holds its liveness lock.
func keeperLockPath(key string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", key+".keeper")
}

// TestUnifiedHomeLaunchComposesItsSliceAndTheNextLaunchFindsTheSession: a session the first jail
// writes is moved into the unified home by the next launch, which leaves a link in the slice, and
// that launch's jail reads the session through the link.
func TestUnifiedHomeLaunchComposesItsSliceAndTheNextLaunchFindsTheSession(t *testing.T) {
	requireUnifiedHomeRuntime(t)
	dir := writeProjectWithPacks(t, `{}`, "claude")
	const rel = ".claude/projects/-workspace/w.jsonl"

	first := unifiedHomeLaunch(t, dir, "mkdir -p ~/.claude/projects/-workspace && echo session > ~/"+rel)
	if first.rc != 0 {
		t.Fatalf("first launch: rc %d\n%s", first.rc, first.combined())
	}
	second := unifiedHomeLaunch(t, dir, "cat ~/"+rel)
	if second.rc != 0 || !strings.Contains(second.stdout, "session") {
		t.Fatalf("the next launch's jail did not read the session through the link: rc %d\n%s", second.rc, second.combined())
	}

	unified := filepath.Join(paths.UnifiedHome(dir), filepath.FromSlash(rel))
	if b, err := os.ReadFile(unified); err != nil || strings.TrimSpace(string(b)) != "session" {
		t.Errorf("the session is not in the unified home %s: %q %v", unified, b, err)
	}
	link := filepath.Join(jailHome(dir), "claude", "projects")
	target, err := os.Readlink(link)
	if err != nil || target != "/workspace/.yolo/unified-home/.claude/projects" {
		t.Errorf("%s -> %q (%v), want the in-jail unified home", link, target, err)
	}
}

// TestUnifiedHomeLaunchIsRefusedByARunningContainerPeer: a keeper holding the liveness lock of an
// apple-container-self jail of this workspace refuses a podman-self launch, naming the setup to stop,
// before anything is written to the unified home.
func TestUnifiedHomeLaunchIsRefusedByARunningContainerPeer(t *testing.T) {
	requireUnifiedHomeRuntime(t)
	dir := writeProjectWithPacks(t, `{}`, "claude")
	fakePeerSetup(t, dir, "apple-container-self")
	release := holdFlock(t, keeperLockPath(jailNameOn(dir, "apple-container-self")))

	r := unifiedHomeLaunch(t, dir, "true")
	release()
	if r.rc == 0 {
		t.Fatalf("a launch beside a running container peer was not refused\n%s", r.combined())
	}
	if !strings.Contains(r.combined(), "yolo stop --setup apple-container-self") {
		t.Errorf("the refusal does not name the setup to stop:\n%s", r.combined())
	}
	if _, err := os.Lstat(paths.UnifiedHome(dir)); err == nil {
		t.Errorf("a refused launch made %s", paths.UnifiedHome(dir))
	}
}

// TestUnifiedHomeLaunchProceedsBesideARunningGuest: the same lock held under the `.guest` key is a
// guest, which may run beside a container, so the launch goes ahead.
func TestUnifiedHomeLaunchProceedsBesideARunningGuest(t *testing.T) {
	requireUnifiedHomeRuntime(t)
	dir := writeProjectWithPacks(t, `{}`, "claude")
	fakePeerSetup(t, dir, "seatbelt-self")
	holdFlock(t, keeperLockPath(jailNameOn(dir, "seatbelt-self")+".guest"))

	r := unifiedHomeLaunch(t, dir, "echo UNIFIED-HOME-RAN")
	if r.rc != 0 || !strings.Contains(r.stdout, "UNIFIED-HOME-RAN") {
		t.Fatalf("a launch beside a running guest was refused or failed: rc %d\n%s", r.rc, r.combined())
	}
}

// TestUnifiedHomeLaunchMergesStrandedHomes: sessions stranded in the legacy home and in another
// setup's slice arrive in the unified home on the first launch with no jail running, none stays
// behind, and the launch says so once.
func TestUnifiedHomeLaunchMergesStrandedHomes(t *testing.T) {
	requireUnifiedHomeRuntime(t)
	dir := writeProjectWithPacks(t, `{}`, "claude")
	legacy := filepath.Join(paths.WorkspaceStateDir(dir), "home", "claude", "projects", "legacy.jsonl")
	apple := filepath.Join(paths.SetupHomeState(dir, "apple-container-self"), ".claude", "projects", "apple.jsonl")
	for p, body := range map[string]string{legacy: "legacy", apple: "apple"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	r := unifiedHomeLaunch(t, dir, "true")
	if r.rc != 0 {
		t.Fatalf("launch: rc %d\n%s", r.rc, r.combined())
	}
	for name, want := range map[string]string{"legacy.jsonl": "legacy", "apple.jsonl": "apple"} {
		p := filepath.Join(paths.UnifiedHome(dir), ".claude", "projects", name)
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("%s = %q (%v), want %q", p, b, err, want)
		}
	}
	for _, gone := range []string{legacy, apple} {
		if _, err := os.Lstat(gone); err == nil {
			t.Errorf("%s was left behind in the old home", gone)
		}
	}
	line := regexp.MustCompile(`(?m)^Merged 2 files from 2 homes into this workspace's shared home\.`)
	if len(line.FindAllString(r.combined(), -1)) != 1 {
		t.Errorf("the launch did not say what it merged exactly once:\n%s", r.combined())
	}
}
