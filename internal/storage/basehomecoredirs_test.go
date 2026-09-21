package storage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestEnsureGlobalStorageCreatesEveryCoreBaseHomeDir pins the CALL SITE of
// paths.BaseHomeCoreDirs.
//
// The list used to be an inline slice literal inside EnsureGlobalStorage with no exported
// authority, which is why the base-home sweep's exclusion set would have become a second
// hand-maintained copy (OQ-BH4) — and a sweep that disagrees with the provisioner about
// which dirs are core's is a sweep that proposes archiving `.ssh`. Extracting it only
// helps while both sides keep using it: delete the `paths.BaseHomeCoreDirs()...` append
// and the exclusion set still looks right while nothing creates the dirs.
//
// Nothing pinned these dirs by name before, in either package.
func TestEnsureGlobalStorageCreatesEveryCoreBaseHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if err := EnsureGlobalStorage(nil); err != nil {
		t.Fatalf("EnsureGlobalStorage: %v", err)
	}

	dirs := paths.BaseHomeCoreDirs()
	if len(dirs) == 0 {
		t.Fatal("paths.BaseHomeCoreDirs is empty")
	}
	for _, rel := range dirs {
		p := filepath.Join(paths.GlobalHome(), rel)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", rel)
		}
	}
}
