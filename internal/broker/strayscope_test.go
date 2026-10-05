package broker

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

// strayProcessEnv makes this package's test binary a STRAY: a process that carries a
// host-wide daemon's argv and does nothing else until it is killed (TestMain).
const strayProcessEnv = "BROKER_TEST_STRAY_PROCESS"

// startStray runs this test binary under argv, argv[0] included (so a legacy console
// name can be spelled), and returns its PID and a channel closed once it has exited.
// The test's end kills it.
func startStray(t *testing.T, argv ...string) (int, <-chan struct{}) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{Path: exe, Args: argv, Env: append(os.Environ(), strayProcessEnv+"=1")}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd.Process.Pid, exited
}

// requirePgrep skips where the stray hunt cannot run: RealPgrepStrays treats a missing
// pgrep as "no strays", which would pass the negative half of these tests for nothing.
func requirePgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pgrep"); err != nil {
		t.Skip("needs pgrep, which the stray hunt shells out to")
	}
}

// TestStoppingOneSingletonSignalsNoOtherDaemon is the CI flake on 2026-10-05
// (TestHostScopedBrokerDaemonAnswersThroughTheFront: "front: dial upstream … connection
// refused"), reduced to its cause.
//
// A daemon whose PID file is gone is found by pgrep instead (BrokerKill), and the
// pattern every singleton got was the CLAUDE broker's spawn form, matched anywhere on
// the machine. So stopping `aws-auth` while its PID file was absent SIGTERMed every
// Claude broker on the host and reported "Stopped aws-auth." In CI it was another test
// package's cleanup (`BrokerKill(SingletonDeps("aws-auth", nil))`) stopping
// internal/cli/run's daemon, in its own private singleton directory, between the
// launch's readiness probe and the front's dial; on a developer's machine it is the
// real broker every jail refreshes through.
//
// The victim here is that daemon exactly: the Claude spawn form at a socket that is not
// this package's. Kill only records, so a regression signals nothing real.
func TestStoppingOneSingletonSignalsNoOtherDaemon(t *testing.T) {
	requirePgrep(t)
	elsewhere := filepath.Join(t.TempDir(), "yolo-"+BrokerLoopholeName+".sock")
	victim, _ := startStray(t, "/opt/yolo/bin/yolo", "internal", "daemon", BrokerLoopholeName, "--socket", elsewhere)

	for _, name := range []string{"aws-auth", BrokerLoopholeName} {
		deps := SingletonDeps(name, nil)
		deps.Out = nil
		if _, err := os.Stat(deps.PIDFilePath); err == nil {
			t.Fatalf("fixture: %s already has a PID file at %s, so the stray hunt would not run", name, deps.PIDFilePath)
		}
		var signalled []int
		deps.Kill = func(pid int, _ syscall.Signal) error {
			signalled = append(signalled, pid)
			return nil
		}
		stopped := BrokerKill(deps, syscall.SIGTERM, 0)
		if slices.Contains(signalled, victim) {
			t.Errorf("stopping %s signalled pid %d, a %s daemon serving %s — another "+
				"singleton's socket. Every client of that daemon is refused until something "+
				"respawns it.", name, victim, BrokerLoopholeName, elsewhere)
		}
		if len(signalled) > 0 || stopped {
			t.Errorf("stopping %s with nothing at its socket signalled %v and reported "+
				"stopped=%v; want nothing signalled and stopped=false", name, signalled, stopped)
		}
	}
}

// TestTheStrayHuntFindsTheDaemonAtThisSingletonsSocket is the other half: scoping the
// hunt to a singleton's socket must not stop it finding that singleton's own strays, in
// either spawn form the Claude broker has had (a second pgrep pattern used to exist for
// the retired standalone binary, yolo-claude-oauth-broker-host), and it now works for
// every singleton rather than only Claude's.
func TestTheStrayHuntFindsTheDaemonAtThisSingletonsSocket(t *testing.T) {
	requirePgrep(t)
	claude := SingletonDeps(BrokerLoopholeName, nil)
	aws := SingletonDeps("aws-auth", nil)
	aws.Out = nil
	current, _ := startStray(t, "/opt/yolo/bin/yolo", "internal", "daemon", BrokerLoopholeName,
		"--socket", claude.SocketPath)
	legacy, _ := startStray(t, "yolo-claude-oauth-broker-host", "--socket", claude.SocketPath)
	awsStray, awsExited := startStray(t, "/opt/yolo/bin/yolo", "internal", "daemon", "aws-auth",
		"--socket", aws.SocketPath, "--state-file", "/nonexistent/credentials.json")

	if got := claude.Pgrep(); !slices.Contains(got, current) || !slices.Contains(got, legacy) ||
		slices.Contains(got, awsStray) {
		t.Errorf("%s strays = %v, want the current form %d and the legacy form %d, and not "+
			"aws-auth's %d", BrokerLoopholeName, got, current, legacy, awsStray)
	}
	if got := aws.Pgrep(); !slices.Contains(got, awsStray) || slices.Contains(got, current) ||
		slices.Contains(got, legacy) {
		t.Errorf("aws-auth strays = %v, want %d alone of the three", got, awsStray)
	}

	// Through the verb's own path: `yolo host-daemon stop aws-auth` with its PID file lost.
	if !BrokerKill(aws, syscall.SIGTERM, 5*time.Second) {
		t.Fatal("BrokerKill(aws-auth) found nothing to stop, with a stray at its socket")
	}
	select {
	case <-awsExited:
	case <-time.After(5 * time.Second):
		t.Fatal("aws-auth's stray is still running after BrokerKill returned")
	}
	for _, pid := range []int{current, legacy} {
		if syscall.Kill(pid, 0) != nil {
			t.Errorf("stopping aws-auth also stopped %s's pid %d", BrokerLoopholeName, pid)
		}
	}
}
