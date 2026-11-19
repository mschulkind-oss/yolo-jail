package integration

import (
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// Direct exec keeps the original rule. For our two command-returning factories,
// a launch must execute the receiver created by that factory, not another command.
func launchesYoloUnderTest(fn *ast.FuncDecl) bool {
	direct, factory := false, false
	receivers := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if assignment, ok := n.(*ast.AssignStmt); ok {
			for _, lhs := range assignment.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					delete(receivers, id.Name)
				}
			}
			if len(assignment.Lhs) > 0 && len(assignment.Rhs) == 1 {
				call, ok := assignment.Rhs[0].(*ast.CallExpr)
				if ok && (callName(call.Fun) == "privateNativeCommandChecked" || callName(call.Fun) == "preparationCommand") && len(call.Args) > 1 {
					bin, ok := call.Args[1].(*ast.Ident)
					if ok && bin.Name == "yoloBin" {
						if receiver, ok := assignment.Lhs[0].(*ast.Ident); ok {
							receivers[receiver.Name] = true
						}
					}
				}
			}
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		prog := map[string]int{"exec.Command": 0, "exec.CommandContext": 1}
		if i, known := prog[callName(call.Fun)]; known && i < len(call.Args) {
			if id, ok := call.Args[i].(*ast.Ident); ok && id.Name == "yoloBin" {
				direct = true
			}
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			if receiver, ok := selector.X.(*ast.Ident); ok && receivers[receiver.Name] {
				switch selector.Sel.Name {
				case "Start", "Run", "Output", "CombinedOutput":
					factory = true
				}
			}
		}
		return true
	})
	return direct || factory
}

// Drive the actual whole-package enforcer in an isolated AST-only directory.
// No fixture command is compiled or executed, and child TestMain runs short.
func TestEveryLaunchSiteAwaitsDetachedWritersRecognizesNativeFactories(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := `package integration
func awaitDetachedWriters() {
 run.WaitForScratchRemovers()
 awaitHomeHostDaemons()
 stopHomeProcesses()
 stopHomeNixProcesses()
}
func one(){exec.Command(yoloBin); awaitDetachedWriters()}
func two(){exec.Command(yoloBin); awaitDetachedWriters()}
func three(){exec.CommandContext(ctx,yoloBin); awaitDetachedWriters()}
func four(){exec.CommandContext(ctx,yoloBin); awaitDetachedWriters()}
func five(){exec.Command(yoloBin); awaitDetachedWriters()}
`
	for _, tc := range []struct {
		name, body string
		fails      bool
	}{
		{"native-start-missing", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd.Start()`, true},
		{"native-run-missing", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd.Run()`, true},
		{"native-output-missing", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd.Output()`, true},
		{"native-combined-output-missing", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd.CombinedOutput()`, true},
		{"preparation-start-missing", `worker,err:=preparationCommand(ctx,yoloBin,home,true,args); worker.Start()`, true},
		{"native-awaited", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd.Start(); awaitDetachedWriters()`, false},
		{"warmup-both-halves", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd.Run(); run.WaitForScratchRemovers(); awaitHomeHostDaemons()`, false},
		{"factory-return-only", `return privateNativeCommandChecked(ctx,yoloBin,args,env,f)`, false},
		{"factory-with-unrelated-run", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); other.Run()`, false},
		{"factory-overwritten-before-run", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); cmd=other; cmd.Run()`, false},
		{"factory-wrong-argument", `cmd,err:=privateNativeCommandChecked(yoloBin,other,args,env,f); cmd.Run()`, false},
		{"build-output-not-exec", `exec.Command("go","build","-o",yoloBin)`, false},
		{"direct-missing-still-fails", `exec.Command(yoloBin)`, true},
		{"nested-start-still-counts", `cmd,err:=privateNativeCommandChecked(ctx,yoloBin,args,env,f); go func(){cmd.Start()}()`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := resolvedTempDir(t)
			if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(base+"\nfunc fixture(){"+tc.body+"}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(self, "-test.short", "-test.run=^TestEveryLaunchSiteAwaitsDetachedWriters$", "-test.timeout=2m")
			cmd.Dir, cmd.Env = dir, testtmp.ChildEnv(os.Environ())
			out, err := cmd.CombinedOutput()
			if tc.fails {
				if err == nil || !strings.Contains(string(out), "fixture.go:fixture") || !strings.Contains(string(out), "without waiting for its detached writers") {
					t.Fatalf("actual cleanup enforcer missed %s: %v\n%s", tc.name, err, out)
				}
			} else if err != nil {
				t.Fatalf("actual cleanup enforcer rejected %s: %v\n%s", tc.name, err, out)
			}
		})
	}
}
