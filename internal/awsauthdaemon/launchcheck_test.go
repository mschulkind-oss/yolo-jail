package awsauthdaemon

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// launchcheck_test.go pins this daemon's answer to the launch check over the real framed
// transport (serveHandler): what a launch prints when the SSO session behind the profile cannot
// mint, and that it prints nothing when it can. Nothing here runs `aws`; each case is a canned
// Runner whose stderr is what AWS CLI v2 prints for that failure. The launch's half, that the
// answer is asked for and printed, is internal/cli/run's launchcheck_test.go.

func askCheck(t *testing.T, do func(map[string]any) reply, budget time.Duration) hostservice.LaunchCheckReport {
	t.Helper()
	got := do(map[string]any{"action": hostservice.LaunchCheckAction,
		hostservice.LaunchCheckBudgetKey: budget.Milliseconds()})
	if got.rc != 0 {
		t.Fatalf("launch-check rc = %d, stderr = %q", got.rc, got.stderr)
	}
	var report hostservice.LaunchCheckReport
	if err := json.Unmarshal([]byte(got.stdout), &report); err != nil {
		t.Fatalf("launch-check answer is not a report (%q): %v", got.stdout, err)
	}
	return report
}

func failing(stderr string) awsauth.Output {
	return awsauth.Output{Spawned: true, Code: 255, Stderr: stderr}
}

// TestTheLaunchCheckWarnsForEveryFailureClass is design §8's degenerate inputs at the wire:
// no session ever established, a session that lapsed, and a profile the host's AWS config
// lacks each come back as ONE warning carrying the classifier's Message, which names the fix;
// the two AWS-refused classes carry AWS's own words. The Message is the same one the jail's 4xx
// and `yolo check` show, so the launch says nothing the other two do not.
func TestTheLaunchCheckWarnsForEveryFailureClass(t *testing.T) {
	for _, tc := range []struct {
		name    string
		out     awsauth.Output
		want    []string
		wantNot string
	}{
		{"never established",
			failing("Error loading SSO Token: Token for https://x/start does not exist"),
			[]string{"was never established", "aws sso login --profile bedrock"}, ""},
		{"expired",
			failing("Error when retrieving token from sso: Token has expired and refresh failed"),
			[]string{"has expired", "aws sso login --profile bedrock"}, ""},
		{"profile missing",
			failing("The config profile (bedrock) could not be found"),
			[]string{"not in the host's ~/.aws/config", "aws configure sso --profile bedrock",
				"loopholes.aws-auth.settings.profile"},
			// A login for a profile that does not exist is wrong advice.
			"aws sso login"},
		{"rejected by STS",
			failing("An error occurred (AccessDenied) when calling the AssumeRole operation: not authorized"),
			[]string{"AccessDenied", "not authorized"}, ""},
		{"unavailable",
			awsauth.Output{Spawned: true, Code: 1, Stderr: "Could not connect to the endpoint URL"},
			[]string{"exited 1", "Could not connect"}, ""},
		{"aws CLI gone",
			awsauth.Output{Spawned: false, Code: -1, Stderr: "exec: \"aws\": executable file not found"},
			[]string{"install AWS CLI v2"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker := testHandlerBroker(t, unnarrowed("bedrock"), cannedRunner(tc.out, nil))
			do := serveHandler(t, HandlerConfig{Broker: broker})
			report := askCheck(t, do, time.Second)
			if len(report.Warnings) != 1 || len(report.Notes) != 0 {
				t.Fatalf("report = %+v, want exactly one warning", report)
			}
			w := report.Warnings[0]
			for _, want := range append(tc.want, "cannot mint a Bedrock credential",
				"The launch continues") {
				if !strings.Contains(w, want) {
					t.Errorf("warning lacks %q:\n%s", want, w)
				}
			}
			if tc.wantNot != "" && strings.Contains(w, tc.wantNot) {
				t.Errorf("warning carries %q, which is wrong advice here:\n%s", tc.wantNot, w)
			}
			if strings.Contains(w, "\n") {
				t.Errorf("warning is more than one line:\n%s", w)
			}
		})
	}
}

