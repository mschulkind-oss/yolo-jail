package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// makeTreeRemovable restores the write bit on every DIRECTORY under root so that
// t.TempDir()'s own RemoveAll cleanup can unlink what a vendor installer left
// read-only.
//
// RemoveAll needs write AND execute on a DIRECTORY to unlink the children inside
// it, and Go's does not chmod on the way down. So a single 0555 directory
// anywhere in a fixture tree fails the whole cleanup — and t.TempDir() reports
// that as a TEST FAILURE, raised after the test's own assertions have already
// passed, which is about as misleading as a red badge gets.
//
// Measured: codex 0.153.4's standalone release ships
// `packages/standalone/releases/<version>/codex-path/` read-only, and installing
// the codex pack puts it under the workspace's home overlay
// (<workspace>/.yolo/home, paths.WorkspaceHomeState) — which for these fixtures
// is a t.TempDir(). It took the Pack Installs job down twice (runs 33938946012
// and 33972100139) with `TempDir RemoveAll cleanup: unlinkat
// .../codex-path/rg: permission denied`, while TestAgentToolsAvailable's own
// assertion had passed.
//
// Only writeProject's WORKSPACE needs this. An isolated HOME does not: packHome
// symlinks the HOME-rooted stores back to the machine's copies, so installed
// content never lands inside that temp dir in the first place
// (packHomeSharedStores, and TestPackHomeSharesHostStores which pins it).
//
// Best-effort by design. A path it cannot chmod — one owned by another uid,
// which a rootless-podman userns can produce — is left for RemoveAll to report,
// because that is a different failure with a different fix and swallowing it
// here would only move the confusion somewhere harder to find.
func makeTreeRemovable(root string) {
	// WalkDir is pre-order, so a directory is chmod'd BEFORE its contents are
	// read — which is what lets the walk descend into one that had no execute
	// bit when it started.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if perm := fi.Mode().Perm(); perm&0o300 != 0o300 {
			_ = os.Chmod(path, perm|0o300)
		}
		return nil
	})
}

// TestMakeTreeRemovableClearsAReadOnlyDir runs under -short (no container): it is
// a harness invariant, like TestChildRepoRootEnv and TestPackHomeSharesHostStores,
// so `just test-fast` and the check-go CI job enforce it.
func TestMakeTreeRemovableClearsAReadOnlyDir(t *testing.T) {
	root := t.TempDir()

	// The exact shape codex ships: a file inside a directory with no write bit.
	locked := filepath.Join(root, "codex-path")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "rg"), []byte("not really ripgrep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}

	// Prove the failure is REAL before proving the fix. Without this the test
	// would pass just as happily on a system where RemoveAll never minded, and
	// would then be pinning nothing at all.
	if err := os.RemoveAll(root); err == nil {
		_ = os.Chmod(locked, 0o755)
		t.Skip("this filesystem lets RemoveAll unlink through a read-only directory " +
			"(running as root?), so there is no failure here to fix")
	}

	makeTreeRemovable(root)

	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("after makeTreeRemovable, RemoveAll still could not clear the tree: %v", err)
	}
}

// TestWriteProjectMakesItsTreeRemovable is the call-site pin. The test above
// proves makeTreeRemovable works; nothing in it would notice the call vanishing
// from writeProject, and the regression that follows is a cleanup failure in one
// push-triggered workflow — visible eventually, but only the next time a vendor
// happens to ship a read-only directory. Reading the source is this repo's
// existing answer for a call site a unit test cannot reach (the methodDecl
// pattern in configapproval_test.go; liveoverlayguard_test.go's plain-function
// form, which this mirrors).
func TestWriteProjectMakesItsTreeRemovable(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "harness_test.go", nil, 0)
	if err != nil {
		t.Fatalf("parse harness_test.go: %v", err)
	}
	var found bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "writeProject" {
			continue
		}
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "makeTreeRemovable" {
				found = true
			}
			return true
		})
	}
	if !found {
		t.Fatal("writeProject no longer calls makeTreeRemovable — a pack whose vendor " +
			"ships a read-only directory will fail t.TempDir()'s cleanup again, AFTER " +
			"the test's assertions have passed (see makeTreeRemovable for the two runs " +
			"it took down). If the call moved, move this pin with it rather than " +
			"deleting it.")
	}
}
