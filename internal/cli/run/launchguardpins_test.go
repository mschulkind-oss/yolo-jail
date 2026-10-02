package run

import (
	"go/ast"
	"go/token"
	"testing"
)

// TestTheLaunchGuardIsHandedOverWhereAnArmTakesOver pins the call sites no unit test can drive to
// a session (they start a real container or exec into one), for the launch guard
// (launchguard.go, JL-D75):
//
//   - runContainer retires it right after the keeper's arm is installed, before the herdr pane is
//     registered under that arm, and the keeper's spawn is startKeeper's, which refuses a launch
//     the guard ended (errLaunchEnded) and holds the guard through the spawn. A guard left
//     installed would be innermost again once the first session's arm is disarmed, and a Ctrl-C
//     while the session's quit streams the keeper's teardown would then wait out the pre-ready
//     bound instead of stopping the stream;
//   - attachExisting retires it right after the attach's arm, before the exec;
//   - runContainer tells it the skeleton it built, and that the records are coming before the
//     first of them (the tracking file) is written.
//
// Run's own installation is pinned by TestASIGINTWhileTheImageBuildsLeavesNoPackTree, which fails
// without it.
func TestTheLaunchGuardIsHandedOverWhereAnArmTakesOver(t *testing.T) {
	first := func(fd *ast.FuncDecl, names ...string) map[string]token.Pos {
		pos := map[string]token.Pos{}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			for _, name := range names {
				if _, seen := pos[name]; !seen && skelCallee(call) == name {
					pos[name] = call.Pos()
				}
			}
			return true
		})
		return pos
	}
	inOrder := func(fn string, pos map[string]token.Pos, names ...string) {
		t.Helper()
		last := token.NoPos
		for _, name := range names {
			p, ok := pos[name]
			if !ok {
				t.Errorf("%s no longer calls %s; re-anchor this pin, do not delete it", fn, name)
				return
			}
			if p < last {
				t.Errorf("%s calls %s out of order; want %v in that order", fn, name, names)
				return
			}
			last = p
		}
	}

	fresh := first(funcDecl(t, "run.go", "runContainer"), "buildHomeSkeleton", "noteSkeleton",
		"noteRecorded", "runtimeWriteTracking", "writeLivePackTree", "startKeeper", "armLaunchSignals",
		"retireLaunchGuard", "registerHerdrAgent", "relayKeeper")
	inOrder("runContainer", fresh, "buildHomeSkeleton", "noteSkeleton", "noteRecorded",
		"runtimeWriteTracking", "writeLivePackTree", "startKeeper", "armLaunchSignals",
		"retireLaunchGuard", "registerHerdrAgent", "relayKeeper")

	attach := first(funcDecl(t, "run.go", "attachExisting"), "attachSignalArm", "retireLaunchGuard",
		"runArmedSession")
	inOrder("attachExisting", attach, "attachSignalArm", "retireLaunchGuard", "runArmedSession")

	spawn := first(funcDecl(t, "keeperspawn.go", "startKeeper"), "lockSpawn", "unlockSpawn",
		"defaultKeeperSpawner")
	inOrder("startKeeper", spawn, "lockSpawn", "unlockSpawn", "defaultKeeperSpawner")

	run := first(funcDecl(t, "run.go", "Run"), "stageRunPacks", "armLaunchGuard", "endLaunchGuard",
		"discardUnheldPackTree")
	inOrder("Run", run, "stageRunPacks", "armLaunchGuard", "endLaunchGuard", "discardUnheldPackTree")
}
