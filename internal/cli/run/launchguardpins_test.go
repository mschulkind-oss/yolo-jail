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
//   - runContainer tells it the skeleton it built and writes both records under its hold
//     (record), stopping where the guard refuses either, since a teardown under way took only
//     what it already knew.
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

	runContainer := funcDecl(t, "run.go", "runContainer")
	fresh := first(runContainer, "buildHomeSkeleton", "noteSkeleton",
		"record", "runtimeWriteTracking", "writeLivePackTree", "startKeeper", "armLaunchSignalsWith",
		"retireLaunchGuard", "registerHerdrAgent", "relay")
	inOrder("runContainer", fresh, "buildHomeSkeleton", "noteSkeleton", "record",
		"runtimeWriteTracking", "writeLivePackTree", "startKeeper", "armLaunchSignalsWith",
		"retireLaunchGuard", "registerHerdrAgent", "relay")

	// The two records are written INSIDE the guard's record, under its hold, and the launch stops
	// where the guard refuses its skeleton or its records: written after the teardown took what it
	// knew, either would be left behind.
	ast.Inspect(runContainer, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || skelCallee(call) != "record" {
			return true
		}
		inside := first(&ast.FuncDecl{Name: ast.NewIdent("record"), Type: &ast.FuncType{},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: call}}}},
			"runtimeWriteTracking", "writeLivePackTree")
		for _, name := range []string{"runtimeWriteTracking", "writeLivePackTree"} {
			if _, ok := inside[name]; !ok {
				t.Errorf("runContainer calls %s outside the launch guard's record", name)
			}
		}
		return false
	})
	conditions := map[string]bool{}
	ast.Inspect(runContainer, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok {
			ast.Inspect(ifs.Cond, func(c ast.Node) bool {
				if call, ok := c.(*ast.CallExpr); ok {
					conditions[skelCallee(call)] = true
				}
				return true
			})
		}
		return true
	})
	for _, name := range []string{"noteSkeleton", "record"} {
		if !conditions[name] {
			t.Errorf("runContainer ignores whether the launch guard took its %s: it must stop when the guard refuses", name)
		}
	}

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
