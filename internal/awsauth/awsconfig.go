package awsauth

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// awsconfig.go answers ONE question about the host's ~/.aws/config: which SSO
// config form the configured profile uses.
//
// # Why the question is worth answering at all
//
// Design §8 supports BOTH forms and the jail behaves identically under each. What
// differs is how often a HUMAN has to act: the legacy profile form has no refresh
// token, so the login lasts eight hours and cannot be refreshed automatically, while
// the `sso-session` token-provider form carries one and the portal session can run to
// ninety days. That is a cadence a user is signing up for, and §8 says the service
// should report which form it resolved at startup so the cadence is visible rather
// than discovered.
//
// # It is NOT a gate, and must not become one
//
// Nothing branches on this to refuse anything. A profile that is not an SSO profile
// at all — static keys, `credential_process` — is served the same way, because the
// service resolves a profile and how that profile gets its credentials is AWS's
// problem. Neither form is special-cased beyond this report.
//
// # Hand-parsed, ~40 lines, on purpose
//
// No INI parser is vendored and adding one for four key lookups would be a
// dependency for a diagnostic. This reads the file the way the AWS CLI's own grammar
// is documented: `[default]`, `[profile NAME]`, `[sso-session NAME]` and
// `[services NAME]` sections, `key = value` lines, `#` and `;` comments.

// ConfigForm is which SSO config form a profile uses.
type ConfigForm string

const (
	// FormTokenProvider is the `sso-session` token-provider form: a refresh token
	// exists, so the access token refreshes automatically and the portal session
	// (up to 90 days) is the only thing a human renews.
	FormTokenProvider ConfigForm = "sso-session"
	// FormLegacy is the profile-only SSO form: no refresh token exists, so the
	// session is fixed at eight hours and a human logs in again that often. The
	// jail's behaviour is unchanged either way.
	FormLegacy ConfigForm = "legacy-sso"
	// FormNonSSO is a profile that is not an SSO profile. Served identically.
	FormNonSSO ConfigForm = "non-sso"
	// FormUnknown is "there is no config file, or it does not name this profile".
	// Not an error here: the mint reports a missing profile with AWS's own words.
	FormUnknown ConfigForm = "unknown"
)

// Cadence is the one-line human summary §8 asks the service to print at startup.
func (f ConfigForm) Cadence() string {
	switch f {
	case FormTokenProvider:
		return "sso-session token-provider form: the access token refreshes itself, so a " +
			"human logs in again only when the portal session ends (up to 90 days)"
	case FormLegacy:
		return "legacy profile-only SSO form: NOTHING refreshes, so `aws sso login` is " +
			"needed roughly every 8 hours. That is a login every 8 hours, not a jail every " +
			"8 hours — a running jail picks up the new session with no restart"
	case FormNonSSO:
		return "not an SSO profile: whatever this profile resolves with is served the same way"
	default:
		return "no SSO config form could be read for this profile"
	}
}

// DefaultConfigPath is AWS_CONFIG_FILE, else ~/.aws/config.
func DefaultConfigPath() string {
	if p := os.Getenv("AWS_CONFIG_FILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".aws", "config")
}

// DetectForm reports the SSO config form of one profile.
//
// An absent file or an absent profile is FormUnknown with a nil error — the caller
// is reporting a cadence, not validating a configuration, and the mint reports a
// missing profile in AWS's own words. Only an unreadable-but-present file errors.
func DetectForm(configPath, profile string) (ConfigForm, error) {
	if configPath == "" || profile == "" {
		return FormUnknown, nil
	}
	file, err := os.Open(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FormUnknown, nil
		}
		return FormUnknown, err
	}
	defer file.Close()

	// The section a profile named X lives in is `[profile X]`, except for the
	// default profile, which is bare `[default]`.
	want := "profile " + profile
	if profile == "default" {
		want = "default"
	}
	keys := map[string]bool{}
	inSection := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			// Section headers may carry inner whitespace runs; collapse them so
			// `[profile  foo]` and `[profile foo]` are the same section.
			name := strings.Join(strings.Fields(line[1:len(line)-1]), " ")
			inSection = name == want
			continue
		}
		if !inSection {
			continue
		}
		if key, _, ok := strings.Cut(line, "="); ok {
			keys[strings.ToLower(strings.TrimSpace(key))] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return FormUnknown, err
	}
	switch {
	case len(keys) == 0:
		return FormUnknown, nil
	case keys["sso_session"]:
		// The discriminator, and the ONLY one: `sso_session` is what points a
		// profile at an `[sso-session NAME]` block, which is what carries a refresh
		// token. A profile with both `sso_session` and the legacy keys is the
		// token-provider form — the CLI reads the session block.
		return FormTokenProvider, nil
	case keys["sso_start_url"] || (keys["sso_account_id"] && keys["sso_role_name"]):
		return FormLegacy, nil
	default:
		return FormNonSSO, nil
	}
}