// TestTheLaunchCheckSaysHowEachFixReachesTheRunningService: a fix on the host's AWS side (a
// login, the profile added to ~/.aws/config, the CLI installed) is found by the next mint, so
// the running jail recovers with no relaunch. A change to loopholes.aws-auth.settings is not:
// the daemon reads its settings only when it starts, and only a launch restarts it on a
// settings change. So the promise of "no relaunch" is made unconditionally only where every fix
// the Message names is on the AWS side, and a warning that names a setting says when a setting
// takes effect.
func TestTheLaunchCheckSaysHowEachFixReachesTheRunningService(t *testing.T) {
	const awsSideOnly = "and then work with no relaunch"
	for _, tc := range []struct {
		name       string
		out        awsauth.Output
		settingFix bool
	}{
		{"lapsed session", failing("Token has expired and refresh failed"), false},
		{"never established", failing("Error loading SSO Token: Token does not exist"), false},
		{"aws CLI gone", awsauth.Output{Spawned: false, Code: -1, Stderr: "not found"}, false},
		{"profile missing", failing("The config profile (bedrock) could not be found"), true},
		{"rejected by STS", failing("An error occurred (AccessDenied) when calling AssumeRole"), true},
		{"static keys, no session token", awsauth.Output{Spawned: true,
			Stdout: `{"Version":1,"AccessKeyId":"AKIA1","SecretAccessKey":"zzsecretzz"}`}, true},
		{"unclassified", awsauth.Output{Spawned: true, Code: 1, Stderr: "Could not connect"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			broker := testHandlerBroker(t, unnarrowed("bedrock"), cannedRunner(tc.out, nil))
			report := askCheck(t, serveHandler(t, HandlerConfig{Broker: broker}), time.Second)
			if len(report.Warnings) != 1 {
				t.Fatalf("report = %+v, want exactly one warning", report)
			}
			w := report.Warnings[0]
			if !tc.settingFix {
				if !strings.Contains(w, awsSideOnly) {
					t.Errorf("an AWS-side fix is not promised to work with no relaunch:\n%s", w)
				}
				return
			}
			if strings.Contains(w, awsSideOnly) {
				t.Errorf("a warning whose fix can be a setting promises no relaunch outright:\n%s", w)
			}
			for _, want := range []string{"picked up with no relaunch",
				"a change to loopholes.aws-auth.settings", "next launch"} {
				if !strings.Contains(w, want) {
					t.Errorf("warning lacks %q:\n%s", want, w)
				}
			}
		})
	}
}

// TestTheLaunchCheckIsSilentOnAHealthyMintAndWarmsTheCache: a cold cache that mints cleanly
// says nothing, and the mint the check made is the one the agent's first request is then served
// from, so the launch paid for work that request would otherwise have done inside the SDK's
// one-second budget (design R1).
func TestTheLaunchCheckIsSilentOnAHealthyMintAndWarmsTheCache(t *testing.T) {
	var calls atomic.Int32
	broker := testHandlerBroker(t, unnarrowed("bedrock"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), &calls))
	do := serveHandler(t, HandlerConfig{Broker: broker})
	if report := askCheck(t, do, time.Second); len(report.Warnings)+len(report.Notes) != 0 {
		t.Fatalf("a healthy mint produced %+v", report)
	}
	if _, ok := broker.Current(); !ok {
		t.Fatal("the check minted but left nothing cached for the first request")
	}
	if got := do(map[string]any{"action": "credentials"}); got.rc != 0 {
		t.Fatalf("first request after the check: rc %d, %q", got.rc, got.stderr)
	}
	if calls.Load() != 1 {
		t.Errorf("`aws` ran %d times across the check and the first request, want 1", calls.Load())
	}
}

// TestTheLaunchCheckAnswersAWarmCacheWithoutRunningAWS: the common launch, every one after the
// first, costs no `aws` invocation at all.
func TestTheLaunchCheckAnswersAWarmCacheWithoutRunningAWS(t *testing.T) {
	var calls atomic.Int32
	broker := testHandlerBroker(t, unnarrowed("bedrock"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), &calls))
	if _, err := broker.Fetch(context.Background(), "warm-up"); err != nil {
		t.Fatal(err)
	}
	calls.Store(0)
	do := serveHandler(t, HandlerConfig{Broker: broker})
	if report := askCheck(t, do, time.Second); len(report.Warnings)+len(report.Notes) != 0 {
		t.Fatalf("a warm cache produced %+v", report)
	}
	if calls.Load() != 0 {
		t.Errorf("a warm cache ran `aws` %d times for the launch check", calls.Load())
	}
}

// blockingRunner fails fast on its first call, when failFirst is set, and blocks every later
// call until release is closed, then succeeds.
func blockingRunner(calls *atomic.Int32, failFirst bool, release <-chan struct{}) awsauth.Runner {
	return func(_ context.Context, _ []string) awsauth.Output {
		n := calls.Add(1)
		if failFirst && n == 1 {
			return failing("Error when retrieving token from sso: Token has expired and refresh failed")
		}
		<-release
		return processOutput(time.Now().Add(time.Hour), "ASIA1")
	}
}

