package check

// envoverrides_test.go pins the env-override PREDICTION, and — like protocols_test.go — it
// drives `sectionPacks` rather than `envOverrideGap`.
//
// A test that called the predictor directly would stay green with the call site deleted,
// which is the callee-pinned/call-site-unpinned shape AGENTS.md names. Every case below
// goes through the section, so removing the wiring in packs.go turns them red — verified
// by mutation, not assumed.
//
// The fixture pack declares MADE-UP variables. The rule is the pack's declaration, never
// a name core knows (OQ-SSO8), so a pack that is not aws-auth, overridden by a variable
// no AWS SDK has heard of, is the test of that claim end to end.
//
// ⚠ THE OVERRIDES ARRIVE THROUGH env_sources. The first cut of this file delivered them
// through the shell `check` ran in and pinned a FAIL on that: no backend forwards that
// environment into a jail, so the launch it predicted would have worked.
// TestSectionPacksIgnoresTheShellCheckRunsIn is the regression test.

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const (
	widgetPointer = "WIDGET_POINTER"
	widgetToken   = "WIDGET_TOKEN"
	widgetKey     = "WIDGET_KEY"
	widgetSecret  = "WIDGET_SECRET"
	widgetProfile = "WIDGET_PROFILE"
)

// overriddenPack writes a local pack whose `kind: "env"` contribution sets WIDGET_POINTER,
// gated on a profile, and declares WIDGET_TOKEN and a ~/.widget grant as overriding it —
// aws-auth's shape in made-up names. It carries an agent and a provider too, so
// `use_profiles` can select the gating profile the way a real config does.
func overriddenPack(t *testing.T, agentBin, profile string) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `{
  "name": "widgetpack",
  "contributes": [
    {"kind": "program", "bin": "` + agentBin + `", "via": "npm",
     "package": "@example/` + agentBin + `", "protocols": ["openai"]},
    {"kind": "provider", "name": "` + profile + `",
     "endpoints": {"openai": {"base_url": "https://api.example.test/v1"}}},
    {"kind": "profile", "name": "` + profile + `", "provider": "` + profile + `"},
    {"kind": "env", "profile": "` + profile + `",
     "vars": {"` + widgetPointer + `": "http://127.0.0.1:1461/credentials"},
     "overridden_by": [
       {"vars": ["` + widgetToken + `"], "because": "the widget client sends WIDGET_TOKEN first"},
       {"vars": ["` + widgetKey + `", "` + widgetSecret + `"], "unless": ["` + widgetProfile + `"],
        "because": "the widget client signs with a static pair first"},
       {"host_file": ".widget", "because": "the widget config dir answers first"}
     ]}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// shellWith is an Options whose invoking environment carries exactly vars.
func shellWith(vars map[string]string) *Options {
	return &Options{Getenv: func(k string) string { return vars[k] }}
}

// withEnvSources returns merged with one inline env_sources entry carrying vars — the channel
// an override actually reaches a jail by.
func withEnvSources(merged *jsonx.OrderedMap, vars map[string]string) *jsonx.OrderedMap {
	entry := jsonx.NewOrderedMap()
	for _, k := range sortedKeys(vars) {
		entry.Set(k, vars[k])
	}
	merged.Set("env_sources", []any{entry})
	return merged
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tokenDelivered is the gating selection with the overriding token in env_sources.
func tokenDelivered() *jsonx.OrderedMap {
	return withEnvSources(useProfiles("someagent", "gatedprofile"),
		map[string]string{widgetToken: "frozen-token-value"})
}

// The launch refuses a jail carrying the contribution and its override, so `check` must
// FAIL. A warning would be this file's own defect with the sign flipped: `yolo check`
// exiting 0 on a config that cannot start a jail.
func TestSectionPacksPredictsTheOverrideRefusal(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r, tokenDelivered())

	if r.failed == 0 {
		t.Fatalf("a delivered override beside its contribution must FAIL the check:\n%s", buf.String())
	}
	out := buf.String()
	// The prediction must carry the launch's own words — both variables, the origin, the
	// pack's reason and the remedy — or a reader who runs `check` and then the launch gets
	// two accounts of one problem and has to reconcile them.
	for _, want := range []string{
		widgetPointer,
		widgetToken + " is delivered by " + packload.FromEnvSources,
		"the widget client sends WIDGET_TOKEN first",
		"Drop one.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the prediction should say %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "frozen-token-value") {
		t.Errorf("the prediction printed the overriding variable's VALUE:\n%s", out)
	}
}

// The same pack with NO profile selected delivers no contribution, so there is nothing to
// override — and nothing to print, not even a PASS line. It matches the launch, which
// never announces a gate it did not trip.
func TestSectionPacksSaysNothingWhenTheContributionIsNotDelivered(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r,
		withEnvSources(jsonx.NewOrderedMap(), map[string]string{widgetToken: "frozen-token-value"}))

	if r.failed != 0 {
		t.Errorf("a pack whose contribution is gated on an unselected profile must not fail "+
			"the check — nothing is delivered for anything to override:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), widgetToken) {
		t.Errorf("an undelivered contribution must produce no override line at all:\n%s", buf.String())
	}
}

// NO OVERRIDE, NO REFUSAL: the contribution alone is the pack's own working shape.
func TestSectionPacksIgnoresTheContributionAlone(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: func(string) string { return "" }}).
		sectionPacks(r, useProfiles("someagent", "gatedprofile"))

	if r.failed != 0 {
		t.Errorf("the contribution alone is the pack working as designed:\n%s", buf.String())
	}
}

// THE SHELL `check` RUNS IN IS NOT A DELIVERY. A token exported there never reaches the
// jail, so the launch this predicts holds the contribution alone and works; a FAIL would be
// `check` refusing a config that launches. That is the regression the first cut shipped.
func TestSectionPacksIgnoresTheShellCheckRunsIn(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	shellWith(map[string]string{widgetToken: "frozen-token-value", widgetKey: "k", widgetSecret: "s"}).
		sectionPacks(r, useProfiles("someagent", "gatedprofile"))

	if r.failed != 0 {
		t.Errorf("a variable only in the invoking shell is not delivered, so it overrides "+
			"nothing:\n%s", buf.String())
	}
}

// AN `unless` MUST BE DELIVERED TOO. The pair in env_sources with the exception only in the
// shell is the jail getting the pair and not the exception — the pair still wins, so the
// check fails; with the exception delivered beside it, it steps aside.
func TestSectionPacksNeedsTheExceptionDelivered(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)
	pair := map[string]string{widgetKey: "k", widgetSecret: "s"}

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	shellWith(map[string]string{widgetProfile: "work"}).
		sectionPacks(r, withEnvSources(useProfiles("someagent", "gatedprofile"), pair))
	if r.failed == 0 || !strings.Contains(buf.String(), "Remove any one of") {
		t.Errorf("a delivered pair beside a shell-only exception must FAIL:\n%s", buf.String())
	}

	both := map[string]string{widgetKey: "k", widgetSecret: "s", widgetProfile: "work"}
	buf.Reset()
	r = &reporter{w: &buf}
	shellWith(nil).sectionPacks(r, withEnvSources(useProfiles("someagent", "gatedprofile"), both))
	if r.failed != 0 {
		t.Errorf("the pair with its exception delivered beside it must not FAIL:\n%s", buf.String())
	}
}

// A DIRECTORY GRANT COUNTS ONLY OFF macOS. There the launch runs on podman, which binds it;
// on macOS the runtime may be macos-user (never delivers a directory) or an Apple Container
// below its read-only-bind floor (declines one), and telling them apart takes the launch's
// runtime probe — so the prediction stays narrower, a false negative rather than a FAIL on a
// config that launches.
func TestSectionPacksCountsADirectoryGrantOnlyOffMacOS(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"], "host_files": ["~/.widget/"]}`)
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".widget"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		macOS bool
		fail  bool
	}{{false, true}, {true, false}} {
		var buf bytes.Buffer
		r := &reporter{w: &buf}
		(&Options{IsMacOS: tc.macOS, Getenv: func(string) string { return "" }}).
			sectionPacks(r, useProfiles("someagent", "gatedprofile"))
		if got := strings.Contains(buf.String(), "renders ~/.widget into the jail"); got != tc.fail {
			t.Errorf("IsMacOS=%v: predicted the directory grant=%v, want %v:\n%s",
				tc.macOS, got, tc.fail, buf.String())
		}
	}
}

