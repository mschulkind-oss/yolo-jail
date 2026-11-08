package gitremote

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The stall ends on an EVENT: the fake Git announces through a FIFO that it is
// stalled, and only then is the session's context canceled. The elapsed check is a
// generous failure guard under the fake's 30s stall, never a budget to beat: an
// unbounded proposal would wait out the stall and trip it.
func TestForwardAccessBoundsStalledGitProposalBeforeDial(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "invoked")
	stalled := filepath.Join(bin, "stalled.fifo")
	if err := syscall.Mkfifo(stalled, 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho invoked > \""+marker+"\"\necho stalled > \""+stalled+"\"\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	session, endSession := context.WithCancel(context.Background())
	defer endSession()
	announced := make(chan struct{})
	go func() {
		defer close(announced)
		// Blocks until the fake Git opens the FIFO to write, just before it stalls.
		f, err := os.OpenFile(stalled, os.O_RDONLY, 0)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, f)
		_ = f.Close()
		endSession()
	}()
	t.Cleanup(func() {
		// Unblock the reader when the fake Git was never reached.
		if f, err := os.OpenFile(stalled, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = f.Close()
		}
		<-announced
	})
	var stderr bytes.Buffer
	start := time.Now()
	_, code := ForwardAccess("evaluate", []string{"git", "push", "origin", "HEAD:refs/heads/main"}, Env{
		Getenv: func(key string) string {
			if key == EndpointEnv {
				return "synthetic-unreachable-endpoint"
			}
			return ""
		},
		Stderr: &stderr, accessContext: session,
	})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("production proposal did not reach stalled Git: %v", err)
	}
	msg := strings.TrimSpace(stderr.String())
	if code != exitUsage || strings.Contains(msg, "cannot reach git-broker") || time.Since(start) > 20*time.Second {
		t.Fatalf("ForwardAccess did not bound its Git proposal before dial: code=%d elapsed=%s stderr=%s", code, time.Since(start), msg)
	}
}
