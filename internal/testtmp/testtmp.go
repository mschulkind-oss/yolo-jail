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
	"crypto/rand"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
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
	root, err := mkSessionDir(os.TempDir())
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

// ChildEnv is env plus what a child of this binary needs to leave nothing behind: the private
// TMPDIR's marker, and TMPDIR itself when env names none. Every exec.Cmd whose Env a test spells
// from scratch (a CLEAN environment, never os.Environ()) and that reaches this binary again —
// directly, through a stub script named `yolo`, or through a program that runs one — wraps it:
//
//	cmd.Env = testtmp.ChildEnv([]string{"HOME=" + home, "PATH=" + bin})
//
// Without it the child cannot tell it is one: its TestMain isolates again, in the machine's /tmp,
// and it exits from inside its test before that directory is removed. An env that already names
// TMPDIR keeps it (the test chose it, and Isolate leaves a descendant's choice alone). In a
// binary that did not isolate, env is returned unchanged.
func ChildEnv(env []string) []string {
	root := IsolatedRoot()
	if root == "" {
		return env
	}
	out := make([]string, 0, len(env)+2)
	hasTMPDIR := false
	for _, kv := range env {
		if strings.HasPrefix(kv, rootEnv+"=") {
			continue
		}
		hasTMPDIR = hasTMPDIR || strings.HasPrefix(kv, "TMPDIR=")
		out = append(out, kv)
	}
	out = append(out, rootEnv+"="+root)
	if !hasTMPDIR {
		out = append(out, "TMPDIR="+root)
	}
	return out
}

// SessionDirPrefix starts the name of every directory Isolate makes. The rest of the name is
// the making process's session id in five base-36 digits, then four random base-36 digits: the
// contract scripts/tmp-leak-gate.py and scripts/completion-census.py read. Each runs its command
// in a session of its own, so a directory a child leaves in the machine's /tmp — one whose
// environment a test spelled without ChildEnv, so it isolated again there and exited from inside
// its test — names the run that leaked it, even after every ancestor has exited. The name is
// short on purpose: /tmp/ plus it is MaxShortDir bytes, so ShortDir still hands it out.
const SessionDirPrefix = "tt"

// mkSessionDir makes base/tt<session id><random>, mode 0700.
func mkSessionDir(base string) (string, error) {
	sid, err := unix.Getsid(0)
	if err != nil {
		sid = 0
	}
	tag := strconv.FormatInt(int64(sid), 36)
	if len(tag) < 5 {
		tag = strings.Repeat("0", 5-len(tag)) + tag
	}
	tag = tag[len(tag)-5:]
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var lastErr error
	for range 100 {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		suffix := make([]byte, len(b))
		for i, c := range b {
			suffix[i] = digits[int(c)%len(digits)]
		}
		dir := filepath.Join(base, SessionDirPrefix+tag+string(suffix))
		lastErr = os.Mkdir(dir, 0o700)
		if lastErr == nil {
			return dir, nil
		}
		if !os.IsExist(lastErr) {
			return "", lastErr
		}
	}
	return "", lastErr
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
