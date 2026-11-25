package config

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A launch's staging hands the resolver the progress tail (ResolvePackSpec.Tail), and the resolver
// writes a per-pack status to it and closes the stream: delete the Open or the staging line in
// resolvePack and this fails.
func TestResolvePackWritesItsStagingToTheTail(t *testing.T) {
	releaseProcessTreeAtEnd(t)
	root := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"pack.json": `{"name":"plain"}`, "a.md": "a\n", "b.md": "b\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var got bytes.Buffer
	var opened []string
	closed := false
	tail := func(name string) io.WriteCloser {
		opened = append(opened, name)
		return &closeRecorder{Writer: &got, closed: &closed}
	}
	entry := PackEntry{Source: "file://" + root, Name: "plain"}
	res, err := ResolvePack(entry, ResolvePackSpec{Dest: filepath.Join(t.TempDir(), "plain"), Tail: tail})
	if err != nil || res.Staged == nil {
		t.Fatalf("ResolvePack: %v", err)
	}
	if len(opened) != 1 || opened[0] != "plain" {
		t.Fatalf("streams opened = %q, want one named for the pack", opened)
	}
	if !strings.Contains(got.String(), "staging ") || !strings.Contains(got.String(), "staged 3 files") {
		t.Errorf("the staging copy did not report to the tail:\n%s", got.String())
	}
	if !closed {
		t.Error("the pack's stream stayed open after it was resolved")
	}

	// No tail, no stream: the resolver is what it was.
	if _, err := ResolvePack(entry, ResolvePackSpec{Dest: filepath.Join(t.TempDir(), "plain")}); err != nil {
		t.Fatalf("ResolvePack without a tail: %v", err)
	}
}

type closeRecorder struct {
	io.Writer
	closed *bool
}

func (c *closeRecorder) Close() error { *c.closed = true; return nil }
