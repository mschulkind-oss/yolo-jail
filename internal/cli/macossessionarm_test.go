package cli

// macossessionarm_test.go pins the front door's half of the macos-user arm (internal/cli/run's
// macosuserarm.go): `yolo run` makes the arm and hands it to the pipeline, and the handler it
// injects hands the backend the arm's session runner — not the plain exec run.RunWithProxy, which
// has no arm — the arm's Ending, and its session-start hook. No unit test can drive a macos-user
// session on Linux, so the wiring is pinned twice: by the values the Deps carry, and in source.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
)

// identName is e's name when it is a bare identifier, else "".
func identName(e ast.Expr) string {
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// funcPC is the code a func value runs: a method value of one method has one for every receiver.
func funcPC(f any) uintptr { return reflect.ValueOf(f).Pointer() }

func TestMacosUserSessionDepsAreTheArms(t *testing.T) {
	arm := run.NewMacosUserArm()
	log := perf.New(nil)
	deps := macosUserSessionDeps(arm, log, nil)
	if deps.RunWithProxy == nil || funcPC(deps.RunWithProxy) != funcPC(arm.RunSession) {
		t.Error("the session does not run under the launch's arm (MacosUserArm.RunSession): a SIGTERM " +
			"or a closed window would end the launcher past its teardown")
	}
	if funcPC(deps.RunWithProxy) == funcPC(run.RunWithProxy) {
		t.Error("the session runs through run.RunWithProxy, which has no signal arm")
	}
	if deps.Ending == nil || funcPC(deps.Ending) != funcPC(arm.Ending) {
		t.Error("the backend is not asked the arm's Ending: a setup signal would continue to the agent")
	}
	if deps.Perf != log {
		t.Error("the backend's steps are not spanned on the launch's collector")
	}
	// Still a launch's Deps: the lock, the guest binaries and the account home's hold.
	if deps.LockWorkspace == nil || deps.GuestBinaries == nil || deps.HoldAccountHome == nil {
		t.Error("macosUserSessionDeps dropped a launch-only seam macosLaunchDeps wires")
	}
}

// THE FRONT DOOR MAKES THE ARM and hands Run the same one its handler closes over.
func TestTheFrontDoorHandsThePipelineAMacosUserArm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	captureBoth(t, func() {
		if rc := Main([]string{"yolo", "--", "true"}); rc != 0 {
			t.Errorf("Main rc = %d with the pipeline stubbed", rc)
		}
	})
	if seen.MacosUserArm == nil {
		t.Error("the front door handed the pipeline no macos-user arm, so Run installs one of its own " +
			"that the handler's session runner never sees")
	}
	if seen.PerfRef == nil {
		t.Error("the front door handed no PerfRef, so the macos-user handler spans nothing")
	}
}

// THE WIRING IN SOURCE: macosUserRun builds its Deps from the arm it was handed, and the Options it
// hands the backend carry the arm's AgentStarting; runRun passes the arm it handed the pipeline.
func TestMacosUserRunWiresTheArmItWasHanded(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	decl := func(name string) *ast.FuncDecl {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Name.Name == name {
				return fd
			}
		}
		t.Fatalf("commands.go has no %s", name)
		return nil
	}
	selector := func(e ast.Expr, x, sel string) bool {
		s, ok := e.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		id, ok := s.X.(*ast.Ident)
		return ok && id.Name == x && s.Sel.Name == sel
	}

	sessionDeps, agentStart := false, false
	ast.Inspect(decl("macosUserRun"), func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "macosUserSessionDeps" && len(n.Args) == 3 {
				sessionDeps = identName(n.Args[0]) == "arm" && identName(n.Args[1]) == "log"
			}
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "macosLaunchDeps" {
				t.Error("macosUserRun builds its Deps from macosLaunchDeps directly, past the arm")
			}
		case *ast.KeyValueExpr:
			if k, ok := n.Key.(*ast.Ident); ok && k.Name == "OnAgentStart" && selector(n.Value, "arm", "AgentStarting") {
				agentStart = true
			}
		}
		return true
	})
	if !sessionDeps {
		t.Error("macosUserRun does not build its Deps with macosUserSessionDeps(arm, log, …)")
	}
	if !agentStart {
		t.Error("macosUserRun hands the backend no OnAgentStart: arm.AgentStarting")
	}

	runner, ending := false, false
	ast.Inspect(decl("macosUserSessionDeps"), func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "macosLaunchDeps" && len(n.Args) > 0 &&
				selector(n.Args[0], "arm", "RunSession") {
				runner = true
			}
		case *ast.AssignStmt:
			if len(n.Lhs) == 1 && selector(n.Lhs[0], "deps", "Ending") && selector(n.Rhs[0], "arm", "Ending") {
				ending = true
			}
		}
		return true
	})
	if !runner || !ending {
		t.Errorf("macosUserSessionDeps hands the backend the arm's runner %v and Ending %v; want both", runner, ending)
	}

	handed := false
	ast.Inspect(decl("runRun"), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "macosUserRun" {
				for _, a := range call.Args {
					if identName(a) == "arm" {
						handed = true
					}
				}
			}
		}
		return true
	})
	if !handed {
		t.Error("runRun's macos-user handler does not pass the arm it made to macosUserRun")
	}
}
