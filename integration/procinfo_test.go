package integration

// procinfo_test.go is the harness's view of HOST processes: whether one is gone, what its command
// line is, and which are its children. The answers come from /proc on Linux
// (procinfo_linux_test.go) and from ps on darwin (procinfo_darwin_test.go).
//
// THE SPLIT IS FOR THE MAC JOBS, where a keeper's lifecycle is to be run next
// (docs/design/jail-lifetime-last-session-wins.md §7 step 4). Every reader here used to open /proc,
// which a Mac does not have, and on darwin each failed in the direction that hides it:
// processGone read the missing /proc/<pid>/stat as "the process is gone", so a wait for a live
// process to end returned at once, and keeperPID read an empty command line, so it never found a
// keeper and failed after its whole timeout. TestTheProcessProbesSeeALiveChildAndItsEnd needs no
// container, so it runs under -short, and ci.yml's check-macos job runs the darwin half on every
// push to main.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type processStateObservation struct {
	state          string
	found          bool
	stateErr       error
	flags          uint64
	flagsFound     bool
	flagsErr       error
	startTime      string
	startTimeFound bool
	startTimeErr   error
}

type processLivenessObservation struct {
	gone           bool
	killErr        error
	state          string
	found          bool
	stateErr       error
	flags          uint64
	flagsFound     bool
	flagsErr       error
	startTime      string
	startTimeFound bool
	startTimeErr   error
}

var errProcessStatFlagsUnavailable = errors.New("process stat flags are unavailable")

func observeProcessLiveness(pid int) processLivenessObservation {
	observation := processLivenessObservation{}
	observation.killErr = syscall.Kill(pid, 0)
	if errors.Is(observation.killErr, syscall.ESRCH) {
		observation.gone = true
		return observation
	}
	stat := processStateWithFlags(pid)
	observation.state, observation.found, observation.stateErr = stat.state, stat.found, stat.stateErr
	observation.flags, observation.flagsFound, observation.flagsErr = stat.flags, stat.flagsFound, stat.flagsErr
	observation.startTime, observation.startTimeFound, observation.startTimeErr = stat.startTime, stat.startTimeFound, stat.startTimeErr
	if observation.stateErr == nil {
		observation.gone = !observation.found || strings.HasPrefix(observation.state, "Z")
	}
	return observation
}

// processGone reports that pid is no longer a running process. A ZOMBIE counts as gone: the
// singleton is spawned detached, so its reaper is whatever adopted it, and a container's PID 1
// may never reap it — kill(pid, 0) alone would call it alive forever. A process whose state
// cannot be read at all is NOT gone, so a wait on it runs out its bound rather than passing.
func processGone(pid int) bool {
	return observeProcessLiveness(pid).gone
}

