package awsauthdaemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// main_test.go covers what the daemon decides AT SPAWN, and the order it decides it
// in. Nothing here runs the real `aws` binary: the refusals all land before the
// dependency check, and the one test that reaches the check points --aws-binary at a
// path that does not exist.

func settingsFileWith(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func absState(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), awsauth.StateFileName)
}

// mainRC runs Main with a DEADLINE.
//
// Every test below asserts that Main REFUSES, and a refusal that stops refusing
// does not fail — it serves, and Main blocks in its accept loop forever. That turns
// a deleted guard into a suite-wide hang reported ten minutes later as a timeout,
// which is the worst failure signal available. The deadline turns it into one named
// test failing in seconds.
func mainRC(t *testing.T, argv ...string) int {
	t.Helper()
	rc := make(chan int, 1)
	go func() { rc <- Main(argv) }()
	select {
	case got := <-rc:
		return got
	case <-time.After(15 * time.Second):
		t.Fatalf("Main(%v) did not return — it is SERVING where it should have refused", argv)
		return 0
	}
}

// TestMainRefusesRelativeStatePathBeforeCreatingAnything mirrors
// openaiauthdaemon's: the manifest substitutes an absolute {state}, so a relative
// value means an unsubstituted token or a hand-run from an arbitrary cwd, and either
// would scatter a credential cache wherever the process started.
func TestMainRefusesRelativeStatePathBeforeCreatingAnything(t *testing.T) {
	t.Chdir(t.TempDir())
	if rc := mainRC(t, "--self-check", "--state-file", "{state}/credentials.json"); rc != 2 {
		t.Fatalf("rc = %d, want usage failure 2", rc)
	}
}

func TestMainRequiresASocketOutsideSelfCheck(t *testing.T) {
	if rc := mainRC(t, "--state-file", absState(t)); rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
}

func TestSettingsCheckProjectsTypedRefusalsWithoutSettingsValues(t *testing.T) {
	profile := "SENTINEL_PROFILE_DO_NOT_RENDER"
	policyKey := "SENTINEL_POLICY_KEY_DO_NOT_RENDER"
	policyValue := "SENTINEL_POLICY_VALUE_DO_NOT_RENDER"
	settings := settingsFileWith(t, `{"profile":"`+profile+`","role_arn":"","session_policy":"","unnarrowed":false}`)
	var out strings.Builder
	if rc := SettingsCheck(settings, &out); rc == 0 {
		t.Fatal("missing narrowing passed settings validation")
	}
	for _, want := range []string{"No AWS credential mode is configured", "loopholes.aws-auth.settings.role_arn", "~/.config/yolo-jail/config.jsonc", "unnarrowed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("safe diagnostic lacks %q: %s", want, out.String())
		}
	}
	for _, secret := range []string{profile, policyKey, policyValue} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("validator output leaked %q: %s", secret, out.String())
		}
	}
}

func TestSettingsRefusalStartupReasonAndLaunchTextDoNotRenderAWSValues(t *testing.T) {
	profile := "SENTINEL_AWS_PROFILE_DO_NOT_RENDER"
	policyKey := "SENTINEL_POLICY_KEY_DO_NOT_RENDER"
	policyValue := "SENTINEL_POLICY_VALUE_DO_NOT_RENDER"
	policy := `{"Statement":[{"Effect":"Allow","Action":["` + policyKey + `"],"Resource":"` + policyValue + `"}]}`
	settings := settingsFileWith(t, `{"profile":"`+profile+`","role_arn":"","session_policy":`+
		string(mustJSON(t, policy))+`,"unnarrowed":false}`)
	parent, child, attempt, err := hostservice.NewStartupReasonChannel()
	if err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Dup(int(child.Fd()))
	if err != nil {
		_ = parent.Close()
		_ = child.Close()
		t.Fatal(err)
	}
	syscall.CloseOnExec(fd)
	_ = child.Close()
	defer parent.Close()
	t.Setenv(hostservice.StartupReasonFDEnv, strconv.Itoa(fd))
	t.Setenv(hostservice.StartupReasonAttemptEnv, attempt)
	t.Setenv(hostservice.StartupReasonServiceEnv, "aws-auth")
	var log strings.Builder
	_, rc := prepare(spawnOptions{SettingsPath: settings, StatePath: absState(t),
		AWSBinary: "/must-not-run/aws"}, &log)
	if rc != 2 {
		t.Fatalf("prepare returned %d, want typed configuration refusal", rc)
	}
	reason, err := hostservice.ReadStartupReason(parent, "aws-auth", attempt, time.Now().Add(time.Second))
	if err != nil {
		t.Fatalf("current AWS startup refusal record: %v", err)
	}
	if !strings.Contains(reason.Reason, "session policy is configured without a role") ||
		!strings.Contains(reason.Remedy, "loopholes.aws-auth.settings.role_arn") {
		t.Fatalf("AWS refusal projection lost its safe reason or setting remedy: %+v", reason)
	}
	rendered := reason.Reason + " " + reason.Remedy + " " + log.String()
	for _, secret := range []string{profile, policyKey, policyValue} {
		if strings.Contains(rendered, secret) {
			t.Errorf("AWS startup cause rendered sentinel %q: %s", secret, rendered)
		}
	}
}

