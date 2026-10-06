package run

// retiredshareddirs_test.go pins noteRetiredSharedDirs (retiredshareddirs.go): a fresh launch
// names a machine-scope folder a selected pack stopped sharing, when it is still in the machine
// store, with the command that deletes it, and says nothing otherwise.

import (
	"bytes"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// retiredNote runs noteRetiredSharedDirs over packs and returns what it wrote to stderr,
// failing if it wrote to stdout: a launch's stdout belongs to the command the user ran.
func retiredNote(t *testing.T, packs []*packload.Pack) string {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	o := goldenOptions("/ws", t.TempDir())
	o.Stdout, o.Stderr = &outBuf, &errBuf
	o.noteRetiredSharedDirs(packs)
	if outBuf.Len() != 0 {
		t.Errorf("the note reached stdout:\n%s", outBuf.String())
	}
	return errBuf.String()
}

// TestALeftoverPiSharedNpmFolderIsNamedWithItsDelete is XB-D14's launch line, on the SHIPPED pi
// manifest: `.pi-shared-npm` left in the machine store gets one line naming pi and the exact
// `rm -rf`, quoted for a home with a space in it. `.pi-shared-git`, which pi unshares too, is
// absent here and so is not named.
func TestALeftoverPiSharedNpmFolderIsNamedWithItsDelete(t *testing.T) {
	t.Setenv("HOME", filepath.Join(t.TempDir(), "a home"))
	left := filepath.Join(paths.GlobalHome(), ".pi-shared-npm")
	if err := os.MkdirAll(filepath.Join(left, "node_modules", "pi-lens"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := retiredNote(t, packsFixture(t, "pi"))
	if n := strings.Count(strings.TrimRight(got, "\n"), "\n") + 1; got == "" || n != 1 {
		t.Fatalf("want exactly one line for the one folder left, got %d:\n%s", n, got)
	}
	for _, want := range []string{"pi no longer uses its old shared folder", "rm -rf " + shquote.Quote(left)} {
		if !strings.Contains(got, want) {
			t.Errorf("the note is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, ".pi-shared-git") {
		t.Errorf("a retired folder that is not there was named:\n%s", got)
	}
	if _, err := os.Stat(left); err != nil {
		t.Errorf("the note deleted the folder it names; deleting it is the user's call: %v", err)
	}
}

// TestNoRetiredFolderNoteWithoutARealDirectory: absent, a symlink or a file is not the folder
// the old hook used, so nothing is said, and nothing is said for a pack that unshares nothing.
func TestNoRetiredFolderNoteWithoutARealDirectory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		plant func(t *testing.T, path string)
	}{
		{"absent", func(*testing.T, string) {}},
		{"a symlink", func(t *testing.T, path string) {
			if err := os.Symlink(t.TempDir(), path); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := os.MkdirAll(paths.GlobalHome(), 0o755); err != nil {
				t.Fatal(err)
			}
			tc.plant(t, filepath.Join(paths.GlobalHome(), ".pi-shared-npm"))
			if got := retiredNote(t, packsFixture(t, "pi")); got != "" {
				t.Errorf("want no note, got:\n%s", got)
			}
		})
	}
	t.Run("a pack that unshares nothing", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		if err := os.MkdirAll(filepath.Join(paths.GlobalHome(), ".pi-shared-npm"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := retiredNote(t, packsFixture(t, "claude")); got != "" {
			t.Errorf("a pack that unshares nothing named a folder:\n%s", got)
		}
	})
}

// TestAFolderAPackStillSharesIsNeverOfferedForDeletion: a selected pack that still declares the
// directory at scope machine is using it, whichever pack unshares it, so it is not retired.
func TestAFolderAPackStillSharesIsNeverOfferedForDeletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(paths.GlobalHome(), ".shared-store"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifest := `{"name": "keeper", "contributes": [
		{"kind": "state", "at": ".shared-store", "scope": "machine", "because": "still shared"},
		{"kind": "hook", "hook": "unshare_directory", "from": ".tool/old", "at": ".shared-store"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "keeper")
	if len(problems) != 0 {
		t.Fatalf("loading the fixture pack: %v", problems)
	}
	if got := retiredNote(t, []*packload.Pack{p}); got != "" {
		t.Errorf("a folder a selected pack still shares was offered for deletion:\n%s", got)
	}
}

// TestAShippedPacksMachineFolderIsNeverOfferedForDeletion: a configured pack's unshare hook may
// name any directory, a shipped pack's machine-scope one included, and storage.EnsureGlobalStorage
// makes every shipped pack's on every machine, whatever this workspace selects, for the other
// workspaces' jails to mount. `.claude-shared-credentials` is every claude jail's login, so a line
// offering its `rm -rf` in a workspace that does not select claude would log every one of them
// out.
func TestAShippedPacksMachineFolderIsNeverOfferedForDeletion(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(paths.GlobalHome(), ".claude-shared-credentials"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	manifest := `{"name": "mytool", "contributes": [
		{"kind": "hook", "hook": "unshare_directory", "from": ".mytool/creds", "at": ".claude-shared-credentials"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, "mytool")
	if len(problems) != 0 {
		t.Fatalf("loading the fixture pack: %v", problems)
	}
	if got := retiredNote(t, []*packload.Pack{p}); got != "" {
		t.Errorf("a shipped pack's machine folder, which other workspaces' jails mount, was offered "+
			"for deletion:\n%s", got)
	}
}

// TestRetiredSharedDirsReadTheKnownHook pins the spelled hook name against the closed set
// packdecl publishes: a typo would read no hook and say nothing, with every cell above that
// builds its own fixture still green.
func TestRetiredSharedDirsReadTheKnownHook(t *testing.T) {
	if !slices.Contains(packdecl.KnownHooks, hookUnshareDirectory) {
		t.Fatalf("packdecl.KnownHooks = %v does not contain %q", packdecl.KnownHooks, hookUnshareDirectory)
	}
}

// TestRunContainerNotesRetiredSharedDirs pins the call site: runContainer says it after the
// launch banner, on BOTH container backends (both bind the machine store), for loadedPacks,
// and not for a sealed build. Delete the call and every cell above stays green while no launch
// ever says it.
func TestRunContainerNotesRetiredSharedDirs(t *testing.T) {
	fd := funcDecl(t, "run.go", "runContainer")
	var banner token.Pos
	var calls []*ast.CallExpr
	ast.Inspect(fd, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch skelCallee(call) {
		case "emitLaunchBanner":
			banner = call.Pos()
		case "noteRetiredSharedDirs":
			calls = append(calls, call)
		}
		return true
	})
	if len(calls) != 1 {
		t.Fatalf("runContainer calls noteRetiredSharedDirs %d times, want once", len(calls))
	}
	if banner == token.NoPos {
		t.Fatal("runContainer's launch banner moved; re-anchor this pin, do not delete it")
	}
	if calls[0].Pos() < banner {
		t.Error("noteRetiredSharedDirs runs before the launch banner, where the nix build scrolls it away")
	}
	if len(calls[0].Args) != 1 || skelIdent(calls[0].Args[0]) != "loadedPacks" {
		t.Error("noteRetiredSharedDirs is not handed loadedPacks, the launch's selection")
	}
	for _, st := range fd.Body.List {
		if ifs, ok := st.(*ast.IfStmt); ok && skelIsPodmanArm(ifs.Cond) && callsIn(ifs.Body)["noteRetiredSharedDirs"] {
			t.Error("noteRetiredSharedDirs runs inside a podman-only arm, but Apple Container binds the " +
				"machine store too")
		}
	}
	// The sealed-build gate: the one call sits in the body of an `if !o.Sealed`, so a sealed
	// build's log never carries the line.
	gated := false
	ast.Inspect(fd, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok && isNotSealed(ifs.Cond) &&
			ifs.Body.Pos() <= calls[0].Pos() && calls[0].End() <= ifs.Body.End() {
			gated = true
		}
		return true
	})
	if !gated {
		t.Error("noteRetiredSharedDirs is not inside an `if !o.Sealed`, so a sealed build's log carries it")
	}
}

// isNotSealed reports whether cond is `!o.Sealed`.
func isNotSealed(cond ast.Expr) bool {
	ue, ok := cond.(*ast.UnaryExpr)
	if !ok || ue.Op != token.NOT {
		return false
	}
	sel, ok := ue.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Sealed" && skelIdent(sel.X) == "o"
}
