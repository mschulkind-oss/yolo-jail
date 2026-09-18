package awsauth

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mint_test.go exercises the AWS half through the Runner seam. NO TEST IN THIS FILE
// EXECUTES THE REAL `aws` BINARY OR TOUCHES THE NETWORK, and none may: the whole
// point of the seam is that this code is testable on a machine with no AWS
// configuration at all.

const (
	processJSON = `{"Version":1,"AccessKeyId":"ASIAEXPORT","SecretAccessKey":"export-secret",` +
		`"SessionToken":"export-token","Expiration":"2033-05-18T03:33:20+00:00"}`
	assumeJSON = `{"Credentials":{"AccessKeyId":"ASIAASSUME","SecretAccessKey":"assume-secret",` +
		`"SessionToken":"assume-token","Expiration":"2033-05-18T03:33:20Z"},` +
		`"AssumedRoleUser":{"Arn":"arn:aws:sts::1:assumed-role/r/yolo-jail"}}`
)

// fakeRunner returns a Runner serving canned Outputs in order, and a pointer to the
// argv list it was asked to run.
func fakeRunner(outs ...Output) (Runner, *[][]string) {
	var seen [][]string
	i := 0
	return func(_ context.Context, argv []string) Output {
		seen = append(seen, argv)
		out := outs[min(i, len(outs)-1)]
		i++
		return out
	}, &seen
}

func okOut(stdout string) Output { return Output{Stdout: stdout, Code: 0, Spawned: true} }

func failOut(stderr string) Output { return Output{Stderr: stderr, Code: 255, Spawned: true} }

