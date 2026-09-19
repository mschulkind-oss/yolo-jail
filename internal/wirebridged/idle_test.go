package wirebridged

// idle_test.go pins the daemon's HEALTHY IDLE, which crashed the process for as long
// as it has existed.
//
// The package doc calls an idle boot "bind nothing, publish nothing, sleep forever,
// one stderr line saying why". What it actually did was print the line and then:
//
//	fatal error: all goroutines are asleep - deadlock!
//	goroutine 1 [chan receive (nil chan)]:
//	  wirebridged.idleUntilStopped(...)
//
// because Main passed context.Background(), whose Done() is nil, and a receive on a
// nil channel with no other goroutine is a runtime-fatal deadlock.
//
// ⚠ THE DEFECT CANNOT BE PINNED BY CALLING THE OLD CODE. A test that reproduced it
// would take the whole test binary down with it — there is no recovering from a
// runtime deadlock panic. So what is asserted is the PROPERTY that makes idling
// sound: the daemon's context can be done. That is why the first test looks at a
// channel rather than at behaviour.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
	"time"
)

// THE REGRESSION PIN. Revert Main to context.Background() and this fails here
// instead of killing a jail at boot.
func TestDaemonContextCanBeDone(t *testing.T) {
	ctx, stop := daemonContext()
	defer stop()
	if ctx.Done() == nil {
		t.Fatal("the daemon's context has a nil Done channel, so idleUntilStopped " +
			"will deadlock the process instead of idling — which is what " +
			"context.Background() does and why every credential-less boot crashed")
	}
}

// THE CALL-SITE PIN, and it is the one that matters. The test above asserts
// daemonContext is sound; it says nothing about whether Main USES it — revert Main to
// context.Background() and that test still passes, which is precisely the
// callee-pinned/call-site-unpinned shape AGENTS.md names. Main runs the daemon for
// real (it dials, binds and blocks), so an AST check is what is left, the same shape
// internal/cli/run uses for call sites inside runContainer.
func TestMainIdlesOnTheDaemonContext(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "boot.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var usesDaemonContext, usesBackground bool
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Main" || fn.Body == nil {
			return true
		}
		ast.Inspect(fn.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "daemonContext" {
					usesDaemonContext = true
				}
			case *ast.SelectorExpr:
				if id, ok := fun.X.(*ast.Ident); ok && id.Name == "context" && fun.Sel.Name == "Background" {
					usesBackground = true
				}
			}
			return true
		})
		return false
	})
	if !usesDaemonContext {
		t.Error("Main does not call daemonContext(). Whatever context it builds must " +
			"have a non-nil Done, or every healthy idle deadlocks the process instead " +
			"of idling.")
	}
	if usesBackground {
		t.Error("Main builds a context.Background() — its Done() is nil, and a receive " +
			"on nil with no other goroutine is a runtime-fatal deadlock. That is the " +
			"exact defect this file exists for.")
	}
}

// And the behaviour that context buys: an idle daemon exits when stopped, rather
// than blocking forever. `yolo-jaild supervise` stops a child by signalling it, so
// without this there is no path from a stop signal to a clean exit.
func TestIdleUntilStoppedReturnsWhenTheContextIsDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- idleUntilStopped(ctx) }()

	select {
	case <-done:
		t.Fatal("idleUntilStopped returned before the context was done")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case rc := <-done:
		if rc != 0 {
			t.Errorf("an idle that was asked to stop is not a failure: rc = %d, want 0", rc)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idleUntilStopped did not return after its context was cancelled")
	}
}
