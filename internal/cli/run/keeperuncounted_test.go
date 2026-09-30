package run

// keeperuncounted_test.go pins what a keeper does for a first session its launch could not count
// (docs/design/jail-lifetime-last-session-wins.md JL-P3: "could not count" is never zero; JL-D3).

import (
	"go/ast"
	"go/token"
	"syscall"
	"testing"
	"time"
)

// TestAKeeperWhoseFirstSessionIsUncountedNeverDrainsOnTheCount: a launch that could not take its
// session lock is a session the count does not hold, so zero on the count is not zero sessions. Its
// keeper therefore never ends the jail on the count, as it never does when it cannot open the lock
// itself (JL-D3), and ends it only when its container ends or it is told to: a SIGTERM here, as
// `yolo stop` stops the container. The first build drained at once, stopping the jail under the
// very session it was started for.
func TestAKeeperWhoseFirstSessionIsUncountedNeverDrainsOnTheCount(t *testing.T) {
	f := startKeeperFixture(t, true, func(p *keeperPlan) { p.Uncounted = true })
	if !f.relay() {
		t.Fatalf("the relay ended before ready:\n%s", f.errOut.String())
	}
	time.Sleep(300 * time.Millisecond)
	if n := f.jail.stopCount(); n != 0 {
		t.Fatalf("the keeper stopped the jail of an uncounted first session on the count (%d stops)", n)
	}
	f.signals <- syscall.SIGTERM
	if rc := f.wait(); rc != 128+int(syscall.SIGTERM) {
		t.Errorf("the keeper exited %d, want %d", rc, 128+int(syscall.SIGTERM))
	}
	if f.jail.stopCount() != 1 {
		t.Errorf("the signalled keeper stopped the jail %d times, want once", f.jail.stopCount())
	}
}

// TestTheFreshLaunchTellsItsKeeperWhenItsFirstSessionIsUncounted is the call site: runContainer sets
// the plan's Uncounted from its own session lock after it took it and before it spawns the keeper.
func TestTheFreshLaunchTellsItsKeeperWhenItsFirstSessionIsUncounted(t *testing.T) {
	var holdAt, setAt, spawnAt token.Pos
	ast.Inspect(funcDecl(t, "run.go", "runContainer"), func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			switch skelCallee(x) {
			case "holdSessionLock":
				if holdAt == token.NoPos {
					holdAt = x.Pos()
				}
			case "startKeeper":
				if spawnAt == token.NoPos {
					spawnAt = x.Pos()
				}
			}
		case *ast.AssignStmt:
			if len(x.Lhs) == 1 {
				if sel, ok := x.Lhs[0].(*ast.SelectorExpr); ok && sel.Sel.Name == "Uncounted" && setAt == token.NoPos {
					setAt = x.Pos()
				}
			}
		}
		return true
	})
	if holdAt == token.NoPos || setAt == token.NoPos || spawnAt == token.NoPos || !(holdAt < setAt && setAt < spawnAt) {
		t.Error("runContainer must set the plan's Uncounted between its holdSessionLock and its startKeeper")
	}
}
