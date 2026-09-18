package awsauth

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// mint.go is the WHOLE AWS surface of this package: one exec seam, two argv
// builders, one parser, one classifier. Nothing else in the tree runs `aws`.
//
// # The seam exists so no test ever runs `aws`
//
// A test that shells out to the real CLI would need a real login, would reach the
// network, and would pass or fail on a machine's AWS configuration rather than on this
// code. Runner is the injection point; ExecRunner is the one production
// implementation and the only place exec.CommandContext appears.

// Output is one `aws` invocation's captured result.
//
// SPAWNED IS SEPARATE FROM CODE on purpose. "`aws` is not on this host" and "`aws`
// ran and said no" are different failures with different audiences: the first is a
// loud spawn failure the human fixes by installing the CLI, the second is a per-request
// 4xx. Collapsing them into "err != nil" is how a missing dependency comes to look
// like an expired session.
type Output struct {
	Stdout  string
	Stderr  string
	Code    int
	Spawned bool
}

// Runner runs one `aws` argv and returns what it printed.
type Runner func(ctx context.Context, argv []string) Output

// ExecRunner is the production Runner: it executes argv and captures both streams.
// A non-zero exit is NOT an error here — it is Code, because `aws`'s stderr is the
// diagnosis and discarding it for an *ExitError would throw the message away.
func ExecRunner() Runner {
	return func(ctx context.Context, argv []string) Output {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		out := Output{Stdout: stdout.String(), Stderr: stderr.String(), Spawned: true}
		if err == nil {
			return out
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			out.Code = exitErr.ExitCode()
			return out
		}
		// The binary could not be executed at all, or the context died. Either way
		// nothing ran, so there is no stderr to classify.
		out.Spawned = false
		out.Code = -1
		if out.Stderr == "" {
			out.Stderr = err.Error()
		}
		return out
	}
}

// Minter performs one mint. It holds no credential and no access token between
// calls: each Mint shells out afresh, which is what makes a host-side re-login
// transparent to a running jail (design §8, "Never hold an access token across
// mints").
type Minter struct {
	Config Config
	Run    Runner
	Now    func() time.Time
	// Timeout bounds one `aws` invocation. A mint is off the request path — the
	// ticker and the cold-cache case are its only callers — so this is generous.
	Timeout time.Duration
	// Binary is the CLI to run; "" means "aws". Tests set it only to prove the
	// argv, never to run something.
	Binary string
}

func (m Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m Minter) binary() string {
	if m.Binary != "" {
		return m.Binary
	}
	return "aws"
}

func (m Minter) timeout() time.Duration {
	if m.Timeout > 0 {
		return m.Timeout
	}
	return 30 * time.Second
}

// ExportArgv is `aws configure export-credentials --profile X --format process`,
// the un-narrowed arm's whole implementation.
//
// `--format process` is what refreshes the SSO access token itself wherever a
// refresh token exists, which is P2 inherited rather than implemented.
func (m Minter) ExportArgv() []string {
	return []string{m.binary(), "configure", "export-credentials",
		"--profile", m.Config.Profile, "--format", "process"}
}

// AssumeArgv is the N2/N3 arm: `aws sts assume-role` against the configured
// profile, so the profile's own (SSO) credentials are what call STS.
//
// ONE CALL, NOT TWO. An export followed by an assume-role would hold the exported
// credential in this process for no reason; `--profile` makes the CLI resolve it
// internally and hand it straight to STS.
func (m Minter) AssumeArgv() []string {
	argv := []string{m.binary(), "sts", "assume-role",
		"--profile", m.Config.Profile,
		"--role-arn", m.Config.Narrowing.RoleARN,
		"--role-session-name", SessionName,
		"--duration-seconds", strconv.Itoa(int(MintDuration / time.Second)),
	}
	if m.Config.Narrowing.SessionPolicy != "" {
		argv = append(argv, "--policy", m.Config.Narrowing.SessionPolicy)
	}
	return append(argv, "--output", "json")
}

// Argv is the argv this configuration mints with.
func (m Minter) Argv() []string {
	if m.Config.Narrowing.Kind == NarrowNone {
		return m.ExportArgv()
	}
	return m.AssumeArgv()
}

// Mint runs one `aws` invocation and returns the credential it yielded.
//
// Errors are always *MintError, so every caller has a Code and a Message it can put
// on the wire without deciding what is safe to say.
func (m Minter) Mint(ctx context.Context) (Credential, error) {
	if m.Run == nil {
		return Credential{}, &MintError{Kind: FailureUnavailable, Code: "NotConfigured",
			Message: "the aws-auth service has no command runner"}
	}
	if m.Config.Profile == "" || m.Config.Narrowing.Kind == "" {
		return Credential{}, &MintError{Kind: FailureUnavailable, Code: "NotConfigured",
			Message: "the aws-auth service has no resolved profile and narrowing"}
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout())
	defer cancel()
	argv := m.Argv()
	out := m.Run(ctx, argv)
	if !out.Spawned {
		return Credential{}, &MintError{Kind: FailureCLIMissing, Code: "AwsCliUnavailable",
			Message: "the `aws` CLI could not be run on the host (" + firstLine(out.Stderr) +
				") — install AWS CLI v2; this loophole depends on it and deliberately does not " +
				"probe for it, because a loophole that vanishes when its program is missing is " +
				"worse than one that says so"}
	}
	if out.Code != 0 {
		return Credential{}, classify(m.Config.Profile, out)
	}
	cred, err := parseCredential(m.Config.Narrowing.Kind, out.Stdout)
	if err != nil {
		return Credential{}, err
	}
	cred.MintedAtMS = m.now().UnixMilli()
	cred.Narrowing = string(m.Config.Narrowing.Kind)
	cred.NarrowingDigest = m.Config.Narrowing.Digest()
	if !cred.Complete() {
		return Credential{}, &MintError{Kind: FailureUnavailable, Code: "IncompleteCredential",
			Message: "profile " + m.Config.Profile + " resolved to a credential with no session " +
				"token or no expiry (long-term static keys look like this). The " +
				"container-credentials protocol requires all four of AccessKeyId, " +
				"SecretAccessKey, Token and Expiration, so there is nothing servable here — " +
				"point " + settingsScope(SettingProfile) + " at a role-session profile, or set " +
				settingsScope(SettingRoleARN) + " so a session token is minted"}
	}
	return cred, nil
}

