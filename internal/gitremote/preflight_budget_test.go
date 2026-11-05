package gitremote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitPreflightSubprocessDeadlineCoversHelperAndRemainingBudget(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := gitOutput(ctx, bin, "remote", "get-url", "--push", "--all", "origin")
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("unbounded first Git helper: %v after %s", err, time.Since(start))
	}
	next := time.Now()
	_, err = gitOutput(ctx, bin, "config", "--get-all", "push.pushOption")
	if err == nil || time.Since(next) > time.Second {
		t.Fatalf("remaining budget restarted: %v after %s", err, time.Since(next))
	}
}

func TestGitPreflightSubprocessCapsCombinedOutput(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nhead -c 2097152 /dev/zero\nhead -c 2097152 /dev/zero >&2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	output, err := gitOutput(ctx, bin, "config", "--get-all", "push.pushOption")
	if err == nil || len(output) != 0 || time.Since(start) > 2*time.Second {
		t.Fatalf("unbounded output: bytes=%d error=%v elapsed=%s", len(output), err, time.Since(start))
	}
	if !strings.Contains(err.Error(), "signal") && !strings.Contains(err.Error(), "output") {
		t.Fatalf("unexpected cap failure: %v", err)
	}
}
