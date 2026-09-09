package run

// currentimagecallsite_test.go is the call-site pin for the launch-path
// current-image pointer (OQ-LS3,
// docs/design/the-load-sentinel-is-not-a-liveness-oracle.md §6.2).
//
// currentimage_test.go covers the WRITE (what it records, the degraded launch,
// the lock, a failed write) and internal/prune/currentimages_test.go covers the
// READ — but nothing there notices `o.recordCurrentImage(...)` being deleted
// from runContainer, or being MOVED. Both are silent, and each is silent in a
// different direction:
//
//   - DELETED, the pointer set stays empty, every sweep declines for ever
//     (fail-safe), and disk stops being reclaimed. That looks like nothing:
//     it is the exact shape of the defect this whole effort started from —
//     a hint that sat true for 24+ days while 404+ GiB accrued.
//   - MOVED above the image load, there is no store path to record yet, so the
//     pointer names nothing (or the previous launch's image), and the reap can
//     select the image this launch is about to run.
//   - MOVED below the housekeeping slot, the reap runs BEFORE this workspace's
//     evidence exists — the ordering bug that made the reap destructive in the
//     first place, arriving at a new file.
//
// That is the shape AGENTS.md names — a test that pins the callee while the
// call site is unpinned is not a test — and this is the repo's existing answer
// for a call site a unit test cannot reach (autoreapcallsite_test.go, itself
// following liveoverlayguard_test.go and configapproval_test.go).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestRunContainerRecordsTheCurrentImageBetweenTheLoadAndTheReap(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}

	var loadPos, recordPos, reapPos token.Pos
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
			case "recordCurrentImage":
				if recordPos == token.NoPos {
					recordPos = call.Pos()
				}
			case "runHousekeeping":
				if reapPos == token.NoPos {
					reapPos = call.Pos()
				}
			}
			return true
		})
	}

	if recordPos == token.NoPos {
		t.Fatal("runContainer no longer records this workspace's current image — the retention " +
			"set that replaced `--keep-images` (OQ-LS3) is written NOWHERE ELSE, so it stays " +
			"empty, every sweep declines on missing evidence, and superseded images accumulate " +
			"silently. That failure reports itself only in <workspace>/.yolo/housekeeping.log.")
	}
	if loadPos == token.NoPos || reapPos == token.NoPos {
		t.Fatal("runContainer no longer calls autoLoadImage and/or runHousekeeping — this pin " +
			"cannot check the ordering the pointer's correctness depends on")
	}
	if recordPos < loadPos {
		t.Fatal("the current-image pointer is recorded BEFORE autoLoadImage — there is no store " +
			"path to record yet, so the pointer names the previous launch's image (or nothing) " +
			"and the reap can select the image this launch is about to run")
	}
	if reapPos < recordPos {
		t.Fatal("the housekeeping slot is wired BEFORE the current-image pointer is recorded — " +
			"the reap then reads a retention set that does not yet include this workspace, " +
			"which is the ordering that made an auto-reap destructive in the first place")
	}
}
