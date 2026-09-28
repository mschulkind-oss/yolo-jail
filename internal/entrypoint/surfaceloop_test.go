package entrypoint

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

// surfaceloop_test.go pins docs/plans/notch-convergence.md item 24 (row D3) STRUCTURALLY: every
// entry that writes a pack's declared surfaces — the jail boot, `yolo check`'s single-pack probe
// and the host apply — walks them through the one loop's head (planPackSurfaces) and writes them
// through the one dispatch (writeSurfaceThrough). The behavior of each entry is pinned by the
// suites that already existed (TestRenderFingerprintStable and the host render tests); what this
// pins is the property the refactor bought, which no behavioral test can see: that a fifth loop,
// or a notch growing its own switch on the declared mode again, fails here rather than drifting.

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

func TestEveryPackSurfaceWriterRunsTheOneLoop(t *testing.T) {
	got := callersOf(t, "planPackSurfaces", "planSurface", "renderPackSet", "renderHostPlans",
		"renderPlannedSurface", "writeSurfaceThrough", "SurfacesForReport",
		"renderSurfaceRMWSurface", "renderSurfaceStatefulDetail")
	for callee, want := range map[string][]string{
		// The head: the jail's pack walk and the host apply, and nothing else folds a pack's
		// posture into its surfaces for a render.
		"planPackSurfaces":  {"RenderHostPack", "renderPackSet"},
		"SurfacesForReport": {"planPackSurfaces"},
		// One surface's plan — its mechanism from the target's census — is decided in the
		// head alone. A second planner beside it (renderDeclaredSurface, deleted) decides the
		// same thing a second way. The user's host_files entries at the host (OQ-NC8) are
		// planned by the same planner, not a copy of it.
		"planSurface": {"planHostFileSurfaces", "planPackSurfaces"},
		// The host half of the loop: a pack's surfaces and the user's host_files entries.
		"renderHostPlans": {"RenderHostPack", "RenderHostUserFiles"},
		// The jail walk: the boot and the check probe, which differ only in their failure
		// disposition.
		"renderPackSet": {"ConfigurePackSurfaces", "configureOnePack"},
		// The jail tail is reached from the jail walk alone.
		"renderPlannedSurface": {"renderPackSet"},
		// The dispatch: the jail tail and the host apply's half of the loop.
		"writeSurfaceThrough": {"renderHostPlans", "renderPlannedSurface"},
		// The two pack writers are reached from the dispatch and the wrappers over it alone.
		// renderSurfaceStatefulSurface is the wrapper the core surfaces and `host_files` use.
		"renderSurfaceRMWSurface":     {"writeSurfaceThrough"},
		"renderSurfaceStatefulDetail": {"renderSurfaceStatefulSurface", "writeSurfaceThrough"},
	} {
		if strings.Join(got[callee], ",") != strings.Join(want, ",") {
			t.Errorf("%s is called from %v, want exactly %v — a pack-surface writer that does not "+
				"go through the one render loop (surfaceloop.go) is the second path item 24 deleted",
				callee, got[callee], want)
		}
	}
}
