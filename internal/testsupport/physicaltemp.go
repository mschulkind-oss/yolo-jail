package testsupport

import (
	"os"
	"path/filepath"
)

// UsePhysicalTempDir points TMPDIR at the symlink-free spelling of the temp directory, so
// every t.TempDir() and os.MkdirTemp in the test binary mints a path that already equals its
// own filepath.EvalSymlinks. Call it from the TestMain of a package whose production code
// canonicalizes the paths it is handed (the config edit resolver binds the workspace through
// EvalSymlinks on purpose) and whose tests compare those results against the paths they
// minted.
//
// On macOS the default temp directory is under /var, a symlink to /private/var, so a fixture
// minted there and compared against a resolved path passes on Linux and fails on darwin.
// Reproduce on Linux with a symlinked TMPDIR:
//
//	mkdir -p /tmp/real && ln -sfn /tmp/real /tmp/link && TMPDIR=/tmp/link go test -short ./...
//
// A test about a logical (symlinked) spelling makes its own alias, as the edit-target matrix
// does; resolving the binary's TMPDIR takes nothing from it. When the temp directory cannot
// be resolved the environment is left alone and the tests run as before.
func UsePhysicalTempDir() {
	resolved, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return
	}
	_ = os.Setenv("TMPDIR", resolved)
}