// THE SECRET CHANNEL IS SEEN. `env_sources` is where a jail using the bearer today declares
// it, so a prediction blind to it would miss the single most likely spelling of this
// conflict — an existing Bedrock jail whose owner adds aws-auth.
func TestSectionPacksReadsTheOverrideFromEnvSources(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dotenv := filepath.Join(home, "widget.env")
	if err := os.WriteFile(dotenv, []byte(widgetToken+"=frozen\n"), 0o600); err != nil {
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

	merged := useProfiles("someagent", "gatedprofile")
	merged.Set("env_sources", []any{dotenv})

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).
		sectionPacks(r, merged)

	if r.failed == 0 {
		t.Fatalf("an override hydrated from env_sources beside a pack contribution must "+
			"FAIL:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), packload.FromEnvSources) {
		t.Errorf("the refusal must name env_sources as the override's origin, or the reader "+
			"cannot find the declaration:\n%s", buf.String())
	}
}

// THE GRANT HALF IS PREDICTED HERE TOO. It was a ValidateConfig error until OQ-SSO8 moved
// it into the pack's declaration; a source-less host_files entry under the declared path
// renders a file, so it overrides the contribution and the check fails.
func TestSectionPacksPredictsTheHostFilesGrant(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	merged := useProfiles("someagent", "gatedprofile")
	entry := jsonx.NewOrderedMap()
	entry.Set("path", "~/.widget/config")
	entry.Set("content", "x = 1\n")
	merged.Set("host_files", []any{entry})

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Getenv: func(string) string { return "" }}).sectionPacks(r, merged)

	if r.failed == 0 {
		t.Fatalf("a rendered ~/.widget/config beside the contribution must FAIL:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "A host_files entry renders ~/.widget/config into the jail.") {
		t.Errorf("the prediction must name the rendered destination:\n%s", buf.String())
	}
}

