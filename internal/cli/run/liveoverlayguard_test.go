package run

// liveoverlayguard_test.go pins the guard that turns AGENTS.md's
// "never launch on /workspace from inside a jail" rule into a refusal — the
// accident it exists for was measured twice from inside this very repo (see
// liveoverlayguard.go), the second time by the session that wrote this file,
// whose cwd had drifted. Three pins: the truth table, the Run-level refusal
// (which must fire BEFORE any side effect — storage, config, staging), and the
// call-site pin, because a guard that exists and is never consulted is the
// silent-skip failure with a badge on it.

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// guardEnv builds the Getenv seam one jail-environment at a time: in-jail is
// YOLO_VERSION set, the hatch is YOLO_ALLOW_LIVE_WORKSPACE, everything else "".
func guardEnv(vals map[string]string) func(string) string {
	return func(name string) string { return vals[name] }
}

func TestRefuseLiveWorkspaceLaunch(t *testing.T) {
	cases := []struct {
		name string
		vals map[string]string
		ws   string
		want bool
	}{
		{"in a jail, on /workspace", map[string]string{"YOLO_VERSION": "0.8.0"}, "/workspace", true},
		{"in a jail, on an unrelated workspace", map[string]string{"YOLO_VERSION": "0.8.0"}, "/tmp/yolo-nested", false},
		{"in a jail, on a SUBDIR of the live tree", map[string]string{"YOLO_VERSION": "0.8.0"}, "/workspace/sub", false},
		{"on the host (no YOLO_VERSION), /workspace", nil, "/workspace", false},
		{"hatch set, in a jail, on /workspace", map[string]string{
			"YOLO_VERSION": "0.8.0", "YOLO_ALLOW_LIVE_WORKSPACE": "1"}, "/workspace", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := goldenOptions(tc.ws, t.TempDir())
			o.Getenv = guardEnv(tc.vals)
			if got := refuseLiveWorkspaceLaunch(o); got != tc.want {
				t.Errorf("refuseLiveWorkspaceLaunch = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRunRefusesALiveWorkspaceLaunch drives the real Run far enough to refuse.
// The guard is the FIRST thing Run does, so a refusal proves the whole pipeline
// (storage, config load, pack staging, image build) never started — which is
// the property that makes the guard safe to add, and the assertion a
// move-down-later regression would break.
func TestRunRefusesALiveWorkspaceLaunch(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, "/workspace", "podman", &stdout, &stderr, nil)
	o.Getenv = guardEnv(map[string]string{"YOLO_VERSION": "0.8.0-test"})

	rc := Run(*o)
	if rc != 1 {
		t.Fatalf("a live-workspace launch from inside a jail must refuse, rc=%d:\n%s%s",
			rc, stdout.String(), stderr.String())
	}
	out := stdout.String() + stderr.String()
	for _, want := range []string{
		"Refusing to launch",
		"/tmp/yolo-nested",
		"YOLO_ALLOW_LIVE_WORKSPACE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal should name %q:\n%s", want, out)
		}
	}
}

// TestRunCallsTheLiveOverlayGuardBeforeAnyWork is the call-site pin: Run-level
// behaviour is covered above, but nothing there would notice the guard call
// moving below the storage/config/staging phase — which would turn the cheapest
// refusal in the pipeline into one that had already done the work it exists to
// prevent. Reading the source is the repo's existing answer for unrunnable
// call sites (configapproval_test.go's methodDecl pattern, plain-function form).
func TestRunCallsTheLiveOverlayGuardBeforeAnyWork(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "run.go", nil, 0)
	if err != nil {
		t.Fatalf("parse run.go: %v", err)
	}
	var runDecl, guardPos, stagePos token.Pos
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "Run" {
			continue
		}
		runDecl = fd.Pos()
		ast.Inspect(fd, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				switch id.Name {
				case "refuseLiveWorkspaceLaunch":
					if guardPos == token.NoPos {
						guardPos = call.Pos()
					}
				case "stageRunPacks":
					if stagePos == token.NoPos {
						stagePos = call.Pos()
					}
				}
			}
			return true
		})
	}
	if runDecl == token.NoPos {
		t.Fatal("run.go has no function Run")
	}
	if guardPos == token.NoPos {
		t.Fatal("Run no longer calls refuseLiveWorkspaceLaunch — a launch on the live " +
			"workspace from inside a jail would regenerate the running session's home " +
			"again (liveoverlayguard.go records both measured incidents). If the guard " +
			"moved, move this pin with it rather than deleting it.")
	}
	if stagePos != token.NoPos && guardPos > stagePos {
		t.Fatal("refuseLiveWorkspaceLaunch sits BELOW pack staging — the refusal would " +
			"fire only after the work it exists to prevent had already run.")
	}
}
