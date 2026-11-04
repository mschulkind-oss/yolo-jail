package integration

// homedaemons_test.go makes the end of every test that launched yolo WAIT for the host daemons
// that launch ran under the test's temp HOME, before the HOME is removed.
//
// # What a launch leaves behind
//
// Since HD-R1 (docs/design/host-daemon-ownership.md) every host-side daemon is the launch's own:
// a jail's keeper, a macos-user session or a `yolo host` launch spawns it, and the launch's end
// SIGTERMs it. A credential daemon declares `detach` (the manifest key), so that SIGTERM is not
// waited for: the daemon finishes a refresh in flight and exits on its own, bounded by its
// thirty-second upstream deadline (§1.3, mode 6). In this suite there is never a refresh in
// flight, so it exits within milliseconds — but "within milliseconds of the launch returning"
// is still after the launch returned, and t.TempDir's RemoveAll of the HOME the daemon writes
// into can land first (the "directory not empty" failure detachedwriters_test.go describes).
//
// The suite used to STOP host-wide daemons here instead: one daemon per machine, adopted by every
// later launch, spawned under whichever test's HOME came first. No launch of this build starts
// one, so there is nothing to stop, and stopping by name would now reach a machine-wide daemon
// an older yolo's jails on the developer's machine are using.
//
// # How a daemon is identified as this test's
//
// By its HOME and its command line, both read from /proc: a process whose environment carries
// HOME=<this test's temp home> and whose argv holds `internal daemon <name>` (the self-exec'd
// form every yolo host daemon runs as). The HOME is unique to the test, so a match cannot be
// anyone else's process. The jail keeper is excluded: WaitForKeeper waits for it first. On a host
// without /proc (darwin) nothing is identified, and the daemon's own exit when its state dir goes
// (internal/hostservice/statedir.go) is the whole remedy.
//
// # The bound
//
// homeDaemonBound is the upstream deadline with margin. A daemon still alive past it is a
// straggler HD-R1 does not allow, so it is killed (so the HOME can go) and the test fails naming
// it.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// homeDaemonBound is how long a launch's host daemons may outlive it: the thirty-second upstream
// deadline a refresh in flight is bounded by, with margin.
const homeDaemonBound = 45 * time.Second

// homeHostDaemons lists the live host daemons running under home: pid → daemon name.
func homeHostDaemons(home string) map[int]string {
	out := map[int]string{}
	if home == "" || goruntime.GOOS != "linux" {
		return out
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		args, err := processArgs(pid)
		if err != nil {
			continue
		}
		for i := 0; i+2 < len(args); i++ {
			if args[i] == "internal" && args[i+1] == "daemon" && args[i+2] != "jail-keeper" {
				if !processGone(pid) && processRunsWithHome(pid, home) {
					out[pid] = args[i+2]
				}
				break
			}
		}
	}
	return out
}

// awaitHomeHostDaemons waits up to bound for every host daemon running under home to exit, and
// returns the ones that did not ("<name> pid <n>"), each of which it has killed so the HOME can
// be removed.
func awaitHomeHostDaemons(home string, bound time.Duration) []string {
	var left map[int]string
	for deadline := time.Now().Add(bound); ; time.Sleep(50 * time.Millisecond) {
		left = homeHostDaemons(home)
		if len(left) == 0 || time.Now().After(deadline) {
			break
		}
	}
	var named []string
	for pid, name := range left {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		named = append(named, fmt.Sprintf("%s pid %d", name, pid))
	}
	slices.Sort(named)
	return named
}

// processRunsWithHome reports that pid's environment carries HOME=home. False whenever that
// cannot be read, which is the direction that waits for nothing.
func processRunsWithHome(pid int, home string) bool {
	env, err := processEnviron(pid)
	if err != nil {
		return false
	}
	want := "HOME=" + filepath.Clean(home)
	for _, kv := range env {
		if strings.HasPrefix(kv, "HOME=") {
			return "HOME="+filepath.Clean(strings.TrimPrefix(kv, "HOME=")) == want
		}
	}
	return false
}

// --- the tests ---