func testMinter(t *testing.T, cfg Config, run Runner) Minter {
	t.Helper()
	return Minter{Config: cfg, Run: run, Binary: "aws",
		Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
}

// TestTheContainerProtocolSpellsTheTokenToken is the trap the plan names: `aws
// --format process` says SessionToken, the container-credentials protocol says Token,
// and an SDK rejects a body missing that field WITHOUT naming it.
func TestTheContainerProtocolSpellsTheTokenToken(t *testing.T) {
	run, _ := fakeRunner(okOut(processJSON))
	cred, err := testMinter(t, Config{Profile: "p", Narrowing: Narrowing{Kind: NarrowNone}}, run).
		Mint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	body := cred.ContainerCredentials()
	if len(body) != 4 {
		t.Errorf("body has %d keys, want exactly 4: %v", len(body), body)
	}
	for _, key := range []string{"AccessKeyId", "SecretAccessKey", "Token", "Expiration"} {
		v, ok := body[key]
		if !ok {
			t.Fatalf("body has no %q: %v", key, body)
		}
		if s, isStr := v.(string); !isStr || s == "" {
			t.Errorf("body[%q] = %#v, want a non-empty string", key, v)
		}
	}
	if _, leaked := body["SessionToken"]; leaked {
		t.Error("body carries SessionToken — that is the `aws` spelling, not the protocol's")
	}
	if body["Token"] != "export-token" {
		t.Errorf("Token = %v, want the SessionToken value", body["Token"])
	}
	// RFC3339, because that is what the protocol carries.
	if _, err := time.Parse(time.RFC3339, body["Expiration"].(string)); err != nil {
		t.Errorf("Expiration %q is not RFC3339: %v", body["Expiration"], err)
	}
}

func TestUnnarrowedArmRunsExportCredentialsInProcessFormat(t *testing.T) {
	run, seen := fakeRunner(okOut(processJSON))
	m := testMinter(t, Config{Profile: "bedrock", Narrowing: Narrowing{Kind: NarrowNone}}, run)
	cred, err := m.Mint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(*seen) != 1 {
		t.Fatalf("ran %d commands, want 1: %v", len(*seen), *seen)
	}
	argv := strings.Join((*seen)[0], " ")
	want := "aws configure export-credentials --profile bedrock --format process"
	if argv != want {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	if cred.AccessKeyID != "ASIAEXPORT" || cred.Narrowing != string(NarrowNone) {
		t.Errorf("credential = %+v", cred)
	}
}

func TestSessionPolicyArmRunsAssumeRoleWithThePolicyAndTheChainingCeiling(t *testing.T) {
	policy := `{"Statement":[{"Effect":"Allow","Action":"bedrock:InvokeModel","Resource":"*"}]}`
	run, seen := fakeRunner(okOut(assumeJSON))
	m := testMinter(t, Config{Profile: "bedrock", Narrowing: Narrowing{
		Kind: NarrowSessionPolicy, RoleARN: "arn:aws:iam::1:role/r", SessionPolicy: policy,
	}}, run)
	cred, err := m.Mint(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	argv := (*seen)[0]
	joined := strings.Join(argv, "\x00")
	for _, want := range []string{
		"sts\x00assume-role", "--profile\x00bedrock", "--role-arn\x00arn:aws:iam::1:role/r",
		"--role-session-name\x00" + SessionName,
		"--duration-seconds\x00" + strconv.Itoa(int(MintDuration/time.Second)),
		"--policy\x00" + policy, "--output\x00json",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv is missing %q: %v", strings.ReplaceAll(want, "\x00", " "), argv)
		}
	}
	if cred.AccessKeyID != "ASIAASSUME" || cred.SessionToken != "assume-token" {
		t.Errorf("credential = %+v", cred)
	}
	if cred.Narrowing != string(NarrowSessionPolicy) {
		t.Errorf("narrowing = %q, want %q", cred.Narrowing, NarrowSessionPolicy)
	}
}

// TestRoleOnlyArmSendsNoPolicyFlag: an empty --policy would be a malformed policy
// document, so the flag has to be absent rather than empty.
func TestRoleOnlyArmSendsNoPolicyFlag(t *testing.T) {
	run, seen := fakeRunner(okOut(assumeJSON))
	m := testMinter(t, Config{Profile: "p", Narrowing: Narrowing{
		Kind: NarrowRole, RoleARN: "arn:x"}}, run)
	if _, err := m.Mint(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, a := range (*seen)[0] {
		if a == "--policy" {
			t.Errorf("role-only arm sent --policy: %v", (*seen)[0])
		}
	}
}

// TestALapsedSessionIsAMessageCarryingTheLoginCommand is OQ-SSO6: the 4xx names the
// command and a human runs it. Nothing here waits, prompts or logs in.
func TestALapsedSessionIsAMessageCarryingTheLoginCommand(t *testing.T) {
	stderrs := []string{
		"Error when retrieving token from sso: Token has expired and refresh failed",
		"The SSO session associated with this profile has expired or is otherwise invalid. " +
			"To refresh this SSO session run aws sso login with the corresponding profile.",
		"Error loading SSO Token: Token for https://example.awsapps.com/start does not exist",
		"An error occurred (ExpiredToken) when calling the AssumeRole operation: " +
			"The security token included in the request is expired",
	}
	for _, stderr := range stderrs {
		t.Run(firstLine(stderr)[:24], func(t *testing.T) {
			run, _ := fakeRunner(failOut(stderr))
			m := testMinter(t, Config{Profile: "bedrock",
				Narrowing: Narrowing{Kind: NarrowNone}}, run)
			_, err := m.Mint(context.Background())
			var mintErr *MintError
			if !asMintError(err, &mintErr) {
				t.Fatalf("err = %v (%T), want a *MintError", err, err)
			}
			if mintErr.Kind != FailureLoginRequired {
				t.Errorf("kind = %q, want %q", mintErr.Kind, FailureLoginRequired)
			}
			// VERBATIM: this string is what a human reads in the agent's error text.
			if !strings.Contains(mintErr.Message, "aws sso login --profile bedrock") {
				t.Errorf("message does not carry the login command verbatim: %s", mintErr.Message)
			}
			body := mintErr.ContainerError()
			if len(body) != 2 || body["Code"] == "" || body["Message"] == "" {
				t.Errorf("4xx body = %v, want exactly Code and Message", body)
			}
		})
	}
}

// TestAMissingProfileIsNotAdvisedToLogIn: `aws sso login --profile X` for a profile
// that does not exist is wrong advice, not merely unhelpful.
func TestAMissingProfileIsNotAdvisedToLogIn(t *testing.T) {
	run, _ := fakeRunner(failOut("The config profile (bedrock) could not be found"))
	m := testMinter(t, Config{Profile: "bedrock", Narrowing: Narrowing{Kind: NarrowNone}}, run)
	_, err := m.Mint(context.Background())
	var mintErr *MintError
	if !asMintError(err, &mintErr) {
		t.Fatalf("err = %v, want a *MintError", err)
	}
	if mintErr.Kind != FailureProfileMissing {
		t.Fatalf("kind = %q, want %q", mintErr.Kind, FailureProfileMissing)
	}
	if strings.Contains(mintErr.Message, "aws sso login") {
		t.Errorf("a missing profile was advised to log in: %s", mintErr.Message)
	}
	if !strings.Contains(mintErr.Message, settingsScope(SettingProfile)) {
		t.Errorf("message does not name the setting that is wrong: %s", mintErr.Message)
	}
}

// TestSTSRefusalIsForwardedVerbatim: STS names the thing to fix and nothing here
// knows that vocabulary better.
func TestSTSRefusalIsForwardedVerbatim(t *testing.T) {
	sts := "An error occurred (AccessDenied) when calling the AssumeRole operation: " +
		"User: arn:aws:sts::1:assumed-role/PS/me is not authorized to perform: sts:AssumeRole"
	run, _ := fakeRunner(failOut(sts))
	m := testMinter(t, Config{Profile: "p", Narrowing: Narrowing{
		Kind: NarrowRole, RoleARN: "arn:x"}}, run)
	_, err := m.Mint(context.Background())
	var mintErr *MintError
	if !asMintError(err, &mintErr) {
		t.Fatalf("err = %v, want a *MintError", err)
	}
	if mintErr.Kind != FailureRejected {
		t.Errorf("kind = %q, want %q", mintErr.Kind, FailureRejected)
	}
	if mintErr.Message != sts {
		t.Errorf("message = %q, want STS's own words", mintErr.Message)
	}
}

// TestTheAwsCLIMissingIsItsOwnFailureKind: §8 makes it a LOUD SPAWN FAILURE, and a
// requires.command_on_path probe is deliberately not the mechanism — that is the
// shape which removed the Claude broker for the user it existed for.
func TestTheAwsCLIMissingIsItsOwnFailureKind(t *testing.T) {
	run, _ := fakeRunner(Output{Spawned: false, Code: -1,
		Stderr: `exec: "aws": executable file not found in $PATH`})
	m := testMinter(t, Config{Profile: "p", Narrowing: Narrowing{Kind: NarrowNone}}, run)
	_, err := m.Mint(context.Background())
	var mintErr *MintError
	if !asMintError(err, &mintErr) {
		t.Fatalf("err = %v, want a *MintError", err)
	}
	if mintErr.Kind != FailureCLIMissing {
		t.Errorf("kind = %q, want %q — a missing CLI must not read as an expired session",
			mintErr.Kind, FailureCLIMissing)
	}
	if strings.Contains(mintErr.Message, "aws sso login") {
		t.Errorf("a missing CLI was advised to log in: %s", mintErr.Message)
	}
}

// TestStaticKeysAreNotServableAndSayWhy: §8 says a non-SSO profile is served the
// same way, and there is no SSO check anywhere in this package. What the
// container-credentials protocol cannot carry is a credential with no session token,
// so that becomes a named 4xx rather than a body the SDK rejects without naming a field.
func TestStaticKeysAreNotServableAndSayWhy(t *testing.T) {
	static := `{"Version":1,"AccessKeyId":"AKIASTATIC","SecretAccessKey":"s"}`
	run, _ := fakeRunner(okOut(static))
	m := testMinter(t, Config{Profile: "static", Narrowing: Narrowing{Kind: NarrowNone}}, run)
	_, err := m.Mint(context.Background())
	var mintErr *MintError
	if !asMintError(err, &mintErr) {
		t.Fatalf("err = %v, want a *MintError", err)
	}
	if mintErr.Code != "IncompleteCredential" {
		t.Errorf("code = %q, want IncompleteCredential", mintErr.Code)
	}
	for _, want := range []string{"Token", "Expiration", settingsScope(SettingRoleARN)} {
		if !strings.Contains(mintErr.Message, want) {
			t.Errorf("message does not mention %q: %s", want, mintErr.Message)
		}
	}
}

func TestUnparseableAwsOutputIsNamedRatherThanServedEmpty(t *testing.T) {
	run, _ := fakeRunner(okOut("Please login again."))
	m := testMinter(t, Config{Profile: "p", Narrowing: Narrowing{Kind: NarrowNone}}, run)
	_, err := m.Mint(context.Background())
	var mintErr *MintError
	if !asMintError(err, &mintErr) {
		t.Fatalf("err = %v, want a *MintError", err)
	}
	if mintErr.Code != "UnparseableAwsOutput" {
		t.Errorf("code = %q, want UnparseableAwsOutput", mintErr.Code)
	}
}

// TestBothExpirationSpellingsParse: `sts assume-role` renders +00:00 and other
// surfaces render Z. Both are RFC3339 and both have to land on the same instant.
func TestBothExpirationSpellingsParse(t *testing.T) {
	offset, err := parseExpiration("2033-05-18T03:33:20+00:00")
	if err != nil {
		t.Fatal(err)
	}
	zulu, err := parseExpiration("2033-05-18T03:33:20Z")
	if err != nil {
		t.Fatal(err)
	}
	if !offset.Equal(zulu) {
		t.Errorf("%v != %v", offset, zulu)
	}
	if _, err := parseExpiration("nope"); err == nil {
		t.Error("a non-timestamp parsed")
	}
}

func TestMintRefusesWithoutARunnerOrAResolvedConfig(t *testing.T) {
	if _, err := (Minter{Config: Config{Profile: "p",
		Narrowing: Narrowing{Kind: NarrowNone}}}).Mint(context.Background()); err == nil {
		t.Error("a Minter with no Runner minted")
	}
	run, _ := fakeRunner(okOut(processJSON))
	if _, err := (Minter{Run: run}).Mint(context.Background()); err == nil {
		t.Error("a Minter with no resolved config minted")
	}
}

// asMintError is errors.As spelled locally so each test reads as one assertion.
func asMintError(err error, target **MintError) bool {
	return err != nil && errors.As(err, target)
}
