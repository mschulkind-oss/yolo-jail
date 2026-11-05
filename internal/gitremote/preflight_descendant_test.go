package gitremote

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A Git process may exit normally after leaving a descendant holding its output FD.
// The descendant must not keep running after the preflight's finite deadline.
func TestGitPreflightExitedParentStopsPipeHoldingDescendant(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "survived")
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n(sleep 1; echo survived > \"$1\") &\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := gitOutput(ctx, bin, marker)
	if err == nil || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("descendant output FD was not bounded: %v (%s)", err, time.Since(start))
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Git's descendant survived after gitOutput returned")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// Block the group signal at the old Cancel-vs-Wait scheduling point: Wait must
// not return (or reap the exited leader) before the signal has completed.
func TestGitPreflightDeadlineJoinsGroupSignalBeforeWait(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "survived")
	ready := filepath.Join(bin, "ready")
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\n(echo ready > \"$2\"; sleep 1; echo survived > \"$1\") &\nwhile [ ! -f \"$2\" ]; do sleep 0.01; done\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := gitOutputWithGroupSignal(ctx, bin, func(pid int, sig syscall.Signal) error {
			select {
			case <-entered:
			default:
				close(entered)
				<-release
			}
			return syscall.Kill(pid, sig)
		}, marker, ready)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("deadline did not enter group cancellation")
	}
	select {
	case err := <-finished:
		t.Fatalf("reaped Git before group cancellation finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("deadline returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("deadline cancellation did not return")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Git descendant ran after synchronized group cancellation")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestGitPreflightOverflowJoinsGroupSignalBeforeWait(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "survived")
	ready := filepath.Join(bin, "ready")
	// The child owns the output pipe before the parent floods and exits.
	script := "#!/bin/sh\n(echo ready > \"$2\"; sleep 1; echo survived > \"$1\") &\nwhile [ ! -f \"$2\" ]; do sleep 0.01; done\nhead -c 1048577 /dev/zero\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := gitOutputWithGroupSignal(ctx, bin, func(pid int, sig syscall.Signal) error {
			close(entered)
			<-release
			return syscall.Kill(pid, sig)
		}, marker, ready)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("overflow did not enter group cancellation")
	}
	select {
	case err := <-finished:
		t.Fatalf("reaped Git before overflow group cancellation finished: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("overflow returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("overflow cancellation did not return")
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("Git descendant ran after overflow cancellation")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestGitPreflightEOFStillCancelsSilentGroupAtDeadline(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "survived")
	script := "#!/bin/sh\nexec 1>&- 2>&-\n(sleep 1; echo survived > \"$1\") &\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := gitOutput(ctx, bin, marker)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("closed output lost deadline: %v (%s)", err, time.Since(start))
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("closed-output Git descendant survived deadline")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestGitPreflightEOFWaitsForOrdinaryExitStatus(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec 1>&- 2>&-\nsleep 0.05\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, err := gitOutput(ctx, bin)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("ordinary closed-output exit changed: %v (%s)", err, time.Since(start))
	}
}