// processHasArgs reports whether pid's command line holds want as consecutive arguments.
func processHasArgs(pid int, want ...string) bool {
	args, err := processArgs(pid)
	if err != nil || len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(args); i++ {
		if slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// TestTheProcessProbesSeeALiveChildAndItsEnd runs the three probes against a child of this test,
// with no container, so it runs under -short on both halves: Linux in every job, darwin in ci.yml's
// check-macos. A live child is not gone, has its command line and is one of this process's
// children; killed and not yet reaped it is a zombie, which counts as gone; reaped it is gone.
func TestTheProcessProbesSeeALiveChildAndItsEnd(t *testing.T) {
	const marker = "4987"
	cmd := exec.Command("sleep", marker)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	reaped := false
	t.Cleanup(func() {
		if !reaped {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	pid := cmd.Process.Pid

	if processGone(pid) {
		state, found, err := processState(pid)
		t.Fatalf("a running child (pid %d) reads as gone: state %q, found %v, err %v", pid, state, found, err)
	}
	// Polled: on Linux Start can return once the exec has replaced the child's memory and before
	// the kernel has written the new command line into it, so a read at once can find it empty
	// (seen once, in a loaded integration run). Every real caller polls too (findKeeperPID).
	for deadline := time.Now().Add(5 * time.Second); !processHasArgs(pid, "sleep", marker); {
		if time.Now().After(deadline) {
			args, err := processArgs(pid)
			t.Errorf("the child's command line does not read as `sleep %s`: %q (%v)", marker, args, err)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if processHasArgs(pid, "sleep", marker+"0") {
		t.Errorf("processHasArgs matched an argument the child was not given")
	}
	if kids := processChildren(os.Getpid()); !slices.Contains(kids, pid) {
		t.Errorf("the child (pid %d) is not among this process's children: %v", pid, kids)
	}

	if err := cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("killing the child: %v", err)
	}
	if !awaitProcessGone(pid, 10*time.Second) {
		state, found, err := processState(pid)
		t.Errorf("a killed child not yet reaped does not read as gone: state %q, found %v, err %v",
			state, found, err)
	}
	if state, found, _ := processState(pid); found && !strings.HasPrefix(state, "Z") {
		t.Errorf("a killed child not yet reaped reads as state %q, want a zombie", state)
	}
	_ = cmd.Wait()
	reaped = true
	if !processGone(pid) {
		t.Errorf("a reaped child (pid %d) does not read as gone", pid)
	}
	if processGone(os.Getpid()) {
		t.Errorf("this test's own process (pid %d) reads as gone", os.Getpid())
	}
	if _, found, err := processState(os.Getpid()); !found || err != nil {
		t.Errorf("this process's own state was not found (found %v, err %v)", found, err)
	}
}

// standInSleepName is the name this test binary answers to as a stand-in's `sleep`
// (standInSleepDir): run under that name, TestMain sleeps instead of running the suite.
const standInSleepName = "sleep"

// runAsStandInSleepIfCalledSo is TestMain's first step: when this binary was run as
// standInSleepDir's `sleep <seconds>`, it sleeps that long and exits 0, as sleep(1) does, and
// SIGTERM ends it as it ends sleep(1). The test binary is never otherwise named `sleep`.
func runAsStandInSleepIfCalledSo() {
	if filepath.Base(os.Args[0]) != standInSleepName {
		return
	}
	if len(os.Args) != 2 {
		os.Stderr.WriteString("usage: sleep <seconds> (the integration test binary as a stand-in's sleep)\n")
		os.Exit(2)
	}
	secs, err := strconv.ParseFloat(os.Args[1], 64)
	if err != nil || secs < 0 {
		os.Stderr.WriteString("sleep: invalid time interval " + strconv.Quote(os.Args[1]) + "\n")
		os.Exit(2)
	}
	time.Sleep(time.Duration(secs * float64(time.Second)))
	os.Exit(0)
}

// standInSleepDir is a directory holding a `sleep` that is THIS test binary (a link named
// standInSleepName), to put first on a stand-in's PATH. A stand-in that runs the system's
// `sleep` depends on that binary's environment being readable, and macOS 26 hides a PLATFORM
// binary's environment from kern.procargs2, its owner's read included: `/bin/sleep` shows its
// arguments and no HOME (the Mac rerun of 2026-11-03,
// docs/lab-reports/2026-11-03-mac-slice-12a-witness-w1.md §10). The test binary is no platform
// binary on any host, so with this first on PATH a stand-in's only platform-sensitive program is
// its `sh` (envVisibleShellDir), and the install that skip names is the whole remedy.
func standInSleepDir(t *testing.T) string {
	t.Helper()
	self := mustExecutable(t)
	dir := t.TempDir()
	if err := os.Symlink(self, filepath.Join(dir, standInSleepName)); err != nil {
		t.Fatalf("linking the stand-in's sleep: %v", err)
	}
	return dir
}

// standInPath is the PATH a stand-in runs with: standInSleepDir's `sleep` first, then
// envVisibleShellDir's `sh` where this host needs one (darwin; it skips naming the install when
// none resolves), then this process's own PATH.
func standInPath(t *testing.T) string {
	t.Helper()
	dirs := []string{standInSleepDir(t)}
	if dir := envVisibleShellDir(t); dir != "" {
		dirs = append(dirs, dir)
	}
	return strings.Join(append(dirs, os.Getenv("PATH")), string(os.PathListSeparator))
}

// THE REMEDY envVisibleShellDir's skip names is the whole remedy: run as startStandIn runs one
// (standInPath), a stand-in shell AND the `sleep` it waits on both show the HOME they were
// given, so no stand-in depends on a platform binary's environment being readable (on darwin the
// system `sleep` is one, and its environment reads empty: the Mac rerun of 2026-11-03, §10). The
// sleep is this test binary, and the stand-in's SIGTERM ends it as it ends sleep(1).
func TestAStandInAndItsSleepBothShowTheirHome(t *testing.T) {
	home := t.TempDir()
	path := standInPath(t)
	sh := "sh"
	if dir := envVisibleShellDir(t); dir != "" {
		sh = filepath.Join(dir, "sh")
	}
	cmd := exec.Command(sh, "-c", standInScript)
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	shell := cmd.Process.Pid
	sleeper := 0
	for deadline := time.Now().Add(10 * time.Second); sleeper == 0; time.Sleep(10 * time.Millisecond) {
		if processRunsWithHome(shell, home) {
			for _, kid := range processChildren(shell) {
				if processRunsWithHome(kid, home) && processHasArgs(kid, standInSleepName, "600") {
					sleeper = kid
				}
			}
		}
		if sleeper == 0 && time.Now().After(deadline) {
			shEnv, shErr := processEnviron(shell)
			var kids []string
			for _, kid := range processChildren(shell) {
				args, _ := processArgs(kid)
				env, err := processEnviron(kid)
				kids = append(kids, fmt.Sprintf("pid %d %q: environment %q (%v)", kid, args, env, err))
			}
			t.Fatalf("the stand-in (pid %d, environment %q (%v)) and a `sleep 600` child never both "+
				"showed HOME=%s; children: %v", shell, shEnv, shErr, home, kids)
		}
	}
	self, err := filepath.EvalSymlinks(mustExecutable(t))
	if err != nil {
		t.Fatal(err)
	}
	if real, err := filepath.EvalSymlinks(filepath.Join(filepath.SplitList(path)[0], standInSleepName)); err != nil || real != self {
		t.Errorf("the stand-in's sleep resolves to %q (%v), want this test binary %s", real, err, self)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if !awaitProcessGone(sleeper, 5*time.Second) {
		t.Errorf("the stand-in's sleep (pid %d) outlived the stand-in's SIGTERM", sleeper)
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return self
}
