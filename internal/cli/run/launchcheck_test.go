package run

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
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
// and a jail daemon, the shape of aws-auth's manifest. Its host argv never runs: the daemon
// is already up (serveLaunchCheckDaemon), so the launch ensures it rather than spawning.
func launchCheckPack(t *testing.T) *packload.Pack {
	t.Helper()
	return writeRealLoopholePack(t, "yjtest-launchcheck-pack", launchCheckFixtureName, `{
		"name": "`+launchCheckFixtureName+`",
		"description": "a host service that answers the launch check",
		"default_enabled": true,
		"transport": "loopback-tls",
		"lifecycle": "spawned",
		"host_daemon": {"cmd": ["/bin/false", "{socket}"], "publishes": "socket",
			"scope": "host", "launch_check": true},
		"jail_daemon": {"cmd": ["yolo-jaild", "fixture-adapter"]}
	}`)
}

// servedPayload is a jail-daemon payload that serves the fixture loophole's jail daemon, as
// jailDaemonsFor composes one when some agent's profile selects what it serves.
func servedPayload() []loopholes.JailDaemonSpec {
	return []loopholes.JailDaemonSpec{{Name: launchCheckFixtureName,
		Cmd: []string{"yolo-jaild", "fixture-adapter"}, Restart: "on-failure"}}
}

// serveLaunchCheckDaemon stands the handler up where a RUNNING host-wide daemon is: behind
// hostservice.ServeFrontedUnix at paths.HostSingletonSocket, with a pid file naming a live
// process (this one), so the launch's ensure finds it alive and fronts it.
func serveLaunchCheckDaemon(t *testing.T, handler hostservice.Handler) {
	t.Helper()
	saved := hostservice.Logger
	hostservice.Logger = log.New(io.Discard, "", 0)
	sock := paths.HostSingletonSocket(launchCheckFixtureName)
	assertSockPathFits(t, sock)
	_ = os.Remove(sock)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeFrontedUnix(handler, sock, stop)
	}()
	pidFile := paths.HostSingletonPIDFile(launchCheckFixtureName)
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(stop)
		<-done
		hostservice.Logger = saved
		_ = os.Remove(sock)
		_ = os.Remove(pidFile)
		_ = os.Remove(paths.HostSingletonLock(launchCheckFixtureName))
	})
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(sock) {
		if time.Now().After(deadline) {
			t.Fatal("the fixture daemon never bound its socket")
		}
		time.Sleep(2 * time.Millisecond)
	}
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
// and returns what the launch printed on its stderr.
func launchWithCheckedService(t *testing.T, rt string, payload []loopholes.JailDaemonSpec) string {
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
	handles := o.startLoopholesDisclosed(cname, rt, newConfig(), []*packload.Pack{pack}, payload)
	for _, h := range handles {
		if h.stop != nil {
			h.stop()
		}
	}
	if !startedLoophole(handles, launchCheckFixtureName) {
		t.Fatalf("the fixture's host service did not start, so nothing could be asked:\n%s",
			errBuf.String())
	}
	return errBuf.String()
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

// TestADaemonThatCannotAnswerIsSaidSo: a daemon started by an older yolo does not know the
// action and exits non-zero. The launch still proceeds, and says it could not ask, so the
// absence of a warning is never read as a clean bill.
func TestADaemonThatCannotAnswerIsSaidSo(t *testing.T) {
	launchCheckIsolation(t)
	serveLaunchCheckDaemon(t, func(s *hostservice.Session) {
		s.Stderr("unknown action: launch-check\n")
		s.Exit(2)
	})
	got := launchWithCheckedService(t, "podman", servedPayload())
	for _, want := range []string{"loophole " + launchCheckFixtureName + ": could not ask",
		"exited 2", "unknown action: launch-check"} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch does not say %q:\n%s", want, got)
		}
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
// Both the container arm and the macos-user arm must pass the payload they composed.
func TestBothLaunchArmsHandTheLaunchCheckTheirPayload(t *testing.T) {
	body, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	all := regexp.MustCompile(`startLoopholesDisclosed\(`).FindAllIndex(body, -1)
	withPayload := regexp.MustCompile(
		`startLoopholesDisclosed\(cname, rt, cfg, [a-zA-Z.]+, jailDaemons\)`).FindAllIndex(body, -1)
	if len(all) != 2 || len(withPayload) != 2 {
		t.Errorf("run.go calls startLoopholesDisclosed %d times, %d of them with the launch's "+
			"jailDaemons payload; want both arms (container and macos-user) to hand it over",
			len(all), len(withPayload))
	}
}
