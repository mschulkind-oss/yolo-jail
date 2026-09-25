package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestEnsureGlobalStorageCreatesEveryMachineScopeDir pins the CALL SITE of
// packload.EmbeddedSharedDirs, the way its core-dirs sibling pins BaseHomeCoreDirs.
//
// The machine tier IS these host directories: every container backend binds
// <GlobalHome>/<dir> at /home/agent/<dir> for each selected pack that declares one, and
// this loop makes every bind SOURCE. (The MOUNTPOINT inside podman's :ro home is the per-jail
// skeleton's, buildHomeSkeleton in internal/cli/run; it used to be this same directory, seen
// through the shared base bind.) Delete the append and nothing fails: the list still looks
// right in every other reader, and the tier degrades quietly.
//
// UNGATED ON SELECTION, because EnsureGlobalStorage runs before the config is loaded (its
// doc says so): the provisioning list is the union over every pack yolo SHIPS, so a machine
// that never selects pi still gets an empty `.pi-shared-npm` in the machine store, which no
// jail that does not select pi mounts. That is also why a migration must key on CONTENT
// rather than existence — the directory is already there, empty, before the pack ever runs.
func TestEnsureGlobalStorageCreatesEveryMachineScopeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := EnsureGlobalStorage(nil); err != nil {
		t.Fatalf("EnsureGlobalStorage: %v", err)
	}

	dirs := packload.EmbeddedSharedDirs()
	if len(dirs) == 0 {
		t.Fatal("packload.EmbeddedSharedDirs is empty — the usual cause is internal/packreg " +
			"not being imported, and every assertion below would be vacuous")
	}
	for _, rel := range dirs {
		info, err := os.Stat(filepath.Join(paths.GlobalHome(), rel))
		if err != nil {
			t.Errorf("%s was not provisioned: %v", rel, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", rel)
		}
	}
}
