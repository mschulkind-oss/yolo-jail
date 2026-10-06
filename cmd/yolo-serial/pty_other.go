//go:build !linux && !darwin

package main

import (
	"fmt"
	"os"
)

// openPty refuses on every platform but linux and darwin, the two this client runs on: a
// container jail (pty_linux.go) and the macos-user guest (pty_darwin.go). This half is the
// completeness arm of the constraint set, so the package still has an openPty under a GOOS
// nothing ships for.
//
// No lint pass selects it (lintedGOOS is linux and darwin), which is recorded as a decision
// in internal/capture/lintgate_pin_test.go's unanalyzedFiles rather than left as an oversight.
func openPty() (*os.File, string, error) {
	return nil, "", fmt.Errorf("virtual PTY bridge is only supported on linux and macOS")
}
