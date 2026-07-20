package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAlreadyExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "yolo-jail.jsonc")
	must(t, os.WriteFile(existing, []byte("{ existing }"), 0o644))
	var buf bytes.Buffer
	Init(dir, nil, &buf, false)
	if !strings.Contains(buf.String(), "yolo-jail.jsonc already exists.") {
		t.Errorf("expected already-exists message, got %q", buf.String())
	}
	// The existing file is NOT overwritten.
	if data, _ := os.ReadFile(existing); string(data) != "{ existing }" {
		t.Error("existing config was clobbered")
	}
}

func TestInitGitignore(t *testing.T) {
	dir := t.TempDir()
	Init(dir, nil, &bytes.Buffer{}, false)
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), ".yolo/") {
		t.Errorf(".gitignore missing .yolo/: %q", gi)
	}
	// Re-running doesn't duplicate the entry (config now exists → early return,
	// but even the append guard checks containment).
	before := string(gi)
	Init(dir, nil, &bytes.Buffer{}, false)
	after, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(after) != before {
		t.Errorf(".gitignore changed on re-run:\n%q\n->\n%q", before, after)
	}
}
