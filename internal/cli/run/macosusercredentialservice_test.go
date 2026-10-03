package run

// macosusercredentialservice_test.go pins what the macos-user arm does when the machine's one
// OpenAI credential service (the openai-auth-broker host singleton, which every jail and session
// on the machine shares) exits in the instant between the launch's ensure finding it alive and
// the launch's own connect, and what it does when a service it has just started does not accept.
//
// That instant is real for a user: a concurrent `yolo host-daemon restart openai-auth-broker`,
// another launch replacing the daemon, or the daemon leaving because its state directory was
// retired (hostservice.WatchStateDir) all stop it while a launch is between those two steps, and
// a slow or loaded Mac widens the gap. It is also how check-macos failed on bd9527733:
// reapTestSpawnedOpenAIBroker reads /proc, which darwin lacks, so each test there adopts the
// broker an earlier test spawned, and that broker exits within two seconds of the earlier test's
// temp HOME (its state dir) being deleted. A launch that adopted it in its last instant refused
// with "OpenAI credential service did not start" in 0.04 s.

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// stopHostSingletonNow SIGTERMs the host-wide daemon the PID file names and returns once its
// socket refuses a connect, so the caller observes a daemon that has stopped accepting. It
// returns the stopped PID, or 0 (after reporting why) when there was nothing to stop.
func stopHostSingletonNow(t *testing.T, name string) int {
	t.Helper()
	raw, err := os.ReadFile(paths.HostSingletonPIDFile(name))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 1 {
		t.Errorf("no live %s to stop (PID file: %q, %v): the premise is gone", name, raw, err)
		return 0
	}
	// ESRCH is the daemon having left on its own, which is the state this helper exists to
	// produce: the socket check below then finds it refusing at once.
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		t.Errorf("SIGTERM %s (pid %d): %v", name, pid, err)
		return 0
	}
	awaitHostSingletonRefusing(t, name, pid)
	return pid
}

// awaitHostSingletonRefusing returns once the host-wide daemon's socket refuses a connect,
// reporting a daemon still accepting five seconds on.
func awaitHostSingletonRefusing(t *testing.T, name string, pid int) {
	t.Helper()
	sock := paths.HostSingletonSocket(name)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Errorf("%s (pid %d) still accepts at %s 5s after SIGTERM", name, pid, sock)
			return
		}
	}
}

// stopAnyHostSingleton is stopHostSingletonNow for a test that must start from NO running
// daemon, where there being none to stop is fine. On darwin an earlier test's daemon is usually
// still running (reapTestSpawnedOpenAIBroker reads /proc), and a launch would reuse it.
func stopAnyHostSingleton(t *testing.T, name string) {
	t.Helper()
	raw, err := os.ReadFile(paths.HostSingletonPIDFile(name))
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err == nil && pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	awaitHostSingletonRefusing(t, name, pid)
}

// startOpenAIServiceToReuse runs o's launch once, ordinarily, from no credential service at
// all, so the service then running is one this test started under its own HOME, and the launch
// the test drives next finds it alive and REUSES it, as the launch check-macos failed did. It
// clears what that first launch left in the buffers, seen and doors, and returns the PID.
func startOpenAIServiceToReuse(t *testing.T, o *Options, stderr *bytes.Buffer, seen *nativeLaunch,
	doors *doorwaysSeen) int {
	t.Helper()
	stopAnyHostSingleton(t, openAIAuthBrokerName)
	if rc := Run(*o); rc != 0 || !seen.reached {
		t.Fatalf("the launch that starts the credential service for the next one to reuse: "+
			"Run() = %d (reached the command: %v)\n%s", rc, seen.reached, stderr.String())
	}
	deps := broker.SingletonDeps(openAIAuthBrokerName, nil)
	pid, ok := broker.BrokerReadPID(deps)
	if !ok || !broker.BrokerIsAlive(deps) {
		t.Fatalf("no live credential service after the first launch (pid %d, recorded: %v)", pid, ok)
	}
	seen.reached, seen.env = false, nil
	*doors = doorwaysSeen{}
	stderr.Reset()
	o.Stdout.(*bytes.Buffer).Reset()
	return pid
}

