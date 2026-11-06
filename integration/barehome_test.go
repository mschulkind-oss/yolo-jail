package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoJailTestPointsHOMEAtABareTempDir refuses a container test that sets HOME to a
// fresh temp dir with none of packHomeSharedStores linked into it.
//
// That HOME passes in yolo's own jail and fails on every GitHub runner. Root podman keeps
// its graphroot at /var/lib/containers/storage whatever HOME says; rootless podman keeps
// it at $HOME/.local/share/containers/storage, so the launch loads the whole jail image
// into the temp dir and leaves overlay files owned by the runner's subuids, which
// t.TempDir()'s RemoveAll cannot unlink. The four MCP auth tests failed that way in CI run
// 42265513840 after their assertions passed. Use isolateHome/packHome, or
// withPrivateFixtureYoloStore for private yolo state; both keep the store links.
//
// It runs under -short, because the class it guards is invisible to every local run.
func TestNoJailTestPointsHOMEAtABareTempDir(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range bareTempHOMEs(fset, f) {
			t.Errorf("%s: a jail test sets HOME to a bare temp dir; on rootless podman its "+
				"image store moves there and TempDir cleanup fails. Use isolateHome, packHome "+
				"or withPrivateFixtureYoloStore (see packHomeSharedStores).", hit)
		}
	}
}

// TestBareTempHOMEGuardCatchesTheShape pins the guard's predicate on the shape the MCP auth
// tests had, and on the helper they use now, so a guard that silently matches nothing fails.
func TestBareTempHOMEGuardCatchesTheShape(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		want      int
	}{
		{"bare t.TempDir", `package p
func TestX(t *testing.T) { requireJail(t); home := t.TempDir(); t.Setenv("HOME", home) }`, 1},
		{"bare resolvedTempDir inline", `package p
func TestX(t *testing.T) { requireJail(t); t.Setenv("HOME", resolvedTempDir(t)) }`, 1},
		{"no container", `package p
func TestX(t *testing.T) { home := t.TempDir(); t.Setenv("HOME", home) }`, 0},
		{"the helper", `package p
func TestX(t *testing.T) { requireJail(t); home := mcpAuthHome(t, "x"); _ = home }`, 0},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "fixture.go", tc.src, 0)
		if err != nil {
			t.Fatal(err)
		}
		if got := len(bareTempHOMEs(fset, f)); got != tc.want {
			t.Errorf("%s: %d hits, want %d", tc.name, got, tc.want)
		}
	}
	// And the real tree: the guard must read files at all, or the first test is vacuous.
	if _, err := os.Stat("mcpauth_test.go"); err != nil {
		t.Fatalf("the guard globs this package's directory, which lacks mcpauth_test.go: %v", err)
	}
}

// bareTempHOMEs returns the positions, in functions that call requireJail, of a
// t.Setenv("HOME", x) whose x is a t.TempDir()/resolvedTempDir(t) call or a variable
// assigned from one in the same function.
func bareTempHOMEs(fset *token.FileSet, f *ast.File) []string {
	isTempCall := func(e ast.Expr) bool {
		call, ok := e.(*ast.CallExpr)
		if !ok {
			return false
		}
		switch fn := call.Fun.(type) {
		case *ast.SelectorExpr:
			return fn.Sel.Name == "TempDir"
		case *ast.Ident:
			return fn.Name == "resolvedTempDir"
		}
		return false
	}
	var hits []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		jail := false
		temps := map[string]bool{}
		var setenvs []*ast.CallExpr
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range n.Rhs {
					if i < len(n.Lhs) && isTempCall(rhs) {
						if id, ok := n.Lhs[i].(*ast.Ident); ok {
							temps[id.Name] = true
						}
					}
				}
			case *ast.CallExpr:
				if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "requireJail" {
					jail = true
				}
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Setenv" && len(n.Args) == 2 {
					if lit, ok := n.Args[0].(*ast.BasicLit); ok && strings.Trim(lit.Value, "\"`") == "HOME" {
						setenvs = append(setenvs, n)
					}
				}
			}
			return true
		})
		if !jail {
			continue
		}
		for _, call := range setenvs {
			arg := call.Args[1]
			if id, ok := arg.(*ast.Ident); (ok && temps[id.Name]) || isTempCall(arg) {
				hits = append(hits, fset.Position(call.Pos()).String())
			}
		}
	}
	return hits
}
