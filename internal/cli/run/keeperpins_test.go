package run

// keeperpins_test.go pins the keeper's call sites that no behavioral unit test reaches
// (docs/design/jail-lifetime-last-session-wins.md step 3): each fails when its call is deleted.

import (
	"go/ast"
	"go/token"
	"os"
	"testing"
	"time"
)

// TestEveryFreshLaunchWaitsForAnOldKeeper is JL-D28 (1) at its call site: runContainer's arrival
// makes its attach decision in a loop, and once no attach took the launch, asks whether a keeper
// still holds the name and waits for it (with the launch lock released, awaitPreviousKeeper) before
// deciding again, so one container name never has two keepers.
func TestEveryFreshLaunchWaitsForAnOldKeeper(t *testing.T) {
	var loop *ast.ForStmt
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		if f, ok := n.(*ast.ForStmt); ok && loop == nil && callsIn(f.Body)["attachExisting"] {
			loop = f
		}
		return true
	})
	if loop == nil {
		t.Fatal("runContainer's attach decision is no longer a loop; re-anchor this pin, do not delete it")
	}
	pos := map[string]token.Pos{}
	ast.Inspect(loop.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name := skelCallee(call); pos[name] == token.NoPos {
				pos[name] = call.Pos()
			}
		}
		return true
	})
	for _, name := range []string{"holdLaunchLock", "attachExisting", "probeKeeper", "awaitPreviousKeeper"} {
		if pos[name] == token.NoPos {
			t.Errorf("runContainer's arrival loop no longer calls %s", name)
		}
	}
	if !(pos["holdLaunchLock"] < pos["attachExisting"] && pos["attachExisting"] < pos["probeKeeper"] &&
		pos["probeKeeper"] < pos["awaitPreviousKeeper"]) {
		t.Error("the arrival must take the launch lock, try the attach, and only then wait for an old keeper")
	}
	calls := callsIn(funcDecl(t, "keeperspawn.go", "awaitPreviousKeeper"))
	if !calls["releaseLaunchLock"] || !calls["waitForKeeper"] {
		t.Error("awaitPreviousKeeper must release the launch lock and wait for the keeper, never wait holding it")
	}
}

// TestAnAttachSkewRestartWaitsForTheOldKeeper is JL-D26 at its call site: the restart stops the
// jail, and before the fresh launch it continues as spawns a keeper of its own, waits for the old
// one's liveness lock, holding the launch lock throughout.
func TestAnAttachSkewRestartWaitsForTheOldKeeper(t *testing.T) {
	pos := map[string]token.Pos{}
	ast.Inspect(funcDecl(t, "contracttags.go", "restartJailForAttach"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name := skelCallee(call); pos[name] == token.NoPos {
				pos[name] = call.Pos()
			}
		}
		return true
	})
	if pos["stopJail"] == token.NoPos || pos["probeKeeper"] == token.NoPos || pos["waitForKeeper"] == token.NoPos ||
		!(pos["stopJail"] < pos["probeKeeper"] && pos["probeKeeper"] < pos["waitForKeeper"]) {
		t.Error("restartJailForAttach must stop the jail, then wait for its keeper to be gone")
	}
	if pos["releaseLaunchLock"] != token.NoPos {
		t.Error("the restart releases the launch lock; it must hold it throughout (JL-D26)")
	}
}

// TestATeardownRemovesOnlyItsOwnOwnerFile is JL-D28 (4): the chain removes the owner-PID file only
// while it names the process running the chain, so a teardown that runs late cannot take the next
// jail's keeper's file away.
func TestATeardownRemovesOnlyItsOwnOwnerFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	emptyLoopholeDirs(t)
	const cname = "yolo-owner-file"
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPIDFile(cname), []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := goldenOptions("/ws", t.TempDir())
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{Ran: true} }
	o.Getpid = func() int { return 1 }
	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)
	if pid, ok := readOwnerPID(cname); !ok || pid != 99 {
		t.Fatalf("a teardown removed another keeper's owner file (%d, %v)", pid, ok)
	}
	o.Getpid = func() int { return 99 }
	o.teardownAfterExit(nil, "", nil, "", cname, "podman", "", 0)
	if _, ok := readOwnerPID(cname); ok {
		t.Error("the keeper's own teardown left its owner file")
	}
	if callsIn(funcDecl(t, "lifecycle.go", "stopJail"))["clearOwnerPID"] {
		t.Error("stopJail removes the owner file unconditionally again")
	}
}

// TestTheKeepersSelfExecsGoThroughItsOwnBinary: a daemon's argv and the scratch remover's are built
// with selfExecArgv, which gives a keeper its own inode (JL-D5), never execx.SelfExecArgv directly.
func TestTheKeepersSelfExecsGoThroughItsOwnBinary(t *testing.T) {
	for _, tc := range []struct{ file, fn string }{
		{"scratchremoval.go", "spawnScratchRemover"},
		{"loopholesruntime.go", "resolveDaemonArgv"},
	} {
		calls := callsIn(funcDecl(t, tc.file, tc.fn))
		if !calls["selfExecArgv"] {
			t.Errorf("%s no longer builds its self-exec with selfExecArgv", tc.fn)
		}
		if calls["SelfExecArgv"] {
			t.Errorf("%s calls execx.SelfExecArgv, which re-resolves the binary's path", tc.fn)
		}
	}
}
