package gitremote

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForwardAccessBoundsStalledGitProposalBeforeDial(t *testing.T) {
	bin := t.TempDir()
	marker := filepath.Join(bin, "invoked")
	script := filepath.Join(bin, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho invoked > \""+marker+"\"\nsleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stderr bytes.Buffer
	start := time.Now()
	_, code := ForwardAccess("evaluate", []string{"git", "push", "origin", "HEAD:refs/heads/main"}, Env{
		Getenv: func(key string) string {
			if key == EndpointEnv {
				return "synthetic-unreachable-endpoint"
			}
			return ""
		},
		Stderr: &stderr, accessTimeout: 120 * time.Millisecond,
	})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("production proposal did not reach stalled Git: %v", err)
	}
	if code == 0 || time.Since(start) > 800*time.Millisecond {
		t.Fatalf("ForwardAccess did not bound its Git proposal: code=%d elapsed=%s stderr=%s", code, time.Since(start), strings.TrimSpace(stderr.String()))
	}
}
