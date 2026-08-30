package check

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEverySectionIsWired is a CLASS test, written because a section that nothing calls
// is invisible in exactly the way a section exists to prevent.
//
// `yolo check` has no section registry — Check() is a hand-ordered sequence of
// `o.sectionX(r, …)` calls — and every section's own tests invoke the method DIRECTLY. So
// deleting a call from Check() leaves the whole suite green while the check silently stops
// running, which is the shape AGENTS.md names: "a test that pins the CALLEE while the CALL
// SITE is unpinned is not a test". Measured on the host-wrappers section: removing its
// call from check.go failed nothing.
//
// This walks the package source instead of hand-listing the sections, so a section added
// tomorrow is covered without anyone remembering to add it here.
func TestEverySectionIsWired(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}

	defined := map[string]string{} // method name -> file it is declared in
	var callers []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		callers = append(callers, f)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !strings.HasPrefix(fn.Name.Name, "section") {
				continue
			}
			// Only methods on *Options — the section shape.
			star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if id, ok := star.X.(*ast.Ident); ok && id.Name == "Options" {
				defined[fn.Name.Name] = name
			}
		}
	}
	if len(defined) == 0 {
		t.Fatal("found no section methods — the source scan is broken, not the code")
	}

	called := map[string]bool{}
	for _, f := range callers {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			// o.sectionX(...) — the receiver is a plain identifier, and we do not care
			// which one: what matters is that a non-test file invokes it.
			if _, ok := sel.X.(*ast.Ident); ok && strings.HasPrefix(sel.Sel.Name, "section") {
				called[sel.Sel.Name] = true
			}
			return true
		})
	}

	for name, file := range defined {
		if !called[name] {
			t.Errorf("%s is declared in %s but never called from any non-test file — "+
				"`yolo check` will never run it, and its own tests will not notice "+
				"because they invoke it directly", name, file)
		}
	}
	t.Logf("checked %d section methods, all wired", len(defined))
}
