package integration

// homenix_test.go stops, at the end of every test that launched yolo, the nix processes that
// launch left running under the test's temp HOME.
//
// # What a launch leaves behind
//
// A fresh launch evaluates its image identity (`nix eval --impure --raw .#imageIdentity`) beside
// its fork-build slot (internal/cli/run/imageprewarm.go). A launch the slot then REFUSES — a patch
// branch that does not merge, say — returns without waiting for that eval, and its nix carries on
// with no parent, under the launch's HOME, writing nix's fetcher cache
// (`$HOME/.cache/nix/fetcher-cache-v4.sqlite`). The internal/nixchildren set stops such a nix when
// a SIGNAL ends the launch; an ordinary refusal is not a signal.
//
// In this suite that HOME is a t.TempDir, so the orphan recreated `.cache/nix` under it after the
// test's own cleanup had removed it, and Go's RemoveAll then failed with only
// `TempDir RemoveAll cleanup: unlinkat <tempdir>: directory not empty`: CI run 38924394239,
// TestPatchBranchForkMergesItsBranchAndRefusesAConflict, whose third launch is refused. Reproduced
// in a jail by listing the test's temp dir just before its RemoveAll: the private fixture HOME was
// already gone, a `nix eval … .#imageIdentity` with parent pid 1 and that HOME was running, and a
// second later `<home>/.cache/nix/fetcher-cache-v4.sqlite` was back.
//
// # How a nix is identified as this test's
//
// By argv[0] (`nix`, as nixchildren starts it) and by HOME, read from /proc as
// homedaemons_test.go does: the HOME is unique to the test, so a match cannot be anyone else's
// nix, and the host's nix daemon never runs with it. On a host without /proc nothing is
// identified and nothing is stopped.
//
// The stop is SIGINT, the interrupt nixchildren gives a nix (what a Ctrl-C at a terminal would
// send), then SIGKILL after the same grace.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// homeNixStopGrace is how long an interrupted nix has to exit before it is killed: nixchildren's
// own bound.
const homeNixStopGrace = 10 * time.Second

// homeNixProcesses lists the live nix processes running under home.
func homeNixProcesses(home string) []int {
	if home == "" || goruntime.GOOS != "linux" {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		args, err := processArgs(pid)
		if err != nil || len(args) == 0 || filepath.Base(args[0]) != "nix" {
			continue
		}
		if !processGone(pid) && processRunsWithHome(pid, home) {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// stopHomeNixProcesses interrupts every nix running under home, waits for each to exit (killing
// it after homeNixStopGrace), and names the ones it stopped ("nix pid <n>").
func stopHomeNixProcesses(home string) ([]string, error) {
	var stopped []string
	var errs []error
	for _, pid := range homeNixProcesses(home) {
		_ = syscall.Kill(pid, syscall.SIGINT)
		for deadline := time.Now().Add(homeNixStopGrace); !processGone(pid) && time.Now().Before(deadline); {
			time.Sleep(50 * time.Millisecond)
		}
		if !processGone(pid) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			for deadline := time.Now().Add(2 * time.Second); !processGone(pid) && time.Now().Before(deadline); {
				time.Sleep(50 * time.Millisecond)
			}
		}
		if !processGone(pid) {
			errs = append(errs, fmt.Errorf("nix pid %d survived SIGINT and SIGKILL", pid))
			continue
		}
		stopped = append(stopped, fmt.Sprintf("nix pid %d", pid))
	}
	return stopped, errors.Join(errs...)
}

// --- the tests ---

// fakeHomeNix starts a stand-in nix running under home: a shell with argv[0] spelled `nix`, as a
// real one's is, which ends on the interrupt as nix does. A shell, not `sleep` itself, which is
// coreutils' multi-call binary on this image and exits at once under an argv[0] it does not know.
func fakeHomeNix(t *testing.T, home string) int {
	t.Helper()
	if goruntime.GOOS != "linux" {
		t.Skip("the harness identifies a nix's HOME through /proc, which this host lacks " +
			"(homenix_test.go's header); it stops nothing here, so there is nothing to test")
	}
	cmd := exec.Command("sh", "-c", `trap 'kill $!; exit 130' INT; sleep 600 & wait`)
	cmd.Args[0] = "nix"
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
	// As fakeHostDaemonProcess: /proc/<pid>/environ reads empty for a moment after Start. And
	// the shell's `sleep &` forks a copy of the shell, argv[0] `nix` and the same HOME, until it
	// execs sleep: wait until no nix under this home is the stand-in's child, or a scan in that
	// window counts two.
	pid := cmd.Process.Pid
	forkedCopy := func() bool {
		for _, p := range homeNixProcesses(home) {
			if parentPid(p) == pid {
				return true
			}
		}
		return false
	}
	// And "no forked copy now" holds too before the shell has forked at all, so a scan right
	// after could still meet the copy: wait until the background child has exec'd sleep (its
	// argv[0] is no longer `nix`), after which the shell only waits and forks nothing more.
	sleeping := func() bool {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return false
		}
		for _, e := range entries {
			child, err := strconv.Atoi(e.Name())
			if err != nil || parentPid(child) != pid {
				continue
			}
			if args, err := processArgs(child); err == nil && len(args) > 0 && filepath.Base(args[0]) != "nix" {
				return true
			}
		}
		return false
	}
	for deadline := time.Now().Add(10 * time.Second); !processRunsWithHome(pid, home) || !sleeping() || forkedCopy(); {
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in nix (pid %d) never stood alone under HOME=%s: %v",
				pid, home, homeNixProcesses(home))
		}
		time.Sleep(time.Millisecond)
	}
	return pid
}

