package render_test

// onerenderer_test.go is the GATE on the collapse, not a description of it
// (host-render-target.md §8 step 3). Every other test in this package pins what the
// renderer DOES; this one pins that there is only the one, which is the property the
// step was for and the only one a second implementation can silently take away.
//
// It is written against the call sites rather than against the renderer because that is
// where the failure lives. The tree carried two render paths for a year with both halves'
// unit tests green the whole time: each tested its own callee, neither could fail because
// the other existed, and the drift between them is what every §6.1 data-loss probe turned
// out to be. A test of render.Target.Compose cannot see that class at all — delete the
// call in internal/cli and it still passes — so the assertion has to be about who calls
// what.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// engineEntries are the composition engine's two entries — the functions that ARE a
// render path. A package that calls one is a renderer; the point of the collapse is that
// exactly one package is.
var engineEntries = map[string]bool{"Compose": true, "ComposeStateful": true}

// renderersAllowed are the import paths whose non-test files may call them: the engine
// itself (agentcfg, where the entries live and its own tests exercise them) and this
// package, which is the renderer. Anything else is a second render path.
var renderersAllowed = []string{
	"internal/agentcfg",
	"internal/render",
}

// TestTheEngineHasOneCaller walks every non-test Go file yolo ships and fails on an
// agentcfg.Compose / agentcfg.ComposeStateful call outside the two packages above.
//
// AST, not grep: half a dozen comments in this tree name these functions on purpose
// (adoptionarchive.go explains which branch adopts, prism.go names the second of the two
// derivations), and a test that counted those would either be silenced with a carve-out
// list or would teach people to stop writing the comments. A CallExpr is unambiguous.
func TestTheEngineHasOneCaller(t *testing.T) {
	root := repoRootForRenderTest(t)
	var offenders []string
	for _, dir := range []string{"internal", "cmd"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				rel = path
			}
			if inAllowedRenderer(filepath.ToSlash(rel)) {
				return
			}
			alias := agentcfgAlias(file)
			if alias == "" {
				return // the file does not import the engine at all
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, isCall := n.(*ast.CallExpr)
				if !isCall {
					return true
				}
				sel, isSel := call.Fun.(*ast.SelectorExpr)
				if !isSel || !engineEntries[sel.Sel.Name] {
					return true
				}
				pkg, isIdent := sel.X.(*ast.Ident)
				if !isIdent || pkg.Name != alias {
					return true
				}
				offenders = append(offenders, rel+": "+alias+"."+sel.Sel.Name)
				return true
			})
		})
	}
	if len(offenders) > 0 {
		t.Fatalf("a second render path: these files call the composition engine directly, "+
			"which is what internal/render exists to be the only one to do "+
			"(docs/design/host-render-target.md §8 step 3). Route them through a "+
			"render.Target instead:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestTheEngineCallerGateCoversTheRenderer is the gate's own gate: it proves the walk
// above actually reaches the renderer's call sites, so a green result means "no second
// caller" rather than "nothing was scanned". Without it a bad path or a silent parse
// failure would report the invariant held everywhere, forever.
func TestTheEngineCallerGateCoversTheRenderer(t *testing.T) {
	root := repoRootForRenderTest(t)
	found := 0
	walkGoFiles(t, filepath.Join(root, "internal", "render"), func(_ string, file *ast.File) {
		alias := agentcfgAlias(file)
		if alias == "" {
			return
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, isCall := n.(*ast.CallExpr)
			if !isCall {
				return true
			}
			if sel, isSel := call.Fun.(*ast.SelectorExpr); isSel && engineEntries[sel.Sel.Name] {
				if pkg, isIdent := sel.X.(*ast.Ident); isIdent && pkg.Name == alias {
					found++
				}
			}
			return true
		})
	})
	if found != len(engineEntries) {
		t.Fatalf("the renderer makes %d engine calls, want one per entry (%d) — either the "+
			"collapse regressed or this scan is not reaching the code it claims to",
			found, len(engineEntries))
	}
}

// inAllowedRenderer reports whether a repo-relative path sits in a package permitted to
// call the engine. Prefix match on the directory, so a subpackage of agentcfg (codec,
// manifest, luahook) is covered with it.
func inAllowedRenderer(rel string) bool {
	for _, dir := range renderersAllowed {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// agentcfgAlias returns the name this file refers to internal/agentcfg by, or "" when it
// does not import it. It honors a rename import because a second render path spelled
// `cfg.Compose(...)` is the same second render path.
func agentcfgAlias(file *ast.File) string {
	const engine = `"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"`
	for _, imp := range file.Imports {
		if imp.Path.Value != engine {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name
		}
		return "agentcfg"
	}
	return ""
}

// walkGoFiles parses every non-test .go file under dir and hands each to fn. A file that
// does not parse is a hard failure rather than a skip: silently passing over it is how a
// scanning test comes to cover nothing.
func walkGoFiles(t *testing.T, dir string, fn func(path string, file *ast.File)) {
	t.Helper()
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		full, ferr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if ferr != nil {
			return ferr
		}
		fn(path, full)
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", dir, err)
	}
}

// repoRootForRenderTest locates the checkout from this test file's own compiled-in path,
// which is the idiom the other source-scanning tests in this tree use (it survives a `go
// test` run from any directory).
func repoRootForRenderTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}