func TestSettingsCheckAcceptsValidSnapshotWithoutRunningAws(t *testing.T) {
	fakeBin := t.TempDir()
	called := filepath.Join(t.TempDir(), "aws-called")
	fakeAWS := filepath.Join(fakeBin, "aws")
	t.Setenv("FAKE_AWS_CALLED", called)
	if err := os.WriteFile(fakeAWS, []byte("#!/bin/sh\nprintf called > \"$FAKE_AWS_CALLED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)
	policyKey := "SENTINEL_POLICY_KEY_DO_NOT_RENDER"
	policyValue := "SENTINEL_POLICY_VALUE_DO_NOT_RENDER"
	policy := `{"Statement":[{"Action":["` + policyKey + `"],"Resource":"` + policyValue + `"}]}`
	settings := settingsFileWith(t, `{"profile":"SENTINEL_PROFILE","role_arn":"arn:aws:iam::123456789012:role/bedrock","session_policy":`+string(mustJSON(t, policy))+`,"unnarrowed":false}`)
	var out strings.Builder
	if rc := SettingsCheck(settings, &out); rc != 0 || out.Len() != 0 {
		t.Fatalf("settings check rc=%d output=%q", rc, out.String())
	}
	for _, secret := range []string{"SENTINEL_PROFILE", policyKey, policyValue} {
		if strings.Contains(out.String(), secret) {
			t.Errorf("valid validator output leaked %q: %s", secret, out.String())
		}
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatalf("settings validator invoked the AWS CLI shim: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// TestAbsentNarrowingRefusesAtSpawnNamingTheKey is STEP 2. A widening default
// retrofitted later breaks working setups, which is why the requirement lands with
// the first release rather than after it.
func TestAbsentNarrowingRefusesAtSpawnNamingTheKey(t *testing.T) {
	settings := settingsFileWith(t,
		`{"profile":"bedrock","role_arn":"","session_policy":"","unnarrowed":false}`)
	socket := filepath.Join(shortTempDir(t), "d.sock")
	rc := mainRC(t, "--socket", socket, "--state-file", absState(t), "--settings", settings)
	if rc != 2 {
		t.Fatalf("rc = %d, want 2 — a profile with no narrowing must not serve", rc)
	}
	// Nothing was bound and nothing was created: the refusal is before every side
	// effect.
	if _, err := os.Stat(socket); err == nil {
		t.Error("the refusing daemon still bound its socket")
	}
}

// TestAnAbsentSettingsFileRefusesAtSpawn: an unconfigured loophole that got spawned
// anyway says which key is missing rather than serving something.
func TestAnAbsentSettingsFileRefusesAtSpawn(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "settings.json")
	rc := mainRC(t, "--socket", filepath.Join(shortTempDir(t), "d.sock"),
		"--state-file", absState(t), "--settings", missing)
	if rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
}

// TestTheSettingsRefusalPrecedesTheDependencyCheck pins the ORDER. Both are spawn
// refusals with different exit codes, and a configuration fault must not be reported
// as a missing CLI on a host that has neither.
func TestTheSettingsRefusalPrecedesTheDependencyCheck(t *testing.T) {
	settings := settingsFileWith(t, `{"profile":"","unnarrowed":true}`)
	rc := mainRC(t, "--socket", filepath.Join(shortTempDir(t), "d.sock"),
		"--state-file", absState(t), "--settings", settings,
		"--aws-binary", "/nonexistent/aws")
	if rc != 2 {
		t.Errorf("rc = %d, want the configuration refusal (2) rather than the CLI refusal (1)", rc)
	}
}

// TestAnUnrunnableAwsCLIRefusesAtSpawn is §8's last failure row: the daemon fails
// LOUDLY at spawn. It is deliberately not a manifest requires.command_on_path probe
// — that shape removed the Claude broker for exactly the user it existed for — so
// the loophole stays listed and the daemon says which dependency is absent.
func TestAnUnrunnableAwsCLIRefusesAtSpawn(t *testing.T) {
	settings := settingsFileWith(t, `{"profile":"bedrock","unnarrowed":true}`)
	socket := filepath.Join(shortTempDir(t), "d.sock")
	rc := mainRC(t, "--socket", socket, "--state-file", absState(t),
		"--settings", settings, "--aws-binary", filepath.Join(t.TempDir(), "no-such-aws"))
	if rc != 1 {
		t.Fatalf("rc = %d, want 1", rc)
	}
	if _, err := os.Stat(socket); err == nil {
		t.Error("the refusing daemon still bound its socket")
	}
}

func TestHostSocketPathIsASibling(t *testing.T) {
	if got := HostSocketPath("/run/x/aws-auth.sock"); got != "/run/x/aws-auth.sock.host" {
		t.Errorf("HostSocketPath = %q", got)
	}
}

// --- self-check ---

func selfCheckOpts(t *testing.T, settingsPath string, run awsauth.Runner) SelfCheckOptions {
	t.Helper()
	return SelfCheckOptions{
		SettingsPath: settingsPath, StatePath: absState(t),
		Runner: run, AWSBinary: "aws",
		ConfigPath: filepath.Join(t.TempDir(), "config"),
	}
}

func TestSelfCheckIsGreenOnAFreshMachine(t *testing.T) {
	// No --settings at all: the daemon was run by hand.
	var out strings.Builder
	if rc := SelfCheck(selfCheckOpts(t, "", nil), &out); rc != 0 {
		t.Errorf("rc = %d, want 0", rc)
	}
	if !strings.HasPrefix(out.String(), "OK:") {
		t.Errorf("output = %q", out.String())
	}
	// A settings file that does not exist yet: yolo writes it when a jail launches
	// this loophole, so a fresh install must not get a red line it cannot act on.
	out.Reset()
	missing := filepath.Join(t.TempDir(), "settings.json")
	if rc := SelfCheck(selfCheckOpts(t, missing, nil), &out); rc != 0 {
		t.Errorf("rc = %d for an unlaunched loophole, want 0: %s", rc, out.String())
	}
	if !strings.Contains(out.String(), "no settings resolved yet") {
		t.Errorf("output = %q", out.String())
	}
}

// TestSelfCheckFailsOnASettingsFileThatDoesNotDescribeAConfiguration: a file that
// EXISTS and does not resolve is the real fault, and it is the one thing this check
// can see that nothing else reports.
func TestSelfCheckFailsOnASettingsFileThatDoesNotDescribeAConfiguration(t *testing.T) {
	for name, body := range map[string]string{
		"unparseable":       "{ not json",
		"no profile":        `{"profile":"","unnarrowed":true}`,
		"no narrowing":      `{"profile":"bedrock"}`,
		"both arms at once": `{"profile":"bedrock","role_arn":"arn:x","unnarrowed":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			rc := SelfCheck(selfCheckOpts(t, settingsFileWith(t, body), nil), &out)
			if rc != 1 {
				t.Errorf("rc = %d, want 1: %s", rc, out.String())
			}
			if !strings.HasPrefix(out.String(), "FAIL:") {
				t.Errorf("output does not start with a graded FAIL line: %q", out.String())
			}
		})
	}
}

// TestSelfCheckMintsOnceAndPrintsTheFourKeysElided is the plan's step-1 Proves
// column. It is the only thing short of an agent turn that shows the host's session
// is live, which is why it wants a host with an `aws` login.
func TestSelfCheckMintsOnceAndPrintsTheFourKeysElided(t *testing.T) {
	var calls atomic.Int32
	run := cannedRunner(assumeOutput(time.Now().Add(50*time.Minute), "ASIASELFCHECK"), &calls)
	settings := settingsFileWith(t,
		`{"profile":"bedrock","role_arn":"arn:aws:iam::1:role/r","session_policy":"","unnarrowed":false}`)
	var out strings.Builder
	if rc := SelfCheck(selfCheckOpts(t, settings, run), &out); rc != 0 {
		t.Fatalf("rc = %d: %s", rc, out.String())
	}
	if calls.Load() != 1 {
		t.Errorf("minted %d times, want 1", calls.Load())
	}
	text := out.String()
	for _, want := range []string{"AccessKeyId", "SecretAccessKey", "Token", "Expiration",
		awsauth.Fingerprint("ASIASELFCHECK"), "(elided)", "lifetime remaining"} {
		if !strings.Contains(text, want) {
			t.Errorf("output does not contain %q:\n%s", want, text)
		}
	}
	for _, secret := range []string{"ASIASELFCHECK", "zzsecretzz", "zztokenzz"} {
		if strings.Contains(text, secret) {
			t.Errorf("output carries %q:\n%s", secret, text)
		}
	}
	// EVERY line is graded, or reportSelfCheckLines drops it.
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if !strings.HasPrefix(line, "OK:") && !strings.HasPrefix(line, "NOTE:") &&
			!strings.HasPrefix(line, "FAIL:") {
			t.Errorf("ungraded self-check line: %q", line)
		}
	}
}

// TestSelfCheckGradesAnUnnarrowedConfigurationAsHealthy: permission-set-as-configured is
// an explicit, valid mode. yolo check must not frame that user choice as a warning.
func TestSelfCheckGradesAnUnnarrowedConfigurationAsHealthy(t *testing.T) {
	run := cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), nil)
	settings := settingsFileWith(t, `{"profile":"wide","unnarrowed":true}`)
	var out strings.Builder
	if rc := SelfCheck(selfCheckOpts(t, settings, run), &out); rc != 0 {
		t.Fatalf("rc = %d: %s", rc, out.String())
	}
	text := out.String()
	if !strings.Contains(text, `OK: AWS profile "wide"; narrowing: none — the permission set is served as-is`) {
		t.Errorf("the selected permission-set mode is not reported as healthy configuration:\n%s", text)
	}
	if strings.Contains(text, "NOTE:") || strings.Contains(text, "UN-NARROWED") {
		t.Errorf("the valid permission-set mode was turned into an alarm or warning:\n%s", text)
	}
}

func TestSelfCheckFailsWithTheLoginCommandOnALapsedSession(t *testing.T) {
	run := cannedRunner(awsauth.Output{Spawned: true, Code: 255,
		Stderr: "The SSO session associated with this profile has expired or is otherwise " +
			"invalid. To refresh this SSO session run aws sso login with the corresponding profile."}, nil)
	settings := settingsFileWith(t, `{"profile":"bedrock","unnarrowed":true}`)
	var out strings.Builder
	if rc := SelfCheck(selfCheckOpts(t, settings, run), &out); rc != 1 {
		t.Fatalf("rc = %d, want 1: %s", rc, out.String())
	}
	if !strings.Contains(out.String(), "FAIL:") ||
		!strings.Contains(out.String(), "aws sso login --profile bedrock") {
		t.Errorf("output does not fail with the login command:\n%s", out.String())
	}
}

func TestPrepareDoesNotReportRoutineUnnarrowedMode(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config")
	configBody := "[profile wide]\nsso_start_url = https://x/start\nsso_account_id = 1\n" +
		"sso_role_name = PowerUser\n"
	if err := os.WriteFile(configPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	var seen [][]string
	run := func(_ context.Context, argv []string) awsauth.Output {
		seen = append(seen, append([]string(nil), argv...))
		return awsauth.Output{Spawned: true}
	}
	settings := settingsFileWith(t, `{"profile":"wide","unnarrowed":true}`)
	opts := prepareOpts(t, settings, run)
	opts.ConfigPath = configPath
	var log strings.Builder
	broker, rc := prepare(opts, &log)
	if rc != 0 {
		t.Fatalf("prepare rc = %d: %s", rc, log.String())
	}
	if broker.Config.Profile != "wide" || broker.Config.Narrowing.Kind != awsauth.NarrowNone {
		t.Fatalf("prepare changed the explicitly selected assigned profile mode: %+v", broker.Config)
	}
	if len(seen) != 1 || strings.Join(seen[0], " ") != "aws --version" {
		t.Fatalf("prepare dependency probes = %v, want only aws --version", seen)
	}
	text := log.String()
	for _, want := range []string{`profile "wide"`, string(awsauth.FormLegacy), "8 hours"} {
		if !strings.Contains(text, want) {
			t.Errorf("routine startup facts lack %q:\n%s", want, text)
		}
	}
	for _, notice := range []string{"narrowing:", "permission set is served as-is", "UN-NARROWED"} {
		if strings.Contains(text, notice) {
			t.Errorf("routine startup log contains mode notice %q:\n%s", notice, text)
		}
	}
}

// --- startup report ---

// TestReportStartupNamesTheLoginCadence: §8 asks the service to say which SSO config
// form it resolved, because the cadence is the whole difference between the two forms.
func TestReportStartupNamesTheLoginCadence(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config")
	body := "[profile bedrock]\nsso_start_url = https://x/start\nsso_account_id = 1\n" +
		"sso_role_name = PowerUser\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	reportStartup(&out, awsauth.Config{Profile: "bedrock",
		Narrowing: awsauth.Narrowing{Kind: awsauth.NarrowRole, RoleARN: "arn:x"}}, configPath)
	text := out.String()
	for _, want := range []string{`profile "bedrock"`, "arn:x", string(awsauth.FormLegacy), "8 hours"} {
		if !strings.Contains(text, want) {
			t.Errorf("startup report does not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "UN-NARROWED") {
		t.Errorf("a narrowed configuration disclosed a widening:\n%s", text)
	}
	out.Reset()
	reportStartup(&out, awsauth.Config{Profile: "wide",
		Narrowing: awsauth.Narrowing{Kind: awsauth.NarrowNone}}, configPath)
	if !strings.Contains(out.String(), `profile "wide"`) || strings.Contains(out.String(), "narrowing:") ||
		strings.Contains(out.String(), "permission set is served as-is") {
		t.Errorf("routine startup report did not retain profile facts without a routine mode notice:\n%s", out.String())
	}
}

// --- the proactive ticker ---

// TestRunProactiveMintsImmediatelyBeforeTheFirstTick is R1: the mint is the slow
// step and the adapter's whole budget is 200 ms, so a freshly spawned daemon is warm
// by the time the jail's first request arrives.
func TestRunProactiveMintsImmediatelyBeforeTheFirstTick(t *testing.T) {
	var calls atomic.Int32
	minted := make(chan struct{})
	var once atomic.Bool
	dir := t.TempDir()
	broker := awsauth.Broker{
		StatePath: filepath.Join(dir, awsauth.StateFileName),
		LockPath:  filepath.Join(dir, awsauth.LockFileName),
		Config:    unnarrowed("bedrock"),
		Minter: awsauth.Minter{Binary: "aws", Run: func(_ context.Context, _ []string) awsauth.Output {
			calls.Add(1)
			if once.CompareAndSwap(false, true) {
				close(minted)
			}
			return processOutput(time.Now().Add(time.Hour), "ASIA1")
		}},
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	// An hour-long interval: anything that happens promptly happened BEFORE the
	// first tick.
	go func() { defer close(done); runProactive(broker, newMintTracker(nil), time.Hour, stop) }()
	select {
	case <-minted:
	case <-time.After(5 * time.Second):
		t.Fatal("runProactive did not mint before its first tick")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runProactive did not return on stop")
	}
	if calls.Load() != 1 {
		t.Errorf("minted %d times, want 1 — the cache was warm after the first", calls.Load())
	}
	if _, ok := broker.Current(); !ok {
		t.Error("the ticker minted but left nothing cached")
	}
}

// TestRunProactiveSurvivesAFailedMint: a lapsed session is a runtime state a human
// fixes with one host-side command. The ticker logs and keeps going, so the very next
// tick after a re-login succeeds with no restart — done-condition 4's mechanism.
func TestRunProactiveSurvivesAFailedMint(t *testing.T) {
	var calls atomic.Int32
	dir := t.TempDir()
	broker := awsauth.Broker{
		StatePath: filepath.Join(dir, awsauth.StateFileName),
		LockPath:  filepath.Join(dir, awsauth.LockFileName),
		Config:    unnarrowed("bedrock"),
		Minter: awsauth.Minter{Binary: "aws", Run: func(_ context.Context, _ []string) awsauth.Output {
			if calls.Add(1) == 1 {
				return awsauth.Output{Spawned: true, Code: 255,
					Stderr: "Error loading SSO Token: Token for https://x/start does not exist"}
			}
			return processOutput(time.Now().Add(time.Hour), "ASIA1")
		}},
	}
	var log strings.Builder
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); runProactive(broker, newMintTracker(&log), 5*time.Millisecond, stop) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := broker.Current(); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the ticker never recovered from a failed mint; log: %s", log.String())
		}
		time.Sleep(2 * time.Millisecond)
	}
	close(stop)
	<-done
	if !strings.Contains(log.String(), "proactive mint failed") {
		t.Errorf("the failed mint was not logged: %q", log.String())
	}
	if !strings.Contains(log.String(), "aws sso login --profile bedrock") {
		t.Errorf("the log does not carry the login command: %q", log.String())
	}
}

// TestRunProactiveDefaultsToHalfTheRemintLead: the interval has to be short enough
// that the window cannot be stepped over by one tick.
func TestRunProactiveDefaultsToHalfTheRemintLead(t *testing.T) {
	var calls atomic.Int32
	dir := t.TempDir()
	broker := awsauth.Broker{
		StatePath: filepath.Join(dir, awsauth.StateFileName),
		LockPath:  filepath.Join(dir, awsauth.LockFileName),
		Config:    unnarrowed("p"),
		Minter: awsauth.Minter{Binary: "aws",
			Run: cannedRunner(processOutput(time.Now().Add(time.Hour), "ASIA1"), &calls)},
	}
	if broker.TickInterval() != awsauth.RemintLead/2 {
		t.Fatalf("TickInterval = %v", broker.TickInterval())
	}
	// A zero interval falls back to that value rather than to a busy loop.
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); runProactive(broker, newMintTracker(nil), 0, stop) }()
	time.Sleep(20 * time.Millisecond)
	close(stop)
	<-done
	if got := calls.Load(); got != 1 {
		t.Errorf("a zero interval minted %d times in 20ms; it should have ticked once at "+
			"startup and then waited %v", got, awsauth.RemintLead/2)
	}
}

func TestDefaultStatePathIsUnderTheYoloStateDir(t *testing.T) {
	got := defaultStatePath()
	if !filepath.IsAbs(got) {
		t.Errorf("defaultStatePath = %q, want an absolute path", got)
	}
	if !strings.Contains(got, filepath.Join("yolo-jail", LoopholeName)) {
		t.Errorf("defaultStatePath = %q, want it under yolo-jail/%s", got, LoopholeName)
	}
	if filepath.Base(got) != awsauth.StateFileName {
		t.Errorf("defaultStatePath basename = %q", filepath.Base(got))
	}
}

func TestElidedBodyIsValidJSON(t *testing.T) {
	cred := awsauth.Credential{AccessKeyID: "a", SecretAccessKey: "s", SessionToken: "t",
		ExpiresAtMS: time.Now().UnixMilli()}
	if _, err := json.Marshal(cred.Elided()); err != nil {
		t.Fatal(err)
	}
}

// --- prepare: the spawn decisions and their order ---

func prepareOpts(t *testing.T, settingsPath string, run awsauth.Runner) spawnOptions {
	t.Helper()
	return spawnOptions{
		SettingsPath: settingsPath, StatePath: absState(t), AWSBinary: "aws",
		ConfigPath: filepath.Join(t.TempDir(), "config"), Runner: run,
	}
}

// TestPrepareReportsNarrowedStartupFactsAfterRefusalsPass exercises the real prepare
// path and keeps narrowed-mode details in routine startup diagnostics.
func TestPrepareReportsNarrowedStartupFactsAfterRefusalsPass(t *testing.T) {
	var log strings.Builder
	settings := settingsFileWith(t, `{"profile":"wide","role_arn":"arn:aws:iam::123456789012:role/bedrock"}`)
	opts := prepareOpts(t, settings, cannedRunner(awsauth.Output{Spawned: true}, nil))
	configBody := "[profile wide]\nsso_start_url = https://x/start\nsso_account_id = 1\n" +
		"sso_role_name = PowerUser\n"
	if err := os.WriteFile(opts.ConfigPath, []byte(configBody), 0o600); err != nil {
		t.Fatal(err)
	}
	broker, rc := prepare(opts, &log)
	if rc != 0 {
		t.Fatalf("rc = %d: %s", rc, log.String())
	}
	if broker.Config.Profile != "wide" || broker.Config.Narrowing.Kind != awsauth.NarrowRole {
		t.Errorf("broker config = %+v", broker.Config)
	}
	if filepath.Dir(broker.LockPath) != filepath.Dir(broker.StatePath) {
		t.Error("the lock is not beside the state file")
	}
	text := log.String()
	for _, want := range []string{`profile "wide"`, "AssumeRole arn:aws:iam::123456789012:role/bedrock", string(awsauth.FormLegacy)} {
		if !strings.Contains(text, want) {
			t.Errorf("startup facts do not contain %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "permission set is served as-is") || strings.Contains(text, "UN-NARROWED") {
		t.Errorf("routine startup report described an as-configured mode:\n%s", text)
	}
}

// TestPrepareRefusesInTheOrderTheLogReaderNeeds: a configuration fault is reported
// as one even on a host that also lacks the CLI.
func TestPrepareRefusesInTheOrderTheLogReaderNeeds(t *testing.T) {
	unrunnable := cannedRunner(awsauth.Output{Spawned: false, Code: -1,
		Stderr: `exec: "aws": executable file not found in $PATH`}, nil)
	cases := []struct {
		name, body string
		run        awsauth.Runner
		wantRC     int
		wantText   string
	}{
		{"no permission mode outranks a missing CLI", `{"profile":"bedrock"}`, unrunnable, 2,
			"No AWS credential mode is configured"},
		{"no profile outranks a missing CLI", `{"unnarrowed":true}`, unrunnable, 2,
			"No AWS profile is configured"},
		{"a missing CLI with a good configuration", `{"profile":"bedrock","unnarrowed":true}`,
			unrunnable, 1, "AWS CLI v2 is not available"},
		{"an unparseable settings file", "{ not json",
			cannedRunner(awsauth.Output{Spawned: true}, nil), 2, "AWS settings file could not be read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log strings.Builder
			_, rc := prepare(prepareOpts(t, settingsFileWith(t, tc.body), tc.run), &log)
			if rc != tc.wantRC {
				t.Errorf("rc = %d, want %d: %s", rc, tc.wantRC, log.String())
			}
			if !strings.Contains(log.String(), tc.wantText) {
				t.Errorf("log does not contain %q:\n%s", tc.wantText, log.String())
			}
			if strings.Contains(log.String(), "serving AWS profile") {
				t.Errorf("a refusing daemon still printed its startup report:\n%s", log.String())
			}
		})
	}
}

// TestPrepareProbesTheCLIWithVersionAndNothingElse: `aws --version` needs no
// credentials, no network and no configuration, which is the whole reason it is the
// probe. A probe that needed a login could not tell a missing CLI from a lapsed
// session — the two failures §8 keeps apart.
func TestPrepareProbesTheCLIWithVersionAndNothingElse(t *testing.T) {
	var seen [][]string
	run := func(_ context.Context, argv []string) awsauth.Output {
		seen = append(seen, argv)
		return awsauth.Output{Spawned: true}
	}
	var log strings.Builder
	settings := settingsFileWith(t, `{"profile":"bedrock","unnarrowed":true}`)
	opts := prepareOpts(t, settings, run)
	opts.AWSBinary = "/opt/aws/bin/aws"
	if _, rc := prepare(opts, &log); rc != 0 {
		t.Fatalf("rc = %d: %s", rc, log.String())
	}
	if len(seen) != 1 {
		t.Fatalf("prepare ran %d commands, want exactly 1: %v", len(seen), seen)
	}
	if got := strings.Join(seen[0], " "); got != "/opt/aws/bin/aws --version" {
		t.Errorf("probe argv = %q", got)
	}
}
