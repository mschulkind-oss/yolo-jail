package jailcontent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// onecomposer_test.go is the jail's half of docs/plans/notch-convergence.md item 25's structural
// pin (internal/hostskills/onecomposer_test.go is the host's): a jail stages a pack's skills
// through hostskills.ComposeInto — the host's own layer writer — and this package keeps no copier
// of pack skills beside it. The copier it had (copySkillSubdirs, flat and last-wins) is the second
// composer the ruling deleted; a function of that shape coming back fails here.

// jailcontentFuncs parses this package's non-test files and returns every top-level function
// name, and {callee → sorted callers} for the named callees.
func jailcontentFuncs(t *testing.T, callees ...string) (map[string]bool, map[string][]string) {
	t.Helper()
	want := map[string]bool{}
	for _, c := range callees {
		want[c] = true
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	decls := map[string]bool{}
	found := map[string]map[string]bool{}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			decls[fn.Name.Name] = true
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name string
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					name = fun.Name
				case *ast.SelectorExpr:
					name = fun.Sel.Name
				}
				if want[name] {
					if found[name] == nil {
						found[name] = map[string]bool{}
					}
					found[name][fn.Name.Name] = true
				}
				return true
			})
		}
	}
	out := map[string][]string{}
	for callee, set := range found {
		for caller := range set {
			out[callee] = append(out[callee], caller)
		}
		sort.Strings(out[callee])
	}
	return decls, out
}

func TestTheJailStagesPackSkillsThroughTheHostComposer(t *testing.T) {
	decls, got := jailcontentFuncs(t, "ComposeInto", "SkillCollisionError", "SkillPlan")
	for _, gone := range []string{"copySkillSubdirs", "copyTreeDeref", "copyFileDeref"} {
		if decls[gone] {
			t.Errorf("jailcontent declares %s again — the jail's own flat copier of pack skills is "+
				"the second composer item 25 deleted; stage through hostskills.ComposeInto", gone)
		}
	}
	for callee, want := range map[string][]string{
		// Every destination's pack layers are written by the host's writer, from one place.
		"ComposeInto": {"composeSkillLayers"},
		// The staging refuses a collision over the WHOLE plan before any destination is touched,
		// as RenderHostSkills does; the launch's pre-flight calls the same function from run.
		"SkillCollisionError": {"PrepareSkillsWith"},
		// One plan, read by the collision check and the staging alike.
		"SkillPlan": {"PrepareSkillsWith", "SkillCollisionError"},
	} {
		if strings.Join(got[callee], ",") != strings.Join(want, ",") {
			t.Errorf("%s is called from %v, want exactly %v", callee, got[callee], want)
		}
	}
}
