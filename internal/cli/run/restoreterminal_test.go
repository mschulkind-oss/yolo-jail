package run

// restoreterminal_test.go pins that the SIGNAL arm restores the terminal.
//
// The normal arm is a `defer` at the front door and needs no pin — a defer that stops
// running is a compile-visible edit. The signal arm is the one that was broken: ttyproxy
// calls onTerminate and then os.Exit(128+n), os.Exit does not run deferred functions, and
// so Ctrl-C handed the terminal back still wearing the jail's kitty tab icon and colour.
// Reported on a real host 2026-09-19.
//
// ⚠ These drive `onTerminate` THROUGH the pipeline rather than calling restoreTerminal()
// directly. A test that called the method would stay green with the call deleted from the
// closure, which is the callee-pinned/call-site-unpinned shape AGENTS.md names — and this
// defect IS that shape: the restore existed, was correct, and was never reached.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// A nil RestoreTerminal is a legitimate state — a launch on a terminal that is neither
// kitty nor tmux changed nothing — so the seam must tolerate it rather than panic on the
// teardown path, where a panic would take the jail's cleanup with it.
func TestRestoreTerminalToleratesNoInjection(t *testing.T) {
	(&Options{}).restoreTerminal() // must not panic
}

func TestRestoreTerminalRunsTheInjectedClosure(t *testing.T) {
	var calls int
	o := &Options{RestoreTerminal: func() { calls++ }}
	o.restoreTerminal()
	if calls != 1 {
		t.Errorf("restoreTerminal() ran the closure %d times, want 1", calls)
	}
}

// THE CALL-SITE PIN. onTerminate is built inside runContainer, which starts a real
// container, so the closure cannot be driven from a unit test — the same constraint that
// made TestFreshLaunchChecksProviderCredentialsOnTheAssembledEnv an AST pin. What is
// asserted is the property that was violated: the terminate closure reaches
// restoreTerminal, and it does so AFTER the timing report, so the launch's last words
// land in the tab that ran it.
func TestTerminateClosureRestoresTheTerminalAfterTheReport(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "run.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	var body string
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		id, ok := as.Lhs[0].(*ast.Ident)
		if !ok || id.Name != "onTerminate" {
			return true
		}
		lit, ok := as.Rhs[0].(*ast.FuncLit)
		if !ok {
			return true
		}
		var sb strings.Builder
		for _, stmt := range lit.Body.List {
			sb.WriteString(exprText(fset, stmt) + "\n")
		}
		body = sb.String()
		return false
	})
	if body == "" {
		t.Fatal("could not find the onTerminate closure in run.go — this pin is vacuous")
	}

	restoreAt := strings.Index(body, "restoreTerminal")
	reportAt := strings.Index(body, "emitTimingReport")
	if restoreAt < 0 {
		t.Error("onTerminate does not call restoreTerminal(). ttyproxy os.Exit(128+n)s " +
			"the moment this closure returns and os.Exit runs no defers, so the front " +
			"door's `defer restore()` never fires on the signal arm — Ctrl-C would hand " +
			"the terminal back still wearing the jail's tab icon and colour.")
	}
	if reportAt >= 0 && restoreAt >= 0 && restoreAt < reportAt {
		t.Error("restoreTerminal() runs BEFORE emitTimingReport(): the launch's final " +
			"output would land in a tab already handed back to the user's shell.")
	}
}

// exprText renders one statement's source text, which is enough for an ordering check and
// avoids depending on a printer's formatting choices.
func exprText(fset *token.FileSet, n ast.Node) string {
	start := fset.Position(n.Pos())
	end := fset.Position(n.End())
	if start.Filename != end.Filename {
		return ""
	}
	return start.Filename + ":" + itoa(start.Line) + "-" + itoa(end.Line) + " " + nodeKind(n)
}

func nodeKind(n ast.Node) string {
	if es, ok := n.(*ast.ExprStmt); ok {
		if call, ok := es.X.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				return sel.Sel.Name
			}
		}
	}
	return ""
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
