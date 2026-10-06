package run

// accounthomehold_test.go pins the account-home hold (accounthomehold.go) on real flocks: a live
// session of one workspace refuses another's launch, naming it and the next steps; the same
// workspace is admitted; a released or dead holder admits; and every answer the probe cannot read
// counts as live.

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func holdDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(d, "locks", accountHomeHoldLeaf)
}

func mustHold(t *testing.T, dir, ws, cname string) func() {
	t.Helper()
	release, refusal := holdAccountHomeIn(dir, ws, cname, "", time.Second)
	if refusal != "" || release == nil {
		t.Fatalf("%s was refused the account home: %s", ws, refusal)
	}
	return release
}

func TestALiveSessionOfAnotherWorkspaceRefusesTheLaunch(t *testing.T) {
	dir := holdDir(t)
	releaseA := mustHold(t, dir, "/Users/Shared/a", "yolo-a-1111")

	_, refusal := holdAccountHomeIn(dir, "/Users/Shared/b", "yolo-b-2222", " (and `--at jail`)", time.Second)
	for _, want := range []string{"/Users/Shared/a", "Quit that session", "run `yolo` here again",
		`"runtime": "container"`, " (and `--at jail`)."} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, refusal)
		}
	}

	// The same workspace is admitted beside its own live session (OQ-HT3: it lays the same links).
	mustHold(t, dir, "/Users/Shared/a", "yolo-a-1111")()

	// Once A's session releases, B is admitted, and now holds it against A.
	releaseA()
	releaseB := mustHold(t, dir, "/Users/Shared/b", "yolo-b-2222")
	defer releaseB()
	if _, refusal := holdAccountHomeIn(dir, "/Users/Shared/a", "yolo-a-1111", "", time.Second); !strings.Contains(refusal, "/Users/Shared/b") {
		t.Errorf("A was not refused while B holds the home: %q", refusal)
	}
	// No lock file is unlinked by a probe or a release (JL-D28).
	if _, err := os.Stat(filepath.Join(dir, "yolo-a-1111.lock")); err != nil {
		t.Errorf("A's lock file is gone: %v", err)
	}
}

// A HOLDER THAT IS GONE admits: its file is still there, unlocked, as a SIGKILLed launch leaves it.
func TestADeadHoldersFileAdmitsTheLaunch(t *testing.T) {
	dir := holdDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo-a-1111.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yolo-a-1111.workspace"), []byte("/Users/Shared/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustHold(t, dir, "/Users/Shared/b", "yolo-b-2222")()
}

// AN ANSWER THE PROBE CANNOT READ COUNTS AS LIVE (JL-P3), naming the file and what to do.
func TestAnUnopenableHoldCountsAsLive(t *testing.T) {
	dir := holdDir(t)
	bad := filepath.Join(dir, "yolo-a-1111.lock")
	if err := os.MkdirAll(bad, 0o755); err != nil { // a directory where a lock file belongs
		t.Fatal(err)
	}
	_, refusal := holdAccountHomeIn(dir, "/Users/Shared/b", "yolo-b-2222", "", time.Second)
	for _, want := range []string{"could not tell", bad, "remove " + bad} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, refusal)
		}
	}
}

// A HOLD DIRECTORY THAT CANNOT BE MADE is its own refusal: there is no lock file to remove, so the
// next step it names is the directory's, not a file the launch could never have made.
func TestAnUnmakeableHoldDirectoryNamesTheDirectory(t *testing.T) {
	dir := holdDir(t)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o644); err != nil { // a file where the directory belongs
		t.Fatal(err)
	}
	_, refusal := holdAccountHomeIn(dir, "/Users/Shared/b", "yolo-b-2222", " (and `--at jail`)", time.Second)
	for _, want := range []string{"could not make " + dir, "remove the file in its place", "run `yolo` again",
		`"runtime": "container"`, " (and `--at jail`)."} {
		if !strings.Contains(refusal, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, refusal)
		}
	}
	if strings.Contains(refusal, "yolo-b-2222.lock") {
		t.Errorf("the refusal names a lock file inside a directory that does not exist:\n%s", refusal)
	}
}

// THE MUTEX IS BOUNDED: an arrival waits for another's probe, but not for ever, and says so.
func TestTheProbeMutexIsBounded(t *testing.T) {
	dir := holdDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, accountHomeMutexName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, refusal := holdAccountHomeIn(dir, "/Users/Shared/b", "yolo-b-2222", "", 200*time.Millisecond)
	if !strings.Contains(refusal, accountHomeMutexName) || !strings.Contains(refusal, "`yolo` again") {
		t.Errorf("a held mutex did not refuse naming it and the next step: %q", refusal)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("the bounded wait took %s", d)
	}
}

// THE HOLD IS REAL: the launch holds its file shared for the session, so an exclusive take — what a
// second workspace's probe does — is refused until the release.
func TestTheHoldIsASharedFlockUntilReleased(t *testing.T) {
	dir := holdDir(t)
	release := mustHold(t, dir, "/Users/Shared/a", "yolo-a-1111")
	f, err := os.OpenFile(filepath.Join(dir, "yolo-a-1111.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("the held file takes an exclusive lock: a second workspace would be admitted")
	}
	release()
	release() // idempotent
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Errorf("the hold outlived its release: %v", err)
	}
}

// HoldAccountHome is the same hold, in the global storage's locks dir.
func TestHoldAccountHomeIsUnderGlobalStorage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	release, refusal := HoldAccountHome("/Users/Shared/a", "yolo-a-1111", "")
	if refusal != "" {
		t.Fatal(refusal)
	}
	defer release()
	if _, err := os.Stat(filepath.Join(accountHomeHoldDir(), "yolo-a-1111.lock")); err != nil {
		t.Errorf("no hold file under %s: %v", accountHomeHoldDir(), err)
	}
}
