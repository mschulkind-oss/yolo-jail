package awsauth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// settings_test.go covers the STEP 2 half of the design: the narrowing setting and
// every way a configuration can fail to describe one. OQ-SSO1's property is that
// ABSENCE IS NEVER UN-NARROWED, so the absent case is the first test here.

func writeSettingsFile(t *testing.T, s Settings) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAbsentNarrowingRefusesAndNamesBothKeys(t *testing.T) {
	_, err := Settings{Profile: "bedrock"}.Resolve()
	if err == nil {
		t.Fatal("a profile with no narrowing resolved; OQ-SSO1 requires a narrowing by default")
	}
	for _, want := range []string{
		settingsScope(SettingRoleARN), settingsScope(SettingUnnarrowed),
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s — the reader is about to edit a file:\n%v", want, err)
		}
	}
}

func TestAbsentProfileRefusesAndNamesTheKeyAndTheScope(t *testing.T) {
	_, err := Settings{Unnarrowed: true}.Resolve()
	if err == nil {
		t.Fatal("no profile resolved to a servable configuration")
	}
	if !strings.Contains(err.Error(), settingsScope(SettingProfile)) {
		t.Errorf("refusal does not name the profile key: %v", err)
	}
	// The user-scope half is the security half (OQ-SSO4): a workspace file is one
	// the jail's own agent can rewrite.
	if !strings.Contains(err.Error(), "USER config") {
		t.Errorf("refusal does not say the key is user-scope: %v", err)
	}
}

func TestSessionPolicyArmIsN2(t *testing.T) {
	cfg, err := Settings{
		Profile: "bedrock", RoleARN: "arn:aws:iam::1:role/r",
		SessionPolicy: `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"bedrock:InvokeModel","Resource":"*"}]}`,
	}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Narrowing.Kind != NarrowSessionPolicy {
		t.Errorf("kind = %q, want %q", cfg.Narrowing.Kind, NarrowSessionPolicy)
	}
	if cfg.Narrowing.DisclosureLine(cfg.Profile) != "" {
		t.Error("a narrowed configuration produced an un-narrowed disclosure line")
	}
}

func TestRoleOnlyArmIsN3(t *testing.T) {
	cfg, err := Settings{Profile: "bedrock", RoleARN: "arn:aws:iam::1:role/r"}.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Narrowing.Kind != NarrowRole {
		t.Errorf("kind = %q, want %q", cfg.Narrowing.Kind, NarrowRole)
	}
	if cfg.Narrowing.SessionPolicy != "" {
		t.Error("a role-only arm carried a session policy")
	}
}

func TestUnnarrowedByNameServesAndDisclosesOnce(t *testing.T) {
	cfg, err := Settings{Profile: "wide", Unnarrowed: true}.Resolve()
	if err != nil {
		t.Fatalf("un-narrowed asked for BY NAME must be servable: %v", err)
	}
	if cfg.Narrowing.Kind != NarrowNone {
		t.Fatalf("kind = %q, want %q", cfg.Narrowing.Kind, NarrowNone)
	}
	line := cfg.Narrowing.DisclosureLine(cfg.Profile)
	if line == "" {
		t.Fatal("serving un-narrowed produced no disclosure line; OQ-SSO1 discloses at every launch")
	}
	for _, want := range []string{"UN-NARROWED", "wide", settingsScope(SettingUnnarrowed)} {
		if !strings.Contains(line, want) {
			t.Errorf("disclosure line does not contain %q: %s", want, line)
		}
	}
}

func TestPolicyWithoutARoleIsRefusedRatherThanIgnored(t *testing.T) {
	_, err := Settings{Profile: "p", SessionPolicy: `{"Statement":[]}`}.Resolve()
	if err == nil {
		t.Fatal("a session policy with no role resolved; it would have served un-narrowed")
	}
	if !strings.Contains(err.Error(), settingsScope(SettingRoleARN)) {
		t.Errorf("refusal does not name the missing role key: %v", err)
	}
}

func TestBothArmsAtOnceIsRefusedInsteadOfGuessed(t *testing.T) {
	_, err := Settings{Profile: "p", RoleARN: "arn:x", Unnarrowed: true}.Resolve()
	if err == nil {
		t.Fatal("unnarrowed=true with a role resolved; which wins is not guessable")
	}
	for _, want := range []string{settingsScope(SettingUnnarrowed), settingsScope(SettingRoleARN)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not name %s: %v", want, err)
		}
	}
}

