package setup

// removedspellings_test.go is the guard that no message yolo prints names a removed selection as
// a next step: the `--at` flag, the `runtime`, `confinement` and `macos_user_mode` keys, and
// YOLO_RUNTIME, each of which a launch now refuses (named-setups-plan.md slices 4a and 5). A step
// that names one walks the reader into a second refusal. Named setups replaced all five, and a
// step naming another setup spells it with Choose or LaunchLine.
//
// The scan reads every non-test Go file under cmd/ and internal/ and checks the string each
// literal, or each chain of literals joined with +, spells; comments are not read. The only
// strings allowed to spell a removed selection are the ones that say it is removed: the
// refusal functions themselves (removalFuncs).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// removedStepRE matches a removed selection spelled as something to type or set: `--at` with a
// notch (either spelling of the flag), the removed keys in each config spelling or as a
// `yolo config set`, and YOLO_RUNTIME set to anything, a format verb included.
var removedStepRE = regexp.MustCompile(`--at[ =](jail|guest|host)\b` +
	`|\bruntime: "` + `|"runtime": "` + `|\bruntime = "` +
	"|`confinement` set to" + `|\bconfinement: "` + `|"confinement": "` + `|\bconfinement = "` +
	`|\bmacos_user_mode: "` + `|"macos_user_mode": "` + `|\bmacos_user_mode = "` +
	`|config set (runtime|confinement|macos_user_mode)\b` +
	`|YOLO_RUNTIME=[^\s,.;)]`)

// removedFlagTailRE matches a chain that ends in the flag itself, as `"--at " + notch` spells it:
// the notch is a variable the chain drops, so the raw text ends at the flag.
var removedFlagTailRE = regexp.MustCompile(`--at[ =]$`)

// removalFuncs are the functions whose messages explain a removal, and so must name what was
// removed: the `--at` refusal, and the selection's refusal and warning of YOLO_RUNTIME. Only
// these functions' strings are skipped; the rest of each file is scanned like any other.
var removalFuncs = map[string]map[string]bool{
	"internal/cli/atremoved.go":      {"atRemovedFor": true},
	"internal/config/setupselect.go": {"RemovedRuntimeEnvRefusal": true, "removedRuntimeEnvWarning": true},
}

func TestNoMessageNamesARemovedSelectionAsItsNextStep(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	scanned := 0
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			scanned++
			for _, s := range spelledStrings(t, path, removalFuncs[rel]) {
				m := removedStepRE.FindString(s.text)
				if m == "" {
					m = removedFlagTailRE.FindString(s.raw)
				}
				if m != "" {
					t.Errorf("%s:%d names the removed %q as a step; name a setup instead "+
						"(setup.Choose, setup.ContainerStep or setup.LaunchLine):\n    %s", rel, s.line, m, s.text)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 100 {
		t.Fatalf("scanned %d files; the walk did not find the source tree from %s", scanned, root)
	}
}

type spelled struct {
	line int
	// text is the string with whitespace folded; raw is it as spelled.
	text, raw string
}

// spelledStrings is every string path's code spells: each chain of string literals joined with +
// as one string (whatever else the chain joins is dropped), and each other literal alone, with
// whitespace runs folded to one space so a wrapped message reads as printed. The bodies of the
// functions skip names are not read.
func spelledStrings(t *testing.T, path string, skip map[string]bool) []spelled {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var out []spelled
	inChain := map[*ast.BasicLit]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncDecl:
			if n.Recv == nil && skip[n.Name.Name] {
				return false
			}
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			var b strings.Builder
			var lits []*ast.BasicLit
			collectChain(n, &lits)
			if len(lits) == 0 {
				return true
			}
			for _, l := range lits {
				if inChain[l] {
					return true // an inner + of a chain already read
				}
				inChain[l] = true
				b.WriteString(unquote(l))
			}
			out = append(out, spelled{fset.Position(n.Pos()).Line, fold(b.String()), b.String()})
		case *ast.BasicLit:
			if n.Kind == token.STRING && !inChain[n] {
				out = append(out, spelled{fset.Position(n.Pos()).Line, fold(unquote(n)), unquote(n)})
			}
		}
		return true
	})
	return out
}

// collectChain appends, in order, the string literals of the + chain rooted at e.
func collectChain(e ast.Expr, lits *[]*ast.BasicLit) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			collectChain(e.X, lits)
			collectChain(e.Y, lits)
		}
	case *ast.ParenExpr:
		collectChain(e.X, lits)
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			*lits = append(*lits, e)
		}
	}
}

func unquote(l *ast.BasicLit) string {
	s, err := strconv.Unquote(l.Value)
	if err != nil {
		return l.Value
	}
	return s
}

func fold(s string) string { return strings.Join(strings.Fields(s), " ") }