// releaseAndDrain, at cleanup, unblocks the runner and waits for the mint it was holding to
// finish, so that mint's state write lands before the test's temp dir is removed. Registered
// after the broker's temp dir, so it runs first.
func releaseAndDrain(t *testing.T, release chan struct{}, mints *mintTracker, before *mintAttempt) {
	t.Helper()
	t.Cleanup(func() {
		close(release)
		deadline := time.Now().Add(5 * time.Second)
		for mints.previous() == before {
			if time.Now().After(deadline) {
				t.Error("the held mint never finished")
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
}

// TestASlowMintIsBoundedByTheBudget: the launch check never holds a launch past its budget.
// With nothing known before, it says it could not tell, as a note; with an earlier failure it
// reports that one, saying how old it is. Either way the mint keeps running for the first
// request.
func TestASlowMintIsBoundedByTheBudget(t *testing.T) {
	t.Run("nothing known before", func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		broker := testHandlerBroker(t, unnarrowed("bedrock"), blockingRunner(&calls, false, release))
		mints := newMintTracker(nil)
		releaseAndDrain(t, release, mints, nil)
		do := serveHandler(t, HandlerConfig{Broker: broker, Mints: mints})
		start := time.Now()
		report := askCheck(t, do, 100*time.Millisecond)
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("the check took %s against a 100ms budget", took)
		}
		if len(report.Warnings) != 0 || len(report.Notes) != 1 ||
			!strings.Contains(report.Notes[0], "had not finished within 100ms") {
			t.Fatalf("report = %+v, want one note saying the mint had not finished", report)
		}
	})
	t.Run("an earlier failure", func(t *testing.T) {
		var calls atomic.Int32
		release := make(chan struct{})
		broker := testHandlerBroker(t, unnarrowed("bedrock"), blockingRunner(&calls, true, release))
		mints := newMintTracker(nil)
		// The earlier attempt: the proactive minter's, which failed.
		earlier := mints.begin(broker, "proactive")
		<-earlier.done
		releaseAndDrain(t, release, mints, earlier)
		do := serveHandler(t, HandlerConfig{Broker: broker, Mints: mints})
		report := askCheck(t, do, 100*time.Millisecond)
		if len(report.Warnings) != 1 {
			t.Fatalf("report = %+v, want the earlier failure as one warning", report)
		}
		for _, want := range []string{"aws sso login --profile bedrock", "from the mint",
			"a new one had not finished within 100ms"} {
			if !strings.Contains(report.Warnings[0], want) {
				t.Errorf("warning lacks %q:\n%s", want, report.Warnings[0])
			}
		}
	})
}

// TestTheLaunchCheckJoinsTheMintInFlight: a launch that arrives while the proactive minter's
// spawn-time mint runs waits for THAT mint, so a fresh daemon runs `aws` once, not twice.
func TestTheLaunchCheckJoinsTheMintInFlight(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	broker := testHandlerBroker(t, unnarrowed("bedrock"), blockingRunner(&calls, false, release))
	mints := newMintTracker(nil)
	inflight := mints.begin(broker, "proactive")
	deadline := time.Now().Add(5 * time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the proactive mint never started")
		}
		time.Sleep(time.Millisecond)
	}
	do := serveHandler(t, HandlerConfig{Broker: broker, Mints: mints})
	// The request runs on its own goroutine and is DECODED on this one: a t.Fatalf off the
	// test goroutine would end only that goroutine and leave this test waiting forever.
	answered := make(chan reply, 1)
	go func() {
		answered <- do(map[string]any{"action": hostservice.LaunchCheckAction,
			hostservice.LaunchCheckBudgetKey: int64(3000)})
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	var got reply
	select {
	case got = <-answered:
	case <-time.After(10 * time.Second):
		t.Fatal("the launch check never answered")
	}
	<-inflight.done
	if got.rc != 0 {
		t.Fatalf("launch-check rc = %d, stderr = %q", got.rc, got.stderr)
	}
	var report hostservice.LaunchCheckReport
	if err := json.Unmarshal([]byte(got.stdout), &report); err != nil {
		t.Fatalf("launch-check answer is not a report (%q): %v", got.stdout, err)
	}
	if len(report.Warnings)+len(report.Notes) != 0 {
		t.Errorf("report = %+v, want nothing: the joined mint succeeded", report)
	}
	if calls.Load() != 1 {
		t.Errorf("`aws` ran %d times, want 1: the check must join the mint in flight", calls.Load())
	}
}

// TestTheLaunchCheckCarriesNoCredential: the answer is safe from a jail too, like `status`.
func TestTheLaunchCheckCarriesNoCredential(t *testing.T) {
	broker := testHandlerBroker(t, unnarrowed("bedrock"),
		cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIASECRET"), nil))
	do := serveHandler(t, HandlerConfig{Broker: broker})
	got := do(map[string]any{"action": hostservice.LaunchCheckAction})
	for _, secret := range []string{"ASIASECRET", "zzsecretzz", "zztokenzz"} {
		if strings.Contains(got.stdout+got.stderr, secret) {
			t.Errorf("the launch check's answer carries %q: %s", secret, got.stdout)
		}
	}
}