func TestMalformedSessionPolicyIsRefusedAtSpawnNotPerRequest(t *testing.T) {
	cases := map[string]string{
		"not json":      "Allow bedrock:InvokeModel",
		"empty object":  "{}",
		"no statements": `{"Version":"2012-10-17"}`,
	}
	for name, policy := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Settings{Profile: "p", RoleARN: "arn:x", SessionPolicy: policy}.Resolve()
			if err == nil {
				t.Fatalf("policy %q resolved; STS would refuse every mint", policy)
			}
			if !strings.Contains(err.Error(), settingsScope(SettingSessionPolicy)) {
				t.Errorf("refusal does not name the policy key: %v", err)
			}
		})
	}
}

func TestLoadSettingsTreatsAbsenceAndCorruptionDifferently(t *testing.T) {
	// No path: the daemon was run by hand. Not a fault.
	if s, err := LoadSettings(""); err != nil || s != (Settings{}) {
		t.Errorf("LoadSettings(\"\") = %+v, %v; want a zero value and no error", s, err)
	}
	// Absent file: no jail has launched this loophole yet — the normal state of a
	// fresh machine, which `yolo check` runs on.
	missing := filepath.Join(t.TempDir(), "settings.json")
	if s, err := LoadSettings(missing); err != nil || s != (Settings{}) {
		t.Errorf("LoadSettings(absent) = %+v, %v; want a zero value and no error", s, err)
	}
	// Present and unparseable: a real fault, and the one thing nothing else reports.
	bad := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSettings(bad); err == nil {
		t.Error("an unparseable settings file loaded silently")
	}
}

func TestLoadSettingsReadsTheFlatFileYoloWrites(t *testing.T) {
	// The shape is internal/loopholes.WriteSettings': a flat JSON object, one entry
	// per declared key, every key present.
	path := filepath.Join(t.TempDir(), "settings.json")
	flat := `{"profile":"bedrock","role_arn":"arn:aws:iam::1:role/r","session_policy":"","unnarrowed":false}`
	if err := os.WriteFile(path, []byte(flat+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Profile != "bedrock" || cfg.Narrowing.Kind != NarrowRole {
		t.Errorf("resolved %+v from %s", cfg, flat)
	}
}

func TestLoadSettingsRoundTripsEveryKey(t *testing.T) {
	want := Settings{Profile: "p", RoleARN: "arn:x",
		SessionPolicy: `{"Statement":[]}`, Unnarrowed: false}
	got, err := LoadSettings(writeSettingsFile(t, want))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// TestNarrowingDigestCoversThePolicyText: editing the policy without editing the
// role must invalidate a cached credential. The digest is what the broker compares.
func TestNarrowingDigestCoversThePolicyText(t *testing.T) {
	a := Narrowing{Kind: NarrowSessionPolicy, RoleARN: "arn:x", SessionPolicy: `{"Statement":[1]}`}
	b := Narrowing{Kind: NarrowSessionPolicy, RoleARN: "arn:x", SessionPolicy: `{"Statement":[2]}`}
	if a.Digest() == b.Digest() {
		t.Error("two different session policies share a digest; a policy edit would serve a stale credential")
	}
	if a.Digest() != (Narrowing{Kind: NarrowSessionPolicy, RoleARN: "arn:x",
		SessionPolicy: `{"Statement":[1]}`}).Digest() {
		t.Error("the digest is not stable for one configuration")
	}
	if (Narrowing{Kind: NarrowRole, RoleARN: "arn:x"}).Digest() ==
		(Narrowing{Kind: NarrowNone}).Digest() {
		t.Error("a role arm and the un-narrowed arm share a digest")
	}
}

// TestDescribeNeverPrintsThePolicyBody: Describe lands in a startup line and a
// `yolo check` row. A policy body is not a secret, but it is long and it is not what
// either reader wants; the fingerprint is.
func TestDescribeNeverPrintsThePolicyBody(t *testing.T) {
	policy := `{"Statement":[{"Sid":"SECRETLOOKINGSID"}]}`
	got := Narrowing{Kind: NarrowSessionPolicy, RoleARN: "arn:x", SessionPolicy: policy}.Describe()
	if strings.Contains(got, "SECRETLOOKINGSID") {
		t.Errorf("Describe printed the policy body: %s", got)
	}
	if !strings.Contains(got, Fingerprint(policy)) {
		t.Errorf("Describe does not identify the policy by fingerprint: %s", got)
	}
}
