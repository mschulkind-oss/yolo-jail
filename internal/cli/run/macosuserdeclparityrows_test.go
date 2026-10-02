package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// THE MAC CHECK'S BRIEFING ROWS, HELD AGAINST THE LINUX COMPOSITION.
//
// integration/macosuserdeclparity_test.go's TestMacosUserBriefingAndLaunchLinesDescribeThisBackend
// asserts declaration parity's macos-user rows (docs/design/declaration-parity.md §11 step 7)
// against the briefing a real macos-user launch delivers, and only the scheduled macos-user
// job on a Mac runs it. Its rows were written on 2026-10-01 without a Mac, and one of them
// contradicted what this package composes: DP-B6 refused ANY mention of `yolo-cglimit`, while
// backendLimits names that client on purpose, to say it is NOT available here
// (TestMacosUserBriefingCarriesTheBackendLimits). The first nightly to run the row failed on
// the sentence telling the agent not to use it.
//
// The composition half of every such row can be decided here. The bytes the Mac job reads are
// written by refreshJailBriefings on the macos-user arm, which macosUserBriefingWith drives. So
// this test reads the Mac test's two row tables, and the workspace config it launches with, out
// of its SOURCE, and holds each row against the Linux composition of that same config. A row
// that fails here fails on the Mac too. A row that passes here can still fail there, on what
// only a real launch decides (the delivered destination, the real workspace path), and that
// half stays the Mac job's.
func TestMacosUserDeclParityBriefingRowsHoldForTheComposedBriefing(t *testing.T) {
	src := readMacosDeclParitySource(t)

	decoded, err := jsonx.Decode([]byte(src.workspaceConfig))
	if err != nil {
		t.Fatalf("the Mac test's workspace config is not JSON: %v\n%s", err, src.workspaceConfig)
	}
	declared, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("the Mac test's workspace config is not an object:\n%s", src.workspaceConfig)
	}
	cfg := appliedTestConfig()
	for _, k := range declared.Keys() {
		v, _ := declared.Get(k)
		cfg.Set(k, v)
	}

	var ws string
	got := macosUserBriefingWith(t, cfg, func(o *Options) { ws = o.Workspace })
	t.Logf("holding %d wanted and %d unwanted rows against the composed macos-user briefing",
		len(src.want), len(src.unwanted))

	for _, r := range src.want {
		if want := strings.ReplaceAll(r.text, declParityWS, ws); !strings.Contains(got, want) {
			t.Errorf("%s: the Mac job requires %q in the delivered briefing, and the macos-user "+
				"briefing this package composes for the same config lacks it, so that job fails. "+
				"Fix whichever side is wrong; the row's ruling is in "+
				"docs/design/declaration-parity.md.\n%s", r.row, want, got)
		}
	}
	for _, r := range src.unwanted {
		if unwanted := strings.ReplaceAll(r.text, declParityWS, ws); strings.Contains(got, unwanted) {
			t.Errorf("%s: the Mac job refuses %q in the delivered briefing, and the macos-user "+
				"briefing this package composes for the same config contains it, so that job "+
				"fails. Fix whichever side is wrong; the row's ruling is in "+
				"docs/design/declaration-parity.md.\n%s", r.row, unwanted, got)
		}
	}
}

// DP-B6, by its ruling's own words: "`yolo-cglimit` is not offered to an agent with no delegate
// to talk to". The ruling forbids the OFFER, and the one mention the macos-user briefing keeps
// is its opposite — backendLimits' sentence saying the client is not available here. So every
// line naming the binary must be that sentence. Any other line is a second source telling the
// agent about a client it cannot run, whatever that line's wording.
func TestMacosUserBriefingNamesYoloCglimitOnlyToSayItIsAbsent(t *testing.T) {
	res := jsonx.NewOrderedMap()
	res.Set("memory", "2g")
	got := macosUserBriefing(t, appliedTestConfig("resources", res))

	mentions := 0
	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "yolo-cglimit") {
			continue
		}
		mentions++
		if !strings.Contains(line, "are not available here") {
			t.Errorf("DP-B6: a macos-user briefing line names `yolo-cglimit` without saying it "+
				"is unavailable. That client talks to the cgroup delegate, a Linux-only loophole "+
				"this backend never stages:\n%s", line)
		}
	}
	if mentions == 0 {
		t.Errorf("the briefing no longer names `yolo-cglimit` at all, so this test checks "+
			"nothing. backendLimits' Linux-only-clients sentence is the one mention DP-B6 "+
			"allows; if it was reworded or dropped on purpose, update this test with it:\n%s", got)
	}
}

