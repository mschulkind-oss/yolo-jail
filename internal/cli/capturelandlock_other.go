//go:build !linux

package cli

import (
	"fmt"
	"io"
	"os"
	goruntime "runtime"
)

// capturelandlock_other.go is the host capture's non-Linux half (docs/design/host-tool-provisioning.md
// HP-D18): Landlock is Linux's, so chooseCaptureArm never picks the host capture here, and the
// confining verb refuses. A Mac's floor captures through the macos-user capture act instead (HP-D2).

// runLandlockExec refuses: there is no Landlock to confine with.
func runLandlockExec([]string) int {
	fmt.Fprintf(os.Stderr, "%s: Landlock is a Linux security module, and this machine is %s — nothing was run\n",
		landlockExecVerb, goruntime.GOOS)
	return 2
}

// runLandlockCapture is unreachable here (chooseCaptureArm), and says so if a caller gets it wrong.
func runLandlockCapture(_ string, target *captureTarget, _ int, _, errw io.Writer) int {
	fmt.Fprintf(errw, "yolo capture: the host capture of %s runs on Linux only, and this machine is %s\n",
		target.Bin, goruntime.GOOS)
	return 1
}