// TestEnvOverrideGapWordsItTheWayTheLaunchDoes is the anti-drift pin behind this file's ⚠:
// the prediction is not a second copy of the rule, so its text is byte-identical to what
// packload.EnvOverrideRefusal hands the launch. If someone reimplements the message here,
// this fails.
func TestEnvOverrideGapWordsItTheWayTheLaunchDoes(t *testing.T) {
	pack := overriddenPack(t, "someagent", "gatedprofile")
	packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

	errs, warns := envOverrideGap(nil, tokenDelivered(), t.TempDir(), true, func(string) {})
	if len(warns) != 0 {
		t.Errorf("this gate has no escape hatch, so it has no warning arm: %v", warns)
	}
	// With no packs loaded there is no declaration, so nothing is reported — which is also
	// the control for the assertion below.
	if len(errs) != 0 {
		t.Fatalf("no declaration produced a refusal: %v", errs)
	}

	// The effective name is the address's last segment (packload.Pack.Name's rule), which
	// for a file:// fixture is its temp directory's name — never the manifest's `name`.
	loaded, problems := packload.LoadDir(pack, filepath.Base(pack))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	want := packload.EnvOverrideRefusal([]*packload.Pack{loaded},
		map[string]string{"someagent": "gatedprofile"},
		func(name string) (string, bool) {
			if name == widgetToken {
				return packload.FromEnvSources, true
			}
			return "", false
		}, nil)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r, tokenDelivered())
	for _, line := range want {
		if !strings.Contains(buf.String(), strings.TrimSpace(line)) {
			t.Errorf("the section did not render the launch's own line verbatim:\nwant: %s\ngot:\n%s",
				line, buf.String())
		}
	}
}
