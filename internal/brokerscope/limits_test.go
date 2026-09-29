package brokerscope

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The git config is agent-writable, and every fresh launch and `yolo check` on the host
// reads it. A sparse file costs the agent no disk, so the reader stops at a cap instead of
// loading it whole.
func TestReadRemotesStopsAtTheCap(t *testing.T) {
	ws := resolvedTempDir(t)
	cfg := filepath.Join(ws, ".git", "config")
	writeFile(t, cfg, "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	if err := os.Truncate(cfg, 256<<20); err != nil { // sparse: no disk
		t.Fatal(err)
	}
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	r := ReadRemotes(ws, "github.com")
	runtime.ReadMemStats(&after)
	if len(r.Remotes) != 0 || !strings.Contains(r.Problem, "larger than") {
		t.Fatalf("an oversized git config was read: %d remotes, problem %q", len(r.Remotes), r.Problem)
	}
	// It stopped reading at the cap rather than loading the file and then judging its size.
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 32<<20 {
		t.Fatalf("reading a 256 MiB config allocated %d bytes", grew)
	}

	// A config at the cap is still a config.
	if err := os.Truncate(cfg, GitConfigCap); err != nil {
		t.Fatal(err)
	}
	if r := ReadRemotes(ws, "github.com"); r.Problem != "" || len(r.Remotes) != 1 {
		t.Fatalf("a config at the cap: %+v", r)
	}
}

// A FIFO in the config's place is refused on the file the reader opened, without waiting
// for a writer that never comes.
func TestReadRemotesDoesNotWaitOnAFIFO(t *testing.T) {
	ws := resolvedTempDir(t)
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(ws, ".git", "config"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan Read, 1)
	go func() { done <- ReadRemotes(ws, "github.com") }()
	select {
	case r := <-done:
		if !strings.Contains(r.Problem, "not a regular file") {
			t.Fatalf("read %+v", r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the scope reader blocked opening a FIFO")
	}
}

// The helper decides on what it opened: a symlink is refused at open (O_NOFOLLOW), not by a
// look at the path beforehand that a swap can outrun.
func TestReadCappedRefusesASymlinkAtOpen(t *testing.T) {
	dir := resolvedTempDir(t)
	real := filepath.Join(dir, "real")
	writeFile(t, real, "x")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readCapped(link, 10); err != errNotRegular {
		t.Fatalf("err %v, want errNotRegular", err)
	}
	if b, err := readCapped(real, 10); err != nil || string(b) != "x" {
		t.Fatalf("%q, %v", b, err)
	}
	if _, err := readCapped(real, 0); err != errTooLarge {
		t.Fatalf("err %v, want errTooLarge", err)
	}
}
