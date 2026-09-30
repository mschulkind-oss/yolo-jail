package check

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/internaldaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testsupport"
)

// TestMain gives this package's host singletons a private directory instead of the
// machine-wide /tmp/yolo-<name>.* and stops the ones its tests started
// (testsupport.IsolateHostSingletons says why).
func TestMain(m *testing.M) {
	// A SELF-EXEC'D DAEMON RUNS THE DAEMON, never this package's suite: `yolo check` runs each
	// loophole's doctor_cmd, whose leading "yolo" execx.SelfExecArgv turns into this test binary.
	// Without this, the child re-runs every test here and its own self-checks outlive the run
	// (internal/cli's TestLoopholesStatusSelfChecksLeaveNothingRunning measured that shape).
	if internaldaemon.IsDaemonArgv(os.Args) {
		os.Exit(internaldaemon.Run(os.Args[3:]))
	}
	// `<test-binary> -settings-sleeper-child <socket> <settings>` is a fake host-wide daemon
	// for singletonsettings_test.go: it binds its socket and accepts until killed. The
	// settings path is in its argv only so the spawn records what it was handed.
	if len(os.Args) >= 4 && os.Args[1] == "-settings-sleeper-child" {
		os.Exit(settingsSleeperChildMain(os.Args[2]))
	}
	// THE PODMAN READINESS GATE REFUSES BY DEFAULT HERE (podmanready.go): its real attempt
	// runner starts `podman info` on whatever PATH the test process has. A test that reaches
	// the gate says what podman answers (Options.PodmanReadiness, answeringPodman).
	defaultPodmanAttempt = refusingPodmanAttempt
	// A fixture's git reads no machine configuration (testsupport.HermeticGitEnv), and the
	// tripwire makes one that does fail here and on CI, not only on a machine that signs.
	testsupport.ArmGitConfigTripwire()
	release := testsupport.IsolateHostSingletons()
	code := m.Run()
	release()
	os.Exit(code)
}

// TestGitConfigTripwireIsArmedHere pins TestMain's testsupport.ArmGitConfigTripwire call:
// without it a fixture that reads the machine's git configuration passes again on every
// machine that does not sign its commits.
func TestGitConfigTripwireIsArmedHere(t *testing.T) {
	if !testsupport.GitConfigTripwireArmed() {
		t.Fatal("the git configuration tripwire is not armed; this package's TestMain must call testsupport.ArmGitConfigTripwire")
	}
}

// TestHostSingletonsAreIsolatedHere pins the TestMain call above: without it this
// package's tests spawn singletons on the machine-wide /tmp/yolo-<name>.* again.
func TestHostSingletonsAreIsolatedHere(t *testing.T) {
	if paths.HostSingletonDir == paths.DefaultHostSingletonDir {
		t.Fatalf("paths.HostSingletonDir is the machine-wide %q; this package's TestMain must call testsupport.IsolateHostSingletons", paths.HostSingletonDir)
	}
}

// TestASelfExecdDaemonRunsTheDaemonHere pins the dispatch above: this binary, run the way a
// doctor_cmd runs it, must answer as the daemon's self-check, not run this package's suite.
func TestASelfExecdDaemonRunsTheDaemonHere(t *testing.T) {
	if os.Getenv("YOLO_TEST_SELFEXEC_CHILD") != "" {
		t.Skip("the child of this test; without the dispatch it would recurse")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	cmd := exec.Command(self, "internal", "daemon", "claude-oauth-broker", "--self-check", "-test.run=^$")
	cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"), "YOLO_TEST_SELFEXEC_CHILD=1")
	done := make(chan struct{})
	var out []byte
	go func() { out, _ = cmd.CombinedOutput(); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the self-exec'd daemon did not return within 30s")
	}
	if strings.Contains(string(out), "=== RUN") || strings.Contains(string(out), "\nPASS\n") || strings.HasPrefix(string(out), "PASS") {
		t.Fatalf("the self-exec'd daemon ran this package's tests instead of the daemon:\n%s", out)
	}
}
