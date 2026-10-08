package entrypoint

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

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

// A watcher that exits — at once, as it does in a jail with nothing to watch, or later, when it
// dies — is REAPED by the process that started it, never left a zombie. A zombie is not
// harmless here: it has no I/O context left, so it reads as an unset priority to every probe
// of `resources.io.priority`, and the jail's main process keeps one for the jail's life.
func TestAWatcherThatExitsIsReapedNotLeftAZombie(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	bin := filepath.Join(dir, "watcher")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho $$ > "+pidFile+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := startNixRootWatcherFn(bin, filepath.Join(dir, "logs", "nix-roots.log")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	pid := 0
	for pid == 0 && time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(b), "\n") {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("the watcher stand-in never ran")
	}
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	stat, _ := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	t.Fatalf("the exited watcher (pid %d) was never reaped: %s", pid, stat)
}

// Only the jail's own boot starts the watcher. A session's pass in a hold-main jail would start
// one that finds the lock taken and exits, racing the session's exec into a program that does
// not reap it. MUTATION: drop the step's notSessionPass and the session case spawns.
func TestASessionsPassStartsNoRootWatcher(t *testing.T) {
	for _, c := range []struct {
		name        string
		sessionPass bool
		want        int
	}{{"the jail's own boot", false, 1}, {"a session's pass", true, 0}} {
		t.Run(c.name, func(t *testing.T) {
			spawned := watcherSeams(t, t.TempDir(), nil)
			e := testEnv(t)
			e.Vars[nixroots.MapEnv] = nixroots.HostMap{"/workspace": "/host/proj"}.Encode()
			runSteps(&bootRun{e: e, target: bootContainer, sessionPass: c.sessionPass, perf: newPerfLog()},
				[]bootStep{mustBootStep(t, "start_nix_root_watcher")})
			if len(*spawned) != c.want {
				t.Errorf("spawned %v, want %d", *spawned, c.want)
			}
		})
	}
}
