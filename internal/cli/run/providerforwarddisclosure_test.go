package run

// providerforwarddisclosure_test.go is the call-site pin for the LAUNCH-LINE half of the
// implicit localhost-provider forward disclosure (OQ-PC2,
// docs/reference/wire-bridge.md#only-a-users-provider-url-becomes-an-implicit-forward).
//
// providerlocal_test.go pins the helper — what discloseImplicitProviderForwards prints for a
// given declared list and source set — and the briefing half is pinned by rendering the real
// briefing (TestRenderedBriefingNamesAnImplicitProviderForward). Nothing pinned the CALL:
// deleting it from the launch pipeline left the whole unit suite green, and a port forwarded
// into the jail that the user never wrote became invisible again — the state that cost four
// hypotheses when an aliasing defect put the wire bridge's own port into this list
// (wire-bridge.md#where-the-post-mortem-lives).
//
// WHY THE AST AND NOT A BEHAVIORAL RUN. The call sits in runContainer after the image build and
// load, the credential pre-flights and the argv assembly, and before the host socat forwarders
// start; no unit test drives a launch that far without a real image. This is the repo's existing
// answer for a site a unit test cannot reach (currentimagecallsite_test.go, and the files it
// names). What is pinned, each clause a way the disclosure goes silently wrong:
//
//   - DELETED, or moved to any other function of the package: no launch line at all.
//   - MOVED OUT of the bridge-mode block: printed under host networking, where nothing is
//     forwarded, or not printed where it is.
//   - MOVED BELOW the merge in that block: after mergeHostForwards the declared list and the
//     implicit one are no longer separable, so every forward reads as user-declared and none is
//     named.
//   - HANDED THE WRONG LISTS: the declared list must be the pre-merge `forwardHostPorts`, and
//     the sources the channel's `localProviderForwardSources` (the only list carrying provider
//     attribution).
//   - PRINTING NOWHERE: the printer must write through runContainer's own `out`, the launch
//     stream everything else in the arm uses.
//   - MOVED BELOW the forwarders' start: the hole into the host would open before it is named.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunContainerDisclosesImplicitProviderForwardsBeforeTheMerge(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}
	var fd *ast.FuncDecl
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "runContainer" {
			fd = d
		}
	}
	if fd == nil {
		t.Fatal("run.go has no runContainer — this pin's anchor moved; re-anchor it, do not delete it")
	}

	// The bridge-mode block holding the disclosure, as an `if` whose condition compares
	// appliedNetMode(...) with "bridge".
	var block *ast.IfStmt
	var discloses []*ast.CallExpr
	var startPos token.Pos
	ast.Inspect(fd, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.IfStmt:
			if block == nil && pfdIsBridgeCond(n.Cond) && pfdCalls(n.Body, "discloseImplicitProviderForwards") {
				block = n
			}
		case *ast.CallExpr:
			switch pfdCallee(n) {
			case "discloseImplicitProviderForwards":
				discloses = append(discloses, n)
			case "startHostPortForwarding":
				if startPos == token.NoPos {
					startPos = n.Pos()
				}
			}
		}
		return true
	})

	if len(discloses) == 0 {
		t.Fatal("runContainer no longer calls discloseImplicitProviderForwards: a port a user " +
			"provider caused to be forwarded into the jail is bound with NO launch line naming it " +
			"(OQ-PC2), and the user's own config cannot be grepped for it")
	}
	if len(discloses) > 1 {
		t.Errorf("runContainer calls discloseImplicitProviderForwards %d times; the launch prints "+
			"each implicit forward once", len(discloses))
	}
	if block == nil {
		t.Fatal("the disclosure is not inside runContainer's `if appliedNetMode(...) == \"bridge\"` " +
			"block — implicit forwards exist only in bridge mode, so outside it the line is either " +
			"printed for a forward that does not exist or missing for one that does")
	}
	call := discloses[0]

	// Before the merge, in the same block.
	var mergePos token.Pos
	for _, st := range block.Body.List {
		as, ok := st.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			continue
		}
		if rhs, ok := as.Rhs[0].(*ast.CallExpr); ok && pfdCallee(rhs) == "mergeHostForwards" &&
			pfdIdent(as.Lhs[0]) == "forwardHostPorts" {
			mergePos = as.Pos()
		}
	}
	if mergePos == token.NoPos {
		t.Fatal("the bridge block no longer assigns `forwardHostPorts = mergeHostForwards(...)` — " +
			"this pin's anchor moved; re-anchor it, do not delete it")
	}
	if !(call.Pos() < mergePos) {
		t.Error("the disclosure runs AFTER mergeHostForwards: past the merge the declared and the " +
			"implicit forwards are one list, so no implicit port can be told apart and named")
	}
	if startPos == token.NoPos {
		t.Fatal("runContainer no longer calls startHostPortForwarding — this pin's anchor moved")
	}
	if !(call.Pos() < startPos) {
		t.Error("the disclosure runs after the host forwarders start: the hole opens before it is named")
	}

	// The lists it is handed.
	if len(call.Args) != 3 {
		t.Fatalf("discloseImplicitProviderForwards takes (printer, declared, sources); the call "+
			"passes %d arguments", len(call.Args))
	}
	if got := pfdIdent(call.Args[1]); got != "forwardHostPorts" {
		t.Errorf("the declared list handed to the disclosure is %q, want the pre-merge "+
			"`forwardHostPorts` (a port the user wrote is not named)", pfdExpr(call.Args[1]))
	}
	if got := pfdExpr(call.Args[2]); got != "channel.localProviderForwardSources" {
		t.Errorf("the sources handed to the disclosure are %q, want "+
			"`channel.localProviderForwardSources` — the only list that carries which provider "+
			"asked for each port", got)
	}

	// The printer writes through runContainer's launch stream.
	lit, ok := call.Args[0].(*ast.FuncLit)
	if !ok {
		t.Fatalf("the disclosure's printer is %s, want a func literal writing through `out`",
			pfdExpr(call.Args[0]))
	}
	writes := false
	ast.Inspect(lit.Body, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if sel, ok := c.Fun.(*ast.SelectorExpr); ok && pfdIdent(sel.X) == "out" &&
				strings.HasPrefix(sel.Sel.Name, "print") {
				writes = true
			}
		}
		return true
	})
	if !writes {
		t.Error("the disclosure's printer does not write through runContainer's `out` — the line " +
			"is computed and then printed nowhere")
	}

	// And nowhere else in the package's production code: assembleRunCmd performs the same merge
	// for the container argv and must not print a second copy (wire-bridge.md).
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		pf, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range pf.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || (file == "run.go" && d.Name.Name == "runContainer") {
				continue
			}
			if pfdCalls(d, "discloseImplicitProviderForwards") {
				t.Errorf("%s: %s also calls discloseImplicitProviderForwards; the launch prints each "+
					"implicit forward once, from runContainer", file, d.Name.Name)
			}
		}
	}
}

