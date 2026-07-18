package runtime

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestTrackingRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Write → read round-trips the resolved workspace.
	ws := "/home/matt/code/thing"
	if err := WriteContainerTracking("yolo-thing-abcd1234", ws); err != nil {
		t.Fatal(err)
	}
	// The file lives under CONTAINER_DIR with a trailing newline.
	raw, err := os.ReadFile(filepath.Join(paths.ContainerDir(), "yolo-thing-abcd1234"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != ws+"\n" {
		t.Errorf("tracking file = %q, want %q", raw, ws+"\n")
	}
	got, ok := ReadContainerWorkspace("yolo-thing-abcd1234")
	if !ok || got != ws {
		t.Errorf("read = %q, %v (want %q)", got, ok, ws)
	}

	// Cleanup removes it; a second cleanup is a no-op (missing_ok).
	CleanupContainerTracking("yolo-thing-abcd1234")
	if _, ok := ReadContainerWorkspace("yolo-thing-abcd1234"); ok {
		t.Error("tracking file should be gone after cleanup")
	}
	CleanupContainerTracking("yolo-thing-abcd1234") // no panic/error

	// Absent file reads as absent.
	if _, ok := ReadContainerWorkspace("nope"); ok {
		t.Error("absent tracking file should read as absent")
	}
}

func TestPruneStaleTrackingFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, n := range []string{"yolo-live-1", "yolo-dead-1", "yolo-dead-2"} {
		if err := WriteContainerTracking(n, "/ws/"+n); err != nil {
			t.Fatal(err)
		}
	}
	live := map[string]struct{}{"yolo-live-1": {}}
	removed := PruneStaleTrackingFiles(live)
	sort.Strings(removed)
	if len(removed) != 2 || removed[0] != "yolo-dead-1" || removed[1] != "yolo-dead-2" {
		t.Errorf("removed = %v, want the two dead ones", removed)
	}
	// The live one survives.
	if _, ok := ReadContainerWorkspace("yolo-live-1"); !ok {
		t.Error("live tracking file must survive prune")
	}
	// Missing CONTAINER_DIR: no-op.
	t.Setenv("HOME", t.TempDir())
	if got := PruneStaleTrackingFiles(nil); got != nil {
		t.Errorf("missing dir => %v, want nil", got)
	}
}
