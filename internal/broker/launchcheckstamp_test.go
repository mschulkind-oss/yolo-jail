package broker

import (
	"os"
	"syscall"
	"testing"
)

// launchcheckstamp_test.go pins the launch-check stamp (launchcheckstamp.go): what tells a launch
// that a host-wide daemon answering `unknown action: launch-check` was left running by an older
// yolo, which a restart fixes, from one this yolo spawned, whose program a restart does not change.

// TestASpawnStampsThePidItStarted: the ensure's spawn is where the stamp is written, naming the
// pid it records, so a daemon this yolo started reads as one that knows the launch check.
func TestASpawnStampsThePidItStarted(t *testing.T) {
	st := &fakeState{spawnPID: 4321}
	deps := newFakeDeps(t, st)
	touch(t, deps.SocketPath)
	if got := EnsureSingleton(deps); !got.Started {
		t.Fatalf("the ensure did not spawn: %+v", got)
	}
	if !SpawnedKnowingLaunchCheck(deps) {
		raw, _ := os.ReadFile(launchCheckStampPath(deps))
		t.Fatalf("a daemon this yolo just spawned does not read as one that knows the launch "+
			"check; the stamp reads %q", raw)
	}
	if !SingletonSpeaksPreamble(deps) {
		t.Error("the spawn no longer writes the preamble's stamp beside the launch check's")
	}
}

// TestADaemonWithNoStampWasSpawnedByAnOlderYolo is the upgrade itself: a daemon that a yolo older
// than the stamp started has a PID file and no stamp.
func TestADaemonWithNoStampWasSpawnedByAnOlderYolo(t *testing.T) {
	deps := newFakeDeps(t, &fakeState{})
	writePID(t, deps, 77)
	if SpawnedKnowingLaunchCheck(deps) {
		t.Fatal("a daemon with no launch-check stamp reads as one this yolo started")
	}
}

// TestAStampNamingAnotherPidIsStale: a yolo that does not know the stamp stops and respawns the
// daemon, leaving this yolo's stamp behind with the old pid. The new daemon is the older yolo's,
// so the stamp must not vouch for it.
func TestAStampNamingAnotherPidIsStale(t *testing.T) {
	deps := newFakeDeps(t, &fakeState{})
	StampLaunchCheck(deps, 42)
	writePID(t, deps, 77)
	if SpawnedKnowingLaunchCheck(deps) {
		t.Fatal("a stamp naming pid 42 vouched for the daemon the PID file says is 77")
	}
	writePID(t, deps, 42)
	if !SpawnedKnowingLaunchCheck(deps) {
		t.Fatal("a stamp naming the PID file's own pid is not honored")
	}
}

// TestAStampOfAnotherVersionIsNotHonored: the constant is versioned so a later change to what the
// stamp means can retire the old ones.
func TestAStampOfAnotherVersionIsNotHonored(t *testing.T) {
	deps := newFakeDeps(t, &fakeState{})
	writePID(t, deps, 9)
	if err := os.WriteFile(launchCheckStampPath(deps), []byte("launch-check-v99 9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if SpawnedKnowingLaunchCheck(deps) {
		t.Fatal("a stamp of another version was honored")
	}
}

// TestBrokerKillRemovesTheLaunchCheckStamp: the stamp goes with the daemon it describes, as the
// PID file and the preamble's stamp do.
func TestBrokerKillRemovesTheLaunchCheckStamp(t *testing.T) {
	st := &fakeState{alive: map[int]bool{123: true}}
	deps := newFakeDeps(t, st)
	writePID(t, deps, 123)
	StampLaunchCheck(deps, 123)
	deps.Kill = func(pid int, sig syscall.Signal) error {
		st.alive[pid] = false
		return nil
	}
	if !BrokerKill(deps, syscall.SIGTERM, BrokerKillTimeout) {
		t.Fatal("want true: a daemon was running")
	}
	if _, err := os.Lstat(launchCheckStampPath(deps)); !os.IsNotExist(err) {
		t.Errorf("the launch-check stamp outlived its daemon (%v)", err)
	}
}
