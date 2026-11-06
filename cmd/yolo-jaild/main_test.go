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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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

func TestDispatchRoutesCallBrokerHTTPFront(t *testing.T) {
	// A front with no generation of its own reaches the selected front's startup
	// and returns its daemon failure code; a missing dispatch row falls through to
	// usage (2). No listener or host endpoint is created by this probe.
	t.Setenv(paths.HTTPFrontGenerationsEnv, "")
	t.Setenv(paths.JailDaemonReadyFDEnv, "")
	if rc := run([]string{"call-broker-http-front", "--endpoint", "/selected/host.endpoint", "--loophole", "tasks"}); rc != 1 {
		t.Fatalf("call-broker-http-front dispatch rc = %d, want front refusal 1", rc)
	}
	// The front chooses its own loopback port; a caller-named listen address is
	// no longer a flag, and neither selected identity flag may be omitted.
	for name, args := range map[string][]string{
		"listen":      {"--endpoint", "/selected/host.endpoint", "--loophole", "tasks", "--listen", "127.0.0.1:0"},
		"no endpoint": {"--loophole", "tasks"},
		"no loophole": {"--endpoint", "/selected/host.endpoint"},
	} {
		if rc := run(append([]string{"call-broker-http-front"}, args...)); rc != 2 {
			t.Fatalf("call-broker-http-front %s rc = %d, want usage 2", name, rc)
		}
	}
}

func TestDispatchRoutesOpenAIAuthAdapter(t *testing.T) {
	t.Setenv("YOLO_SERVICE_OPENAI_AUTH_BROKER_TOKEN", strings.Repeat("ab", 32)) // past the caller-token idle
	// An invalid listen address reaches adapter startup and returns its runtime
	// failure code. A missing dispatch row would fall through to usage (2).
	if rc := run([]string{"openai-auth-adapter", "--listen", "bad address"}); rc != 1 {
		t.Fatalf("openai-auth-adapter dispatch rc = %d, want adapter failure 1", rc)
	}
}

// TestDispatchRoutesAWSCredentialAdapter pins the row packs/aws-auth's manifest
// composes (`yolo-jaild aws-credential-adapter --listen 127.0.0.1:1461`).
//
// Same discriminator as its openai-auth sibling and for the same reason: a jail
// daemon's spawn failure is otherwise SILENT — supervisor.superviseOne drops
// start()'s error and backs off forever — so a deleted case here is an empty log
// and no process, with every unit test of the adapter itself still green.
func TestDispatchRoutesAWSCredentialAdapter(t *testing.T) {
	t.Setenv("YOLO_SERVICE_AWS_AUTH_TOKEN", strings.Repeat("ab", 32)) // past the caller-token idle
	if rc := run([]string{"aws-credential-adapter", "--listen", "bad address"}); rc != 1 {
		t.Fatalf("aws-credential-adapter dispatch rc = %d, want adapter failure 1 "+
			"(2 means it fell through to usage — the case is missing or misspelled)", rc)
	}
}

// The root watcher's row: the entrypoint starts ["yolo-jaild", "nix-roots"], and with no
// auto dir bound (this test process has none) the watcher exits 0, where a missing row
// would fall through to usage and exit 2.
func TestNixRootsIsDispatched(t *testing.T) {
	if _, err := os.Stat("/run/yolo/nix-gcroots-auto"); err == nil {
		t.Skip("this process has the auto dir bound; the watcher would run")
	}
	if rc := run([]string{"nix-roots"}); rc != 0 {
		t.Fatalf("run(nix-roots) = %d, want 0 (nothing to watch)", rc)
	}
}

// The worktree reaper's row: the pi pack's extension starts ["yolo-jaild", "reap-worktrees",
// ...]. With a base that does not exist the pass exits 0; a missing row falls through to
// usage and exits 2.
func TestReapWorktreesIsDispatched(t *testing.T) {
	base := filepath.Join(t.TempDir(), "absent")
	if rc := run([]string{"reap-worktrees", "--base", base, "--prefix", "pi-worktree-"}); rc != 0 {
		t.Fatalf("run(reap-worktrees) = %d, want 0 (nothing to reap)", rc)
	}
}

// The background size count's row: the boot starts ["yolo-jaild", "durable-size",
// "--workspace", ws] when its own walk stopped. A workspace with no `.yolo` exits 0; a missing
// row falls through to usage and exits 2.
func TestDurableSizeIsDispatched(t *testing.T) {
	if rc := run([]string{"durable-size", "--workspace", t.TempDir()}); rc != 0 {
		t.Fatalf("run(durable-size) = %d, want 0", rc)
	}
}

// TestRunJudgesGuestScopeAgainstTheRealHome pins run's paths.GuardGuestScope call site (MMP-D7):
// in a run-as-you macos-user session HOME is <ws>/.yolo/home, so a daemon judging scope by HOME
// would refuse the workspace's own .yolo. The dispatch is observed through usage, the one row
// that needs nothing running; delete the call in run and the first assertion fails.
func TestRunJudgesGuestScopeAgainstTheRealHome(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realHome := filepath.Join(base, "you")
	ws := filepath.Join(realHome, "code", "alpha")
	if err := os.MkdirAll(paths.WorkspaceHomeState(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", paths.WorkspaceHomeState(ws))
	var breach *paths.ScopeBreach
	saved := usage
	usage = func() int { breach = paths.WorkspaceScopeBreach(ws); return 2 }
	t.Cleanup(func() { usage = saved })

	t.Setenv(paths.DarwinRealHomeEnv, realHome)
	run(nil)
	if breach != nil {
		t.Fatalf("yolo-jaild's run judged scope by HOME with the real home named: %v", breach.What())
	}
	t.Setenv(paths.DarwinRealHomeEnv, "")
	run(nil)
	if breach == nil {
		t.Error("with no real home named, a workspace containing HOME was admitted; this pin's contrast is gone")
	}
}
