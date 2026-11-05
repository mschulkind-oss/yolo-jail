package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// launchcheck_test.go pins the launch check (launchcheck.go) THROUGH THE PRODUCTION CALL
// SITE: every test here drives startLoopholesDisclosed, the one boundary both launch arms
// cross, so deleting its runLaunchChecks call fails them. The daemon behind the front is the
// REAL aws-auth handler (awsauthdaemon.BuildHandler) over a broker whose `aws` is a canned
// Runner, served at the host-wide singleton path the way a running daemon serves it, so the
// words asserted are the classifier's and the transport is the real front. Nothing runs `aws`
// and nothing reaches AWS.
//
// The loophole is a fixture named for this file, not the shipped aws-auth, because a real
// name would put this test's daemon where the developer's own could be (paths.HostSingletonDir
// is per-package-run, but the name is still a reader's hazard) and because the check is keyed
// on the declaration, which is what the fixture declares. TestShippedAWSAuthFields
// (internal/loopholedecl) pins that the shipped aws-auth manifest declares it.

const launchCheckFixtureName = "yjtest-launchcheck"

// launchCheckPack is a pack shipping one host-wide loophole that declares the launch check
// and a jail daemon, the shape of aws-auth's manifest. A launch spawns its host argv as the
// jail's own daemon (HD-R1), a relay to the handler serveLaunchCheckDaemon serves
// (spliceChildMain), so the words asserted are still the real handler's.
func launchCheckPack(t *testing.T) *packload.Pack {
	t.Helper()
	cmd, err := json.Marshal([]string{os.Args[0], "-splice-child", "{socket}", launchCheckFixtureSocket()})
	if err != nil {
		t.Fatal(err)
	}
	return writeRealLoopholePack(t, "yjtest-launchcheck-pack", launchCheckFixtureName, `{
		"name": "`+launchCheckFixtureName+`",
		"description": "a host service that answers the launch check",
		"transport": "loopback-tls",
		"lifecycle": "spawned",
		"host_daemon": {"cmd": `+string(cmd)+`, "publishes": "socket",
			"launch_check": true, "detach": true},
		"jail_daemon": {"cmd": ["yolo-jaild", "fixture-adapter"]}
	}`)
}

// servedPayload is a jail-daemon payload that serves the fixture loophole's jail daemon, as
// jailDaemonsFor composes one when some agent's profile selects what it serves.
func servedPayload() []loopholes.JailDaemonSpec {
	return []loopholes.JailDaemonSpec{{Name: launchCheckFixtureName,
		Cmd: []string{"yolo-jaild", "fixture-adapter"}, Restart: "on-failure"}}
}

// launchCheckFixtureSocket is where serveLaunchCheckDaemon serves the handler each launch's own
// copy relays to: in the package's private singleton dir, and deliberately NOT at the name-keyed
// rendezvous an older yolo's machine-wide copy used (paths.HostSingletonSocket), so a fixture is
// never mistaken for one (olderHostWideAlive). leaveAnOlderCopyRunning stands one up there.
func launchCheckFixtureSocket() string {
	return filepath.Join(paths.HostSingletonDir, "yj-lc-fixture.sock")
}

// serveLaunchCheckDaemon stands the handler up behind hostservice.ServeFrontedUnix at
// launchCheckFixtureSocket, where the jail's own copy (spliceChildMain) relays every connection.
func serveLaunchCheckDaemon(t *testing.T, handler hostservice.Handler) {
	t.Helper()
	serveFrontedFixture(t, handler, launchCheckFixtureSocket())
}

