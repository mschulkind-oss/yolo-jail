package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// userCfg writes the machine-wide user config under a temp HOME.
func userCfg(t *testing.T, home, text string) {
	t.Helper()
	p := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHostMainAnswersHelpAndRejectsUnknownVerb(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := hostMain(nil, &out, &errw, false, nil); rc != 0 {
		t.Errorf("bare `yolo host` rc = %d, want 0", rc)
	}
	if !strings.Contains(out.String(), "yolo host") {
		t.Errorf("bare `yolo host` printed %q", out.String())
	}

	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"nonsense"}, &out, &errw, false, nil); rc != 1 {
		t.Errorf("unknown verb rc = %d, want 1", rc)
	}
	if !strings.Contains(errw.String(), "unknown verb") {
		t.Errorf("stderr = %q", errw.String())
	}
}

// TestHostExecRefusesEmptyCommand: `yolo host --` names nothing to run.
func TestHostExecRefusesEmptyCommand(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	if !strings.Contains(errw.String(), "nothing to run") {
		t.Errorf("stderr = %q", errw.String())
	}
}

// TestHostExecRejectsStrayFlagBeforeDashDash keeps the exec half from silently ignoring
// a flag it does not know — a mistyped --profile would otherwise launch with the wrong
// environment and say nothing.
func TestHostExecRejectsStrayFlagBeforeDashDash(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--bogus", "--", "claude"}, &out, &errw, false, nil); rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	if !strings.Contains(errw.String(), "unexpected argument") {
		t.Errorf("stderr = %q", errw.String())
	}
}

func TestParseHostExecFlags(t *testing.T) {
	var errw bytes.Buffer
	for _, args := range [][]string{
		{"-p", "bedrock"},
		{"--profile", "bedrock"},
		{"--profile=bedrock"},
	} {
		f, ok := parseHostExecFlags(args, &errw)
		if !ok || f.profile != "bedrock" {
			t.Errorf("parseHostExecFlags(%q) = %+v, %v", args, f, ok)
		}
	}
	if _, ok := parseHostExecFlags([]string{"-p"}, &errw); ok {
		t.Error("a dangling -p was accepted")
	}
}

// TestHostEnvExportFormat pins the default output shape: POSIX `export` lines, which is
// what OQ-3 ruled and what `eval "$(yolo host env)"` needs.
func TestHostEnvExportFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	t.Chdir(ws)
	userCfg(t, home, `{
	  "providers": {"bedrock": {"region": "us-east-1"}},
	  "agent_profiles": {"claude": "bedrock"},
	  "env_sources": [{"AWS_ACCESS_KEY_ID": "AKIAEXAMPLE", "AWS_PROFILE": null}]
	}`)

	var out, errw bytes.Buffer
	if rc := hostEnv(nil, &out, &errw); rc != 0 {
		t.Fatalf("hostEnv rc = %d, stderr = %s", rc, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		`export AWS_ACCESS_KEY_ID='AKIAEXAMPLE'`,
		`export CLAUDE_CODE_USE_BEDROCK='1'`,
		`export AWS_REGION='us-east-1'`,
		"unset AWS_PROFILE",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hostEnv output missing %q:\n%s", want, got)
		}
	}
	// The inherited environment must NOT be echoed back — it would be enormous and would
	// spill unrelated secrets into whatever the output is piped to.
	if strings.Contains(got, "export HOME=") {
		t.Errorf("hostEnv echoed the inherited environment:\n%s", got)
	}
}

// TestHostEnvJSONFormatSpellsRemovalAsNull: a consumer has to be able to tell "unset"
// from "set to empty", which is the same distinction env_sources uses.
func TestHostEnvJSONFormatSpellsRemovalAsNull(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"env_sources": [{"KEEP": "yes", "AWS_PROFILE": null}]}`)

	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--format", "json"}, &out, &errw); rc != 0 {
		t.Fatalf("rc = %d, stderr = %s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, `"KEEP": "yes"`) {
		t.Errorf("json missing KEEP:\n%s", got)
	}
	if !strings.Contains(got, `"AWS_PROFILE": null`) {
		t.Errorf("json must spell a removal as null:\n%s", got)
	}
}

func TestHostEnvRejectsUnknownFormat(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--format", "yaml"}, &out, &errw); rc != 2 {
		t.Errorf("rc = %d, want 2", rc)
	}
	if !strings.Contains(errw.String(), "unknown --format") {
		t.Errorf("stderr = %q", errw.String())
	}
}

// TestComposeHostEnvOrdering is §6.1 step 3's contract: a removal is applied LAST, so it
// beats an assignment from the inherited environment, and the profile's vars overlay the
// inherited ones.
func TestComposeHostEnvOrdering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("AWS_PROFILE", "work-sso")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{
	  "providers": {"bedrock": {"region": "us-east-1"}},
	  "agent_profiles": {"claude": "bedrock"},
	  "env_sources": [{"AWS_PROFILE": null}]
	}`)

	env, agent, err := composeHostEnv("claude", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if agent != "claude" {
		t.Errorf("agent = %q", agent)
	}
	seen := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			seen[kv[:i]] = kv[i+1:]
		}
	}
	if _, present := seen["AWS_PROFILE"]; present {
		t.Error("AWS_PROFILE survived — a null in env_sources must UNSET it, not empty it")
	}
	if seen["AWS_REGION"] != "us-east-1" {
		t.Errorf("AWS_REGION = %q, want the profile's us-east-1 to overlay the shell's", seen["AWS_REGION"])
	}
	if seen["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("CLAUDE_CODE_USE_BEDROCK = %q", seen["CLAUDE_CODE_USE_BEDROCK"])
	}
	if seen["HOME"] == "" {
		t.Error("the inherited environment was dropped")
	}
}