// parentPid reads pid's parent from /proc/<pid>/stat, or 0 when it cannot.
func parentPid(pid int) int {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	// The command name is in parentheses and may hold spaces; the fields after its close are
	// state, then the parent pid.
	i := strings.LastIndexByte(string(b), ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(string(b)[i+1:])
	if len(f) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(f[1])
	return ppid
}

// Only this home's nix is stopped: one under another home, and a non-nix process under this one,
// are left alone.
func TestStopHomeNixProcessesStopsOnlyTheHomesOwn(t *testing.T) {
	mine, theirs := t.TempDir(), t.TempDir()
	nix := fakeHomeNix(t, mine)
	other := fakeHomeNix(t, theirs)
	notNix := fakeHostDaemonProcess(t, "not nix", mine).Process.Pid

	stopped, err := stopHomeNixProcesses(mine)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{fmt.Sprintf("nix pid %d", nix)}; fmt.Sprint(stopped) != fmt.Sprint(want) {
		t.Errorf("stopped = %v, want %v", stopped, want)
	}
	if !processGone(nix) {
		t.Errorf("the nix running under %s is still alive", mine)
	}
	if processGone(other) {
		t.Errorf("a nix running under ANOTHER home (%s) was stopped", theirs)
	}
	if processGone(notNix) {
		t.Error("a process under this home that is not a nix was stopped")
	}
}

// THE CALL SITE, BY BEHAVIOR: a runCommand launch's cleanup stops a nix running under the
// launch's HOME. The launch is `yolo --version`, since what is pinned is the harness's stop and
// not the orphaning. Delete the stopHomeNixProcesses call from awaitDetachedWriters and the
// stand-in outlives the subtest. (TestEveryLaunchSiteAwaitsDetachedWriters pins the same call by
// reading awaitDetachedWriters, under -short.)
func TestALaunchesCleanupStopsTheNixLeftUnderItsHome(t *testing.T) {
	requireJail(t)
	ws := t.TempDir()
	nix := fakeHomeNix(t, os.Getenv("HOME"))
	t.Run("launch", func(t *testing.T) {
		if res := runYoloCLI(t, ws, "--version"); res.rc != 0 {
			t.Fatalf("yolo --version: rc=%d\n%s", res.rc, res.combined())
		}
	})
	if !processGone(nix) {
		t.Error("a nix running under the launch's HOME outlived the launch's cleanup: " +
			"awaitDetachedWriters no longer calls stopHomeNixProcesses")
	}
}
