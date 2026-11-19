package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestPrivateSuiteColdRecipesOwnTheirCaptureStoreAfterPackHome(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "privatesuiterecipes_linux_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestPrivateSuiteFiniteRecipesReachTheActualGuestColdAndFresh" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			closure, ok := node.(*ast.FuncLit)
			if !ok {
				return true
			}
			configured, isolated := false, false
			for _, statement := range closure.Body.List {
				ast.Inspect(statement, func(node ast.Node) bool {
					if call, ok := node.(*ast.CallExpr); ok && callName(call.Fun) == "packHome" {
						configured = true
					}
					return true
				})
				if expression, ok := statement.(*ast.ExprStmt); ok {
					if call, ok := expression.X.(*ast.CallExpr); ok && callName(call.Fun) == "withPrivateFixtureYoloStore" {
						isolated = configured
					}
				}
				if loop, ok := statement.(*ast.RangeStmt); ok {
					if value, ok := loop.Value.(*ast.Ident); ok && value.Name == "fresh" {
						found = true
						if !configured || !isolated {
							t.Error("actual cold/fresh recipe caller must isolate captures AFTER packHome and BEFORE either launch")
						}
					}
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("actual cold/fresh recipe caller deleted")
	}
}
