package entrypoint

import (
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/nixroots"
)

// watcherSeams points the step at a fixture auto dir and records the spawn instead of making it.
func watcherSeams(t *testing.T, autoDir string, spawnErr error) *[]string {
	t.Helper()
	var spawned []string
	oldDir, oldLook, oldSpawn := nixRootAutoDir, lookJaild, startNixRootWatcherFn
	t.Cleanup(func() { nixRootAutoDir, lookJaild, startNixRootWatcherFn = oldDir, oldLook, oldSpawn })
	nixRootAutoDir = autoDir
	lookJaild = func(string) (string, error) { return "/bin/yolo-jaild", nil }
	startNixRootWatcherFn = func(bin, log string) error {
		spawned = append(spawned, bin+" "+log)
		return spawnErr
	}
	return &spawned
}

// The step is the CALL SITE: run through the boot table, with the auto dir bound and a map
// stated, it starts the watcher, logging beside the supervisor's daemons.
func TestTheBootStartsTheRootWatcherWhenTheAutoDirIsBound(t *testing.T) {
	spawned := watcherSeams(t, t.TempDir(), nil)
	e := testEnv(t)
	e.Vars[nixroots.MapEnv] = nixroots.HostMap{"/workspace": "/host/proj"}.Encode()
	mustBootStep(t, "start_nix_root_watcher").run(&bootRun{e: e, target: bootContainer})
	if len(*spawned) != 1 || !strings.HasSuffix((*spawned)[0], ".local/state/yolo-jail-daemons/nix-roots.log") {
		t.Fatalf("spawned %v", *spawned)
	}
}

func TestTheBootStartsNoWatcherWithoutTheAutoDirOrAMap(t *testing.T) {
	spawned := watcherSeams(t, "/nonexistent-auto-dir", nil)
	e := testEnv(t)
	e.Vars[nixroots.MapEnv] = nixroots.HostMap{"/workspace": "/host/proj"}.Encode()
	startNixRootWatcher(e)
	if len(*spawned) != 0 {
		t.Errorf("started a watcher with no auto dir bound: %v", *spawned)
	}
	spawned = watcherSeams(t, t.TempDir(), nil)
	startNixRootWatcher(testEnv(t))
	if len(*spawned) != 0 {
		t.Errorf("started a watcher with no map: %v", *spawned)
	}
}

func TestAWatcherThatCannotStartNamesTheHandRunForm(t *testing.T) {
	watcherSeams(t, t.TempDir(), errors.New("boom"))
	e, stderr, _ := loudEnv(t)
	e.Vars[nixroots.MapEnv] = nixroots.HostMap{"/workspace": "/host/proj"}.Encode()
	startNixRootWatcher(e)
	mustContain(t, "stderr", stderr, "boom", "yolo nix-roots keep")
}
