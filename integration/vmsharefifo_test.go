package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoJailTestHandsAFIFOAcrossTheWorkspaceShare pins a class the macOS nightly hit
// (run 46031665455, TestAWSBrokerCostExplorerRequiresItsOwnRoot): a FIFO is a kernel
// object, not file bytes. On macOS the jail is in a VM and /workspace is a shared
// directory, so a FIFO the host test made there is a different pipe in the guest: the
// guest's open for write waits for a reader that only the host has, and neither side ever
// wakes. The same test passes on Linux, where both sides share one kernel. A test that
// needs the jail to signal the host prints a marker the test waits for in the session's
// output (awaitOutput), and releases the jail with a regular file the session polls.
//
// It runs under -short: it reads source only.
func TestNoJailTestHandsAFIFOAcrossTheWorkspaceShare(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		// scratchvolumes_test.go's FIFO is a host-only gate for a podman shim; its one
		// /workspace/ mention is unrelated text inside a jail script.
		if name == "vmsharefifo_test.go" || name == "scratchvolumes_test.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), "Mkfifo") && strings.Contains(string(src), "/workspace/") {
			t.Errorf("%s makes a FIFO and also names a /workspace/ path in a jail command; a FIFO "+
				"does not cross the macOS VM share. Signal readiness with awaitOutput on a printed "+
				"marker and release the jail with a regular file it polls (see sessioninputs_test.go).", name)
		}
	}
}
