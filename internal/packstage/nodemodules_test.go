package packstage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A DEPENDENCY TREE IS SKIPPED, NAMED, AND NEVER FOLLOWED (requests #29): a dangling link deep in a
// pnpm virtual store aborted the whole walk. Stage, Check and the FollowSymlinks walk all skip
// `node_modules` at any depth and report it; the same escaping link OUTSIDE it is still refused.
func TestWalkSkipsNodeModulesAtAnyDepthAndNamesIt(t *testing.T) {
	outside := t.TempDir()
	secret := filepath.Join(outside, "id")
	if err := os.WriteFile(secret, []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, follow := range []bool{false, true} {
		for _, write := range []bool{false, true} {
			root := t.TempDir()
			writePack(t, root, map[string]os.FileMode{"briefing/note.md": 0o644})
			nm := filepath.Join(root, "sub", "node_modules", ".pnpm", "pkg")
			if err := os.MkdirAll(nm, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(root, "nowhere"), filepath.Join(nm, "dangling")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secret, filepath.Join(nm, "escape")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nm, "index.js"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "node_modules")); err != nil {
				t.Fatal(err)
			}
			spec := Spec{Root: root, FollowSymlinks: follow}
			var res *Result
			var err error
			if write {
				spec.Dest = t.TempDir()
				res, err = Stage(spec)
			} else {
				res, err = Check(spec)
			}
			if err != nil {
				t.Fatalf("follow=%v write=%v: %v", follow, write, err)
			}
			if got := strings.Join(res.Staged, ","); got != "briefing/note.md" {
				t.Errorf("follow=%v write=%v staged %q, want only the note", follow, write, got)
			}
			if got := strings.Join(res.Skipped, ","); got != "node_modules,sub/node_modules" {
				t.Errorf("follow=%v write=%v skipped %q, want both node_modules named", follow, write, got)
			}
		}
	}
	// The same escaping link OUTSIDE node_modules still refuses.
	root := t.TempDir()
	writePack(t, root, map[string]os.FileMode{"briefing/note.md": 0o644})
	if err := os.MkdirAll(filepath.Join(root, "sub", "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "sub", "leak")); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(Spec{Root: root}); err == nil || !strings.Contains(err.Error(), "outside the pack") {
		t.Errorf("an escaping link beside node_modules = %v, want the refusal", err)
	}
}
