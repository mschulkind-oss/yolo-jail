package hostservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortPoll shrinks WatchStateDir's interval for one test, so a removal is seen in
// milliseconds rather than the production two seconds.
func shortPoll(t *testing.T) {
	t.Helper()
	prev := StateDirPollInterval
	StateDirPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { StateDirPollInterval = prev })
}

// watch starts a WatchStateDir on dir and returns the channel its gone callback reports
// on, plus the stop channel it was given.
func watch(t *testing.T, dir string) (<-chan string, chan struct{}) {
	t.Helper()
	gone := make(chan string, 2)
	stop := make(chan struct{})
	if err := WatchStateDir(dir, stop, func(reason string) { gone <- reason }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
	})
	return gone, stop
}

func TestWatchStateDirReportsARemovedDirectory(t *testing.T) {
	shortPoll(t)
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	gone, _ := watch(t, dir)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-gone:
		if !strings.Contains(reason, dir) || !strings.Contains(reason, "removed") {
			t.Errorf("reason %q should name %s and say it was removed", reason, dir)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a removed state dir was never reported")
	}
	// ONCE: the watch ends with the report, so a daemon is never told twice.
	select {
	case again := <-gone:
		t.Errorf("reported a second time: %q", again)
	case <-time.After(100 * time.Millisecond):
	}
}

// A retirement MOVES the dir away, and a later write elsewhere may make a fresh one at the
// same path: a different directory, which the daemon did not start against. Asked of the
// predicate directly, because a live poll could as well land in the gap between the move and
// the mkdir and report the removal instead.
func TestStateDirGoneReportsADirectoryReplacedAtTheSamePath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	start, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reason := stateDirGone(dir, start); reason != "" {
		t.Fatalf("stateDirGone on the untouched dir = %q, want \"\"", reason)
	}
	if err := os.Rename(dir, filepath.Join(root, "retired")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if reason := stateDirGone(dir, start); !strings.Contains(reason, "replaced") {
		t.Errorf("stateDirGone on a replaced dir = %q, want a 'replaced' reason", reason)
	}
}

func TestWatchStateDirReportsAPathComponentThatIsNoLongerADirectory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	dir := filepath.Join(home, "state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	start, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if reason := stateDirGone(dir, start); !strings.Contains(reason, "removed") {
		t.Errorf("stateDirGone under a parent that became a file = %q, want 'removed'", reason)
	}
}

func TestWatchStateDirIsSilentWhileTheDirectoryStays(t *testing.T) {
	shortPoll(t)
	dir := t.TempDir()
	gone, _ := watch(t, dir)
	// Writing INTO the dir changes its mtime and its entries, never its identity.
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-gone:
		t.Fatalf("reported a dir that is still there: %q", reason)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchStateDirStopsWithoutReportingWhenStopCloses(t *testing.T) {
	shortPoll(t)
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	gone, stop := watch(t, dir)
	close(stop)
	time.Sleep(50 * time.Millisecond) // let the watcher observe stop before the removal
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-gone:
		t.Fatalf("a stopped watch still reported: %q", reason)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWatchStateDirRefusesADirectoryThatIsNotThere(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "never-made")
	if err := WatchStateDir(missing, make(chan struct{}), func(string) {}); err == nil {
		t.Fatal("watching a missing dir should be an error: there is nothing to watch")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WatchStateDir(file, make(chan struct{}), func(string) {}); err == nil {
		t.Fatal("watching a regular file should be an error")
	}
}