// fakeHomeDaemon starts a stand-in host daemon called name, running with HOME=home, whose argv
// carries `internal daemon <name>` as a real one's does. It exits on its own after life. It is
// a shell list, so the shell keeps the argv rather than exec'ing `sleep` in its place.
func fakeHomeDaemon(t *testing.T, name, home string, life time.Duration) *exec.Cmd {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("the harness identifies a daemon's HOME through /proc, which this host lacks " +
			"(homedaemons_test.go's header); it waits for nothing here, so there is nothing to test")
	}
	cmd := exec.Command("sh", "-c", fmt.Sprintf("sleep %.1f; :", life.Seconds()),
		"internal", "daemon", name)
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
	// Until the exec lands, /proc shows neither the argv nor the HOME (see the comment this
	// replaced in TestTheProcessProbesSeeALiveChildAndItsEnd): wait until the harness would see it.
	for deadline := time.Now().Add(10 * time.Second); homeHostDaemons(home)[cmd.Process.Pid] != name; {
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in %s (pid %d) never showed as a host daemon under %s", name,
				cmd.Process.Pid, home)
		}
		time.Sleep(time.Millisecond)
	}
	return cmd
}

// fakeHostDaemonProcess starts the stand-in process alone: `sleep`, running with HOME=home,
// killed at the test's end, and returned only once its environment shows that HOME. The `sleep`
// is this test binary (standInSleepDir), never the system's: macOS 26 hides a platform binary's
// environment, so `/bin/sleep` never shows the HOME there.
func fakeHostDaemonProcess(t *testing.T, name, home string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(filepath.Join(standInSleepDir(t), standInSleepName), "600")
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
	return cmd
}

// TestAwaitHomeHostDaemonsWaitsForOnlyTheHomesOwn runs under -short: a stand-in daemon under the
// test's home that exits on its own is waited for and not reported; one that outlives the bound
// is killed and reported by name; one under ANOTHER home is neither waited for nor touched.
func TestAwaitHomeHostDaemonsWaitsForOnlyTheHomesOwn(t *testing.T) {
	mine, theirs := t.TempDir(), t.TempDir()
	quick := fakeHomeDaemon(t, "fake-quick", mine, 300*time.Millisecond)
	stuck := fakeHomeDaemon(t, "fake-stuck", mine, 600*time.Second)
	other := fakeHomeDaemon(t, "fake-other", theirs, 600*time.Second)

	left := awaitHomeHostDaemons(mine, 2*time.Second)
	if want := fmt.Sprintf("fake-stuck pid %d", stuck.Process.Pid); len(left) != 1 || left[0] != want {
		t.Errorf("left = %v, want [%s]: the one that outlived the bound, and only it", left, want)
	}
	if !processGone(quick.Process.Pid) {
		t.Errorf("the daemon that exits on its own (pid %d) was not waited for", quick.Process.Pid)
	}
	if !awaitProcessGone(stuck.Process.Pid, 5*time.Second) {
		t.Errorf("the daemon that outlived the bound (pid %d) is still alive", stuck.Process.Pid)
	}
	if processGone(other.Process.Pid) {
		t.Errorf("the daemon running under ANOTHER home (%s) was stopped", theirs)
	}
}

// THE CALL SITE, BY BEHAVIOR: a runCommand launch's cleanup waits for a host daemon still
// running under the launch's HOME. The daemon is a stand-in that exits a second after the launch
// returns, and the launch is `yolo --version`, since what is pinned is the harness's wait and not
// the spawn. Delete the awaitHomeHostDaemons call from awaitDetachedWriters and the stand-in is
// still running when the subtest returns.
func TestALaunchesCleanupWaitsForItsHomesHostDaemons(t *testing.T) {
	requireJail(t)
	ws := t.TempDir()
	daemon := fakeHomeDaemon(t, "fake-launch", os.Getenv("HOME"), time.Second)
	t.Run("launch", func(t *testing.T) {
		if res := runYoloCLI(t, ws, "--version"); res.rc != 0 {
			t.Fatalf("yolo --version: rc=%d\n%s", res.rc, res.combined())
		}
	})
	if !processGone(daemon.Process.Pid) {
		t.Error("a host daemon running under the launch's HOME outlived the launch's cleanup: " +
			"awaitDetachedWriters no longer calls awaitHomeHostDaemons")
	}
}
