package storage

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestEnsureGlobalStorageProvisionsNoHome pins what <state>/home is now: a MACHINE STORE,
// holding the machine-scope shared dirs and the credential migration's file, and nothing a
// home needs (docs/design/base-home-legacy-state.md#25-the-machine-store-stays).
//
// Every podman jail used to bind <state>/home at /home/agent, so this function created the
// union of every shipped pack's writable dirs, core's own dirs, the single-file mountpoints
// and the three redirect links in it — and one workspace's view of that union was every
// workspace's. Those now come from the per-jail skeleton (buildHomeSkeleton in
// internal/cli/run, whose own test pins the core dirs this file used to). A mountpoint
// re-added here would be visible to nothing and would quietly revive the shared base's
// shape, so the exact contents are pinned.
func TestEnsureGlobalStorageProvisionsNoHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := EnsureGlobalStorage(nil); err != nil {
		t.Fatalf("EnsureGlobalStorage: %v", err)
	}

	shared := packload.EmbeddedSharedDirs()
	if len(shared) == 0 {
		t.Fatal("packload.EmbeddedSharedDirs is empty — internal/packreg is not registering the " +
			"embedded packs, and the assertion below would pass vacuously")
	}
	want := map[string]bool{
		filepath.Join(".claude-shared-credentials", ".credentials.json"): true,
	}
	for _, d := range shared {
		want[d] = true
	}

	var got []string
	root := paths.GlobalHome()
	err := filepath.WalkDir(root, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		got = append(got, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(got)
	for _, rel := range got {
		if !want[rel] {
			t.Errorf("EnsureGlobalStorage created %s in the machine store; nothing mounts "+
				"<state>/home at /home/agent any more, so a home mountpoint belongs in the "+
				"per-jail skeleton (buildHomeSkeleton), not here", rel)
		}
	}
	for rel := range want {
		if _, err := os.Lstat(filepath.Join(root, rel)); err != nil {
			t.Errorf("the machine store lacks %s: %v", rel, err)
		}
	}
}
