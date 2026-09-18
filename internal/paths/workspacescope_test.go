package paths

// workspacescope_test.go pins the predicate that decides whether a directory may be a
// workspace at all, and the chokepoint that refuses to create a `.yolo` in one that may
// not. Four pins: the truth table (both containment directions and every relation, plus
// the ordinary directories that MUST stay allowed), the relation/kind a caller keys its own
// message off, symlink resolution on both sides of the comparison, and
// EnsureWorkspaceStateDir refusing without creating anything.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeHome mints a HOME whose symlinks are ALREADY resolved and points the process at it.
// Resolving where the path is MINTED rather than at each comparison is the fix for the
// darwin PATH-RESOLUTION class (AGENTS.md, Testing): on macOS t.TempDir() returns
// /var/folders/… which is a symlink to /private/var/folders/…, and this predicate resolves
// both sides — so an unresolved fixture home would be compared against a resolved root,
// match nothing, and pass on Linux while failing on darwin.
func scopeHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestWorkspaceScopeBreach(t *testing.T) {
	home := scopeHome(t)
	under := func(parts ...string) string {
		return filepath.Join(append([]string{home}, parts...)...)
	}

	cases := []struct {
		name     string
		ws       string
		want     bool // want a breach
		kind     ScopeRootKind
		relation ScopeRelation
	}{
		{"the home itself", home, true, RootHome, ScopeIsRoot},
		{"a parent of the home", filepath.Dir(home), true, RootHome, ScopeContainsRoot},
		{"the filesystem root", "/", true, RootHome, ScopeContainsRoot},
		{"the state dir", under(GlobalStorageRel()), true, RootStateDir, ScopeIsRoot},
		{"a parent of the state dir", under(".local", "share"), true, RootStateDir, ScopeContainsRoot},
		{"inside the state dir", under(GlobalStorageRel(), "home"), true, RootStateDir, ScopeInsideRoot},
		{"the user config dir", under(".config", "yolo-jail"), true, RootUserConfigDir, ScopeIsRoot},
		{"a parent of the user config dir", under(".config"), true, RootUserConfigDir, ScopeContainsRoot},
		{"inside the user config dir", under(".config", "yolo-jail", "local"), true, RootUserConfigDir, ScopeInsideRoot},

		// The ordinary directories. A predicate that refused any of these would be
		// unusable, and the home case is the one every project on the machine sits in.
		{"an ordinary project under the home", under("code", "yolo-jail"), false, 0, 0},
		{"a dotfiles tree under the home", under(".dotfiles"), false, 0, 0},
		{"another tool's config under ~/.config", under(".config", "nvim"), false, 0, 0},
		{"a sibling of the state dir", under(".local", "share", "mise"), false, 0, 0},
		{"a directory outside the home entirely", "/tmp/yolo-nested", false, 0, 0},
		// The one exemption: yolo's own capture scratch tree, which `yolo capture` really
		// does launch against (scopeExempt, and internal/capture/scopeexemption_test.go
		// pins it against the real Store.StagingDir).
		{"yolo's capture scratch workspace", under(GlobalStorageRel(), "captures", "staging", "claude"), false, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			breach := WorkspaceScopeBreach(tc.ws)
			if got := breach != nil; got != tc.want {
				t.Fatalf("WorkspaceScopeBreach(%q) breached = %v, want %v", tc.ws, got, tc.want)
			}
			if allowed := WorkspaceStateDirAllowed(tc.ws); allowed == tc.want {
				t.Errorf("WorkspaceStateDirAllowed(%q) = %v, disagrees with the breach", tc.ws, allowed)
			}
			if breach == nil {
				return
			}
			if breach.Kind != tc.kind {
				t.Errorf("Kind = %d, want %d (%s)", breach.Kind, tc.kind, tc.kind.Name())
			}
			if breach.Relation != tc.relation {
				t.Errorf("Relation = %d, want %d", breach.Relation, tc.relation)
			}
		})
	}
}

