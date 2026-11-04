//go:build linux

package cli

import (
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execmonitor"
)

func TestRunStopNormalInternalRmForceNeverSignalsExecMonitor(t *testing.T) {
	_, _ = stopWorkspace(t, "podman-self")
	shrinkStopGone(t)
	proc, _ := writeStopMonitorProcFixture(t)
	savedOps := stopMonitorProcessOps
	stopMonitorProcessOps = execmonitor.Ops{ProcRoot: proc, UIDSet: true, SelfPID: 99}
	t.Cleanup(func() { stopMonitorProcessOps = savedOps })
	runningQueries := 0
	var rmArgv []string
	infoCalled := false
	runner := func(argv []string, _ time.Duration) (string, bool, int) {
		switch {
		case len(argv) > 1 && argv[1] == "inspect" && slices.Contains(argv, "{{.State.Running}}"):
			runningQueries++
			if runningQueries == 1 {
				return "true\n", true, 0
			}
			return "false\n", true, 0
		case len(argv) > 1 && argv[1] == "stop":
			return "", true, 0
		case len(argv) > 2 && argv[1] == "container" && argv[2] == "exists":
			return "", true, 0
		case len(argv) > 1 && argv[1] == "rm":
			rmArgv = slices.Clone(argv)
			return "", true, 125
		case len(argv) > 1 && argv[1] == "info":
			infoCalled = true
			return "{}", true, 0
		default:
			t.Fatalf("unexpected runtime command: %v", argv)
			return "", false, 1
		}
	}
	useStopExec(t, runner)
	var rc int
	_, _ = captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc == 0 {
		t.Fatal("normal stop reported success while the container remained")
	}
	if !slices.Contains(rmArgv, "--force") {
		t.Fatalf("normal stop's internal force-remove was not exercised: %v", rmArgv)
	}
	if infoCalled {
		t.Fatal("internal rm --force granted host exec-monitor recovery without explicit user --force")
	}
}
