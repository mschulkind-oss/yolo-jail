package integration

// homedaemons_test.go stops, at the end of every test that launched yolo, the host-wide
// daemons that launch left running under the test's temp HOME.
//
// # What a launch leaves behind
//
// A host-wide daemon (`host_daemon.scope: "host"` — claude-oauth-broker, openai-auth-broker and
// aws-auth today) is spawned DETACHED by the launch that first needs it (internal/broker's
// EnsureSingleton), and outlives that launch by design: it serves every jail on the machine.
// Its rendezvous files are machine-wide /tmp paths keyed by loophole name
// (paths.HostSingletonPIDFile), but the process runs with the SPAWNING launch's environment —
// in this suite, a temp HOME that t.TempDir deletes when the test ends. Inventoried on
// 2026-09-28 by listing every process whose HOME is an integration temp dir after
// `go test -run 'TestPackRendersConfigAndLauncher|TestWorkspaceSkillsReachAContainerJail|
// TestAWSAuthServesTheFourKeysOverTheLoopbackHop'`:
//
//   - an openai-auth-broker spawned by TestPackRendersConfigAndLauncher/claude (the claude pack
//     `needs` openai-auth), alive with its HOME deleted and --state-file under it. The pi and
//     codex subtests after it spawned none: they ADOPTED it, a daemon serving a deleted HOME;
//   - a claude-oauth-broker from an earlier run, alive for hours with its HOME and cwd deleted,
//     which the claude subtest had likewise adopted instead of spawning its own;
//   - no aws-auth daemon, only because its fixture stops it (awsauth_test.go);
//   - no per-jail host service and no scratch remover: the launcher tears the first down with
//     the jail, and awaitDetachedWriters already waits for the second.
//
// With this file's stop in place the same run's -v output reads, per subtest, "stopped the
// host-wide daemons left running under this test's HOME …": claude-oauth-broker and
// openai-auth-broker for claude, openai-auth-broker for pi and for codex — each subtest now
// spawns its own, and nothing is left alive afterwards.
//
// A daemon like that writes into a HOME that is being deleted (recreating a directory under
// t.TempDir's RemoveAll is the "directory not empty" failure detachedwriters_test.go
// describes), holds the singleton slot every later test then ADOPTS instead of starting its
// own — which is why several tests stop "a grantless openai-auth-broker singleton … so this
// test can own the slot" — and accumulates across runs. The daemons now also exit on their own
// when their state dir goes (internal/hostservice/statedir.go), but that is a poll, not a
// barrier: this file is what makes the cleanup ORDERED, the daemon gone before the HOME is
// removed.
//
// # How a daemon is identified as this test's
//
// Through its PID FILE, the way `yolo host-daemon stop` finds it, and never by a process search
// by name (a search matches daemons this test never started, including the developer's own).
// Then by its HOME: a pid is this test's only if the process runs with HOME set to the test's
// temp home. That dir is unique to the test, so a match cannot be anyone else's process, and a
// stale PID file naming a recycled pid does not match. The HOME is read from
// /proc/<pid>/environ, so on a host without /proc (darwin) nothing is identified and nothing is
// stopped — there the daemon's own exit when its state dir goes is the whole remedy.
//
// The stop runs under the singleton's SPAWN LOCK (paths.HostSingletonLock), the flock every
// ensure takes, so no launch — of this run or an overlapping one — is between "the daemon is
// alive" and fronting it while it goes. The rendezvous files are removed only while the PID
// file still names the stopped process, as internal/broker's BrokerKill does.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostDaemonLockWait bounds the wait for a singleton's spawn lock. An ensure holds it for a
// spawn plus its socket wait (broker.BrokerSpawnTimeout) and, when it restarts a stale daemon,
// a kill (broker.BrokerKillTimeout) before that; this is both with margin.
const hostDaemonLockWait = 30 * time.Second

// stopHomeHostDaemons stops every host-wide daemon whose process runs with HOME=home, waiting
// for each to exit, and names the ones it stopped ("<name> pid <n>"). See the file header for
// how a daemon is identified and why.
func stopHomeHostDaemons(home string) ([]string, error) {
	if home == "" {
		return nil, nil
	}
	var stopped []string
	var errs []error
	for _, name := range broker.RendezvousSingletonNames() {
		pid, err := stopHomeHostDaemon(name, home)
		if err != nil {
			errs = append(errs, err)
		}
		if pid != 0 {
			stopped = append(stopped, fmt.Sprintf("%s pid %d", name, pid))
		}
	}
	return stopped, errors.Join(errs...)
}

