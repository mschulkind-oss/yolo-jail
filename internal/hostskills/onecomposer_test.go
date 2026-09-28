package hostskills

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

// onecomposer_test.go pins docs/plans/notch-convergence.md item 25 (row D6) STRUCTURALLY, in the
// shape surfaceloop_test.go pins item 24: a pack's skills reach a skills destination through ONE
// layer writer (writeLayer) at both notches — the host render (renderDestination) and the jail's
// composition (ComposeInto) — and nothing else calls the per-layer deliveries. The behavior of each
// notch is pinned by its own suites; what this pins is the property the convergence bought, which
// no behavioral test can see: that a second writer, or a notch growing its own copier again,
// fails here. The jail's half of the same pin, that jailcontent stages pack skills through
// ComposeInto and holds no copier of its own, is jailcontent's onecomposer_test.go.

// callersOf maps each callee name to the sorted set of top-level functions (in this package's
// non-test files) whose bodies call it.
func callersOf(t *testing.T, callees ...string) map[string][]string {
	t.Helper()
	want := map[string]bool{}
	for _, c := range callees {
		want[c] = true
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
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
	return out
}

func TestBothNotchesComposeSkillsThroughTheOneLayerWriter(t *testing.T) {
	got := callersOf(t, "writeLayer", "Deliver", "DeliverPlugin", "Collisions")
	for callee, want := range map[string][]string{
		// The one layer writer: the host render and the jail's composition, and nothing else.
		"writeLayer": {"ComposeInto", "renderDestination"},
		// The per-layer deliveries are reached through it alone.
		"Deliver":       {"writeLayer"},
		"DeliverPlugin": {"writeLayer"},
		// Both notches refuse a collision before they write anything.
		"Collisions": {"ComposeInto", "RenderHostSkills"},
	} {
		if strings.Join(got[callee], ",") != strings.Join(want, ",") {
			t.Errorf("%s is called from %v, want exactly %v — a skills writer that does not go "+
				"through the one layer writer is the second composer item 25 deleted",
				callee, got[callee], want)
		}
	}
}
