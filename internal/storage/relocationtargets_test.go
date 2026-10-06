package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// EnsureCacheRelocationTargets is EnsureCacheRelocations for macos-user, which mounts nothing:
// it makes the target's last component and reports which it made, refuses a missing parent and
// a target that is a file, and makes NO mountpoint in the machine cache.
func TestEnsureCacheRelocationTargetsMakesOnlyTheTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	other := t.TempDir()
	fresh := filepath.Join(other, "hf")
	existing := filepath.Join(other, "pw")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	rels := []config.CacheRelocation{{Subdir: "huggingface", Target: fresh}, {Subdir: "ms-playwright", Target: existing}}
	created, err := EnsureCacheRelocationTargets(rels)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || !created[0] || created[1] {
		t.Errorf("created = %v, want [true false]: only a target this call made is reported", created)
	}
	if st, err := os.Stat(fresh); err != nil || !st.IsDir() {
		t.Errorf("the missing target %s was not made: %v", fresh, err)
	}
	if st, _ := os.Stat(existing); st.Mode().Perm() != 0o700 {
		t.Errorf("an existing target's mode was changed to %v", st.Mode().Perm())
	}
	for _, sub := range []string{"huggingface", "ms-playwright"} {
		if _, err := os.Lstat(filepath.Join(paths.GlobalCache(), sub)); err == nil {
			t.Errorf("a machine-cache mountpoint was made for %s, which nothing mounts over", sub)
		}
	}
	// Idempotent: the second call makes nothing.
	if created, err := EnsureCacheRelocationTargets(rels); err != nil || created[0] || created[1] {
		t.Errorf("a second call = %v, %v", created, err)
	}
}

func TestEnsureCacheRelocationTargetsRefusesAMissingParentAndAFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	typo := filepath.Join(dir, "relcoated", "hf")
	if _, err := EnsureCacheRelocationTargets([]config.CacheRelocation{{Subdir: "hf", Target: typo}}); err == nil ||
		!strings.Contains(err.Error(), "parent directory of the target does not exist") {
		t.Errorf("a missing parent was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(typo)); err == nil {
		t.Errorf("the missing parent was created")
	}
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureCacheRelocationTargets([]config.CacheRelocation{{Subdir: "hf", Target: file}}); err == nil ||
		!strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("a target that is a file was not refused: %v", err)
	}
}
