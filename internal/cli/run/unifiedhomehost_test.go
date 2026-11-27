package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// walkEntries lists every path beneath root (root excluded), slash separated and relative.
func walkEntries(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != root {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestHostSetupNeverReachesTheUnifiedHome (unified-workspace-home.md §10: host and jail never share a
// home), over every built-in setup, through the launch's own merge-and-compose and not a callee:
//   - a host setup (and the yolo account while UHG-D8's gate is closed) yields no unified home and
//     moves nothing, however much its slice holds;
//   - a guest's HOME is its own slice under .yolo/setups, never the invoking user's home;
//   - every link a container setup's slice is given points at the in-jail unified home, and resolves
//     (as the jail resolves /workspace) to a file under paths.UnifiedHome; a guest's is relative and
//     resolves, by the kernel, under paths.UnifiedHome.
func TestHostSetupNeverReachesTheUnifiedHome(t *testing.T) {
	for _, s := range setup.BuiltIns() {
		t.Run(s.Name, func(t *testing.T) {
			f := newUnifyFixture(t, s.Name)
			userHome := os.Getenv("HOME")
			slice := f.o.homeDir()
			if slice != paths.SetupHomeState(f.ws, s.Name) {
				t.Fatalf("%s binds home %s, not its own slice", s.Name, slice)
			}
			if strings.HasPrefix(slice, userHome+string(filepath.Separator)) ||
				!strings.HasPrefix(slice, paths.SetupsDir(f.ws)+string(filepath.Separator)) {
				t.Fatalf("%s's HOME %s must be under %s and never under the invoking user's %s",
					s.Name, slice, paths.SetupsDir(f.ws), userHome)
			}
			// A session in the spelling of the state directory the setup's runtime reads.
			rt, dir := "podman", "claude"
			if s.Confinement == setup.ConfinementAppleContainer {
				rt, dir = "container", ".claude"
			}
			if s.Kind() == setup.KindGuest {
				rt, dir = "macos-user", ".claude"
			}
			putUnify(t, slice, dir+"/projects/-workspace/w.jsonl", "session")
			before := walkEntries(t, slice)

			f.o.unifyHome(jsonx.NewOrderedMap(), slice, packsFixture(t, "claude"), rt)

			if s.Kind() == setup.KindHost || (isYoloAccountSetup(s) && !unifiedHomeJoinsYoloAccount) {
				if _, err := os.Lstat(paths.UnifiedHome(f.ws)); err == nil {
					t.Errorf("%s (%s) made %s", s.Name, s.Kind(), paths.UnifiedHome(f.ws))
				}
				if after := walkEntries(t, slice); strings.Join(after, "\n") != strings.Join(before, "\n") {
					t.Errorf("%s (%s) changed its slice:\nbefore %v\nafter  %v", s.Name, s.Kind(), before, after)
				}
				if f.stderr.Len() != 0 && s.Kind() == setup.KindHost {
					t.Errorf("%s (%s) printed a unified-home line:\n%s", s.Name, s.Kind(), f.stderr.String())
				}
				return
			}

			links := 0
			for _, rel := range walkEntries(t, slice) {
				p := filepath.Join(slice, filepath.FromSlash(rel))
				if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSymlink == 0 {
					continue
				}
				links++
				var got string
				if s.Kind() == setup.KindGuest {
					var err error
					if got, err = filepath.EvalSymlinks(p); err != nil {
						t.Fatalf("%s does not resolve: %v", p, err)
					}
				} else {
					got = resolveInJail(t, f.ws, p)
				}
				if !strings.HasPrefix(got, paths.UnifiedHome(f.ws)+string(filepath.Separator)) {
					t.Errorf("%s -> %s resolves to %s, outside %s", p, mustReadlink(t, p), got, paths.UnifiedHome(f.ws))
				}
			}
			if links == 0 {
				t.Errorf("%s composed no link: the guard would pass over nothing", s.Name)
			}
		})
	}
}

// TestLaunchCallsUnifyHome pins the call site no unit harness reaches: the only caller of unifyHome in
// the package's non-test code is runContainer, and it calls it, so deleting that call (which no
// callee test notices) fails here. The guest and host launch paths never name it.
func TestLaunchCallsUnifyHome(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	callers := map[string]int{}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fd, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "unifyHome" {
						callers[fd.Name.Name]++
					}
				}
				return true
			})
		}
	}
	if len(callers) != 1 || callers["runContainer"] != 1 {
		t.Errorf("unifyHome must be called once, by runContainer; callers: %v", callers)
	}
}
