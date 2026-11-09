package run

import (
	"bytes"
	"errors"
	"go/ast"
	"go/token"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ttyproxy"
)

func TestSessionRunOutputErrorNamesTheTerminalCauseAndNextStep(t *testing.T) {
	cause := errors.New("no space left on device")
	output := bytes.Buffer{}
	o := &Options{}
	reportSessionRunError(o.pr(&output), "podman", &ttyproxy.OutputError{Err: cause})
	got := output.String()
	for _, want := range []string{
		"Could not forward terminal output: no space left on device",
		"Retry from a working terminal in this workspace.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output-error report %q does not contain %q", got, want)
		}
	}
	for _, wrong := range []string{"not found on PATH", "yolo check"} {
		if strings.Contains(got, wrong) {
			t.Errorf("output-error report misdiagnoses the failure with %q: %s", wrong, got)
		}
	}

	output.Reset()
	reportSessionRunError(o.pr(&output), "podman", &exec.Error{Name: "podman", Err: exec.ErrNotFound})
	got = output.String()
	for _, want := range []string{
		"Configured runtime 'podman' not found on PATH.",
		"Run `yolo check` to validate runtime availability before restarting.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("ordinary runtime-error control %q does not contain %q", got, want)
		}
	}
	if strings.Contains(got, "working terminal") {
		t.Errorf("ordinary runtime-error control took the terminal-output branch: %s", got)
	}

	// Any other start failure is reported as itself: the runtime may well be installed.
	output.Reset()
	reportSessionRunError(o.pr(&output), "podman", errors.New("fork/exec /usr/bin/podman: permission denied"))
	got = output.String()
	if strings.Contains(got, "not found on PATH") || !strings.Contains(got, "permission denied") {
		t.Errorf("a non-missing start failure is misreported: %s", got)
	}
}

// The keeper's start failure goes through the same report, so it never calls an installed runtime
// missing either.
func TestKeeperStartFailureUsesTheRuntimeStartReport(t *testing.T) {
	decl := funcDecl(t, "keeper.go", "run")
	var found bool
	ast.Inspect(decl.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "reportRuntimeStartError" {
				found = true
			}
		}
		return true
	})
	if !found {
		t.Error("keeper.go's run no longer reports a start failure through reportRuntimeStartError")
	}
}

// TestFreshAndAttachSessionErrorCallsitesArePinned keeps both real error handlers wired to
// the typed classifier; the behavioral test above pins its message and ordinary-error control.
func TestFreshAndAttachSessionErrorCallsitesArePinned(t *testing.T) {
	for _, name := range []string{"runContainer", "attachExisting"} {
		t.Run(name, func(t *testing.T) {
			decl := funcDecl(t, "run.go", name)
			var runCall, reportCall token.Pos
			ast.Inspect(decl.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				switch ident.Name {
				case "runArmedSession":
					runCall = call.Pos()
				case "reportSessionRunError":
					reportCall = call.Pos()
				}
				return true
			})
			if runCall == token.NoPos || reportCall == token.NoPos || reportCall < runCall {
				t.Fatalf("%s must report runArmedSession errors through reportSessionRunError after the session call", name)
			}
		})
	}
}