// TestScopeBreachWhatNamesTheRootAndTheDirection pins the rendered clause, because the
// whole value of a refusal to the person reading it is knowing WHICH root and WHICH way
// round: "your workspace contains ~/.config/yolo-jail" and "your workspace is inside it"
// are different mistakes with different fixes.
func TestScopeBreachWhatNamesTheRootAndTheDirection(t *testing.T) {
	home := scopeHome(t)
	for _, tc := range []struct{ name, ws, want string }{
		{"is", home, "the workspace IS your home directory"},
		{"contains", filepath.Dir(home), "CONTAINS your home directory"},
		{"inside", filepath.Join(home, GlobalStorageRel(), "home"), "is INSIDE yolo's own state directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			breach := WorkspaceScopeBreach(tc.ws)
			if breach == nil {
				t.Fatalf("%q should have breached", tc.ws)
			}
			if !strings.Contains(breach.What(), tc.want) {
				t.Errorf("What() = %q, want it to contain %q", breach.What(), tc.want)
			}
		})
	}
}

// TestWorkspaceScopeBreachResolvesBothSides is the symlinked-home case, and it is not
// hypothetical: a macOS home reached through /var → /private/var, or a Linux home on a
// moved disk reached through a link, would otherwise sail past a predicate that compared
// the strings it was handed. Both spellings of the same directory must breach.
func TestWorkspaceScopeBreachResolvesBothSides(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real-home")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link-home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	t.Run("HOME is the link, the workspace is the real path", func(t *testing.T) {
		t.Setenv("HOME", link)
		if WorkspaceScopeBreach(real) == nil {
			t.Errorf("a workspace at the home's resolved path %q must breach", real)
		}
	})
	t.Run("HOME is the real path, the workspace is the link", func(t *testing.T) {
		t.Setenv("HOME", real)
		if WorkspaceScopeBreach(link) == nil {
			t.Errorf("a workspace at a symlink to the home %q must breach", link)
		}
	})
}

// TestEnsureWorkspaceStateDirRefusesABoundaryDirectory is the chokepoint pin. The
// directory must not exist afterwards: it IS the artifact — a stray ~/.yolo is what
// hijacks workspaceRoot()'s upward walk forever after, long after the command that made it
// is forgotten.
func TestEnsureWorkspaceStateDirRefusesABoundaryDirectory(t *testing.T) {
	home := scopeHome(t)
	for _, ws := range []string{
		home,
		filepath.Join(home, ".config"),
		filepath.Join(home, GlobalStorageRel()),
		filepath.Join(home, GlobalStorageRel(), "home"),
	} {
		dir, err := EnsureWorkspaceStateDir(ws)
		if err == nil {
			t.Errorf("EnsureWorkspaceStateDir(%q) succeeded; it must refuse", ws)
			continue
		}
		var breach *ScopeBreach
		if !errors.As(err, &breach) {
			t.Errorf("EnsureWorkspaceStateDir(%q) error = %T, want a *ScopeBreach a caller "+
				"can inspect", ws, err)
		}
		if !strings.Contains(err.Error(), "refusing to create") || !strings.Contains(err.Error(), dir) {
			t.Errorf("the error should name what it refused to create: %q", err.Error())
		}
		if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
			t.Errorf("%s exists after a refusal — the refusal created the very artifact it "+
				"exists to prevent (stat err: %v)", dir, statErr)
		}
	}
}

// TestEnsureWorkspaceStateDirStillServesAnOrdinaryWorkspace is the other half, and the one
// that fails if the predicate is ever widened carelessly: a project under the home is the
// ordinary case, and every launch depends on this directory being created.
func TestEnsureWorkspaceStateDirStillServesAnOrdinaryWorkspace(t *testing.T) {
	home := scopeHome(t)
	ws := filepath.Join(home, "code", "project")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := EnsureWorkspaceStateDir(ws)
	if err != nil {
		t.Fatalf("EnsureWorkspaceStateDir(%q): %v", ws, err)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("%s was not created (err: %v)", dir, err)
	}
	if _, err := os.Stat(filepath.Join(dir, WorkspaceStateIgnoreName)); err != nil {
		t.Errorf("the state dir is committable: %v", err)
	}
}
