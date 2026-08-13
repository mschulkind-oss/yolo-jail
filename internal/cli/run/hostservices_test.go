package run

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHostServicesDirIsPrivate: the per-jail host-services dir must be created
// 0700, because it holds endpoint files and an endpoint file carries that
// service's bearer token. svcendpoint refuses to publish a credential into a
// group/world-accessible directory, so a 0755 dir does not merely look untidy —
// every host service dies at spawn.
func TestHostServicesDirIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yolo-host-services-deadbeef")
	mkdirHostServicesDir(dir)
	st, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Errorf("fresh host-services dir mode = %#o, want 0700", perm)
	}
}

// TestHostServicesDirTightensExisting: a host upgrading from a yolo that created
// the dir 0755 must not be left with an unusable one. MkdirAll leaves an existing
// directory's mode alone, so the Chmod is the only thing carrying that host over.
func TestHostServicesDirTightensExisting(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "yolo-host-services-deadbeef")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil { // MkdirAll applies umask
		t.Fatal(err)
	}
	mkdirHostServicesDir(dir)
	st, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o700 {
		t.Errorf("pre-existing 0755 dir left at %#o, want tightened to 0700", perm)
	}
}