// serveFrontedFixture serves handler behind hostservice.ServeFrontedUnix at sock until the test
// ends, and returns once it accepts.
func serveFrontedFixture(t *testing.T, handler hostservice.Handler, sock string) {
	t.Helper()
	assertSockPathFits(t, sock)
	_ = os.Remove(sock)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeFrontedUnix(handler, sock, stop)
	}()
	t.Cleanup(func() {
		close(stop)
		<-done
		_ = os.Remove(sock)
	})
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(sock) {
		if time.Now().After(deadline) {
			t.Fatal("the fixture daemon never bound its socket")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestServeFrontedFixtureKeepsGlobalLoggerWithTwoLiveServers pins that fixtures do not
// mutate hostservice's package-global logger while serving concurrently.
func TestServeFrontedFixtureKeepsGlobalLoggerWithTwoLiveServers(t *testing.T) {
	logger := hostservice.Logger
	dir := t.TempDir()
	serveFrontedFixture(t, func(*hostservice.Session) {}, filepath.Join(dir, "first.sock"))
	serveFrontedFixture(t, func(*hostservice.Session) {}, filepath.Join(dir, "second.sock"))
	if hostservice.Logger != logger {
		t.Fatal("serving fixtures changed hostservice.Logger")
	}
}

// leaveAnOlderCopyRunning stands up what an older yolo leaves behind for the fixture loophole
// when it is upgraded with its machine-wide daemon still running: a socket that accepts at the
// name-keyed rendezvous (paths.HostSingletonSocket), and a PID file naming a live process running
// under this test's HOME, as the older copy of this home's would. handler is what that copy
// answers.
func leaveAnOlderCopyRunning(t *testing.T, handler hostservice.Handler) {
	t.Helper()
	home := os.Getenv("HOME")
	fixturePID := leaveAnOlderCopyRunningUnder(t, handler, home)
	if !olderHostWideAlive(launchCheckFixtureName) {
		t.Fatalf("the fixture does not read as an older yolo's machine-wide copy; %s",
			olderCopyFixtureDiagnostic(launchCheckFixtureName, home, fixturePID))
	}
}

func olderCopyFixtureDiagnostic(name, expectedHome string, fixturePID int) string {
	deps := broker.LegacyDeps(name)
	status := broker.BrokerStatus(deps)
	observedHome := "<pid-unavailable>"
	if status.PIDPresent && status.PID == fixturePID {
		observedHome = fixtureProcessHome(fixturePID)
	} else if status.PIDPresent {
		observedHome = fmt.Sprintf("<fixture-pid-mismatch: expected %d, observed %d>", fixturePID, status.PID)
	}
	return fmt.Sprintf("fixture predicate: pid_present=%t pid_live=%t socket_present=%t socket_accepting=%t expected_fixture_HOME=%q observed_fixture_HOME=%q socket_path=%q socket_metadata={%s} pid_file_path=%q pid_file_metadata={%s}",
		status.PIDPresent, status.PIDLive, status.SocketExists, status.Reachable,
		expectedHome, observedHome, deps.SocketPath, fixturePathMetadata(deps.SocketPath),
		deps.PIDFilePath, fixturePathMetadata(deps.PIDFilePath))
}

// fixtureProcessHome extracts only HOME from the test-owned process; no other environment value is reported.
func fixtureProcessHome(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return "<unreadable>"
	}
	for _, entry := range strings.Split(string(raw), "\x00") {
		if home, ok := strings.CutPrefix(entry, "HOME="); ok {
			return home
		}
	}
	return "<missing>"
}

func fixturePathMetadata(path string) string {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Sprintf("unavailable: %v", err)
	}
	return fmt.Sprintf("mode=%s size=%d", info.Mode(), info.Size())
}

// leaveAnOlderCopyRunningUnder is leaveAnOlderCopyRunning with the copy's process under home.
func leaveAnOlderCopyRunningUnder(t *testing.T, handler hostservice.Handler, home string) int {
	t.Helper()
	serveFrontedFixture(t, handler, paths.HostSingletonSocket(launchCheckFixtureName))
	proc := exec.Command("sleep", "120")
	proc.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	// Start returns before the kernel has laid out the new program's environment, so wait until
	// it names the HOME the copy runs under, as a long-running copy's always does.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", proc.Process.Pid)); err == nil &&
			slices.Contains(strings.Split(string(raw), "\x00"), "HOME="+home) {
			break
		}
		if time.Now().After(deadline) {
			t.Skipf("the fixture's environment cannot be read here (/proc/%d/environ)", proc.Process.Pid)
		}
	}
	pidFile := paths.HostSingletonPIDFile(launchCheckFixtureName)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(proc.Process.Pid)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(pidFile) })
	return proc.Process.Pid
}

// olderThanTheCheck answers as a daemon a yolo older than the launch check does: its handler's
// default case, `unknown action`, exit 2.
func olderThanTheCheck(s *hostservice.Session) {
	s.Stderr("unknown action: launch-check\n")
	s.Exit(2)
}

// answersTheCheck answers the launch check with one note and nothing to warn about (controlNote).
func answersTheCheck(s *hostservice.Session) {
	_ = s.AnswerLaunchCheck(hostservice.LaunchCheckReport{Notes: []string{"checked by the control"}})
}

