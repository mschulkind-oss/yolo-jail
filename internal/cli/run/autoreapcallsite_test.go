package run

// autoreapcallsite_test.go is the call-site pin for the launch-path image reap.
//
// autoreapimages_test.go covers the decision (debounce, opt-out, what the reap
// asks prune for) and internal/prune/autoreap_test.go covers the reap itself —
// but nothing there notices `o.autoReapOldImages(rt)` being deleted from
// runContainer, or being MOVED. Both are silent: deletion just stops reclaiming
// disk, which looks like nothing, and a move above the load is worse than
// nothing. That is the shape AGENTS.md names — a test that pins the callee
// while the call site is unpinned is not a test — and this is the repo's
// existing answer for a call site a unit test cannot reach (the plain-function
// form in liveoverlayguard_test.go, itself following configapproval_test.go).
//
// The ORDER is the load-bearing half. The reap must run AFTER autoLoadImage
// succeeds, because that is what puts this launch's own image into the load
// sentinel's recently-used list and therefore under ProtectedImageTags' veto.
// Hoisted above it, the reap would run its liveness read before this launch's
// image was protected — and the reap deletes multi-gigabyte images.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestRunContainerReapsImagesAfterTheLoad(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}

	var loadPos, reapPos token.Pos
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "runContainer" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "autoLoadImage":
				if loadPos == token.NoPos {
					loadPos = call.Pos()
				}
			case "autoReapOldImages":
				if reapPos == token.NoPos {
					reapPos = call.Pos()
				}
			}
			return true
		})
	}

	if reapPos == token.NoPos {
		t.Fatal("runContainer no longer calls autoReapOldImages — superseded images " +
			"stop being reclaimed and nothing else reports it (the store this " +
			"landed for held 24 images / 38.68 GB, 23 minted in three days). If the " +
			"call moved, move this pin with it rather than deleting it.")
	}
	if loadPos == token.NoPos {
		t.Fatal("runContainer no longer calls autoLoadImage — this pin cannot check " +
			"the ordering the reap's safety depends on")
	}
	if reapPos < loadPos {
		t.Fatal("autoReapOldImages runs BEFORE autoLoadImage — this launch's own image " +
			"is not in the load sentinel yet, so ProtectedImageTags cannot veto its " +
			"removal and the reap can delete the image the launch is about to use")
	}
}
