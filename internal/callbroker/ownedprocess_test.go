package callbroker

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// answerOwnedGroupSignalEPERM makes the post-exit group signal answer EPERM without signalling,
// which is darwin's answer when every member of the group is a zombie. Linux answers 0 there,
// so the injection lets both platforms reach the branch that must list the group.
func answerOwnedGroupSignalEPERM(t *testing.T) *int {
	t.Helper()
	calls := 0
	original := signalOwnedGroup
	signalOwnedGroup = func(int, syscall.Signal) error {
		calls++
		return syscall.EPERM
	}
	t.Cleanup(func() { signalOwnedGroup = original })
	return &calls
}

// TestOwnedGroupEPERMWithOnlyTheExitedLeaderIsGone: the leader exited by itself and left no
// descendant, so its group holds only the unreaped leader. EPERM from the group signal is then
// darwin's "no live member", and the completed call keeps its output.
func TestOwnedGroupEPERMWithOnlyTheExitedLeaderIsGone(t *testing.T) {
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := answerOwnedGroupSignalEPERM(t)
	result, err := p.run(context.Background(), Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
	if err != nil || string(result.Stdout) != "done" || result.ExitCode != 0 {
		t.Fatalf("EPERM over a zombie-only group was not treated as gone: stdout=%q exit=%d err=%v", result.Stdout, result.ExitCode, err)
	}
	if *calls != 1 {
		t.Fatalf("group signal calls = %d, want the one post-exit signal", *calls)
	}
}

// TestOwnedGroupEPERMWithALiveMemberFailsClosed: the leader exited but left a live descendant
// in its group. EPERM must not count as gone, because a live member is what refused the
// signal. The descendant is forked into the group before the leader can exit, and it blocks
// on hold, which nothing writes until cleanup releases it, so nothing here waits on a clock.
func TestOwnedGroupEPERMWithALiveMemberFailsClosed(t *testing.T) {
	hold := fixtureFIFO(t, "hold")
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	body := fmt.Sprintf("#!/bin/sh\n(exec 1>&- 2>&-; read x < %q) &\n", hold)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls := answerOwnedGroupSignalEPERM(t)
	type outcome struct {
		result CLIResult
		err    error
	}
	finished := make(chan outcome, 1)
	go func() {
		result, err := p.run(context.Background(), Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
		finished <- outcome{result, err}
	}()
	var got outcome
	select {
	case got = <-finished:
	case <-time.After(testEventGuard):
		t.Fatal("the call did not return after its leader exited")
	}
	if err := got.err; err == nil || !strings.Contains(err.Error(), "group signal failed (operation not permitted)") ||
		!strings.Contains(err.Error(), "descendants may remain") {
		t.Fatalf("EPERM with a live member was treated as gone: err=%v", err)
	}
	if *calls != 1 {
		t.Fatalf("group signal calls = %d, want the one post-exit signal", *calls)
	}
}

// fifoHasReader reports whether some process holds path open for reading, or is blocked opening
// it. Opening it for writing without blocking fails with ENXIO only when there is no reader.
func fifoHasReader(t *testing.T, path string) bool {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
	if errors.Is(err, syscall.ENXIO) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return true
}

// runHeldDescendantFixture runs, through the executor, a leader that forks into its group a
// descendant still holding the leader's stdout and blocked reading hold, then prints done and
// exits 0.
func runHeldDescendantFixture(t *testing.T, hold string) (CLIResult, error) {
	t.Helper()
	binary := filepath.Join(canonicalTempDir(t), "fixture")
	body := fmt.Sprintf("#!/bin/sh\n(read x < %q) &\nprintf done\n", hold)
	if err := os.WriteFile(binary, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := pinCLI(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.run(context.Background(), Classification{Argv: []string{"read"}, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(nil))}, nil)
}

// TestOwnedGroupWaitsForSignalledMembersToExitBeforeWait reproduces a loaded machine where the
// group signal was sent but its members have not exited yet, so they still hold the output pipe.
// The injected signal stops nothing, and the descendant exits only once the code first lists the
// group, so a runner that goes straight to Wait meets WaitDelay every time and reports a
// successful leader as failed (macOS, CI run 42314578948). Nothing here waits on a clock.
func TestOwnedGroupWaitsForSignalledMembersToExitBeforeWait(t *testing.T) {
	hold := fixtureFIFO(t, "hold")
	originalSignal, originalList := signalOwnedGroup, ownedGroupHasNoLiveMember
	t.Cleanup(func() { signalOwnedGroup, ownedGroupHasNoLiveMember = originalSignal, originalList })
	signalOwnedGroup = func(int, syscall.Signal) error { return nil }
	released := false
	ownedGroupHasNoLiveMember = func(pgid int) (bool, error) {
		if !released {
			released = true
			f, err := os.OpenFile(hold, os.O_WRONLY, 0)
			if err != nil {
				return false, err
			}
			_, _ = f.Write([]byte("x\n"))
			_ = f.Close()
		}
		return originalList(pgid)
	}
	result, err := runHeldDescendantFixture(t, hold)
	if err != nil || string(result.Stdout) != "done" || result.ExitCode != 0 {
		t.Fatalf("a successful leader whose group was still exiting failed: stdout=%q exit=%d err=%v", result.Stdout, result.ExitCode, err)
	}
	if !released {
		t.Fatal("the group was never listed after the signal, so nothing waited for its members to exit")
	}
}

// TestOwnedGroupStopsAMemberTheFirstSignalMissed: a member the first group signal did not stop
// (injected here; in production, one forked while that signal was being delivered) is signalled
// again while the leader stays unreaped, and the call succeeds once it is gone.
func TestOwnedGroupStopsAMemberTheFirstSignalMissed(t *testing.T) {
	hold := fixtureFIFO(t, "hold")
	original := signalOwnedGroup
	t.Cleanup(func() { signalOwnedGroup = original })
	calls := 0
	signalOwnedGroup = func(pid int, sig syscall.Signal) error {
		calls++
		if calls == 1 {
			return nil
		}
		return original(pid, sig)
	}
	result, err := runHeldDescendantFixture(t, hold)
	if err != nil || string(result.Stdout) != "done" || result.ExitCode != 0 {
		t.Fatalf("a member the first signal missed failed the call: stdout=%q exit=%d err=%v", result.Stdout, result.ExitCode, err)
	}
	if calls < 2 {
		t.Fatalf("group signal calls = %d, want the group signalled again", calls)
	}
	if fifoHasReader(t, hold) {
		t.Fatal("the descendant the first signal missed survived the call")
	}
}

// TestOwnedGroupMemberStillLiveAfterTheGuardFailsClosed: a member that never exits after the
// signal fails the call once the guard has passed, and the error says descendants may remain.
// The guard is zero because the call is meant to fail; nothing has to beat it.
func TestOwnedGroupMemberStillLiveAfterTheGuardFailsClosed(t *testing.T) {
	hold := fixtureFIFO(t, "hold")
	originalSignal, originalGuard := signalOwnedGroup, ownedGroupDrainGuard
	t.Cleanup(func() { signalOwnedGroup, ownedGroupDrainGuard = originalSignal, originalGuard })
	signalOwnedGroup = func(int, syscall.Signal) error { return nil }
	ownedGroupDrainGuard = 0
	_, err := runHeldDescendantFixture(t, hold)
	if err == nil || !strings.Contains(err.Error(), "still live") || !strings.Contains(err.Error(), "descendants may remain") {
		t.Fatalf("a member still live after the guard was not refused: %v", err)
	}
}
