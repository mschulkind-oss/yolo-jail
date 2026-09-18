package run

// workspacescopeguard_test.go pins the guard that refuses a launch whose workspace would
// bind the host side of the credential boundary into the jail (workspacescopeguard.go).
// Three pins, the same three the live-overlay guard has and for the same reasons: the
// truth table (both containment directions, and the ordinary ~/code case that must stay
// allowed), the Run-level refusal, and the call-site position — a guard nothing consults,
// or one consulted after the launch log has already written into the directory it is
// refusing, is the silent-skip failure AGENTS.md names.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeGuardHome mints a HOME whose symlinks are ALREADY resolved and points the process
// at it. Resolving where the path is minted is the fix for the darwin PATH-RESOLUTION
// class (AGENTS.md, Testing): on macOS t.TempDir() hands back /var/folders/… which is a
// symlink to /private/var/folders/…, and the guard resolves both sides — so an unresolved
// fixture home would compare against a resolved root, miss every containment test, and
// pass on Linux while failing on darwin.
func scopeGuardHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp home: %v", err)
	}
	t.Setenv("HOME", home)
	return home
}

func TestRefuseWorkspaceScope(t *testing.T) {
	home := scopeGuardHome(t)
	join := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }

	cases := []struct {
		name string
		ws   string
		want bool // want a refusal
	}{
		{"the home itself", home, true},
		{"a parent of the home", filepath.Dir(home), true},
		{"the filesystem root", "/", true},
		{"yolo's state directory", join(".local", "share", "yolo-jail"), true},
		{"a parent of the state directory", join(".local", "share"), true},
		{"inside the state directory", join(".local", "share", "yolo-jail", "home"), true},
		{"yolo's user config directory", join(".config", "yolo-jail"), true},
		{"a parent of the user config directory", join(".config"), true},
		{"inside the user config directory", join(".config", "yolo-jail", "local"), true},
		// The ordinary cases. A guard that refused these would be unusable.
		{"an ordinary project under the home", join("code", "yolo-jail"), false},
		{"a dotfiles tree under the home", join(".dotfiles"), false},
		{"another tool's config under ~/.config", join(".config", "nvim"), false},
		{"a sibling of yolo's state dir", join(".local", "share", "mise"), false},
		{"a workspace outside the home entirely", "/tmp/yolo-nested", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := goldenOptions(tc.ws, home)
			if got := refuseWorkspaceScope(o) != nil; got != tc.want {
				t.Errorf("refuseWorkspaceScope(%q) refused = %v, want %v", tc.ws, got, tc.want)
			}
		})
	}
}

// TestRefuseWorkspaceScopeNamesTheRootAndTheDirection pins the message content, because
// the whole value of this refusal to the person reading it is knowing WHICH root and
// WHICH way round: "your workspace contains ~/.config/yolo-jail" and "your workspace is
// inside it" are different mistakes with different fixes.
func TestRefuseWorkspaceScopeNamesTheRootAndTheDirection(t *testing.T) {
	home := scopeGuardHome(t)
	for _, tc := range []struct{ name, ws, wantWhat string }{
		{"the home itself", home, "IS your home directory"},
		{"a parent", filepath.Dir(home), "CONTAINS your home directory"},
		{"inside the state dir", filepath.Join(home, ".local/share/yolo-jail/home"), "is INSIDE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refusal := refuseWorkspaceScope(goldenOptions(tc.ws, home))
			if refusal == nil {
				t.Fatalf("workspace %q should have been refused", tc.ws)
			}
			if !strings.Contains(refusal.what, tc.wantWhat) {
				t.Errorf("refusal.what = %q, want it to contain %q", refusal.what, tc.wantWhat)
			}
			if refusal.why == "" {
				t.Error("a refusal with no `why` tells the reader nothing about what it prevented")
			}
		})
	}
}

// TestRunRefusesAHomeWorkspaceLaunch drives the real Run on a workspace that IS the home.
// The second assertion is the load-bearing one: <workspace>/.yolo must not exist
// afterwards. That directory — with a full home/ overlay inside it — is the artifact the
// accident leaves behind, and a refusal that creates it has already done a piece of what
// it refused.
func TestRunRefusesAHomeWorkspaceLaunch(t *testing.T) {
	home := scopeGuardHome(t)
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, home, "podman", &stdout, &stderr, nil)
	o.Getenv = guardEnv(nil) // on the host: the live-overlay guard must not be what fires

	rc := Run(*o)
	if rc != 1 {
		t.Fatalf("a launch whose workspace is the home must refuse, rc=%d:\n%s%s",
			rc, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".yolo")); !os.IsNotExist(err) {
		t.Errorf("the refusal created %s — the very artifact it exists to prevent (stat err: %v)",
			filepath.Join(home, ".yolo"), err)
	}
	out := stdout.String() + stderr.String()
	for _, want := range []string{"Refusing to launch", "home directory", "cd into the project"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal should name %q:\n%s", want, out)
		}
	}
}

// TestRunCallsTheWorkspaceScopeGuardBeforeAnySideEffect is the call-site pin. The
// Run-level test above covers the behaviour but would not notice the call sliding below
// the launch log or the staging phase, which is where its cheapness — and for the home
// case its correctness — comes from. Source-reading is this repo's answer for a position
// that no runtime assertion can see (liveoverlayguard_test.go's pin, same shape).
func TestRunCallsTheWorkspaceScopeGuardBeforeAnySideEffect(t *testing.T) {
	pos := firstCallsInRun(t, "refuseWorkspaceScope", "attachLaunchLog", "stageRunPacks")

	if pos["refuseWorkspaceScope"] == token.NoPos {
		t.Fatal("Run no longer calls refuseWorkspaceScope — a launch on the home, or on " +
			"either of yolo's own host directories, would bind the credential boundary " +
			"into the jail (workspacescopeguard.go). If the guard moved, move this pin " +
			"with it rather than deleting it.")
	}
	if logPos := pos["attachLaunchLog"]; logPos != token.NoPos && pos["refuseWorkspaceScope"] > logPos {
		t.Fatal("attachLaunchLog sits ABOVE the workspace-scope guard — the refusal would " +
			"write <workspace>/.yolo/launch.log, which for the home case is the stray " +
			"~/.yolo this guard exists to prevent.")
	}
	if stagePos := pos["stageRunPacks"]; stagePos != token.NoPos && pos["refuseWorkspaceScope"] > stagePos {
		t.Fatal("refuseWorkspaceScope sits BELOW pack staging — the refusal would fire only " +
			"after the work it exists to prevent had already run.")
	}
}

// firstCallsInRun reports the position of the first call to each named function inside
// run.go's Run, or token.NoPos for one that is not called at all.
func firstCallsInRun(t *testing.T, names ...string) map[string]token.Pos {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	out := map[string]token.Pos{}
	for _, n := range names {
		out[n] = token.NoPos
	}
	var found bool
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Run" || fd.Recv != nil {
			continue
		}
		found = true
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if ok && want[id.Name] && out[id.Name] == token.NoPos {
				out[id.Name] = call.Pos()
			}
			return true
		})
	}
	if !found {
		t.Fatal("run.go has no function Run")
	}
	return out
}
