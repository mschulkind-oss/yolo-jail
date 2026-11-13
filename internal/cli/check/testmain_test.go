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
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
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
	// THE PODMAN READINESS GATE REFUSES BY DEFAULT HERE (podmanready.go): its real attempt
	// runner starts `podman info` on whatever PATH the test process has. A test that reaches
	// the gate says what podman answers (Options.PodmanReadiness, answeringPodman).
	defaultPodmanAttempt = refusingPodmanAttempt
	// A fixture's git reads no machine configuration (testsupport.HermeticGitEnv), and the
	// tripwire makes one that does fail here and on CI, not only on a machine that signs.
	testsupport.ArmGitConfigTripwire()
	// A CHILD OF THIS BINARY LEAVES NOTHING BEHIND (testtmp.Isolate): a helper re-exec runs this
	// TestMain again and exits, execs away or is killed before its own removals run, so its
	// directories and t.TempDirs go in a private TMPDIR this process removes.
	releaseTmp := testtmp.Isolate()
	release := testsupport.IsolateHostSingletons()
	// AN EMPTY HOME OF ITS OWN: Check() reads the user-scope config and global storage under
	// $HOME, so without this a developer's own config decided what these tests saw. On a Mac
	// on 2026-10-23 a refused key in the maintainer's config ended the merged section early and
	// TestCheckReportsAnOptionalMountItWillSkipAsInformation failed; CI's empty home hid it.
	// A re-exec'd child of a test keeps the HOME its parent chose for it.
	home := ""
	if os.Getenv(signalCleanupChildEnv) != "1" {
		var err error
		if home, err = os.MkdirTemp("", "yolo-check-home-"); err != nil {
			panic("isolate HOME: " + err.Error())
		}
		_ = os.Setenv("HOME", home)
		_ = os.Unsetenv("XDG_CONFIG_HOME")
	}
	code := m.Run()
	release()
	if home != "" {
		_ = os.RemoveAll(home)
	}
	os.Exit(releaseTmp(code))
}

// TestHomeIsIsolatedHere pins TestMain's HOME: no test here reads the developer's own config.
func TestHomeIsIsolatedHere(t *testing.T) {
	home, _ := os.UserHomeDir()
	if !strings.HasPrefix(filepath.Base(home), "yolo-check-home-") {
		t.Fatalf("HOME is %q, not this package's own; TestMain must isolate it", home)
	}
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

// TestThisPackageIsolatesTMPDIR fails if TestMain stops calling testtmp.Isolate, which would
// put every helper child's leftovers back in the machine's TMPDIR.
func TestThisPackageIsolatesTMPDIR(t *testing.T) { testtmp.RequireIsolated(t) }
