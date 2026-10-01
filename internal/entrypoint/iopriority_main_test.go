package entrypoint

import (
	"os"
	"testing"
	"time"
)

// TestMain lets the test binary play the entrypoint for
// TestIOPriorityReachesEveryThreadAfterOneReexec: the apply re-executes the process, which
// only a subprocess can survive. Without the mode variable it runs the tests, searching an
// empty stand-in for the image's bin dirs rather than the machine's (imagebins_test.go).
func TestMain(m *testing.M) {
	switch os.Getenv(ioTestModeEnv) {
	case "child":
		runIOPriorityChild()
		os.Exit(0)
	case "sleep":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	os.Exit(runWithStandInImageBins(m))
}
