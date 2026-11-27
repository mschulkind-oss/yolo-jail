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

// TestLaunchCallsUnifyHome pins the call sites no unit harness reaches: the callers of unifyHome in
// the package's non-test code are exactly runContainer once and Run once, so deleting either call
// (which no callee test notices) fails here. Run's call sits in the macos-user arm after commitHome
// (a refused launch writes nothing) and before armMacosUser (UHG-D5).
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
	if len(callers) != 2 || callers["runContainer"] != 1 || callers["Run"] != 1 {
		t.Fatalf("unifyHome must be called once by runContainer and once by Run; callers: %v", callers)
	}
	calls := funcCalls(t, "run.go", "Run")
	unify, commit, arm := calls["unifyHome"], calls["commitHome"], calls["armMacosUser"]
	if len(unify) != 1 || len(arm) != 1 || len(commit) == 0 {
		t.Fatalf("Run's calls: unifyHome %d, armMacosUser %d, commitHome %d", len(unify), len(arm), len(commit))
	}
	if !(commit[len(commit)-1] < unify[0] && unify[0] < arm[0]) {
		t.Errorf("Run must call unifyHome after the macos-user arm's commitHome and before armMacosUser")
	}
}

// TestSealedAndDryGuestLaunchesComposeNothing (UHG-D5): a sealed guest build is a throwaway home
// and composes nothing, and a dry run composes nothing.
func TestSealedAndDryGuestLaunchesComposeNothing(t *testing.T) {
	if n := len(funcCalls(t, "seal.go", "runSealedMacosUser")["unifyHome"]); n != 0 {
		t.Errorf("runSealedMacosUser calls unifyHome %d times", n)
	}
	f := newUnifyFixture(t, "seatbelt-self")
	putUnify(t, f.o.homeDir(), ".claude/projects/-w/w.jsonl", "s")
	f.o.DryRun = true
	f.unify(t, "macos-user")
	if _, err := os.Lstat(paths.UnifiedHome(f.ws)); err == nil {
		t.Error("a dry run made the unified home")
	}
	if _, err := os.Readlink(filepath.Join(f.o.homeDir(), ".claude", "projects")); err == nil {
		t.Error("a dry run composed the slice")
	}
}
