package run

// macosusercredentialservice_test.go pins what the macos-user arm does when the machine's one
// OpenAI credential service (the openai-auth-broker host singleton, which every jail and session
// on the machine shares) exits in the instant between the launch's ensure finding it alive and
// the launch's own connect.
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
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Errorf("SIGTERM %s (pid %d): %v", name, pid, err)
		return 0
	}
	sock := paths.HostSingletonSocket(name)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return pid
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			t.Errorf("%s (pid %d) still accepts at %s 5s after SIGTERM", name, pid, sock)
			return pid
		}
	}
}

// A CREDENTIAL SERVICE THAT EXITS AFTER THE ENSURE IS STARTED AGAIN, NOT REFUSED. The launch's
// readiness probe is the first to see the daemon gone, so it is where the stop is made to
// happen: the daemon the ensure found is stopped as that probe begins. The launch must ensure
// once more, which starts a fresh daemon, and go on to run the command against it. Deleting the
// second ensure from startHostSingleton fails this with the refusal check-macos printed.
func TestTheMacosUserArmStartsTheOpenAIServiceAgainWhenItExitsAfterTheEnsure(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	doors := observeDoorways(t)
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
	if stopped == 0 {
		t.Fatal("the daemon was never stopped between the ensure and the probe, so this test " +
			"checked nothing")
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
// retried again: the launch refuses, before the command runs, the warning says yolo already
// started it again and names the daemon's log, and the refusal names the command to run next. Deleting the bound makes the launch probe until
// something else stops it.
func TestTheMacosUserArmEnsuresTheOpenAIServiceAtMostTwice(t *testing.T) {
	o, stderr, seen := codexNativeLaunch(t)
	observeDoorways(t)
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
	stdout := o.Stdout.(*bytes.Buffer).String()
	for _, want := range []string{"even after yolo started it again",
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