// processFormat is `aws configure export-credentials --format process` output.
type processFormat struct {
	Version         int    `json:"Version"`
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	// SessionToken IS THE TRAP. The container protocol wants "Token"; see
	// Credential.ContainerCredentials.
	SessionToken string `json:"SessionToken"`
	Expiration   string `json:"Expiration"`
}

// assumeRoleFormat is `aws sts assume-role --output json` output. Only Credentials
// is read; AssumedRoleUser and the rest are AWS's and none of this service's business.
type assumeRoleFormat struct {
	Credentials struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
		SessionToken    string `json:"SessionToken"`
		Expiration      string `json:"Expiration"`
	} `json:"Credentials"`
}

func parseCredential(kind NarrowingKind, stdout string) (Credential, error) {
	unparseable := func(err error) error {
		return &MintError{Kind: FailureUnavailable, Code: "UnparseableAwsOutput",
			Message: "the `aws` CLI returned output this service could not parse: " + err.Error()}
	}
	var cred Credential
	var expiration string
	if kind == NarrowNone {
		var raw processFormat
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			return Credential{}, unparseable(err)
		}
		cred.AccessKeyID, cred.SecretAccessKey = raw.AccessKeyID, raw.SecretAccessKey
		cred.SessionToken, expiration = raw.SessionToken, raw.Expiration
	} else {
		var raw assumeRoleFormat
		if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
			return Credential{}, unparseable(err)
		}
		cred.AccessKeyID = raw.Credentials.AccessKeyID
		cred.SecretAccessKey = raw.Credentials.SecretAccessKey
		cred.SessionToken, expiration = raw.Credentials.SessionToken, raw.Credentials.Expiration
	}
	if expiration != "" {
		at, err := parseExpiration(expiration)
		if err != nil {
			return Credential{}, unparseable(err)
		}
		cred.ExpiresAtMS = at.UnixMilli()
	}
	return cred, nil
}

// parseExpiration accepts what the CLI actually prints. RFC3339 covers both the
// `+00:00` offset form `sts assume-role` renders and the `Z` form; the third layout is
// the offset-less spelling older `--format process` output has been seen with, read as
// UTC because AWS expiries are.
func parseExpiration(s string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05"}
	var err error
	for _, layout := range layouts {
		var at time.Time
		at, err = time.Parse(layout, s)
		if err == nil {
			return at.UTC(), nil
		}
	}
	return time.Time{}, err
}

// lapsedSignatures are the stderr fragments AWS CLI v2 emits for a session that has
// expired or was never established. Matched as lowercase substrings because the
// wording differs between the legacy profile form, the sso-session form and STS.
//
// A MISS HERE IS SAFE AND A FALSE POSITIVE IS NOT, which sets the direction: an
// unrecognised failure falls through to FailureRejected or FailureUnavailable and
// forwards AWS's own words, while a wrong match would tell a human to run a login that
// was not the problem. Keep these specific.
var lapsedSignatures = []string{
	"run aws sso login",
	"token has expired and refresh failed",
	"error loading sso token",
	"sso session associated with this profile has expired",
	"the sso session has expired",
	"expiredtoken",
	"expired or is otherwise invalid",
}

// missingProfileSignatures are the stderr fragments for a profile that is not in
// ~/.aws/config. Separated from the lapsed set because advising `aws sso login
// --profile X` for a profile that does not exist is wrong advice, not merely unhelpful.
var missingProfileSignatures = []string{
	"could not be found",
	"the config profile",
	"profilenotfound",
}

// classify turns a non-zero `aws` exit into the 4xx a jail will see.
func classify(profile string, out Output) *MintError {
	haystack := strings.ToLower(out.Stderr + "\n" + out.Stdout)
	detail := firstLine(out.Stderr)
	for _, sig := range lapsedSignatures {
		if strings.Contains(haystack, sig) {
			return loginRequired(profile, detail)
		}
	}
	for _, sig := range missingProfileSignatures {
		if strings.Contains(haystack, sig) {
			return &MintError{Kind: FailureProfileMissing, Code: "ProfileNotFound",
				Message: "the AWS profile " + profile + " named by " +
					settingsScope(SettingProfile) + " is not in the host's ~/.aws/config (aws said: " +
					detail + ")"}
		}
	}
	// AWS ANSWERED AND SAID NO. Its own message is forwarded verbatim rather than
	// summarised: a session policy or a trust policy refusal names the thing to fix,
	// and nothing here knows that vocabulary better than STS does.
	if strings.Contains(haystack, "accessdenied") || strings.Contains(haystack, "not authorized") ||
		strings.Contains(haystack, "malformedpolicydocument") ||
		strings.Contains(haystack, "invalidparameter") {
		return &MintError{Kind: FailureRejected, Code: "AssumeRoleRejected", Message: detail}
	}
	return &MintError{Kind: FailureUnavailable, Code: "MintFailed",
		Message: "the `aws` CLI exited " + strconv.Itoa(out.Code) + ": " + detail}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "no output"
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