// declParityWS stands for the Mac test's `ws` (its workspace path) inside a row, until the
// composition's own workspace replaces it.
const declParityWS = "\x00ws\x00"

type declParityRow struct{ row, text string }

type macosDeclParitySource struct {
	workspaceConfig string
	want, unwanted  []declParityRow
}

// readMacosDeclParitySource parses the Mac test's source. It finds the rows by the TABLES'
// shape, `[]struct{ row, want, why string }` and `[]struct{ row, unwanted, why string }`, and
// the config by its `macosUserWorkspace(t, <json>)` call. It FAILS rather than skips when
// any of them is missing, because a skip would silently stop checking every row.
func readMacosDeclParitySource(t *testing.T) macosDeclParitySource {
	t.Helper()
	_, thisFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller gave no source path — cannot locate the repo root")
	}
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "..",
		"integration", "macosuserdeclparity_test.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing the macos-user declaration-parity test: %v", err)
	}
	const testName = "TestMacosUserBriefingAndLaunchLinesDescribeThisBackend"
	var body *ast.BlockStmt
	for _, d := range file.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == testName {
			body = fn.Body
		}
	}
	if body == nil {
		t.Fatalf("%s has no %s; this test reads its rows and must follow a rename", path, testName)
	}

	var src macosDeclParitySource
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "macosUserWorkspace" && len(n.Args) == 2 {
				src.workspaceConfig = declParityString(t, fset, n.Args[1])
			}
		case *ast.CompositeLit:
			rows, column := declParityTable(t, fset, n)
			switch column {
			case "want":
				src.want = append(src.want, rows...)
			case "unwanted":
				src.unwanted = append(src.unwanted, rows...)
			}
		}
		return true
	})
	if src.workspaceConfig == "" || len(src.want) == 0 || len(src.unwanted) == 0 {
		t.Fatalf("%s: found config %q, %d wanted rows and %d unwanted rows. This test reads the "+
			"tables by their struct shape and the config by its macosUserWorkspace call; if "+
			"either changed shape, change this reader with it.",
			testName, src.workspaceConfig, len(src.want), len(src.unwanted))
	}
	return src
}

// declParityTable returns the rows of one `[]struct{ row, <column>, why string }{...}` literal
// and its middle column's name, or nothing for any other composite literal.
func declParityTable(t *testing.T, fset *token.FileSet, lit *ast.CompositeLit) ([]declParityRow, string) {
	t.Helper()
	arr, ok := lit.Type.(*ast.ArrayType)
	if !ok {
		return nil, ""
	}
	st, ok := arr.Elt.(*ast.StructType)
	if !ok {
		return nil, ""
	}
	var names []string
	for _, f := range st.Fields.List {
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	if len(names) != 3 || names[0] != "row" || names[2] != "why" ||
		(names[1] != "want" && names[1] != "unwanted") {
		return nil, ""
	}
	var rows []declParityRow
	for _, e := range lit.Elts {
		el, ok := e.(*ast.CompositeLit)
		if !ok || len(el.Elts) != 3 {
			t.Fatalf("%s: a %s row this reader cannot read; keep each row a positional "+
				"{row, %s, why} literal", fset.Position(e.Pos()), names[1], names[1])
		}
		rows = append(rows, declParityRow{
			row:  declParityString(t, fset, el.Elts[0]),
			text: declParityString(t, fset, el.Elts[1]),
		})
	}
	return rows, names[1]
}

// declParityString evaluates the string expressions the Mac test's rows use: literals, `+`,
// and `ws`.
func declParityString(t *testing.T, fset *token.FileSet, e ast.Expr) string {
	t.Helper()
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			s, err := strconv.Unquote(e.Value)
			if err != nil {
				t.Fatalf("%s: %v", fset.Position(e.Pos()), err)
			}
			return s
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return declParityString(t, fset, e.X) + declParityString(t, fset, e.Y)
		}
	case *ast.ParenExpr:
		return declParityString(t, fset, e.X)
	case *ast.Ident:
		if e.Name == "ws" {
			return declParityWS
		}
	}
	t.Fatalf("%s: a row expression this reader cannot evaluate (%T). Keep the Mac test's "+
		"rows to string literals, `+` and `ws`, so their composition half is checked on Linux.",
		fset.Position(e.Pos()), e)
	return ""
}