// stopHomeHostDaemon stops the singleton called name if its process runs with HOME=home, and
// returns the pid it stopped, or 0.
func stopHomeHostDaemon(name, home string) (int, error) {
	// Cheap first look, outside the lock: almost every launch left nothing of its own here.
	if pid := hostDaemonPID(name); pid == 0 || processGone(pid) || !processRunsWithHome(pid, home) {
		return 0, nil
	}
	unlock, err := lockHostDaemonSlot(name)
	if err != nil {
		return 0, err
	}
	defer unlock()
	// Again under the lock: an ensure may have replaced the daemon while this waited for it.
	pid := hostDaemonPID(name)
	if pid == 0 || processGone(pid) || !processRunsWithHome(pid, home) {
		return 0, nil
	}
	if err := stopHostDaemonPID(name, pid); err != nil {
		return 0, err
	}
	return pid, nil
}

// hostDaemonPID is the PID the singleton name's PID file names, or 0.
func hostDaemonPID(name string) int {
	raw, err := os.ReadFile(paths.HostSingletonPIDFile(name))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// processRunsWithHome reports that pid's environment carries HOME=home. False whenever that
// cannot be read, which is the direction that stops nothing.
func processRunsWithHome(pid int, home string) bool {
	if goruntime.GOOS != "linux" {
		return false // no /proc: see the file header
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return false
	}
	want := "HOME=" + filepath.Clean(home)
	for _, kv := range strings.Split(string(raw), "\x00") {
		if strings.HasPrefix(kv, "HOME=") {
			return "HOME="+filepath.Clean(strings.TrimPrefix(kv, "HOME=")) == want
		}
	}
	return false
}

// lockHostDaemonSlot takes the singleton's spawn lock, polling rather than blocking so a lock
// held past hostDaemonLockWait is reported instead of hanging the cleanup.
func lockHostDaemonSlot(name string) (func(), error) {
	f, err := os.OpenFile(paths.HostSingletonLock(name), os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("opening %s's spawn lock: %w", name, err)
	}
	for deadline := time.Now().Add(hostDaemonLockWait); ; time.Sleep(50 * time.Millisecond) {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("taking %s's spawn lock %s: %v", name,
				paths.HostSingletonLock(name), err)
		}
	}
}

// stopHostDaemonPID stops the singleton name's process pid — TERM, then KILL after a grace —
// and then removes the rendezvous files it owned, but only while the PID file still names it,
// so a successor some launch started in between is never unlinked. It never falls back to a
// process search: BrokerKill does, and a search could match a daemon this test never started.
func stopHostDaemonPID(name string, pid int) error {
	_ = syscall.Kill(pid, syscall.SIGTERM)
	for deadline := time.Now().Add(5 * time.Second); !processGone(pid) && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
	}
	if !processGone(pid) {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		for deadline := time.Now().Add(2 * time.Second); !processGone(pid) && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !processGone(pid) {
		return fmt.Errorf("%s pid %d survived SIGTERM and SIGKILL", name, pid)
	}
	if hostDaemonPID(name) != pid {
		return nil
	}
	// The socket, the private host socket beside it (the openai-auth and aws-auth daemons
	// each derive one), and the PID file's `.capability` and `.launch-check` stamps and
	// `.settings` record: what BrokerKill removes, plus the sibling it does not know about. The
	// PID file goes last, so a reader that still finds it finds the rest.
	socket := paths.HostSingletonSocket(name)
	pidFile := paths.HostSingletonPIDFile(name)
	for _, p := range []string{socket, openaiauthdaemon.HostSocketPath(socket),
		awsauthdaemon.HostSocketPath(socket), pidFile + ".capability", pidFile + ".launch-check",
		pidFile + ".settings", pidFile} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// --- the tests ---

// isolateHostSingletons points this process's view of the singleton rendezvous at a private
// directory for one test, so a fake daemon's files never touch the machine's /tmp slots.
func isolateHostSingletons(t *testing.T) {
	t.Helper()
	saved := paths.HostSingletonDir
	paths.HostSingletonDir = t.TempDir()
	t.Cleanup(func() { paths.HostSingletonDir = saved })
}

// fakeHostDaemon starts a stand-in singleton called name, running with HOME=home, and writes
// the rendezvous files an ensure would: the spawn lock, the PID file, a socket path and the
// two PID-file sidecars. It is `sleep`, because what is under test is how the harness FINDS
// and stops a daemon, not anything a daemon does.
func fakeHostDaemon(t *testing.T, name, home string) *exec.Cmd {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("the harness identifies a daemon's HOME through /proc, which this host lacks " +
			"(homedaemons_test.go's header); it stops nothing here, so there is nothing to test")
	}
	cmd := exec.Command("sleep", "600")
	cmd.Env = append(os.Environ(), "HOME="+home)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	// Start returns once the child's execve has closed its close-on-exec fds, which the kernel
	// does BEFORE it records where the new image's environment lives, so for a moment
	// /proc/<pid>/environ reads empty and the process "runs with no HOME". A real daemon is
	// long past that window when the harness looks; this stand-in is looked at at once, so
	// wait until it shows the HOME it was given (a CI flake, 1 in ~400 under load, until this).
	for deadline := time.Now().Add(10 * time.Second); !processRunsWithHome(cmd.Process.Pid, home); {
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in %s (pid %d) never showed HOME=%s in /proc/%d/environ",
				name, cmd.Process.Pid, home, cmd.Process.Pid)
		}
		time.Sleep(time.Millisecond)
	}
	pidFile := paths.HostSingletonPIDFile(name)
	for p, body := range map[string]string{
		paths.HostSingletonLock(name):   "",
		pidFile:                         strconv.Itoa(cmd.Process.Pid) + "\n",
		pidFile + ".capability":         "fake\n",
		pidFile + ".launch-check":       "fake\n",
		pidFile + ".settings":           "{}\n",
		paths.HostSingletonSocket(name): "",
	} {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cmd
}

