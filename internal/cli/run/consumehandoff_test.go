package run

import (
	"os"
	"path/filepath"
	"testing"
)

// consumeHandoff is the one-time carry-in signal (docs/design/host-to-jail-handoff.md):
// a present pointer is read, returned, and consumed (renamed .consumed); an absent one
// returns ""; a second call finds nothing. This pins the consume call site — deleting
// the rename would leave the pointer fresh forever, resurfacing a stale task on every
// later launch.
func TestConsumeHandoff(t *testing.T) {
	ws := t.TempDir()
	dir := filepath.Join(ws, ".yolo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const content = "Task: wire the broker. Context: docs/handoff/broker.md"
	handoff := filepath.Join(dir, "handover.md")
	if err := os.WriteFile(handoff, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// First call: returns the content and consumes the pointer.
	if got := consumeHandoff(ws); got != content {
		t.Fatalf("first consume = %q, want %q", got, content)
	}
	if _, err := os.Stat(handoff); !os.IsNotExist(err) {
		t.Errorf("pointer should be consumed (renamed), still present")
	}
	if _, err := os.Stat(filepath.Join(dir, "handover.md.consumed")); err != nil {
		t.Errorf("consumed marker missing: %v", err)
	}

	// Second call: nothing to hand off.
	if got := consumeHandoff(ws); got != "" {
		t.Errorf("second consume = %q, want empty (already consumed)", got)
	}

	// Absent from the start: empty, no marker created.
	empty := t.TempDir()
	if got := consumeHandoff(empty); got != "" {
		t.Errorf("absent handoff = %q, want empty", got)
	}
}