// pfdCallee names a call's function: `f(...)` → "f", `x.f(...)` → "f".
func pfdCallee(c *ast.CallExpr) string {
	switch fn := c.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// pfdCalls reports whether n contains a call to name.
func pfdCalls(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && pfdCallee(c) == name {
			found = true
		}
		return !found
	})
	return found
}

// pfdIsBridgeCond reports whether cond is `appliedNetMode(...) == "bridge"` (either operand order).
func pfdIsBridgeCond(cond ast.Expr) bool {
	be, ok := cond.(*ast.BinaryExpr)
	if !ok || be.Op != token.EQL {
		return false
	}
	isMode := func(e ast.Expr) bool {
		c, ok := e.(*ast.CallExpr)
		return ok && pfdCallee(c) == "appliedNetMode"
	}
	isBridge := func(e ast.Expr) bool {
		lit, ok := e.(*ast.BasicLit)
		return ok && lit.Value == `"bridge"`
	}
	return (isMode(be.X) && isBridge(be.Y)) || (isMode(be.Y) && isBridge(be.X))
}

// pfdIdent is an identifier's name, or "" for anything else.
func pfdIdent(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// pfdExpr renders a selector chain or identifier ("channel.localProviderForwardSources"), or a
// placeholder naming the node type.
func pfdExpr(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return pfdExpr(e.X) + "." + e.Sel.Name
	}
	return fmt.Sprintf("a %T", e)
}
