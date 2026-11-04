package run

// launchblockcallsite_test.go pins that every loophole discovery the launch path makes reads the
// launch's loopholes block, loopholes.LaunchBlock, which folds a TOML user config's
// `[allow.<pack>] off = true` into it (docs/design/legacy-config-compat.md LC-D3). A site handed
// cfg's raw `loopholes` key instead would run, publish or serve an off-switch loophole the user
// turned off, with every behavior test of the other sites still green.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launchBlockExempt are the functions that may hand loophole discovery something other than a
// LaunchBlock call, each with the reason.
var launchBlockExempt = map[string]string{
	// Its parameter is the block; its one caller is pinned below to pass LaunchBlock.
	"briefingLoopholes": "takes the block as a parameter",
	// Likewise; its one caller is pinned below.
	"mechanicsLoopholeSwitches": "takes the block as a parameter",
	// Discovers only INLINE loopholes (an entry with a `command` naming no pack loophole), which
	// no off switch reaches.
	"HostInlineLoopholes": "inline loopholes only",
}

func TestEveryLoopholeDiscoveryReadsTheLaunchBlock(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sites := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				var arg ast.Expr
				what := ""
				switch n := n.(type) {
				case *ast.CallExpr:
					switch {
					case isSelector(n.Fun, "loopholes", "NewHostSet") && len(n.Args) == 1:
						arg, what = n.Args[0], "loopholes.NewHostSet"
					case isSelector(n.Fun, "loopholes", "NewSelectedSet") && len(n.Args) > 0:
						arg, what = n.Args[0], "loopholes.NewSelectedSet"
					case (isIdent(n.Fun, "briefingLoopholes") || isIdent(n.Fun, "mechanicsLoopholeSwitches")) && len(n.Args) > 0:
						arg, what = n.Args[0], n.Fun.(*ast.Ident).Name
					}
				case *ast.CompositeLit:
					if !isSelector(n.Type, "loopholes", "DiscoverOptions") {
						return true
					}
					for _, elt := range n.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok && isIdent(kv.Key, "LoopholesConfig") {
							arg, what = kv.Value, "loopholes.DiscoverOptions.LoopholesConfig"
						}
					}
				}
				if arg == nil {
					return true
				}
				sites++
				if call, ok := arg.(*ast.CallExpr); ok && isSelector(call.Fun, "loopholes", "LaunchBlock") {
					return true
				}
				if _, exempt := launchBlockExempt[fd.Name.Name]; exempt && launchBlockExempt[what] == "" {
					return true
				}
				t.Errorf("%s: %s in %s is handed %s, not loopholes.LaunchBlock(cfg): an `off = true` under [allow.<pack>] would not reach it",
					fset.Position(arg.Pos()), what, fd.Name.Name, src[fset.Position(arg.Pos()).Offset:fset.Position(arg.End()).Offset])
				return true
			})
		}
	}
	// Control: the walk finds the sites (the review named eight in launch code alone).
	if sites < 8 {
		t.Fatalf("found %d loophole discovery sites; the walk is not seeing the launch's code", sites)
	}
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name && isIdent(sel.X, pkg)
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
