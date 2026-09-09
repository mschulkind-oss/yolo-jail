package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// TestCgroupDelegateServeLoopAuditsEveryPath is a CALL-SITE pin, and it exists
// because the thing it guards was missing for months while everything around it
// looked finished.
//
// docs/reference/security-shim.md Principle 5 says every cgroup operation is
// recorded with the caller's host PID, the operation and the result. cgd.RequestOp
// was written for exactly that and had NO production caller: the serve loop parsed
// a request, resolved the cgroup, answered, and wrote nothing. The design stated
// the invariant, the helper existed, and the line joining them was never written —
// so an audit would have found a documented trail producing no bytes.
//
// internal/cgd's own tests cover what a line CONTAINS. They would all stay green
// with the delegate logging nothing at all, which is precisely the shape AGENTS.md
// warns about: a test that pins the callee while the call site is unpinned is not
// a test. This pins the call site.
//
// It reads the source rather than running the daemon because the daemon needs
// cgroup v2, a bound Unix socket and a live container to answer a request — none
// of which exist under `go test -short`. A source assertion that names the exact
// three return paths is worth more than an integration test nobody can run here.
func TestCgroupDelegateServeLoopAuditsEveryPath(t *testing.T) {
	const src = "cgddaemon_linux.go"
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, b, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}

	var fn *ast.FuncDecl
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == "startCgroupDelegateInProc" {
			fn = fd
			break
		}
	}
	if fn == nil {
		t.Fatal("startCgroupDelegateInProc not found — this pin has lost its subject, " +
			"which means it is now vacuous. Repoint it at whatever serves the delegate.")
	}

	// Count Append calls on the auditor inside the serving function.
	appends := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Append" {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "audit" {
			appends++
		}
		return true
	})

	// THREE paths reach a caller and so must each leave a record:
	//   1. the request did not parse       (what a probe of the socket looks like)
	//   2. the container cgroup is absent  (refused before dispatch)
	//   3. cgd.Handle answered             (the ordinary case, ok or error)
	const wantAppends = 3
	if appends != wantAppends {
		t.Errorf("startCgroupDelegateInProc makes %d audit.Append calls, want %d — "+
			"every path that answers a caller must leave a record, including the two "+
			"refusals. Principle 5 is about what CROSSED the boundary, not about what "+
			"succeeded.", appends, wantAppends)
	}

	// RequestOp having a production caller is the specific regression: it is what
	// was orphaned before, and an audit line built without it cannot name the op.
	if !strings.Contains(string(b), "cgd.RequestOp(") {
		t.Error("cgd.RequestOp has no caller here — it was orphaned for months with a " +
			"doc comment claiming it fed the audit log. An audit line that cannot name " +
			"the operation does not satisfy Principle 5.")
	}

	// The warn sink must not be the terminal. A launch may be handing the TTY to an
	// agent, and a notice printed there overlays whatever is drawing — a defect this
	// project has already shipped once and had reported by its maintainer.
	if !strings.Contains(string(b), "housekeepingNote(") {
		t.Error("the auditor's warn sink should be housekeepingNote, so an unwritable " +
			"log never prints over a running agent")
	}
	for _, banned := range []string{"o.Stderr", "os.Stderr", "fmt.Print"} {
		if strings.Contains(string(b), banned) {
			t.Errorf("%s writes to the terminal via %s — the delegate serves behind a "+
				"launch that may have given the TTY to an agent", src, banned)
		}
	}
}
