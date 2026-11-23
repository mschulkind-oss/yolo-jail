package run

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// TestADeclinedLaunchLeavesTheLegacyHomeInPlace: the legacy home moves only once the launch is
// committed, so a launch that passes the home selection and is then refused by a LATER gate (here the
// config-change approval, which a non-interactive launch declines) moves nothing and creates no
// setup home. Red if the move runs at selectHome, as it did before it was deferred to commitHome.
func TestADeclinedLaunchLeavesTheLegacyHomeInPlace(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{"packages": ["jq"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	marker := seedLegacyHome(t, ws)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	reached := false
	o.MacosUserRun = fakeMacosUserRun(func(macosUserCall) int { reached = true; return 0 })
	// AcceptConfigChanges is unset and nothing is a terminal: the approval gate declines.
	if rc := Run(*o); rc == 0 || reached {
		t.Fatalf("the launch should have been declined at the config gate (rc=%d, reached backend=%v)\nstderr:\n%s", rc, reached, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(paths.LegacyHomeState(ws), marker)); err != nil {
		t.Errorf("a declined launch moved the legacy home: %v\nstderr:\n%s", err, stderr.String())
	}
	if _, err := os.Lstat(paths.SetupHomeState(ws, testSetupForRuntime("macos-user"))); !os.IsNotExist(err) {
		t.Errorf("a declined launch created the setup's home (err %v)", err)
	}
	if strings.Contains(stderr.String(), "Moved this project's home") {
		t.Errorf("a declined launch said it moved the home:\n%s", stderr.String())
	}
}

// TestEveryArmCommitsTheHomeBeforeItWritesIt pins each arm's call site of commitHome, which no
// unit-test harness reaches for the container arm: a test of commitHome alone would pass with the
// call deleted, and the legacy home would then never move. In runContainer it follows the image
// load (the last gate that can fail) and precedes prepareWsState (the first write into the home);
// in Run, the macos-user arm's call precedes the arm's first host service and the sealed arm's
// precedes its build, and none sits above the config approval.
func TestEveryArmCommitsTheHomeBeforeItWritesIt(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}
	// first records, per function, the position of the first call of each method name, and all
	// positions of commitHome calls.
	type pos struct {
		first  map[string]token.Pos
		commit []token.Pos
	}
	funcs := map[string]*pos{}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || (fd.Name.Name != "runContainer" && fd.Name.Name != "Run") {
			continue
		}
		p := &pos{first: map[string]token.Pos{}}
		funcs[fd.Name.Name] = p
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if _, seen := p.first[sel.Sel.Name]; !seen {
					p.first[sel.Sel.Name] = call.Pos()
				}
				if sel.Sel.Name == "commitHome" {
					p.commit = append(p.commit, call.Pos())
				}
			}
			return true
		})
	}
	rc, run := funcs["runContainer"], funcs["Run"]
	if rc == nil || run == nil {
		t.Fatal("run.go no longer declares Run and runContainer")
	}
	if len(rc.commit) != 1 || rc.first["autoLoadImage"] == token.NoPos || rc.first["prepareWsState"] == token.NoPos ||
		rc.commit[0] < rc.first["autoLoadImage"] || rc.commit[0] > rc.first["prepareWsState"] {
		t.Errorf("runContainer must call commitHome once, after autoLoadImage and before prepareWsState")
	}
	// Run: one call before runSealedMacosUser, one before armMacosUser, both after the approval.
	approval, sealed, arm := run.first["checkConfigChanges"], run.first["runSealedMacosUser"], run.first["armMacosUser"]
	if len(run.commit) != 2 || sealed == token.NoPos || arm == token.NoPos {
		t.Fatalf("Run must call commitHome twice (sealed and macos-user arms), got %d", len(run.commit))
	}
	if !(run.commit[0] < sealed && sealed < run.commit[1] && run.commit[1] < arm) {
		t.Errorf("Run's commitHome calls must precede runSealedMacosUser and armMacosUser respectively")
	}
	if approval != token.NoPos && run.commit[1] < approval {
		t.Errorf("the macos-user arm commits the home before its config approval")
	}
}

// TestAnAppleContainerSetupLeavesAPodmanLaidHome: Podman lays the home with its dot-stripped bind
// sources (claude/, npm-global/ ...) and Apple Container reads dotted ones (.claude/), so an Apple
// Container setup that took a Podman-laid home would show none of its sessions. It starts its own
// home, leaves that one in place and names the setup that should adopt it.
func TestAnAppleContainerSetupLeavesAPodmanLaidHome(t *testing.T) {
	for _, c := range []struct {
		name  string
		dirs  []string
		moves bool
	}{
		{"claude and the overlay sources", []string{"claude/projects", "npm-global", "local"}, false},
		{"claude alone, no core marker", []string{"claude/projects"}, true},
		{"both layouts, no apple marker", []string{"claude/projects", ".claude/projects", "yolo-bin"}, false},
		{"dotted layout only", []string{".claude/projects"}, true},
		{"nothing laid in it", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			ws := t.TempDir()
			src := paths.LegacyHomeState(ws)
			if err := os.MkdirAll(src, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, d := range c.dirs {
				if err := os.MkdirAll(filepath.Join(src, filepath.FromSlash(d)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			r := legacyRequest(ws, "apple-container-self", projectJailsStopped)
			r.AppleContainerLaid = func(string) bool { return false }
			got, line := adoptLegacyHome(r)
			if c.moves {
				if got != legacyHomeMoved {
					t.Fatalf("result = %v (%q), want moved", got, line)
				}
				return
			}
			if got != legacyHomeOthers {
				t.Fatalf("result = %v (%q), want %v", got, line, legacyHomeOthers)
			}
			if _, err := os.Lstat(src); err != nil {
				t.Errorf("the Podman-laid home was moved: %v", err)
			}
			if _, err := os.Lstat(paths.SetupHomeState(ws, "apple-container-self")); !os.IsNotExist(err) {
				t.Errorf("the setup's home exists beside the unmoved one (err %v)", err)
			}
			// Happy path: the line names the setup that should launch, and the command.
			for _, want := range []string{"Podman", "podman-self", "yolo run --setup podman-self"} {
				if !strings.Contains(line, want) {
					t.Errorf("the line does not name %q: %q", want, line)
				}
			}
			assertOneLine(t, line)
			// And the launch binds the setup's own home, not the one it left.
			o := &Options{Workspace: ws, Setup: setup.MustBuiltIn("apple-container-self")}
			if h := o.currentHome(); h != paths.SetupHomeState(ws, "apple-container-self") {
				t.Errorf("currentHome = %s, want the setup's own", h)
			}
		})
	}
}