// awsHandler is the real aws-auth request handler over a broker whose `aws` answers out.
func awsHandler(t *testing.T, out awsauth.Output, calls *atomic.Int32) hostservice.Handler {
	t.Helper()
	dir := t.TempDir()
	return awsauthdaemon.BuildHandler(awsauthdaemon.HandlerConfig{Broker: awsauth.Broker{
		StatePath: filepath.Join(dir, awsauth.StateFileName),
		LockPath:  filepath.Join(dir, awsauth.LockFileName),
		Config: awsauth.Config{Profile: "bedrock",
			Narrowing: awsauth.Narrowing{Kind: awsauth.NarrowNone}},
		Minter: awsauth.Minter{Binary: "aws", Run: func(context.Context, []string) awsauth.Output {
			if calls != nil {
				calls.Add(1)
			}
			return out
		}},
	}})
}

// launchWithCheckedService runs the production boundary for rt against the fixture loophole
// and returns what the launch printed on its stderr. A refusal fails the test.
func launchWithCheckedService(t *testing.T, rt string, payload []loopholes.JailDaemonSpec) string {
	t.Helper()
	got, refused := launchCheckedService(t, rt, payload)
	if refused != nil {
		t.Fatalf("the launch was refused: %s\n%s", refused.text(), got)
	}
	return got
}

// launchCheckedService is launchWithCheckedService with the refusal the boundary returned.
func launchCheckedService(t *testing.T, rt string, payload []loopholes.JailDaemonSpec) (string, *servicesRefusal) {
	t.Helper()
	pack := launchCheckPack(t)
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{pack}))

	cname := "yolo-launchcheck-" + strings.ReplaceAll(t.Name(), "/", "-")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var errBuf bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr = &errBuf
	o.Stdout = discardBuf()
	o.PathExists = func(string) bool { return false } // no cgroup delegate
	if rt == "macos-user" {
		t.Cleanup(func() { o.endServicesSession(nil) })
	}
	handles, refused := o.startLoopholesDisclosed(cname, rt, newConfig(), []*packload.Pack{pack}, payload)
	for _, h := range handles {
		if h.stop != nil {
			h.stop()
		}
	}
	if !startedLoophole(handles, launchCheckFixtureName) {
		t.Fatalf("the fixture's host service did not start, so nothing could be asked:\n%s",
			errBuf.String())
	}
	return errBuf.String(), refused
}

func launchCheckIsolation(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("binds host-wide singleton paths")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
}

// generousLaunchCheckBudget raises the launch check's budget to the daemon's cap
// (hostservice.LaunchCheckBudgetCap) for the rest of the test. A test asserting what the daemon
// ANSWERS after a mint uses it, so the answer is the mint's and not the timer's: even a canned
// mint takes the state lock and fsyncs the state file, which under a loaded `just check-ci` once
// outlasted the product's two seconds and turned a healthy mint into the "had not finished" note.
func generousLaunchCheckBudget(t *testing.T) {
	t.Helper()
	saved := launchCheckBudget
	launchCheckBudget = hostservice.LaunchCheckBudgetCap
	t.Cleanup(func() { launchCheckBudget = saved })
}

func failedAWS(stderr string) awsauth.Output {
	return awsauth.Output{Spawned: true, Code: 255, Stderr: stderr}
}

// TestTheLaunchWarnsForEachWayTheAWSSessionCannotMint is design §8's "profile configured, no SSO
// session ever established → the launch warns with the `aws sso login` command and proceeds",
// its lapsed-session twin, and the missing profile that failed every pre-mint from 2026-09-23 to
// 2026-09-28 with only the service log saying so (§11): each prints one `loophole <name>:`
// warning naming the problem and its fix, on both backends whose launch starts the service. The
// launch goes on: startLoopholesDisclosed returned and the service is up.
func TestTheLaunchWarnsForEachWayTheAWSSessionCannotMint(t *testing.T) {
	for _, rt := range []string{"podman", "macos-user"} {
		for _, tc := range []struct {
			name string
			out  awsauth.Output
			want []string
		}{
			{"no session ever established",
				failedAWS("Error loading SSO Token: Token for https://x/start does not exist"),
				[]string{"was never established", "on the HOST run: aws sso login --profile bedrock"}},
			{"session expired",
				failedAWS("Error when retrieving token from sso: Token has expired and refresh failed"),
				[]string{"has expired", "on the HOST run: aws sso login --profile bedrock"}},
			{"profile not in the host's AWS config",
				failedAWS("The config profile (bedrock) could not be found"),
				[]string{"is not in the host's ~/.aws/config",
					"aws configure sso --profile bedrock", "loopholes.aws-auth.settings.profile"}},
			{"refused by STS",
				failedAWS("An error occurred (AccessDenied) when calling the AssumeRole operation"),
				[]string{"AccessDenied"}},
			{"unavailable",
				awsauth.Output{Spawned: true, Code: 1, Stderr: "Could not connect to the endpoint URL"},
				[]string{"the `aws` CLI exited 1", "Could not connect"}},
		} {
			t.Run(rt+"/"+tc.name, func(t *testing.T) {
				launchCheckIsolation(t)
				generousLaunchCheckBudget(t)
				serveLaunchCheckDaemon(t, awsHandler(t, tc.out, nil))
				got := launchWithCheckedService(t, rt, servedPayload())
				prefix := "loophole " + launchCheckFixtureName +
					": cannot mint a Bedrock credential for this launch: "
				var line string
				for _, l := range strings.Split(got, "\n") {
					if strings.Contains(l, prefix) {
						if line != "" {
							t.Fatalf("the launch printed the warning twice:\n%s", got)
						}
						line = l
					}
				}
				if line == "" {
					t.Fatalf("the launch printed no launch-check warning:\n%s", got)
				}
				for _, want := range append(tc.want, "The launch continues") {
					if !strings.Contains(line, want) {
						t.Errorf("the warning lacks %q:\n%s", want, line)
					}
				}
			})
		}
	}
}

