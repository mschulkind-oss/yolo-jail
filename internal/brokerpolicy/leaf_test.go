package brokerpolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Declaration decoding imports this matcher. Pin the entire production package
// to pure stdlib leaves, so config, runtime, store and I/O cannot enter that path.
func TestScopeMatcherImportsOnlyPureLeaves(t *testing.T) {
	allowed := map[string]bool{"fmt": true, "slices": true, "strings": true, "unicode": true, "unicode/utf8": true}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		fmtName := "fmt"
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !allowed[path] || imp.Name != nil && (imp.Name.Name == "." || imp.Name.Name == "_") {
				t.Errorf("%s imports %s; declaration matcher must remain a pure leaf", name, path)
			}
			if path == "fmt" && imp.Name != nil {
				fmtName = imp.Name.Name
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == fmtName && selector.Sel.Name != "Errorf" {
					t.Errorf("%s uses fmt.%s; only pure error construction is allowed", name, selector.Sel.Name)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no production matcher source was checked")
	}
}
