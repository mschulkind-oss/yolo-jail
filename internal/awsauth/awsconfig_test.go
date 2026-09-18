package awsauth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// awsconfig_test.go reads two FIXTURE config files, as the plan's "Ships with" list
// asks: the legacy profile-only SSO form and the `sso-session` token-provider form.
// The difference between them is a human's login cadence, never the jail's behaviour,
// and nothing in this package branches on it to refuse anything.

const legacyConfig = `
# a legacy, profile-only SSO profile: nothing refreshes
[profile bedrock]
sso_start_url = https://example.awsapps.com/start
sso_region = us-east-1
sso_account_id = 111122223333
sso_role_name = PowerUserAccess
region = us-west-2

[profile keys]
aws_access_key_id = AKIAEXAMPLE
aws_secret_access_key = secret
`

const sessionConfig = `
[sso-session corp]
sso_start_url = https://example.awsapps.com/start
sso_region = us-east-1
sso_registration_scopes = sso:account:access

[profile  bedrock]
sso_session = corp
sso_account_id = 111122223333
sso_role_name = BedrockInvokeOnly
region = us-west-2

; a default profile is a BARE [default] section, not [profile default]
[default]
sso_session = corp
sso_account_id = 111122223333
sso_role_name = PowerUserAccess
`

func fixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDetectFormReadsBothSSOConfigForms(t *testing.T) {
	legacy := fixture(t, legacyConfig)
	session := fixture(t, sessionConfig)
	cases := []struct {
		name, path, profile string
		want                ConfigForm
	}{
		{"legacy profile-only", legacy, "bedrock", FormLegacy},
		{"static keys are not SSO", legacy, "keys", FormNonSSO},
		{"absent profile", legacy, "nope", FormUnknown},
		{"sso-session token provider", session, "bedrock", FormTokenProvider},
		{"the default profile has a bare section", session, "default", FormTokenProvider},
		// `[sso-session corp]` must not be mistaken for a profile named corp.
		{"an sso-session block is not a profile", session, "corp", FormUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DetectForm(tc.path, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("DetectForm = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTheCadenceLineNamesTheCostTheUserIsSigningUpFor: §8 asks the service to say
// which form it resolved, because the login cadence is the whole difference — and
// says plainly that the legacy form costs a login every eight hours, NOT a jail every
// eight hours.
func TestTheCadenceLineNamesTheCostTheUserIsSigningUpFor(t *testing.T) {
	legacy := FormLegacy.Cadence()
	if !strings.Contains(legacy, "8 hours") {
		t.Errorf("the legacy cadence line does not name the 8-hour login: %s", legacy)
	}
	if !strings.Contains(legacy, "no restart") {
		t.Errorf("the legacy cadence line does not say a running jail survives a re-login: %s", legacy)
	}
	if !strings.Contains(FormTokenProvider.Cadence(), "refresh") {
		t.Errorf("the token-provider cadence line does not mention refresh: %s",
			FormTokenProvider.Cadence())
	}
	for _, form := range []ConfigForm{FormTokenProvider, FormLegacy, FormNonSSO, FormUnknown} {
		if form.Cadence() == "" {
			t.Errorf("%q has no cadence line", form)
		}
	}
}

func TestDetectFormTreatsAnAbsentFileAsUnknownRatherThanAFault(t *testing.T) {
	missing := filepath.Join(t.TempDir(), ".aws", "config")
	got, err := DetectForm(missing, "bedrock")
	if err != nil {
		t.Fatalf("an absent ~/.aws/config errored: %v", err)
	}
	if got != FormUnknown {
		t.Errorf("DetectForm = %q, want %q", got, FormUnknown)
	}
	if got, err := DetectForm("", "bedrock"); err != nil || got != FormUnknown {
		t.Errorf("DetectForm(\"\") = %q, %v", got, err)
	}
	if got, err := DetectForm(missing, ""); err != nil || got != FormUnknown {
		t.Errorf("DetectForm(no profile) = %q, %v", got, err)
	}
}

func TestDetectFormIgnoresCommentsAndOtherSections(t *testing.T) {
	body := "[profile bedrock]\n#sso_session = corp\n;sso_start_url = x\nregion = us-west-2\n"
	got, err := DetectForm(fixture(t, body), "bedrock")
	if err != nil {
		t.Fatal(err)
	}
	if got != FormNonSSO {
		t.Errorf("DetectForm = %q, want %q — a commented key was read as live", got, FormNonSSO)
	}
}

func TestDefaultConfigPathHonoursAWSConfigFile(t *testing.T) {
	t.Setenv("AWS_CONFIG_FILE", "/tmp/elsewhere/config")
	if got := DefaultConfigPath(); got != "/tmp/elsewhere/config" {
		t.Errorf("DefaultConfigPath = %q, want the AWS_CONFIG_FILE value", got)
	}
	t.Setenv("AWS_CONFIG_FILE", "")
	t.Setenv("HOME", "/tmp/fakehome")
	if got := DefaultConfigPath(); got != filepath.Join("/tmp/fakehome", ".aws", "config") {
		t.Errorf("DefaultConfigPath = %q, want ~/.aws/config", got)
	}
}
