package main

// main_test.go pins the dispatch table's wire-bridge row (docs/design/
// wire-bridge-plan.md, Map row "cmd/yolo-jaild"). The supervisor composes
// ["yolo-jaild", "wire-bridge"] from the pack's service contribution, so a
// deleted or misspelled case means a daemon that never boots while every unit
// test of the daemon itself stays green — the callee-pinned-caller-unpinned
// shape AGENTS.md warns about. This test pins the CALLER: `run("wire-bridge")`
// with an empty environment boots into the selection-lazy IDLE (bind nothing,
// sleep forever), so it never returns; if the case is removed, run() falls
// through to usage() and returns 2, and the test fails.

import (
	"testing"
	"time"
)

func TestDispatchRoutesWireBridge(t *testing.T) {
	// Hermetic idle: with the composed tables cleared, the daemon deterministically
	// finds no claude profile and idles WITHOUT binding anything. It must also use
	// a fresh home: the daemon watches the live per-entry channel there, which can
	// otherwise reintroduce this jail's real selection after the process
	// environment below is cleared.
	t.Setenv("JAIL_HOME", t.TempDir())
	t.Setenv("YOLO_PROVIDERS", "")
	t.Setenv("YOLO_PROFILES", "")
	t.Setenv("YOLO_USE_PROFILES", "")
	done := make(chan int, 1)
	go func() { done <- run([]string{"wire-bridge"}) }()
	select {
	case rc := <-done:
		t.Fatalf("wire-bridge returned %d immediately — the subcommand fell through to "+
			"usage() (exit 2) instead of reaching the daemon's idle loop; the dispatch "+
			"case is missing or misspelled", rc)
	case <-time.After(500 * time.Millisecond):
		// Still running: the daemon booted, found no claude profile in this
		// empty test environment, and entered its healthy idle — exactly the
		// §3.4 no-op. The goroutine is left sleeping on purpose; the test
		// process exits without waiting on it.
	}
}

func TestUsageStillExits2(t *testing.T) {
	if rc := run(nil); rc != 2 {
		t.Errorf("run(nil) = %d, want 2", rc)
	}
	if rc := run([]string{"no-such-daemon"}); rc != 2 {
		t.Errorf("run(no-such-daemon) = %d, want 2", rc)
	}
}

func TestDispatchRoutesOpenAIAuthAdapter(t *testing.T) {
	// An invalid listen address reaches adapter startup and returns its runtime
	// failure code. A missing dispatch row would fall through to usage (2).
	if rc := run([]string{"openai-auth-adapter", "--listen", "bad address"}); rc != 1 {
		t.Fatalf("openai-auth-adapter dispatch rc = %d, want adapter failure 1", rc)
	}
}
