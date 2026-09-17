package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigureScratchPermissionsDirs(t *testing.T) {
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "scratch")
	// Start with mode 0555, mimicking Nix derivation directories.
	if err := os.Mkdir(readOnlyDir, 0o555); err != nil {
		t.Fatal(err)
	}

	configureScratchPermissionsDirs(readOnlyDir)

	fi, err := os.Stat(readOnlyDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o777 {
		t.Errorf("dir perm = %o, want 777", got)
	}
	if fi.Mode()&os.ModeSticky == 0 {
		t.Errorf("dir mode = %v, want sticky bit set", fi.Mode())
	}
}

func TestConfigureScratchPermissionsDirsIgnoresMissing(t *testing.T) {
	// Must not panic or error on non-existent directories.
	configureScratchPermissionsDirs("/nonexistent/path/that/does/not/exist")
}
