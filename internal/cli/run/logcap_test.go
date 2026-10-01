package run

// logcap_test.go pins the two launch-side opens of a host child's log to internal/logcap's
// bound: the socat forwards' <cname>-socat.log (startPortForwards) and a per-jail host daemon's
// host-service-<name>.log (startExternalService). Each drives the production function over a
// log already past the cap, so putting a bare O_APPEND open back at either site fails it.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/logcap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// overfullLog writes a log of whole lines just past logcap.MaxBytes at path.
func overfullLog(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	line := []byte("an old line from an earlier launch\n")
	if err := os.WriteFile(path, bytes.Repeat(line, logcap.MaxBytes/len(line)+2), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertCapped says the log at path was trimmed by its open: under the cap, holding none of
// the old lines, with the old lines in the one archive.
func assertCapped(t *testing.T, path string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if len(got) > logcap.MaxBytes || bytes.Contains(got, []byte("an old line")) {
		t.Errorf("%s is %d bytes and still holds the earlier launches' lines — its open "+
			"no longer bounds it", filepath.Base(path), len(got))
	}
	archive, err := os.ReadFile(path + logcap.ArchiveSuffix)
	if err != nil {
		t.Fatalf("no archived generation beside %s: %v", filepath.Base(path), err)
	}
	if !bytes.Contains(archive, []byte("an old line")) {
		t.Errorf("the archive beside %s does not hold the earlier lines", filepath.Base(path))
	}
}

func TestAPortForwardLaunchCapsTheSocatLog(t *testing.T) {
	fakeSocatOnPath(t)
	t.Setenv("HOME", t.TempDir())
	log := filepath.Join(paths.GlobalStorage(), "logs", "capped-socat.log")
	overfullLog(t, log)

	o := &Options{}
	fillDefaults(o)
	o.Stderr = io.Discard
	socketDir := filepath.Join(t.TempDir(), "yolo-fwd-capped")
	procs := o.startPortForwards(o.planPortForwards([]any{8080}), "capped", socketDir)
	t.Cleanup(func() { cleanupPortForwarding(procs, socketDir) })
	if len(procs) != 1 {
		t.Fatalf("expected 1 socat proc, got %d", len(procs))
	}
	assertCapped(t, log)
}

func TestAHostDaemonLaunchCapsItsServiceLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	log := filepath.Join(paths.GlobalStorage(), "logs", "host-service-capped-svc.log")
	overfullLog(t, log)

	socketsDir := t.TempDir()
	spec := jsonx.NewOrderedMap()
	// Writes its line BEFORE it publishes, so the line is in the log by the time it is ready.
	spec.Set("command", []any{"sh", "-c", `echo "fresh start" >&2; : > "$1"; sleep 30`,
		"capped-svc", "{endpoint}"})
	o := &Options{}
	fillDefaults(o)
	o.Stdout = io.Discard
	h, ok := o.startExternalService("capped-svc", spec, socketsDir, "", "127.0.0.1", nil)
	if !ok {
		t.Fatal("the daemon never became ready")
	}
	t.Cleanup(h.stop)
	assertCapped(t, log)
	if got, _ := os.ReadFile(log); !bytes.Contains(got, []byte("fresh start")) {
		t.Errorf("the daemon's own output did not reach its log after the trim: %q", got)
	}
}
