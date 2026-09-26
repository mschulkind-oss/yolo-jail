// Package testsupport holds helpers shared by test packages. Nothing in a shipped
// binary imports it.
package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

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
// The directory is short on purpose (os.MkdirTemp("/tmp", "ys-")): the socket path must
// stay under darwin's 104-byte sun_path. The spawned daemon learns its socket from its
// argv, so the redirect reaches the child process with no environment variable.
func IsolateHostSingletons() (cleanup func()) {
	// A helper child (a test that re-execs this binary with -test.run) inherits its
	// parent's directory, so any daemon it spawns is stopped with the parent's, and the
	// child, which may exit from inside its test, owns nothing to clean up.
	if dir := os.Getenv(inheritEnv); dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			paths.HostSingletonDir = dir
			return func() {}
		}
	}
	sweepAbandoned()
	dir, err := os.MkdirTemp("/tmp", dirPrefix)
	if err != nil {
		panic("testsupport: creating a private host-singleton dir: " + err.Error())
	}
	_ = os.WriteFile(filepath.Join(dir, ownerFile), []byte(strconv.Itoa(os.Getpid())), 0o644)
	prev := paths.HostSingletonDir
	paths.HostSingletonDir = dir
	_ = os.Setenv(inheritEnv, dir)
	return func() {
		stopSingletonsIn(dir)
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
	ownerFile  = "owner.pid"
)

// sweepAbandoned removes the private directories of test processes that are gone:
// one whose owner.pid names no live process, or that has no owner record and is more
// than ten minutes old. Daemons still running in one are stopped first. A live
// package's directory is never touched, because its owner is alive.
func sweepAbandoned() {
	dirs, _ := filepath.Glob(filepath.Join("/tmp", dirPrefix+"*"))
	for _, d := range dirs {
		fi, err := os.Lstat(d)
		if err != nil || !fi.IsDir() {
			continue
		}
		if data, err := os.ReadFile(filepath.Join(d, ownerFile)); err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 1 && syscall.Kill(pid, 0) == nil {
				continue
			}
		} else if time.Since(fi.ModTime()) < 10*time.Minute {
			continue
		}
		stopSingletonsIn(d)
		_ = os.RemoveAll(d)
	}
}

// stopSingletonsIn stops every daemon a PID file in dir names, but only a process whose
// argv mentions dir: a PID file can outlive its process, and the kernel may have handed
// that PID to something unrelated since. SIGTERM first, then SIGKILL after two seconds.
func stopSingletonsIn(dir string) {
	pidFiles, _ := filepath.Glob(filepath.Join(dir, "yolo-*.pid"))
	for _, f := range pidFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 1 || !processArgvMentions(pid, dir) {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && syscall.Kill(pid, 0) == nil {
			time.Sleep(50 * time.Millisecond)
		}
		if syscall.Kill(pid, 0) == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}

// processArgvMentions reports whether pid's command line contains needle. ps is used
// rather than /proc so the check also runs on darwin.
func processArgvMentions(pid int, needle string) bool {
	out, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
	return err == nil && strings.Contains(string(out), needle)
}
