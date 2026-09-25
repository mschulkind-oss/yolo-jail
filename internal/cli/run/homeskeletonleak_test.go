package run

// homeskeletonleak_test.go pins the two ways a launch could leave a home skeleton (homeskeleton.go)
// that no container ever held — a fatal failure while building it, and a return between the
// build and the container start — and the one output the best-effort half of the builder has:
// its warnings.

import (
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestAFatalSkeletonFailureLeavesNoDirectory: a fatal entry that fails AFTER the directory
// exists refuses the launch, and takes the partial directory with it. The fixture pack's
// state dir sits on core's `.gitconfig` redirect NAME, so the fatal redirect loop hits a
// directory where its link goes.
func TestAFatalSkeletonFailureLeavesNoDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	manifest := `{"name":"collider","contributes":[{"kind":"state","at":".gitconfig","scope":"workspace"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "collider")
	if len(problems) != 0 {
		t.Fatalf("loading the fixture pack: %v", problems)
	}
	const cname = "yolo-fatal-skeleton"
	if _, err := buildHomeSkeleton(paths.HomeSkeletonRoot(cname), []*packload.Pack{p}, nil, nil); err == nil {
		t.Fatal("a pack dir on a redirect name must fail the skeleton: the fixture no longer reaches " +
			"the fatal path, so this test proves nothing")
	}
	entries, err := os.ReadDir(paths.HomeSkeletonRoot(cname))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("a refused launch left %d partial skeleton(s) under %s; no container will ever "+
			"bind them", len(entries), paths.HomeSkeletonRoot(cname))
	}
}

// TestDiscardUnheldSkeletonRemovesOnlyASkeleton: it removes a skeleton this launch built,
// and nothing that is not a direct child of that jail's skeleton root.
func TestDiscardUnheldSkeletonRemovesOnlyASkeleton(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const cname = "yolo-discard"
	sk := buildSkeletonForTest(t, cname, nil, nil, nil)
	other := buildSkeletonForTest(t, "yolo-some-other-jail", nil, nil, nil)
	outside := t.TempDir()

	for _, dir := range []string{"", other, outside, paths.HomeSkeletonRoot(cname),
		filepath.Join(sk, ".config")} {
		discardUnheldSkeleton(cname, dir)
	}
	for _, dir := range []string{sk, other, outside, filepath.Join(sk, ".config")} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("discardUnheldSkeleton reached %s, which is not a skeleton of %s: %v", dir, cname, err)
		}
	}

	discardUnheldSkeleton(cname, sk)
	if _, err := os.Lstat(sk); !os.IsNotExist(err) {
		t.Errorf("the skeleton %s survived its discard (err %v)", sk, err)
	}
}

// TestNoReturnAfterTheSkeletonLeaksIt: in runContainer, every return between the skeleton's
// build and the container's normal end discards it first. Two pre-flights refuse the launch
// after the build (they check the assembled argv, which binds the skeleton), and a runtime
// that never starts ends it there too; each used to leave a directory no container ever held.
//
// Deletion-sensitive by construction: remove any discardUnheldSkeleton call and the return
// after it is reported; add a new refusal below the build without one and it is reported. The
// podman arm's own failure return is exempt, because buildHomeSkeleton removes its partial
// directory itself (TestAFatalSkeletonFailureLeavesNoDirectory), and so is the function's
// final `return rc`: that container ran, and its skeleton is the reaper's (the OQ-BH10 ruling).
func TestNoReturnAfterTheSkeletonLeaksIt(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var arm *ast.IfStmt
	for _, st := range fd.Body.List {
		if ifs, ok := st.(*ast.IfStmt); ok && skelIsPodmanArm(ifs.Cond) && callsIn(ifs.Body)["buildHomeSkeleton"] {
			arm = ifs
		}
	}
	if arm == nil {
		t.Fatal("no podman arm building the skeleton in runContainer; re-anchor this pin, do not delete it")
	}
	final := fd.Body.List[len(fd.Body.List)-1]

	checked := 0
	var walk func(block *ast.BlockStmt)
	walk = func(block *ast.BlockStmt) {
		for i, st := range block.List {
			if ret, ok := st.(*ast.ReturnStmt); ok && ret.Pos() > arm.End() && st != final {
				checked++
				discarded := false
				for _, before := range block.List[:i] {
					if callsIn(before)["discardUnheldSkeleton"] {
						discarded = true
					}
				}
				if !discarded {
					t.Errorf("run.go:%d: runContainer returns after building the skeleton without "+
						"discardUnheldSkeleton(cname, in.homeSkeleton) earlier in the same block, "+
						"so the refused launch leaves a skeleton no container ever held",
						runGoLine(ret.Pos()))
				}
			}
		}
		// Descend into nested blocks, but never into a closure: onStarted and onTerminate
		// run while the container holds the skeleton.
		ast.Inspect(block, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncLit:
				return false
			case *ast.BlockStmt:
				if x != block {
					walk(x)
					return false
				}
			case *ast.CaseClause:
				walk(&ast.BlockStmt{List: x.Body})
				return false
			case *ast.CommClause:
				walk(&ast.BlockStmt{List: x.Body})
				return false
			}
			return true
		})
	}
	walk(fd.Body)
	// The two pre-flight refusals and the runtime-never-started branch.
	if checked < 3 {
		t.Errorf("found %d returns after the skeleton's build, want at least 3; the anchors of this "+
			"pin moved, re-anchor it rather than delete it", checked)
	}
}

// runGoLine is pos's line in run.go, for a failure message a reader can follow. funcDecl parses
// run.go alone into a fresh FileSet, so its positions share this one's base.
func runGoLine(pos token.Pos) int {
	src, err := os.ReadFile("run.go")
	if err != nil {
		return 0
	}
	f := token.NewFileSet().AddFile("run.go", -1, len(src))
	f.SetLinesForContent(src)
	return f.Line(pos)
}

// TestTheSkeletonsWarningsArePrinted: the builder's best-effort entries only degrade, so the
// launch's line is the only place a missing mountpoint is ever said. runContainer's podman arm
// must range over the result's warnings and print each one; deleting that loop left every
// other test green, since the builder's own tests read sk.warnings directly.
func TestTheSkeletonsWarningsArePrinted(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var arm *ast.BlockStmt
	result := ""
	for _, st := range fd.Body.List {
		ifs, ok := st.(*ast.IfStmt)
		if !ok || !skelIsPodmanArm(ifs.Cond) {
			continue
		}
		for _, inner := range ifs.Body.List {
			as, ok := inner.(*ast.AssignStmt)
			if !ok || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
				continue
			}
			if call, ok := as.Rhs[0].(*ast.CallExpr); ok && skelCallee(call) == "buildHomeSkeleton" {
				arm, result = ifs.Body, skelIdent(as.Lhs[0])
			}
		}
	}
	if arm == nil || result == "" {
		t.Fatal("runContainer's podman arm no longer binds buildHomeSkeleton's result; re-anchor this pin")
	}

	printed := false
	for _, st := range arm.List {
		rs, ok := st.(*ast.RangeStmt)
		if !ok {
			continue
		}
		sel, ok := rs.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "warnings" || skelIdent(sel.X) != result {
			continue
		}
		value := skelIdent(rs.Value)
		ast.Inspect(rs.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || skelCallee(call) != "printf" || len(call.Args) < 2 {
				return true
			}
			for _, a := range call.Args[1:] {
				if value != "" && skelIdent(a) == value {
					printed = true
				}
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && !strings.Contains(lit.Value, "Warning") {
				printed = false
			}
			return true
		})
	}
	if !printed {
		t.Errorf("runContainer's podman arm does not print each of %s.warnings as a Warning line: a "+
			"best-effort mountpoint the builder could not make then fails silently, or as podman's "+
			"opaque crun error naming no path", result)
	}
}
