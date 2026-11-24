//go:build linux

package entrypoint

// prelaunchheartbeat_linux_test.go pins that a finished refresh leaves no heartbeat sleep behind.
// Stopping the heartbeat killed its subshell and left that subshell's `sleep` running a full
// REFRESH_HEARTBEAT, which outlived the test run (the TMPDIR leak gate named two `sleep 60`s from
// CI run 45452376220). Linux-only: it reads /proc.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sleepsOf lists the pids of processes whose argv is exactly `sleep <secs>`.
func sleepsOf(t *testing.T, secs string) []string {
	t.Helper()
	cmdlines, err := filepath.Glob("/proc/[0-9]*/cmdline")
	if err != nil {
		t.Fatal(err)
	}
	var pids []string
	for _, c := range cmdlines {
		b, err := os.ReadFile(c)
		if err != nil {
			continue // the process ended
		}
		if string(b) == "sleep\x00"+secs+"\x00" {
			pids = append(pids, filepath.Base(filepath.Dir(c)))
		}
	}
	return pids
}

func TestAFinishedRefreshLeavesNoHeartbeatSleepRunning(t *testing.T) {
	// A beat interval no other test or process uses, so a match is this launcher's own sleep.
	const beat = "7311"
	for _, native := range []bool{false, true} {
		p := newPrelaunchProbe(t, native)
		p.heartbeat = beat
		if _, _, err := p.runStatus(t, ""); err != nil {
			t.Fatalf("launcher failed: %v", err)
		}
		if left := sleepsOf(t, beat); len(left) != 0 {
			t.Errorf("native=%v: the stopped heartbeat left `sleep %s` running (pids %s)", native, beat, strings.Join(left, ","))
		}
	}
}
