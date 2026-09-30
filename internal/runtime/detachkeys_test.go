package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestDetachKeysArgsIsPodmansAlone: the empty sequence on podman, which disables detaching, and
// nothing on a runtime whose detach behavior nobody has measured (JL-D27).
func TestDetachKeysArgsIsPodmansAlone(t *testing.T) {
	if got := DetachKeysArgs("podman"); !slices.Equal(got, []string{"--detach-keys="}) {
		t.Errorf("podman: %q, want [--detach-keys=]", got)
	}
	for _, rt := range []string{"container", "macos-user", ""} {
		if got := DetachKeysArgs(rt); got != nil {
			t.Errorf("%q: %q, want nothing", rt, got)
		}
	}
}

// funcFacts is what the sweep learns about one function: whether it builds a run or exec argv,
// and whether it calls DetachKeysArgs.
type funcFacts struct{ builds, calls bool }

// TestEveryRunAndExecIntoAJailTurnsOffTheDetachSequence is JL-D27's "every": each argv yolo's
// non-test source builds as `<runtime> run …` or `<runtime> exec …` — a string-slice literal
// whose second element is "run" or "exec" and whose first is not a literal — sits in a function
// that calls DetachKeysArgs, or in one handed flags a named function built with it. Read out of
// the tree, so a new run or exec site fails here until it carries the flag.
func TestEveryRunAndExecIntoAJailTurnsOffTheDetachSequence(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A function that only prefixes `<runtime> run` to run flags another function composed,
	// mapped to that function, which must call DetachKeysArgs itself.
	handedFlagsBy := map[string]string{
		"internal/cli/run/assemble_parts.go:podmanBaseMounts":         "internal/cli/run/assemble.go:assembleRunCmd",
		"internal/cli/run/assemble_parts.go:appleContainerBaseMounts": "internal/cli/run/assemble.go:assembleRunCmd",
	}
	facts := map[string]funcFacts{}
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				var ff funcFacts
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					switch x := n.(type) {
					case *ast.CompositeLit:
						if runOrExecArgv(x) {
							ff.builds = true
						}
					case *ast.SelectorExpr:
						if x.Sel.Name == "DetachKeysArgs" {
							ff.calls = true
						}
					case *ast.Ident:
						if x.Name == "DetachKeysArgs" {
							ff.calls = true
						}
					}
					return true
				})
				facts[filepath.ToSlash(rel)+":"+fn.Name.Name] = ff
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	covered := 0
	for key, ff := range facts {
		if !ff.builds {
			continue
		}
		if builder, ok := handedFlagsBy[key]; ok {
			if !facts[builder].calls {
				t.Errorf("%s runs the flags %s composes, and %s no longer calls "+
					"runtime.DetachKeysArgs (JL-D27)", key, builder, builder)
			}
			covered++
			continue
		}
		if !ff.calls {
			t.Errorf("%s builds a `<runtime> run` or `<runtime> exec` argv without "+
				"runtime.DetachKeysArgs: podman's detach sequence stays live on it (JL-D27)", key)
			continue
		}
		covered++
	}
	// The sites known today: the jail's run (through the two base-mount builders), the first
	// session's exec, the attach's exec and the check's probe. Fewer means the scan stopped
	// seeing the argvs it exists to check.
	if covered < 5 {
		t.Fatalf("found %d covered run/exec sites, want at least 5: the scan no longer sees "+
			"the argvs it exists to check", covered)
	}
}

// runOrExecArgv reports a []string literal whose first element is not a string literal (the
// runtime's name, from a variable) and whose second is "run" or "exec".
func runOrExecArgv(c *ast.CompositeLit) bool {
	at, ok := c.Type.(*ast.ArrayType)
	if !ok {
		return false
	}
	if id, ok := at.Elt.(*ast.Ident); !ok || id.Name != "string" {
		return false
	}
	if len(c.Elts) < 2 {
		return false
	}
	if _, lit := c.Elts[0].(*ast.BasicLit); lit {
		return false
	}
	second, ok := c.Elts[1].(*ast.BasicLit)
	if !ok || second.Kind != token.STRING {
		return false
	}
	v, err := strconv.Unquote(second.Value)
	return err == nil && (v == "run" || v == "exec")
}
