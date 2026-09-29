package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestCheckGradesABrokerThatExitedAsNotRunning is the Mac nightly's TestYoloCheckValidConfig
// failure (run 36566584474, shard 10: "[FAIL] loophole claude-oauth-broker: stale PID file,
// pid 59331 not running"), reproduced on Linux with the fixture that run left behind: a PID
// file at the machine-wide singleton path naming a process that has exited, and no socket.
//
// WHY THAT STATE IS ORDINARY NOW. Since 192ea850 the broker EXITS when its state directory
// goes (oauthbrokercmd.go's hostservice.WatchStateDir call) — a launch retiring the
// loophole, a deleted HOME. The PID file is written by the SPAWNER (broker.EnsureSingleton),
// never by the daemon, so a designed, clean exit leaves it standing. The next launch that
// selects claude replaces it without ceremony (BrokerIsAlive reads the dead pid as "not
// alive" and spawns over the file), so the state is exactly "daemon not running" plus a file
// saying which process last ran: a WARN, with the same remedy, not a verdict that fails
// `yolo check` for every workspace on the machine until someone runs a launch.
//
// The nightly met it through a test-isolation gap as well: the integration suite's CLI
// children use the production /tmp singleton paths, so an earlier test's broker — whose
// HOME that test's cleanup deleted — left its PID file for TestYoloCheckValidConfig, the
// first test in the shard that runs `yolo check` after a claude launch. That is the ONLY
// way the fixture here differs from a user's machine after a state-dir exit.
//
// Driven through checkLoopholes, not reportBrokerDaemon: the row is reached through the
// loophole walk's `lp.Name == brokerLoopholeName` call site, and deleting that call site must
// turn this red too.
func TestCheckGradesABrokerThatExitedAsNotRunning(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	selfCheckModule(t, moduleRoot, brokerLoopholeName, []string{"/bin/sh", "-c", "echo OK"})

	pid := exitedPID(t)
	pidFile := paths.HostSingletonPIDFile(brokerLoopholeName)
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pidFile) })

	r, out := runCheckLoopholes(t, t.TempDir())

	if !strings.Contains(out, "loophole "+brokerLoopholeName+": self-check ok") {
		t.Fatalf("the broker's loophole row never reached the daemon-liveness block, so "+
			"nothing below is about the stale PID file:\n%s", out)
	}
	if r.failed != 0 {
		t.Errorf("a broker that exited and left its PID file FAILS `yolo check` (failed=%d). "+
			"The next launch replaces the file; this is \"daemon not running\", a warning:\n%s",
			r.failed, out)
	}
	if !strings.Contains(out, "daemon not running") {
		t.Errorf("the row does not say the daemon is not running:\n%s", out)
	}
	if !strings.Contains(out, "pid "+strconv.Itoa(pid)) {
		t.Errorf("the row no longer names the exited pid the PID file holds, which is what "+
			"tells a reader this daemon RAN and stopped rather than never started:\n%s", out)
	}
	// Report-only: `yolo check` never deletes the file. A concurrent launch may have just
	// spawned a daemon and rewritten it, and a check removing it then would orphan that
	// daemon (the next ensure would spawn a second one over its socket).
	if _, err := os.Stat(pidFile); err != nil {
		t.Errorf("`yolo check` removed the PID file (%v); it only reports", err)
	}
}

// exitedPID returns the pid of a process that has exited and been reaped.
func exitedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(pid, 0); err == nil {
		t.Skipf("pid %d was recycled before the check could read it", pid)
	}
	return pid
}