// TestStopHomeHostDaemonsStopsOnlyTheHomesOwn runs under -short: a stand-in daemon under the
// test's home is stopped and its rendezvous files removed, and one under another home — the
// developer's own, as far as this test can tell — is left running with its files intact.
func TestStopHomeHostDaemonsStopsOnlyTheHomesOwn(t *testing.T) {
	isolateHostSingletons(t)
	mine, theirs := t.TempDir(), t.TempDir()
	own := fakeHostDaemon(t, "fake-own", mine)
	other := fakeHostDaemon(t, "fake-other", theirs)

	stopped, err := stopHomeHostDaemons(mine)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("fake-own pid %d", own.Process.Pid); len(stopped) != 1 || stopped[0] != want {
		t.Errorf("stopped = %v, want [%s]", stopped, want)
	}
	if !processGone(own.Process.Pid) {
		t.Errorf("the daemon running under %s is still alive", mine)
	}
	for _, p := range []string{paths.HostSingletonPIDFile("fake-own"),
		paths.HostSingletonSocket("fake-own"), paths.HostSingletonPIDFile("fake-own") + ".capability",
		paths.HostSingletonPIDFile("fake-own") + ".launch-check",
		paths.HostSingletonPIDFile("fake-own") + ".settings"} {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s survived the stop (%v)", p, err)
		}
	}
	if _, err := os.Lstat(paths.HostSingletonLock("fake-own")); err != nil {
		t.Errorf("the spawn lock is the one rendezvous file that stays (broker.RendezvousSingletonNames "+
			"lists a singleton by it): %v", err)
	}

	if processGone(other.Process.Pid) {
		t.Errorf("the daemon running under ANOTHER home (%s) was stopped", theirs)
	}
	if hostDaemonPID("fake-other") != other.Process.Pid {
		t.Error("the other home's PID file was touched")
	}
}

// A PID file naming a process that is NOT this home's — a recycled pid, or a successor some
// other launch spawned — is left alone, file and process both.
func TestStopHomeHostDaemonsLeavesAPidFileNamingSomeoneElse(t *testing.T) {
	isolateHostSingletons(t)
	mine := t.TempDir()
	stranger := fakeHostDaemon(t, "fake", t.TempDir())
	if stopped, err := stopHomeHostDaemons(mine); err != nil || len(stopped) != 0 {
		t.Fatalf("stopped %v (%v), want nothing", stopped, err)
	}
	if processGone(stranger.Process.Pid) || hostDaemonPID("fake") != stranger.Process.Pid {
		t.Error("a daemon that is not this home's was stopped, or its PID file removed")
	}
}

// THE CALL SITE, BY BEHAVIOR: a runCommand launch's cleanup stops a host-wide daemon running
// under the launch's HOME. The daemon is a stand-in in a private rendezvous dir, and the launch
// is `yolo --version`, since what is pinned is the harness's stop and not the spawn. Delete the
// stopHomeHostDaemons call from awaitDetachedWriters and the stand-in outlives the subtest.
func TestALaunchesCleanupStopsItsHomesHostDaemons(t *testing.T) {
	requireJail(t)
	isolateHostSingletons(t)
	ws := t.TempDir()
	daemon := fakeHostDaemon(t, "fake-launch", os.Getenv("HOME"))
	t.Run("launch", func(t *testing.T) {
		if res := runYoloCLI(t, ws, "--version"); res.rc != 0 {
			t.Fatalf("yolo --version: rc=%d\n%s", res.rc, res.combined())
		}
	})
	if !processGone(daemon.Process.Pid) {
		t.Error("a host-wide daemon running under the launch's HOME outlived the launch's " +
			"cleanup: awaitDetachedWriters no longer calls stopHomeHostDaemons")
	}
}