// A CREDENTIAL SERVICE THAT EXITS AFTER THE ENSURE IS STARTED AGAIN, NOT REFUSED. The launch's
// readiness probe is the first to see the daemon gone, so it is where the stop is made to
// happen: the live daemon the ensure reused is stopped as that probe begins. The launch must
// ensure once more, which starts a fresh daemon, and go on to run the command against it.
// Deleting the second ensure from startHostSingleton fails this with the refusal check-macos
// printed, and so does an ensure that reports every daemon as one it started.
func TestTheMacosUserArmStartsTheOpenAIServiceAgainWhenItExitsAfterTheEnsure(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	doors := observeDoorways(t)
	reused := startOpenAIServiceToReuse(t, o, stderr, seen, doors)
	sock := paths.HostSingletonSocket(openAIAuthBrokerName)
	probes, stopped := 0, 0
	orig := hostSingletonAccepting
	hostSingletonAccepting = func(path string, timeout time.Duration) bool {
		if path == sock {
			probes++
			if probes == 1 {
				stopped = stopHostSingletonNow(t, openAIAuthBrokerName)
			}
		}
		return orig(path, timeout)
	}
	t.Cleanup(func() { hostSingletonAccepting = orig })

	if rc := Run(*o); rc != 0 || !seen.reached {
		t.Fatalf("Run() = %d (reached the command: %v), want the launch to start the credential "+
			"service again and run the command\n%s", rc, seen.reached, stderr.String())
	}
	if stopped != reused {
		t.Fatalf("the probe stopped pid %d, not the daemon the ensure reused (%d), so this test "+
			"checked nothing", stopped, reused)
	}
	if probes != 2 {
		t.Errorf("the launch probed the credential service %d times, want 2: the probe that found "+
			"it gone, and one after ensuring it again", probes)
	}
	deps := broker.SingletonDeps(openAIAuthBrokerName, nil)
	if pid, ok := broker.BrokerReadPID(deps); !ok || pid == stopped || !broker.BrokerIsAlive(deps) {
		t.Errorf("after the launch the credential service is pid %d (recorded: %v, alive: %v), "+
			"want a live daemon other than the stopped pid %d", pid, ok, broker.BrokerIsAlive(deps), stopped)
	}
	if _, ok := seen.env.Get(hostServiceEnvVar(openAIAuthBrokerName)); !ok {
		t.Errorf("the command was not handed the credential service's endpoint (%s)",
			hostServiceEnvVar(openAIAuthBrokerName))
	}
	doors.only(t, "openai-auth-broker")
}

// AND ONLY ONCE. A credential service that still refuses after the second ensure is not
// retried again: the launch refuses, before the command runs, the warning says it was a second
// try and names the daemon's log, and the refusal names the command to run next. Deleting the
// bound makes the launch probe until something else stops it.
func TestTheMacosUserArmEnsuresTheOpenAIServiceAtMostTwice(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	doors := observeDoorways(t)
	startOpenAIServiceToReuse(t, o, stderr, seen, doors)
	sock := paths.HostSingletonSocket(openAIAuthBrokerName)
	probes := 0
	orig := hostSingletonAccepting
	hostSingletonAccepting = func(path string, timeout time.Duration) bool {
		if path == sock {
			probes++
			return false
		}
		return orig(path, timeout)
	}
	t.Cleanup(func() { hostSingletonAccepting = orig })

	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d (reached the command: %v), want the launch refused before the command "+
			"when the credential service never accepts\n%s", rc, seen.reached, stderr.String())
	}
	if probes != 2 {
		t.Errorf("the launch probed the credential service %d times, want 2", probes)
	}
	// Both ensures found the service alive, so the warning may not say yolo started it again.
	stdout := o.Stdout.(*bytes.Buffer).String()
	for _, want := range []string{"is still not accepting connections at " + sock + " on a second try",
		"See " + broker.SingletonLogPath(openAIAuthBrokerName)} {
		if !strings.Contains(stdout, want) {
			t.Errorf("the warning lacks %q:\n%s", want, stdout)
		}
	}
	// EVERY STOP NAMES ITS NEXT STEP: the refusal gives the one command that starts the
	// service (and names its log when it cannot), not only the fact of the refusal.
	for _, want := range []string{"OpenAI credential service did not start; refusing the macos-user launch.",
		"Start it with: " + broker.CycleCommand(openAIAuthBrokerName), "then launch again"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, stderr.String())
		}
	}
}

// A CREDENTIAL SERVICE THIS LAUNCH JUST STARTED IS NOT STARTED A SECOND TIME. When it does not
// accept, it either died at startup or has not bound yet, and a second ensure would start
// another copy beside one that may still bind, leaving one of the two running where nothing
// reaches it. So the launch refuses after the one probe, naming the log, and the daemon it
// started is still the one the PID file names. Ensuring again whatever the first ensure did, or
// an ensure that never reports starting a daemon, fails this.
func TestTheMacosUserArmDoesNotStartTheOpenAIServiceTwice(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	observeDoorways(t)
	stopAnyHostSingleton(t, openAIAuthBrokerName)
	sock := paths.HostSingletonSocket(openAIAuthBrokerName)
	probes := 0
	orig := hostSingletonAccepting
	hostSingletonAccepting = func(path string, timeout time.Duration) bool {
		if path == sock {
			probes++
			return false
		}
		return orig(path, timeout)
	}
	t.Cleanup(func() { hostSingletonAccepting = orig })

	if rc := Run(*o); rc != 1 || seen.reached {
		t.Fatalf("Run() = %d (reached the command: %v), want the launch refused before the command "+
			"when the credential service it started does not accept\n%s", rc, seen.reached, stderr.String())
	}
	if probes != 1 {
		t.Errorf("the launch probed the credential service %d times, want 1: a daemon this launch "+
			"just started is not ensured again", probes)
	}
	stdout := o.Stdout.(*bytes.Buffer).String()
	if want := "is not accepting connections at " + sock + " — "; !strings.Contains(stdout, want) ||
		strings.Contains(stdout, "second try") || strings.Contains(stdout, "in its place") {
		t.Errorf("the warning must say the daemon is not accepting (%q), and nothing about a second "+
			"try:\n%s", want, stdout)
	}
	deps := broker.SingletonDeps(openAIAuthBrokerName, nil)
	if pid, ok := broker.BrokerReadPID(deps); !ok || !broker.BrokerIsAlive(deps) {
		t.Errorf("the credential service this launch started is pid %d (recorded: %v, alive: %v), "+
			"want it still recorded and running", pid, ok, broker.BrokerIsAlive(deps))
	}
}
