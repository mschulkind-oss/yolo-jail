package config

// validateactiveprovider_test.go pins the launch's validation (ValidateConfig, what the launch
// preflight and `yolo check` both run) of two readable-config keys at the wrong scope or shape:
// `active_provider` (OQ-RC14, user-scope only, never beside the legacy `profile` key) and
// `requests` in an old-format user file (a project's key, docs/reference/config/user-config.md#what-a-jail-may-do-with-your-logins).

import (
	"path/filepath"
	"strings"
	"testing"
)

func validateLoaded(t *testing.T, ws string) []string {
	t.Helper()
	cfg, err := LoadConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, ws, nil)
	return errs
}

func errsMentioning(errs []string, needle string) []string {
	var out []string
	for _, e := range errs {
		if strings.Contains(e, needle) {
			out = append(out, e)
		}
	}
	return out
}

func TestActiveProviderInTheTOMLUserFileValidates(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(userDir, "config.toml"),
		"[active_provider]\ndefault = \"bedrock\"\npi = [\"bedrock\", \"codex\"]\nclaude = \"native\"\n")
	if got := errsMentioning(validateLoaded(t, ws), "active_provider"); len(got) != 0 {
		t.Fatalf("a well-formed active_provider in the user file was refused: %v", got)
	}
}

func TestActiveProviderShapeIsChecked(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(userDir, "config.jsonc"), `{"active_provider": {"claude": 7}}`)
	got := errsMentioning(validateLoaded(t, ws), "config.active_provider.claude")
	if len(got) != 1 || !strings.Contains(got[0], "yolo config set active_provider.default") {
		t.Fatalf("errors = %v, want one shape refusal naming the fix", got)
	}
	writeConvertLaterFile(t, filepath.Join(userDir, "config.jsonc"), `{"active_provider": "bedrock"}`)
	if got := errsMentioning(validateLoaded(t, ws), "config.active_provider: expected an object"); len(got) != 1 {
		t.Fatalf("a string active_provider was not refused as not a table: %v", validateLoaded(t, ws))
	}
}

func TestActiveProviderRefusedBesideTheLegacyProfileKey(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(userDir, "config.toml"), "[active_provider]\ndefault = \"bedrock\"\n")
	writeConvertLaterFile(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"profile": "bedrock"}`)
	got := errsMentioning(validateLoaded(t, ws), activeProviderWithProfileProblem)
	if len(got) != 1 || !strings.Contains(got[0], "Remove `profile`") {
		t.Fatalf("errors = %v, want one refusal of both selection keys", validateLoaded(t, ws))
	}
}

func TestActiveProviderRefusedInAWorkspaceFile(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"active_provider": {"default": "bedrock"}}`)
	got := errsMentioning(validateLoaded(t, ws), "config.active_provider: user-scope only")
	if len(got) != 1 {
		t.Fatalf("errors = %v, want one user-scope refusal", validateLoaded(t, ws))
	}
	for _, want := range []string{filepath.Join(userDir, "config.toml"), "yolo config set active_provider.<agent> <provider> --user"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("refusal lacks %q: %s", want, got[0])
		}
	}
}

func TestRequestsRefusedInTheJSONCUserFile(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(userDir, "config.jsonc"), `{"requests": {"git": {"push": ["main"]}}}`)
	got := errsMentioning(validateLoaded(t, ws), "config.requests: a project's key")
	if len(got) != 1 {
		t.Fatalf("errors = %v, want one refusal of requests in the user file", validateLoaded(t, ws))
	}
	for _, want := range []string{filepath.Join(userDir, "config.jsonc"), "yolo request git push main"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("refusal lacks %q: %s", want, got[0])
		}
	}
}

func TestRequestsInTheProjectFileAreNotRefusedAsUserScope(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(userDir, "config.jsonc"), `{}`)
	writeConvertLaterFile(t, filepath.Join(ws, "yolo-jail.toml"), "[requests.git]\npush = [\"main\"]\n")
	if got := errsMentioning(validateLoaded(t, ws), "config.requests: a project's key"); len(got) != 0 {
		t.Fatalf("a project's requests were refused as the user config's: %v", got)
	}
}

// "*" was the old `profile` table's every-agent key; `active_provider` renamed it `default`
// (RC-D4), so "*" names no agent and is refused with the fix.
func TestActiveProviderRefusesAStarKeyNamingDefault(t *testing.T) {
	userDir, ws := convertLaterEnv(t)
	writeConvertLaterFile(t, filepath.Join(userDir, "config.toml"), "[active_provider]\n\"*\" = \"bedrock\"\n")
	got := errsMentioning(validateLoaded(t, ws), "config.active_provider.*")
	if len(got) != 1 || !strings.Contains(got[0], "default") {
		t.Fatalf("errors = %v, want one refusal of the \"*\" key naming `default`", validateLoaded(t, ws))
	}
}
