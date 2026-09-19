package check

// awschannels_test.go pins the AWS exclusivity PREDICTION, and — like protocols_test.go —
// it drives `sectionPacks` rather than `awsCredentialChannelGap`.
//
// A test that called the predictor directly would stay green with the call site deleted,
// which is the callee-pinned/call-site-unpinned shape AGENTS.md names. Every case below
// goes through the section, so removing the wiring in packs.go turns them red — verified
// by mutation, not assumed.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awschain"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// pointerPack writes a local pack whose `kind: "env"` contribution delivers the
// container-credentials pointer, gated on a profile — the shape `packs/aws-auth` ships.
//
// A local fixture rather than the shipped pack, for the reason the rule itself is written
// the way it is: the prediction keys on the VARIABLE a launch would deliver, not on a pack
// name, so a fixture pack that is not aws-auth is the test of that claim. It carries an
// agent and a provider too, so `use_profiles` can select the gating profile the way a real
// config does.
func pointerPack(t *testing.T, agentBin, profile string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
  "name": "pointerpack",
  "contributes": [
    {"kind": "program", "bin": "` + agentBin + `", "via": "npm",
     "package": "@example/` + agentBin + `", "protocols": ["openai"]},
    {"kind": "provider", "name": "` + profile + `",
     "endpoints": {"openai": {"base_url": "https://api.example.test/v1"}}},
    {"kind": "profile", "name": "` + profile + `", "provider": "` + profile + `"},
    {"kind": "env", "profile": "` + profile + `",
     "vars": {"` + awschain.PointerVar + `": "http://127.0.0.1:1461/credentials"}}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// bearerInTheShell is an Options whose invoking environment carries the frozen bearer —
// the pre-existing `env_sources`-style setup §8 of docs/design/sso-backed-bedrock.md
// promises keeps working on its own.
func bearerInTheShell() *Options {
	return &Options{Getenv: func(k string) string {
		if k == awschain.BearerTokenVar {
			return "sk-bedrock-frozen"
		}
		return ""
	}}
}

// The launch refuses a jail carrying both channels, so `check` must FAIL. A warning would
// be this file's own defect with the sign flipped: `yolo check` exiting 0 on a config that
// cannot start a jail.
func TestSectionPacksPredictsTheAWSChannelRefusal(t *testing.T) {
	pack := pointerPack(t, "someagent", "bedrock")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	bearerInTheShell().sectionPacks(r, useProfiles("someagent", "bedrock"))

	if r.failed == 0 {
		t.Fatalf("two delivered AWS credential channels must FAIL the check:\n%s", buf.String())
	}
	out := buf.String()
	// The prediction must carry the launch's own words — both variables and both
	// origins — or a reader who runs `check` and then the launch gets two accounts of
	// one problem and has to reconcile them.
	for _, want := range []string{
		awschain.BearerTokenVar,
		awschain.PointerVar,
		awschain.FromLaunchEnv,
		awschain.FromPackEnv,
		"Drop one.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the prediction should name %q:\n%s", want, out)
		}
	}
}

// The same pack with NO profile selected delivers no pointer, so there is nothing to
// refuse — and nothing to print, not even a PASS line. It matches the launch, which never
// announces a gate it did not trip, and it keeps the golden that pins section ordering and
// the pass/warn/fail counts from moving.
func TestSectionPacksSaysNothingWhenOnlyOneAWSChannelIsDelivered(t *testing.T) {
	pack := pointerPack(t, "someagent", "bedrock")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	bearerInTheShell().sectionPacks(r, jsonx.NewOrderedMap())

	if r.failed != 0 {
		t.Errorf("a pack whose pointer is gated on an unselected profile must not fail the "+
			"check — the SDK never reaches the container provider, so there is no silent "+
			"winner:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), awschain.BearerTokenVar) {
		t.Errorf("a one-armed config must produce no AWS line at all:\n%s", buf.String())
	}
}

// NO BEARER, NO CONFLICT: the pointer alone is `packs/aws-auth`'s own working shape, and a
// prediction that failed on it would refuse the feature this rule exists to protect.
func TestSectionPacksIgnoresThePointerWithoutTheBearer(t *testing.T) {
	pack := pointerPack(t, "someagent", "bedrock")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: func(string) string { return "" }}).
		sectionPacks(r, useProfiles("someagent", "bedrock"))

	if r.failed != 0 {
		t.Errorf("the pointer alone is the feature working as designed:\n%s", buf.String())
	}
}

// THE SECRET CHANNEL IS SEEN. `env_sources` is where a jail using the frozen bearer today
// declares it, so a prediction blind to it would miss the single most likely spelling of
// this conflict — an existing Bedrock jail whose owner adds the aws-auth pack.
func TestSectionPacksReadsTheBearerFromEnvSources(t *testing.T) {
	pack := pointerPack(t, "someagent", "bedrock")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dotenv := filepath.Join(home, "bedrock.env")
	if err := os.WriteFile(dotenv, []byte(awschain.BearerTokenVar+"=sk-bedrock-frozen\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"packs": ["file://` + pack + `"], "env_sources": ["` + dotenv + `"]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	merged := useProfiles("someagent", "bedrock")
	sources := []any{dotenv}
	merged.Set("env_sources", sources)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).
		sectionPacks(r, merged)

	if r.failed == 0 {
		t.Fatalf("a bearer hydrated from env_sources beside a pack-shipped pointer must "+
			"FAIL:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), awschain.FromEnvSources) {
		t.Errorf("the refusal must name env_sources as the bearer's origin, or the reader "+
			"cannot find the declaration:\n%s", buf.String())
	}
}

// TestAWSChannelGapWordsItTheWayTheLaunchDoes is the anti-drift pin behind this file's ⚠:
// the prediction is not a second copy of the rule, so its text is byte-identical to what
// `awschain.ExclusivityRefusal` hands the launch. If someone reimplements the message
// here, this fails.
func TestAWSChannelGapWordsItTheWayTheLaunchDoes(t *testing.T) {
	pack := pointerPack(t, "someagent", "bedrock")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	errs, warns := awsCredentialChannelGap(nil, useProfiles("someagent", "bedrock"),
		t.TempDir(), bearerInTheShell().Getenv, func(string) {})
	if len(warns) != 0 {
		t.Errorf("this gate has no escape hatch, so it has no warning arm: %v", warns)
	}
	// With no packs loaded only one channel is delivered, so nothing is reported — which
	// is also the control for the assertion below.
	if len(errs) != 0 {
		t.Fatalf("one delivered channel produced a refusal: %v", errs)
	}

	want := awschain.ExclusivityRefusal(func(name string) (string, bool) {
		switch name {
		case awschain.BearerTokenVar:
			return awschain.FromLaunchEnv, true
		case awschain.PointerVar:
			return awschain.FromPackEnv, true
		}
		return "", false
	})
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	bearerInTheShell().sectionPacks(r, useProfiles("someagent", "bedrock"))
	for _, line := range want {
		if !strings.Contains(buf.String(), strings.TrimSpace(line)) {
			t.Errorf("the section did not render the launch's own line verbatim:\nwant: %s\ngot:\n%s",
				line, buf.String())
		}
	}
}
