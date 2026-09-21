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
// <GlobalHome>/<dir> at /home/agent/<dir>, and the reason that needs no separate mountpoint
// step — unlike writable_home_dirs, which must pre-create one inside a :ro bind — is that the
// mount SOURCE and the mountpoint are the same directory, already visible through the base
// bind because this loop made it. Delete the append and nothing fails: the list still looks
// right in every other reader, and the tier degrades quietly.
//
// UNGATED ON SELECTION by design (packload/embedded.go): the provisioning list is the union
// over every pack yolo SHIPS, so a machine that never selects pi still gets an empty
// `.pi-shared-npm`. That is also why a migration must key on CONTENT rather than existence —
// the directory is already there, empty, before the pack ever runs.
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
