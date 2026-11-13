// Package testtmp keeps a test binary's temporary files where they are removed.
//
// Nothing in a shipped binary imports it. It has no yolo imports of its own, so any package's
// tests can use it, including the ones internal/testsupport itself depends on.
//
// THE RULE IT SERVES: a `go test` run leaves nothing in TMPDIR. `just test-fast` runs the suite
// under scripts/tmp-leak-gate.py, which hands it a private TMPDIR and fails, naming every
// leftover, when anything is still there afterward. Two kinds of leftover kept reaching the
// machine's /tmp by the thousand (about 1,000 entries in 15 minutes of test runs, measured
// 2026-11-13), and this package is the fix for each:
//
//   - A CHILD OF THE TEST BINARY never runs its cleanup. A helper re-exec runs TestMain again,
//     so it makes TestMain's process-wide directories, and a t.TempDir, and then exits from
//     inside its test, execs away or is killed before either is removed. Isolate gives the
//     binary one private TMPDIR that its children inherit and that the parent removes.
//   - A DIRECTORY MADE IN A LITERAL /tmp is invisible to any TMPDIR check. ShortDir is the
//     short base an AF_UNIX socket needs, and it is TMPDIR whenever TMPDIR is short enough, so
//     the gate sees what those tests leave.
package testtmp

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// rootEnv marks the TMPDIR Isolate made, so a child of the binary that inherits it reuses it
// instead of making one inside it. Read here and nowhere else, never by production code, so
// it is not a yolo dial.
const rootEnv = "TESTTMP_ROOT"

// MaxShortDir is the longest TMPDIR ShortDir hands out: a socket a test binds two directory
// levels under it (a host-singleton dir and a host-services dir in it) must stay under
// darwin's 104-byte sun_path. /tmp/yt.XXXXXX, the leak gate's TMPDIR, fits; a macOS
// /var/folders TMPDIR does not.
const MaxShortDir = 16

// ShortDir is the base for a temporary directory that will hold an AF_UNIX socket: TMPDIR
// when it is at most MaxShortDir bytes long, and /tmp otherwise. Spell
// os.MkdirTemp(testtmp.ShortDir(), prefix), never os.MkdirTemp("/tmp", prefix):
// TestNoGoFileMakesATempDirInALiteralTmp refuses the literal, because the leak gate cannot
// see a directory made there.
func ShortDir() string {
	if dir := os.TempDir(); len(dir) <= MaxShortDir {
		return dir
	}
	return "/tmp"
}

// Isolate points TMPDIR at a fresh private directory for the rest of this test binary and
// every process it starts that inherits its environment, and returns the release that removes
// that directory. Call it from TestMain after any argv dispatch that turns the binary into a
// daemon or helper, and pass the exit code through the release:
//
//	release := testtmp.Isolate()
//	code := m.Run()
//	os.Exit(release(code))
//
// A child that inherits the directory makes no directory of its own and removes nothing: it
// may exit from inside its test, exec away or be killed, and whatever it left — TestMain's
// process-wide directories, a t.TempDir whose cleanup never ran — goes with the parent's
// directory. The release turns a directory it cannot remove into a failing exit code naming
// it, because a leftover nobody sees is the defect this exists to stop.
func Isolate() (release func(code int) int) {
	// A DESCENDANT OF AN ISOLATED BINARY makes none, whatever its TMPDIR: either it is the
	// ancestor's directory, or a test of the ancestor chose another one for this child (a signal
	// test reading what the child left there), and either way the ancestor removes it.
	if root := os.Getenv(rootEnv); root != "" {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			return func(code int) int { return code }
		}
	}
	prev, had := os.LookupEnv("TMPDIR")
	root, err := os.MkdirTemp("", "t")
	if err != nil {
		panic("testtmp: creating the binary's private TMPDIR: " + err.Error())
	}
	_ = os.Setenv("TMPDIR", root)
	_ = os.Setenv(rootEnv, root)
	return func(code int) int {
		if had {
			_ = os.Setenv("TMPDIR", prev)
		} else {
			_ = os.Unsetenv("TMPDIR")
		}
		_ = os.Unsetenv(rootEnv)
		if err := RemoveAll(root); err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: this test binary's private TMPDIR %s could not be removed: %v\n"+
				"A test left something it cannot delete (a read-only file tree, a child still writing): "+
				"look under that path and make the test that made it remove it.\n", root, err)
			if code == 0 {
				code = 1
			}
		}
		return code
	}
}

// IsolatedRoot is the directory Isolate made for this binary, or "" when it made none.
func IsolatedRoot() string { return os.Getenv(rootEnv) }

// RequireIsolated fails t unless this binary's TMPDIR is the one Isolate made: the pin a package
// whose TestMain isolates keeps, so deleting that call fails a test instead of leaking again.
func RequireIsolated(t testing.TB) {
	t.Helper()
	if root := IsolatedRoot(); root == "" || os.TempDir() != root {
		t.Errorf("TMPDIR is %q at a test's start, not a private one testtmp.Isolate made (%q): "+
			"this package's TestMain must call testtmp.Isolate, or its helper children leak into "+
			"the machine's TMPDIR", os.TempDir(), root)
	}
}

// RemoveAll removes dir like os.RemoveAll, and first makes every directory under it writable
// when that is what stopped it: a module cache or a tool's install tree is read-only by
// design, and os.RemoveAll cannot unlink from a directory it may not write.
func RemoveAll(dir string) error {
	if err := os.RemoveAll(dir); err == nil {
		return nil
	}
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