// TestAHealthyMintPrintsNoLaunchCheckLine is the negative every warning above needs: a session
// that mints says nothing at all, on either backend.
func TestAHealthyMintPrintsNoLaunchCheckLine(t *testing.T) {
	for _, rt := range []string{"podman", "macos-user"} {
		t.Run(rt, func(t *testing.T) {
			launchCheckIsolation(t)
			generousLaunchCheckBudget(t)
			var calls atomic.Int32
			ok := awsauth.Output{Spawned: true, Stdout: `{"Version":1,"AccessKeyId":"ASIA1",` +
				`"SecretAccessKey":"s","SessionToken":"t","Expiration":"` +
				time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`}
			serveLaunchCheckDaemon(t, awsHandler(t, ok, &calls))
			got := launchWithCheckedService(t, rt, servedPayload())
			if strings.Contains(got, "loophole "+launchCheckFixtureName+":") {
				t.Errorf("a healthy mint printed a launch-check line:\n%s", got)
			}
			// It WAS asked: the cold cache minted once, for the check. Without this the
			// silence above would pass with the call site deleted.
			if calls.Load() != 1 {
				t.Errorf("`aws` ran %d times, want 1: the launch must have asked the daemon", calls.Load())
			}
		})
	}
}

// TestALaunchWhoseAgentsDoNotReachTheServiceAsksNothing: aws-auth's adapter is served only when
// some agent's provider is on Bedrock (OQ-CN7 (b)), and a launch with no such agent is not
// warned about a session none of its agents would use, nor made to wait for a mint.
func TestALaunchWhoseAgentsDoNotReachTheServiceAsksNothing(t *testing.T) {
	launchCheckIsolation(t)
	var calls atomic.Int32
	serveLaunchCheckDaemon(t, awsHandler(t,
		failedAWS("Error when retrieving token from sso: Token has expired and refresh failed"), &calls))
	got := launchWithCheckedService(t, "podman", nil)
	if strings.Contains(got, "loophole "+launchCheckFixtureName+":") {
		t.Errorf("a launch that serves no Bedrock agent printed a launch-check line:\n%s", got)
	}
	if calls.Load() != 0 {
		t.Errorf("`aws` ran %d times for a launch that asked nothing", calls.Load())
	}
}

// TestADaemonThatCannotAnswerIsSaidSo: a daemon that fails the check exits non-zero. The
// launch still proceeds, and says it could not ask, so the absence of a warning is never read
// as a clean bill.
func TestADaemonThatCannotAnswerIsSaidSo(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, func(s *hostservice.Session) {
		s.Stderr("the state file is unreadable\n")
		s.Exit(1)
	})
	got := launchWithCheckedService(t, "podman", servedPayload())
	for _, want := range []string{"loophole " + launchCheckFixtureName + ": could not ask",
		"exited 1", "the state file is unreadable"} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch does not say %q:\n%s", want, got)
		}
	}
}

// TestAFreshLaunchsOwnDaemonThatDoesNotKnowTheCheckNamesNoRestart: the case OQ-HD11 ruled on,
// a host-wide daemon an earlier yolo started that answers `unknown action: launch-check`, is
// not one a fresh launch can meet under HD-R1 (docs/design/host-daemon-ownership.md): every
// jail backend spawns the jail's own daemon, so the daemon asked is this launch's build. One
// that still answers that way has a manifest declaring a check its daemon lacks, and the line
// says so in the daemon's words, naming no restart and no older yolo. Mark the per-jail spawn's
// handle host-wide again (markLaunchCheck) and the line names a restart that fixes nothing.
func TestAFreshLaunchsOwnDaemonThatDoesNotKnowTheCheckNamesNoRestart(t *testing.T) {
	for _, rt := range []string{"podman", "macos-user"} {
		t.Run(rt, func(t *testing.T) {
			launchCheckIsolation(t)
			serveLaunchCheckDaemon(t, func(s *hostservice.Session) {
				s.Stderr("unknown action: launch-check\n")
				s.Exit(2)
			})
			got := launchWithCheckedService(t, rt, servedPayload())
			var line string
			for _, l := range strings.Split(got, "\n") {
				if strings.Contains(l, "loophole "+launchCheckFixtureName+": ") {
					if line != "" {
						t.Fatalf("the launch printed more than one launch-check line:\n%s", got)
					}
					line = l
				}
			}
			for _, want := range []string{"does not answer the launch check its manifest declares",
				"unknown action: launch-check"} {
				if !strings.Contains(line, want) {
					t.Errorf("the launch-check line does not say %q:\n%s", want, got)
				}
			}
			for _, not := range []string{"predates this yolo", broker.CycleCommand(launchCheckFixtureName)} {
				if strings.Contains(line, not) {
					t.Errorf("the launch-check line says %q of a daemon this launch started:\n%s", not, got)
				}
			}
		})
	}
}

// TestAFreshLaunchProceedsBesideAnOlderYolosMachineWideCopy is the owner's coexistence decision
// (2026-10-16, superseding HD-D21's refusal) at the boundary both launch arms cross: the one
// host-wide daemon that can still exist under HD-R1 is the copy an older yolo left running through
// the upgrade, serving the jails it launched. A fresh launch beside it is not refused, on either
// backend whose launch starts the service: it asks its own copy the check as always, prints one
// line naming the older copy and its optional later stop, and stops nothing itself.
func TestAFreshLaunchProceedsBesideAnOlderYolosMachineWideCopy(t *testing.T) {
	for _, rt := range []string{"podman", "macos-user"} {
		t.Run(rt, func(t *testing.T) {
			launchCheckIsolation(t)
			var asked atomic.Int32
			serveLaunchCheckDaemon(t, func(s *hostservice.Session) {
				asked.Add(1)
				answersTheCheck(s)
			})
			leaveAnOlderCopyRunning(t, olderThanTheCheck)
			got, refused := launchCheckedService(t, rt, servedPayload())
			if refused != nil {
				t.Fatalf("the boundary refused beside an older yolo's machine-wide copy: %s\n%s",
					refused.text(), got)
			}
			if asked.Load() == 0 {
				t.Errorf("the launch did not ask its own copy the check:\n%s", got)
			}
			assertTheOlderCopyLine(t, got)
			if n := strings.Count(got, olderCopyLead); n != 1 {
				t.Errorf("the launch named the older copy %d times, want one line:\n%s", n, got)
			}
			if !olderHostWideAlive(launchCheckFixtureName) {
				t.Error("the launch stopped the older copy itself; only the user may")
			}
		})
	}
}

// TestWithNoOlderCopyAFreshLaunchSaysNothingOfOne is the line's control: the same launch, with
// the older copy's PID file gone (what `yolo host-daemon stop --host-wide` leaves), names none.
func TestWithNoOlderCopyAFreshLaunchSaysNothingOfOne(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, answersTheCheck)
	serveFrontedFixture(t, olderThanTheCheck, paths.HostSingletonSocket(launchCheckFixtureName))
	got := launchWithCheckedService(t, "podman", servedPayload())
	assertNoOlderCopyLine(t, got)
}

// TestAnOlderCopyUnderAnotherHomeIsNotNamed: the rendezvous paths are keyed by the loophole's name
// in /tmp, but a copy running under another home (another user's, or another test run's private
// one) shares none of this launch's credentials, so the launch does not name it.
func TestAnOlderCopyUnderAnotherHomeIsNotNamed(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, answersTheCheck)
	leaveAnOlderCopyRunningUnder(t, olderThanTheCheck, t.TempDir())
	got := launchWithCheckedService(t, "podman", servedPayload())
	assertNoOlderCopyLine(t, got)
}

// olderCopyLead opens the line a fresh launch prints beside an older yolo's machine-wide copy of
// the fixture loophole.
const olderCopyLead = "loophole " + launchCheckFixtureName + ": an older yolo's machine-wide daemon"

// assertTheOlderCopyLine checks the line's words (HD-D31): the copy is running, this launch's jail
// does not use it, only a jail an older yolo launched can, and the stop once none is running. It
// claims no jail it serves, names no refusal and no retired restart.
func assertTheOlderCopyLine(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{olderCopyLead + " is still running",
		"jails this yolo launches do not use it",
		"only a jail an older yolo launched can",
		"Once none is running, `" + broker.StopHostWideCommand(launchCheckFixtureName) + "` ends it"} {
		if !strings.Contains(out, want) {
			t.Errorf("the launch does not say %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"Refusing", broker.CycleCommand(launchCheckFixtureName), "still serving"} {
		if strings.Contains(out, not) {
			t.Errorf("the launch says %q beside an older copy:\n%s", not, out)
		}
	}
}

// assertNoOlderCopyLine: a launch with no older copy of this home's names none.
func assertNoOlderCopyLine(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, "an older yolo's machine-wide daemon") {
		t.Errorf("a launch with no older copy of this home's named one:\n%s", out)
	}
}

// TestADaemonsWordsCannotRestyleOrSplitTheLaunchOutput: a warning is host code's text, printed
// on the launch stream, so a style tag in it prints as text and a control character cannot move
// the cursor or start a second line.
func TestADaemonsWordsCannotRestyleOrSplitTheLaunchOutput(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, func(s *hostservice.Session) {
		_ = s.AnswerLaunchCheck(hostservice.LaunchCheckReport{
			Warnings: []string{"[/bold yellow]forged\n\x1b[2Jline"},
			Notes:    []string{"a note"},
		})
	})
	got := launchWithCheckedService(t, "podman", servedPayload())
	var line string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "loophole "+launchCheckFixtureName+": ") && strings.Contains(l, "forged") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("the warning was not printed on one line:\n%q", got)
	}
	if strings.Contains(line, "\x1b[2J") {
		t.Errorf("a control sequence from the daemon reached the terminal: %q", line)
	}
	if !strings.Contains(line, "[2Jline") {
		t.Errorf("the text after the daemon's newline did not stay on the warning's line: %q", line)
	}
	if !strings.Contains(line, "/bold yellow]forged") {
		t.Errorf("the daemon's style tag was not kept as text: %q", line)
	}
	if !strings.Contains(got, "loophole "+launchCheckFixtureName+": a note") {
		t.Errorf("the note was not printed:\n%s", got)
	}
}

// TestBothLaunchArmsHandTheLaunchCheckTheirPayload is the call-site half the tests above cannot
// reach: they drive startLoopholesDisclosed directly, so they would stay green if run.go handed
// it nil, and then aws-auth, whose jail daemon a nil payload never serves, would never be asked.
// Both the container arm and the macos-user arm must pass the payload they composed: the macos-user
// arm to startLoopholesDisclosed, the container arm to its keeper's plan, which the keeper hands
// its start.
func TestBothLaunchArmsHandTheLaunchCheckTheirPayload(t *testing.T) {
	body, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	all := regexp.MustCompile(`startLoopholesDisclosed\(`).FindAllIndex(body, -1)
	withPayload := regexp.MustCompile(
		`startLoopholesDisclosed\(cname, rt, cfg, [a-zA-Z.]+, jailDaemons\)`).FindAllIndex(body, -1)
	if len(all) != 1 || len(withPayload) != 1 {
		t.Errorf("run.go calls startLoopholesDisclosed %d times, %d of them with the launch's "+
			"jailDaemons payload; want the macos-user arm to hand it over", len(all), len(withPayload))
	}
	if !regexp.MustCompile(`keeperPlanFor\(cfg, rt, cname, staged, services, jailDaemons,`).Match(body) {
		t.Error("the container arm no longer puts its jailDaemons payload in its keeper's plan")
	}
	keeper, err := os.ReadFile("keeper.go")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`startPlannedLoopholes\(p\.Cname, p\.Runtime, cfg, p\.Payload\)`).Match(keeper) {
		t.Error("the keeper no longer hands its start the plan's payload")
	}
}

// TestAPerLaunchDaemonThatDoesNotKnowTheCheckNamesNoRestart: a per-launch daemon was started by
// this launch from its manifest, so restarting it changes nothing, and the line says what is
// wrong (the manifest declares a check the daemon lacks) with the daemon's own words.
func TestAPerLaunchDaemonThatDoesNotKnowTheCheckNamesNoRestart(t *testing.T) {
	e := &launchCheckUnknownError{rc: 2, detail: "unknown action: launch-check"}
	line := unknownLaunchCheckLine(e)
	if strings.Contains(line, "restart") || strings.Contains(line, "predates") {
		t.Errorf("a per-launch daemon's line names a restart or an older yolo: %s", line)
	}
	for _, want := range []string{"its manifest declares", "unknown action: launch-check"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line lacks %q: %s", want, line)
		}
	}
}

// attachWithCheckedService stands up a RUNNING jail's host services the way its launch leaves
// them, through the production launch boundary with a payload that serves nothing (so that
// launch asks nothing), and then runs an attach's launch check for an entry whose payload is
// payload, from a second Options as a second yolo process would. It returns what the attach
// printed. published false skips the launch, leaving no front to find.
func attachWithCheckedService(t *testing.T, payload []loopholes.JailDaemonSpec, published bool) string {
	t.Helper()
	pack := launchCheckPack(t)
	t.Cleanup(loopholes.SnapshotPackModules())
	loopholes.SetPackModules(packLoopholeModules([]*packload.Pack{pack}))
	cname := "yolo-launchcheck-" + strings.ReplaceAll(t.Name(), "/", "-")
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })

	if published {
		launcher := &Options{}
		fillDefaults(launcher)
		var launchErr bytes.Buffer
		launcher.Stderr = &launchErr
		launcher.Stdout = discardBuf()
		launcher.PathExists = func(string) bool { return false }
		// A test that leaves an older copy running stands for a jail launched before it was the
		// older copy; the line naming that copy is not a launch-check answer, so it is cut below.
		handles, _ := launcher.startLoopholesDisclosed(cname, "podman", newConfig(),
			[]*packload.Pack{pack}, nil)
		t.Cleanup(func() {
			for _, h := range handles {
				if h.stop != nil {
					h.stop()
				}
			}
		})
		if !startedLoophole(handles, launchCheckFixtureName) {
			t.Fatalf("the fixture's host service did not start:\n%s", launchErr.String())
		}
		var asked []string
		for _, l := range strings.Split(launchErr.String(), "\n") {
			if strings.Contains(l, "loophole "+launchCheckFixtureName+":") && !strings.Contains(l, olderCopyLead) {
				asked = append(asked, l)
			}
		}
		if len(asked) > 0 {
			t.Fatalf("the launch, which serves no agent of the service, asked it:\n%s",
				launchErr.String())
		}
	}

	var errBuf bytes.Buffer
	attach := &Options{}
	fillDefaults(attach)
	attach.Stderr = &errBuf
	attach.Stdout = discardBuf()
	attach.PathExists = func(string) bool { return false }
	attach.runAttachLaunchChecks(cname, "podman", newConfig(), payload)
	return errBuf.String()
}

// TestAnAttachAsksTheRunningJailsServiceToo: a jail launched while the SSO session was live is
// warned about nothing, and the session then lapses. Attaching is how a user re-enters that
// jail (`yolo -p bedrock -- claude` in a second terminal), and the service its front reaches is
// running, so the attach asks it through that front and prints the same warning a launch would,
// rather than leaving the new agent's first request to find out.
func TestAnAttachAsksTheRunningJailsServiceToo(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, awsHandler(t,
		failedAWS("Error when retrieving token from sso: Token has expired and refresh failed"), nil))
	got := attachWithCheckedService(t, servedPayload(), true)
	for _, want := range []string{"loophole " + launchCheckFixtureName +
		": cannot mint a Bedrock credential for this launch: ", "has expired",
		"on the HOST run: aws sso login --profile bedrock"} {
		if !strings.Contains(got, want) {
			t.Errorf("the attach does not say %q:\n%s", want, got)
		}
	}
}

// TestAnAttachWhoseAgentDoesNotReachTheServiceAsksNothing is the fresh launch's rule on an
// attach: an entry whose selection serves none of the service's agents is not warned about it,
// nor made to wait for a mint.
func TestAnAttachWhoseAgentDoesNotReachTheServiceAsksNothing(t *testing.T) {
	launchCheckIsolation(t)
	var calls atomic.Int32
	serveLaunchCheckDaemon(t, awsHandler(t,
		failedAWS("Error when retrieving token from sso: Token has expired and refresh failed"), &calls))
	if got := attachWithCheckedService(t, nil, true); strings.Contains(got,
		"loophole "+launchCheckFixtureName+":") {
		t.Errorf("an attach that serves no agent of the service printed a launch-check line:\n%s", got)
	}
	if calls.Load() != 0 {
		t.Errorf("`aws` ran %d times for an attach that asked nothing", calls.Load())
	}
}

// TestAnAttachFindingNoFrontAsksNothing: an attach starts no service and no front. With none
// published for the jail (its launch did not start the service, or the backend does not run
// it), there is nothing to ask and nothing is said.
func TestAnAttachFindingNoFrontAsksNothing(t *testing.T) {
	launchCheckIsolation(t)
	var calls atomic.Int32
	serveLaunchCheckDaemon(t, awsHandler(t,
		failedAWS("Error when retrieving token from sso: Token has expired and refresh failed"), &calls))
	if got := attachWithCheckedService(t, servedPayload(), false); strings.Contains(got,
		"loophole "+launchCheckFixtureName+":") {
		t.Errorf("an attach with no published front printed a launch-check line:\n%s", got)
	}
	if calls.Load() != 0 {
		t.Errorf("`aws` ran %d times for an attach with no front to ask through", calls.Load())
	}
}

// TestAnAttachIntoAnOlderYolosJailKeepsTheLineAndNamesTheStop is HD-D5 (1) under the owner's
// coexistence decision (2026-10-16): an attach never refuses. Its jail's front leads to the
// machine-wide copy the older yolo that launched it left (stood in for here by this launch's own
// copy answering as that copy does), and that copy does not answer the check, so the line names
// relaunching the jail and then, once the older yolo's jails have ended, stopping that copy, never
// the retired restart.
func TestAnAttachIntoAnOlderYolosJailKeepsTheLineAndNamesTheStop(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, olderThanTheCheck)
	leaveAnOlderCopyRunning(t, olderThanTheCheck)
	got := attachWithCheckedService(t, servedPayload(), true)
	relaunch := "Leave this jail and launch it again with this yolo"
	stop := "once no jail the older yolo launched is left, `" +
		broker.StopHostWideCommand(launchCheckFixtureName) + "` ends that daemon"
	for _, want := range []string{"loophole " + launchCheckFixtureName + ": this jail was launched by an older yolo",
		relaunch, stop} {
		if !strings.Contains(got, want) {
			t.Errorf("the attach does not say %q:\n%s", want, got)
		}
	}
	if strings.Index(got, stop) < strings.Index(got, relaunch) {
		t.Errorf("the attach names the stop before the relaunch:\n%s", got)
	}
	if strings.Contains(got, broker.CycleCommand(launchCheckFixtureName)) {
		t.Errorf("the attach names the retired restart:\n%s", got)
	}
}

// TestAnAttachIntoThisYolosJailThatLacksTheCheckBlamesTheManifest: with no older copy running, the
// daemon the attach reaches is its jail's own, so `unknown action` is the manifest's fault, and
// the line says so, naming no stop and no older yolo.
func TestAnAttachIntoThisYolosJailThatLacksTheCheckBlamesTheManifest(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, olderThanTheCheck)
	got := attachWithCheckedService(t, servedPayload(), true)
	if !strings.Contains(got, "does not answer the launch check its manifest declares") {
		t.Errorf("the attach does not blame the manifest:\n%s", got)
	}
	for _, not := range []string{"older yolo", broker.StopHostWideCommand(launchCheckFixtureName)} {
		if strings.Contains(got, not) {
			t.Errorf("the attach says %q of its jail's own daemon:\n%s", not, got)
		}
	}
}

// TestTheAttachArmRunsTheLaunchCheckAndStartsNothing pins the call site the tests above drive
// around, and that the attach's check only asks: an attach runs above the config-change gate
// and never starts or restarts a service.
func TestTheAttachArmRunsTheLaunchCheckAndStartsNothing(t *testing.T) {
	if !callsIn(funcDecl(t, "run.go", "attachExisting"))["runAttachLaunchChecks"] {
		t.Error("attachExisting no longer calls runAttachLaunchChecks")
	}
	calls := callsIn(funcDecl(t, "launchcheck.go", "runAttachLaunchChecks"))
	for _, starts := range []string{"startLoopholes", "startLoopholesDisclosed",
		"startHostSingleton", "startExternalService", "EnsureSingleton", "BrokerSpawn"} {
		if calls[starts] {
			t.Errorf("the attach's launch check calls %s: an attach must ask, never start", starts)
		}
	}
}
