package callbroker

import (
	"context"
	"crypto/sha256"
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
