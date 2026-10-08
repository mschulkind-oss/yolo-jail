// Package testsupport holds helpers shared by test packages. Nothing in a shipped
// binary imports it.
package testsupport

import (
	"os"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/heldchildren"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// IsolateHostSingletons points paths.HostSingletonDir at a fresh private directory and
// returns the cleanup that undoes it. Call it from the TestMain of every package whose
// tests spawn a real host-wide singleton daemon (a launch through the run pipeline, the
// broker's ensure, `yolo host-daemon`), with the cleanup after m.Run.
//
// Two reasons, both measured on 2026-09-25. `go test ./...` runs packages in parallel,
// so two packages' daemons on the one machine-wide /tmp/yolo-<name>.sock took each
// other's socket and a launch in the other package failed ("exited at startup without
// binding its socket"). And a daemon a test spawned outlived the test binary: a
// `run.test internal daemon claude-oauth-broker` from a build nine hours earlier was
// still serving the machine path, so every later test that ensured a broker adopted a
// stale one.
//
// THE CLEANUP TOUCHES ONLY WHAT THIS PROCESS MADE: the one directory it created, and the
// daemons this process spawned and holds the handles of (heldchildren). It never looks
// at another directory under /tmp and never signals a PID read from a file, because the
// machine is shared — other agents, other test runs and nested jails run beside it, and
// a PID file can outlive its process and name one the kernel has reused.
//
// The directory is short on purpose (os.MkdirTemp("/tmp", "ys-")): the socket path must
// stay under darwin's 104-byte sun_path. The spawned daemon learns its socket from its
// argv, so the redirect reaches the child process with no environment variable.
func IsolateHostSingletons() (cleanup func()) {
	// A helper child (a test that re-execs this binary with -test.run) inherits its
	// parent's directory and owns nothing to clean up: it may exit from inside its test,
	// and the parent removes the directory. A daemon the child spawned is the child's,
	// not the parent's, so the parent cannot stop it; it exits once its state directory,
	// under the test's temporary HOME, is removed (hostservice.WatchStateDir).
	if dir := os.Getenv(inheritEnv); dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			paths.HostSingletonDir = dir
			return func() {}
		}
	}
	dir, err := os.MkdirTemp("/tmp", dirPrefix)
	if err != nil {
		panic("testsupport: creating a private host-singleton dir: " + err.Error())
	}
	heldchildren.Enable()
	prev := paths.HostSingletonDir
	paths.HostSingletonDir = dir
	_ = os.Setenv(inheritEnv, dir)
	return func() {
		heldchildren.StopAll(stopGrace)
		paths.HostSingletonDir = prev
		_ = os.Unsetenv(inheritEnv)
		_ = os.RemoveAll(dir)
	}
}

const (
	// inheritEnv hands the directory to this binary's helper children. It is read here
	// and nowhere else, never by production code, so it is not a yolo dial.
	inheritEnv = "TESTSUPPORT_HOST_SINGLETON_DIR"
	dirPrefix  = "ys-"
	// stopGrace is how long a held daemon has to exit on SIGTERM before SIGKILL.
	stopGrace = 2 * time.Second
)
