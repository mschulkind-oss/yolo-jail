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
	live := []SourceAnswer{{Confinement: "podman", State: SourceListed,
		Names: map[string]struct{}{"yolo-live-1": {}}}}
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
	if got := PruneStaleTrackingFiles(live); got != nil {
		t.Errorf("missing dir => %v, want nil", got)
	}
}

// TestPruneKeepsRecordsUntilEveryContainerSourceListed is SU-D10 (named-setups.md §12.1): a
// tracking file names no technology, so it goes only when every container source of this OS
// answered (listed, or not installed) and none listed it. The defect: on a Mac, a Podman-only
// `yolo ps` handed the prune Podman's names alone and deleted every running Apple Container
// jail's file.
func TestPruneKeepsRecordsUntilEveryContainerSourceListed(t *testing.T) {
	const ac, deadPodman = "yolo-ac-1111", "yolo-dead-2222"
	seed := func(t *testing.T) {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		for _, n := range []string{ac, deadPodman} {
			if err := WriteContainerTracking(n, "/ws/"+n); err != nil {
				t.Fatal(err)
			}
		}
	}
	podmanEmpty := SourceAnswer{Confinement: "podman", State: SourceListed, Names: map[string]struct{}{}}
	tracked := func(n string) bool { _, ok := ReadContainerWorkspace(n); return ok }

	for _, st := range []SourceState{SourceUnreachable, SourceTimedOut} {
		t.Run("apple-container "+string(st), func(t *testing.T) {
			seed(t)
			removed := PruneStaleTrackingFiles([]SourceAnswer{podmanEmpty,
				{Confinement: "apple-container", State: st}})
			if len(removed) != 0 || !tracked(ac) || !tracked(deadPodman) {
				t.Errorf("a container source that did not answer must keep every record; removed %v", removed)
			}
		})
	}
	t.Run("apple-container not installed", func(t *testing.T) {
		seed(t)
		removed := PruneStaleTrackingFiles([]SourceAnswer{podmanEmpty,
			{Confinement: "apple-container", State: SourceNotInstalled}})
		sort.Strings(removed)
		if len(removed) != 2 || tracked(ac) || tracked(deadPodman) {
			t.Errorf("every source answered and none listed either: want both removed, got %v", removed)
		}
	})
	t.Run("both listed", func(t *testing.T) {
		seed(t)
		removed := PruneStaleTrackingFiles([]SourceAnswer{podmanEmpty,
			{Confinement: "apple-container", State: SourceListed, Names: map[string]struct{}{ac: {}}}})
		if len(removed) != 1 || removed[0] != deadPodman || !tracked(ac) {
			t.Errorf("want only %s removed, got %v (Apple Container's record kept: %v)", deadPodman, removed, tracked(ac))
		}
	})
}
