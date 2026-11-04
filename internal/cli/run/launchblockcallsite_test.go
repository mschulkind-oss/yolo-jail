package run

// launchblockcallsite_test.go pins that every loophole discovery the launch path makes reads the
// launch's loopholes block, loopholes.LaunchBlock, which folds a TOML user config's
// `[allow.<pack>] off = true` into it (docs/design/legacy-config-compat.md LC-D3). A site handed
// cfg's raw `loopholes` key instead would run, publish or serve an off-switch loophole the user
// turned off, with every behavior test of the other sites still green. And LaunchBlock must be
// handed the launch's OWN config — a parameter of the enclosing function named cfg or merged, the
// config Run loads, or one of launchConfigFields — never nil, a literal or another call, which would read a block that
// is not the launch's. `yolo check`'s prediction (internal/cli/check/served.go) is walked too, so
// the preflight cannot predict a daemon the launch keeps off.

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

// launchConfigFields are the selector spellings that name the launch's own merged config: the
// assembled inputs' Merged and a live run's cfg.
var launchConfigFields = map[string]bool{"in.Merged": true, "run.cfg": true}

func TestEveryLoopholeDiscoveryReadsTheLaunchBlock(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, filepath.Join("..", "check", "served.go"))
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
					if len(call.Args) == 1 && isLaunchConfig(call.Args[0], fd) {
						return true
					}
					t.Errorf("%s: %s in %s calls loopholes.LaunchBlock(%s), not on the launch's own config (a cfg or merged parameter, or %v)",
						fset.Position(call.Pos()), what, fd.Name.Name, argsText(src, fset, call.Args...), launchConfigFields)
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

// isLaunchConfig reports whether e names the launch's own config: a parameter of fd named cfg
// or merged (or the local Run assigns from o.loadAndValidateConfig), or a launchConfigFields
// selector whose base is a parameter or the receiver of fd.
func isLaunchConfig(e ast.Expr, fd *ast.FuncDecl) bool {
	params := map[string]bool{}
	for _, fl := range []*ast.FieldList{fd.Recv, fd.Type.Params} {
		if fl == nil {
			continue
		}
		for _, f := range fl.List {
			for _, n := range f.Names {
				params[n.Name] = true
			}
		}
	}
	// Run's own cfg is the one local allowed: the config the launch loads and validates.
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) == 0 || len(as.Rhs) != 1 {
			return true
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok && isSelector(call.Fun, "o", "loadAndValidateConfig") {
			if id, ok := as.Lhs[0].(*ast.Ident); ok {
				params[id.Name] = true
			}
		}
		return true
	})
	switch e := e.(type) {
	case *ast.Ident:
		return (e.Name == "cfg" || e.Name == "merged") && params[e.Name]
	case *ast.SelectorExpr:
		base, ok := e.X.(*ast.Ident)
		return ok && params[base.Name] && launchConfigFields[base.Name+"."+e.Sel.Name]
	}
	return false
}

// argsText is the source text of args, comma-joined.
func argsText(src []byte, fset *token.FileSet, args ...ast.Expr) string {
	var parts []string
	for _, a := range args {
		parts = append(parts, string(src[fset.Position(a.Pos()).Offset:fset.Position(a.End()).Offset]))
	}
	return strings.Join(parts, ", ")
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name && isIdent(sel.X, pkg)
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}