// TestComposeHostEnvProfileFlagOverrides: `yolo host -p bedrock -- claude` selects the
// profile for this launch only, mirroring `yolo run -p`.
func TestComposeHostEnvProfileFlagOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"providers": {"bedrock": {"region": "us-east-1"}}}`)

	env, _, err := composeHostEnv("claude", "bedrock", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEnv(env, "CLAUDE_CODE_USE_BEDROCK=1") {
		t.Error("-p bedrock did not select the profile")
	}
}

func hasEnv(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

func TestSetJSONCBoolPreservesComments(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "replaces an existing value, keeping the comment",
			in:   "{\n  // keep me\n  \"host_wrappers\": false,\n  \"packs\": []\n}\n",
			want: "{\n  // keep me\n  \"host_wrappers\": true,\n  \"packs\": []\n}\n",
		},
		{
			name: "inserts when absent",
			in:   "{\n  // keep me\n  \"packs\": []\n}\n",
			want: "{\n  \"host_wrappers\": true,\n  // keep me\n  \"packs\": []\n}\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := setJSONCBool(tc.in, "host_wrappers", true)
			if !ok {
				t.Fatal("setJSONCBool reported failure")
			}
			if got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
			if !strings.Contains(got, "// keep me") {
				t.Error("the comment was lost — the user's JSONC must survive")
			}
		})
	}
	if _, ok := setJSONCBool("not json at all", "host_wrappers", true); ok {
		t.Error("setJSONCBool claimed success on a file with no object")
	}
}

// TestApplyHostGeneratesWrappersOnlyWhenOptedIn is the end-to-end contract of OQ-4's
// first bullet: not opted in means NO directory and no messages at all.
func TestApplyHostGeneratesWrappersOnlyWhenOptedIn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"]}`)

	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	if _, err := os.Stat(paths.WrapDirUnder(home)); !os.IsNotExist(err) {
		t.Error("a wrapper directory was created without the opt-in")
	}
	if strings.Contains(out.String(), "host_wrappers") {
		t.Errorf("apply mentioned wrappers without the opt-in:\n%s", out.String())
	}
}

// TestApplyHostWrappersLifecycle walks the whole OQ-4/OQ-5 contract end to end: generate
// on opt-in, print the PATH line only on change, stay silent on an unchanged re-apply,
// and clean up when the opt-in is withdrawn.
func TestApplyHostWrappersLifecycle(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)
	dir := paths.WrapDirUnder(home)

	// First apply: writes the wrapper AND prints the PATH line.
	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	body, err := os.ReadFile(filepath.Join(dir, "claude"))
	if err != nil {
		t.Fatalf("no wrapper generated: %v", err)
	}
	if string(body) != hostwrap.Body("claude") {
		t.Errorf("wrapper body = %q", body)
	}
	if !strings.Contains(out.String(), hostwrap.PathLine(dir)) {
		t.Errorf("apply that CREATED the dir did not print the PATH line:\n%s", out.String())
	}

	// Second apply, nothing changed: silent about PATH. This is what stops it nagging.
	out.Reset()
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	if strings.Contains(out.String(), hostwrap.PathLine(dir)) {
		t.Errorf("an unchanged apply printed the PATH line — that is the nag OQ-4 rejected:\n%s", out.String())
	}

	// Opt-out: the wrappers are removed rather than left live on the user's PATH.
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": false}`)
	out.Reset()
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	if _, err := os.Stat(filepath.Join(dir, "claude")); !os.IsNotExist(err) {
		t.Error("a wrapper survived the opt-out, still first on the user's PATH")
	}
}

// TestApplyHostWrappersObserveWritesNothing: the default posture writes nothing anywhere,
// wrappers included.
func TestApplyHostWrappersObserveWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)

	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, false /* observe */, strings.NewReader(""))
	if _, err := os.Stat(filepath.Join(paths.WrapDirUnder(home), "claude")); !os.IsNotExist(err) {
		t.Error("an observing apply wrote a wrapper")
	}
	if !strings.Contains(out.String(), "would write") {
		t.Errorf("an observing apply did not describe the wrapper plan:\n%s", out.String())
	}
}
