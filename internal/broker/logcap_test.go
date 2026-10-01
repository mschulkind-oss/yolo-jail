package broker

// logcap_test.go pins a host-wide daemon's log (host-service-<name>.log, one file across every
// jail) to internal/logcap's bound at both places a launch reaches it: the spawn's open
// (realSpawn) and the reuse of a live daemon (EnsureSingleton), which never reopens the log and
// so is the only launch that can bound a daemon living across many of them.

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/logcap"
)

// overfullLog writes a log of whole lines just past logcap.MaxBytes at path.
func overfullLog(t *testing.T, path string) {
	t.Helper()
	line := []byte("an old line from an earlier launch\n")
	if err := os.WriteFile(path, bytes.Repeat(line, logcap.MaxBytes/len(line)+2), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertCapped says the log at path was trimmed: under the cap, holding none of the old lines,
// with the old lines in the one archive.
func assertCapped(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(got) > logcap.MaxBytes || bytes.Contains(got, []byte("an old line")) {
		t.Errorf("the log is %d bytes and still holds the earlier launches' lines", len(got))
	}
	archive, err := os.ReadFile(path + logcap.ArchiveSuffix)
	if err != nil {
		t.Fatalf("no archived generation beside the log: %v", err)
	}
	if !bytes.Contains(archive, []byte("an old line")) {
		t.Error("the archive does not hold the earlier lines")
	}
}

// A launch that REUSES a live host-wide daemon bounds its log too. Nothing respawns a daemon
// whose settings still match, so without this the log of one that runs for weeks would grow
// for all of them.
func TestEnsureCapsTheLogOfALiveDaemonItReuses(t *testing.T) {
	deps, st, _ := settingsFixture(t, `{"profile":"p"}`, `{"profile":"p"}`)
	overfullLog(t, deps.LogPath)

	EnsureSingleton(deps)

	if len(st.killed) != 0 || len(st.spawnArgv) != 0 {
		t.Fatalf("the fixture's daemon was restarted (killed=%v spawn=%v); this test is about a "+
			"reuse", st.killed, st.spawnArgv)
	}
	assertCapped(t, deps.LogPath)
}

// The spawn's own open bounds the log before handing it to the new daemon, whose output then
// still lands in it.
func TestRealSpawnCapsTheDaemonLog(t *testing.T) {
	deps := newFakeDeps(t, &fakeState{})
	overfullLog(t, deps.LogPath)

	_, exited, err := realSpawn([]string{"sh", "-c", "echo spawned"}, deps.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); !exited(); {
		if time.Now().After(deadline) {
			t.Fatal("the spawned daemon never exited")
		}
		time.Sleep(5 * time.Millisecond)
	}
	assertCapped(t, deps.LogPath)
	if got, _ := os.ReadFile(deps.LogPath); string(got) != "spawned\n" {
		t.Errorf("log = %q, want the new daemon's output alone", got)
	}
}
