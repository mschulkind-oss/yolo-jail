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
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// processGone reports that pid is no longer a running process. A ZOMBIE counts as gone: the
// singleton is spawned detached, so its reaper is whatever adopted it, and a container's PID 1
// may never reap it — kill(pid, 0) alone would call it alive forever. A process whose state
// cannot be read at all is NOT gone, so a wait on it runs out its bound rather than passing.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	state, found, err := processState(pid)
	if err != nil {
		return false
	}
	return !found || strings.HasPrefix(state, "Z")
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
