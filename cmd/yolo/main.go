// Command yolo is the Go front door (go-port plan Stage 12, seam #1). Built to
// dist-go/ and invoked via $YOLO_GO_BIN_DIR/yolo during the transition (nothing
// on host PATH until Stage 17). It handles ported subcommands natively and
// execs `python -m src.cli` (with YOLO_GO_DELEGATED=1, the loop breaker) for
// the rest.
//
// Delegation is decided BEFORE terminal-indicator setup, and the Go front door
// must NOT touch indicators when delegating (else Python saves the
// already-branded state as its restore target and the terminal stays branded
// after exit).
package main

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/frontdoor"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	// Chdir back to the user's real invocation dir (the jail shim chdir'd to
	// the repo root so `python -m src.cli` resolves). Downstream sees the real
	// cwd for workspace/yolo-jail.jsonc resolution.
	if cwd := frontdoor.InvocationCWD(); cwd != "" {
		_ = os.Chdir(cwd)
	}

	args := frontdoor.RewriteArgv(argv)
	sub := frontdoor.Subcommand(args)

	if frontdoor.IsNative(sub) {
		return dispatchNative(sub, args)
	}
	// Delegate to Python. Indicators are Python's job here — do NOT set them.
	return delegateToPython(args)
}

// delegateToPython execs `python -m src.cli <args>` with YOLO_GO_DELEGATED=1
// set (breaks the front-door exec loop) and YOLO_INVOCATION_CWD restored so the
// Python prelude chdirs correctly. Mirrors seam #1's delegation path.
func delegateToPython(args []string) int {
	// The Python side is launched exactly as the jail's yoloCli wrapper does:
	// via uv from the mounted repo. During the transition we shell out to the
	// same `yolo`-equivalent by running the module. The wrapper/env resolution
	// is provided by YOLO_PYTHON (set by the shim) or falls back to python3.
	py := os.Getenv("YOLO_PYTHON")
	if py == "" {
		py = "python3"
	}
	cmdArgs := append([]string{"-m", "src.cli"}, args...)
	cmd := exec.Command(py, cmdArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "YOLO_GO_DELEGATED=1")
	// Restore YOLO_INVOCATION_CWD so Python's prelude chdirs to the real dir
	// (we popped it from our own env; hand it to the child explicitly).
	if cwd, _ := os.Getwd(); cwd != "" {
		cmd.Env = append(cmd.Env, "YOLO_INVOCATION_CWD="+cwd)
	}
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
				return ws.ExitStatus()
			}
			return 1
		}
		return 1
	}
	return 0
}

// dispatchNative handles the subcommands the Go binary owns. Native slices are
// registered in native.go; today the map is empty (behavior unchanged) until
// each slice is byte-goldened, so this is unreachable — kept for the wiring.
func dispatchNative(sub string, args []string) int {
	if fn, ok := nativeDispatch[sub]; ok {
		return fn(args)
	}
	// Not actually native — fall back to delegation (defensive).
	return delegateToPython(args)
}
