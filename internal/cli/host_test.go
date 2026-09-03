package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostwrap"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
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

// writeClaudeBedrockLocalPack writes a local pack shaped like the shipped packs/claude:
// the bedrock selection, the gated env entry that switches claude into Bedrock mode, and
// the env derive that states the delivery — which variable takes its value from which
// provider fact, in Claude Code's own variable names (OQ-CS8: the binding lives in the
// agent pack's derive.lua, nowhere else). It installs the claude CLI on purpose: the env
// derive is discovered by bin ownership, and the pack env fold's gate fires on an
// installed bin first — which is why all three live here and not in a CLI-less provider
// pack.
//
// The derive is the shipped producer verbatim (packs/claude/derive.lua's yolo.env), not
// a stub: bedrock parity is the acceptance bar of the whole move, and a stub here would
// pin whatever the stub said instead of what ships.
func writeClaudeBedrockLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"claude","contributes":[` +
		`{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},` +
		`{"kind":"provider","name":"bedrock"},` +
		`{"kind":"profile","name":"bedrock","provider":"bedrock"},` +
		`{"kind":"env","profile":"bedrock","vars":{"CLAUDE_CODE_USE_BEDROCK":"1"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"),
		[]byte(shippedClaudeDeriveLua(t)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// shippedClaudeDeriveLua is the REAL packs/claude derive.lua, read from the embedded pack
// tree rather than copied into this file: bedrock parity is the acceptance bar of the
// env-derive move (provider-catalog-and-selection-plan.md build order step 3), and a
// hand-written stub here would pin whatever the stub said instead of what ships. A local
// fixture that drifted from the shipped producer would make the host notch look green
// while composing different variables than the jail notch.
func shippedClaudeDeriveLua(t *testing.T) string {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == "claude" {
			return packload.DeriveScript(p)
		}
	}
	t.Fatal("official claude pack not found")
	return ""
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
	writeClaudeBedrockLocalPack(t, home)
	userCfg(t, home, `{
	  "providers": {"bedrock": {"region": "us-east-1"}},
	  "use_profiles": {"claude": "bedrock"},
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
	writeClaudeBedrockLocalPack(t, home)
	userCfg(t, home, `{
	  "providers": {"bedrock": {"region": "us-east-1"}},
	  "use_profiles": {"claude": "bedrock"},
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
	writeClaudeBedrockLocalPack(t, home)
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

// TestHostEnvCarriesTheGatedEnv pins the gated half of the pack env fold at the host
// notch: an entry a pack gated on the selected profile rides the same process-env channel
// its provider's shape does, because a profile that set nothing but its provider's vars
// would be half a profile — claude's bedrock has to say CLAUDE_CODE_USE_BEDROCK=1
// somewhere, and the jail's argv carries it through the same fold (packload.EnvVarsFor).
// Deleting the fold's gate makes this test fail; deleting the jail's fold makes
// agentprofileenv_test.go fail.
//
// The null-unset half this fixture used to pin is gone with the body that spelled it
// (OQ-PT8): a gated entry is a plain `vars` string, and the only removal left on a host
// launch is env_sources' — TestComposeHostEnvOrdering pins that one.
func TestHostEnvCarriesTheGatedEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("AWS_PROFILE", "work-sso") // what the invoking shell had
	t.Chdir(t.TempDir())
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"claude","contributes":[` +
		`{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},` +
		`{"kind":"provider","name":"bedrock"},` +
		`{"kind":"profile","name":"bedrock","provider":"bedrock"},` +
		`{"kind":"env","vars":{"PACK_STATIC":"static","GATED_ONLY_KEY":"static"}},` +
		`{"kind":"env","profile":"bedrock","vars":{` +
		`"CLAUDE_CODE_USE_BEDROCK":"1",` +
		`"GATED_ONLY_KEY":"gated"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	userCfg(t, home, `{"use_profiles": {"claude": "bedrock"}}`)

	env, _, err := composeHostEnv("claude", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			seen[kv[:i]] = kv[i+1:]
		}
	}
	if !hasEnv(env, "CLAUDE_CODE_USE_BEDROCK=1") {
		t.Errorf("the gated env literal did not reach the host process: %q", env)
	}
	// OQ-8: the gated entry is the more specific intent, so it beats its own pack's static
	// value, which the earlier source had already set.
	if seen["GATED_ONLY_KEY"] != "gated" {
		t.Errorf("GATED_ONLY_KEY = %q, want the gated value to beat the static one", seen["GATED_ONLY_KEY"])
	}
	if seen["PACK_STATIC"] != "static" {
		t.Errorf("PACK_STATIC = %q, want the pack's static env untouched by the gated one", seen["PACK_STATIC"])
	}
	// The inherited environment stands: a gated entry overrides by assignment, and the
	// fold has no removal spelling to beat it with.
	if seen["AWS_PROFILE"] != "work-sso" {
		t.Errorf("AWS_PROFILE = %q, want the inherited value — the fold assigns, it never unsets", seen["AWS_PROFILE"])
	}
}

// TestComposeHostEnvReadsUserScopeOnly pins the host env channel's security boundary.
// The process composeHostEnv builds runs ON THE HOST, outside every sandbox — and the
// workspace yolo-jail.jsonc is agent-editable (/workspace is bind-mounted rw), so a
// merged read would let a cloned repository, or an agent editing one, set LD_PRELOAD or
// NODE_OPTIONS for a process on the user's machine. The config source must therefore be
// user scope BY CONSTRUCTION (config.UserScopeConfigOrEmpty), not merged-and-filtered.
//
// Fails if composeHostEnv reverts to LoadConfig("", …), which merges workspace over user.
func TestComposeHostEnvReadsUserScopeOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	t.Chdir(ws)
	userCfg(t, home, `{"env_sources": [{"USER_SCOPE_VAR": "from-user"}]}`)
	if err := os.WriteFile(filepath.Join(ws, "yolo-jail.jsonc"), []byte(`{
	  "env_sources": [{"LD_PRELOAD": "/tmp/evil.so", "WORKSPACE_VAR": "from-workspace"}]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}

	env, _, err := composeHostEnv("claude", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEnv(env, "USER_SCOPE_VAR=from-user") {
		t.Error("user-scope env_sources did not reach the composition — the test proves nothing if this fails")
	}
	for _, leak := range []string{"LD_PRELOAD=/tmp/evil.so", "WORKSPACE_VAR=from-workspace", "LD_PRELOAD=", "WORKSPACE_VAR="} {
		if hasEnv(env, leak) {
			t.Errorf("workspace-scope variable reached a host process composition: %s", leak)
		}
	}
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

// TestApplyHostFlagIsRemovedNotDeprecated pins OQ-7's ruling: `--host` is REMOVED, not
// deprecated-with-a-message. Three spellings for one operation was the problem, so the
// removed one must fail like any other unknown flag rather than quietly still working.
func TestApplyHostFlagIsRemovedNotDeprecated(t *testing.T) {
	var out, errw bytes.Buffer
	rc := applyMain([]string{"--host"}, &out, &errw, false, nil)
	if rc != 2 {
		t.Errorf("apply --host rc = %d, want 2 (unexpected argument)", rc)
	}
	if !strings.Contains(errw.String(), "unexpected argument") {
		t.Errorf("stderr = %q, want an unknown-flag error", errw.String())
	}
	// The help it prints alongside must point at what replaced it.
	combined := out.String() + errw.String()
	if !strings.Contains(combined, "--at host") {
		t.Errorf("the refusal does not show the surviving spelling:\n%s", combined)
	}
}

// TestApplyUsageNoLongerAdvertisesTheRemovedFlag: the surviving help must not teach a
// spelling that now errors.
func TestApplyUsageNoLongerAdvertisesTheRemovedFlag(t *testing.T) {
	if strings.Contains(applyUsage, "--host") {
		t.Errorf("applyUsage still advertises --host:\n%s", applyUsage)
	}
	if !strings.Contains(applyUsage, "yolo host") {
		t.Error("applyUsage does not mention the `yolo host` namespace that replaced it")
	}
}

// TestResolveHostTargetSkipsTheWrapDir pins the CALL SITE of the recursion guard.
//
// internal/hostwrap's own tests prove LookPathSkipping skips what it is told to skip; this
// proves `yolo host` actually TELLS it to. Measured before this test existed: returning
// nil from yoloManagedDirs() compiled and passed the entire suite, and in production
// turned every wrapped launch into an infinite exec loop — the wrapper finds itself first
// on PATH, execs `yolo host -- claude`, which finds the wrapper again.
func TestResolveHostTargetSkipsTheWrapDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	wrapDir := paths.WrapDirUnder(home)
	if err := os.MkdirAll(wrapDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The wrapper, exactly as apply would generate it.
	if err := os.WriteFile(filepath.Join(wrapDir, "claude"), []byte(hostwrap.Body("claude")), 0o755); err != nil {
		t.Fatal(err)
	}
	// The real binary, further down PATH — where claude's own installer puts it.
	realDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The wrap dir FIRST, which is the whole point of prepending it.
	pathEnv := wrapDir + string(os.PathListSeparator) + realDir
	got, err := resolveHostTarget(pathEnv, "claude")
	if err != nil {
		t.Fatalf("resolveHostTarget: %v", err)
	}
	if want := filepath.Join(realDir, "claude"); got != want {
		t.Fatalf("resolved %q, want %q — `yolo host` would exec the WRAPPER, which execs "+
			"`yolo host` again: an infinite loop on every wrapped launch", got, want)
	}
}

// TestYoloManagedDirsCoversTheWholeGeneratedTree: skipping bin/wrap alone would leave
// bin/block and bin/launch reachable the day they exist. The skip list names the parent.
func TestYoloManagedDirsCoversTheWholeGeneratedTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dirs := yoloManagedDirs()
	if len(dirs) == 0 {
		t.Fatal("yoloManagedDirs is empty — the recursion guard is disarmed")
	}
	wrap := paths.WrapDirUnder(home)
	covered := false
	for _, d := range dirs {
		if strings.HasPrefix(wrap, d+string(filepath.Separator)) || wrap == d {
			covered = true
		}
	}
	if !covered {
		t.Errorf("yoloManagedDirs %q does not cover the wrap dir %q", dirs, wrap)
	}
}

// TestHostEnvAgentFlagSelectsTheProfile: the composition is per-agent, and --agent is how
// you ask for a different one. Pins the help's claim against the code's behaviour — the
// help used to promise "every configured one" while the code hard-coded a single name.
func TestHostEnvAgentFlagSelectsTheProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeClaudeBedrockLocalPack(t, home)
	userCfg(t, home, `{
	  "providers": {"bedrock": {"region": "us-east-1"}},
	  "use_profiles": {"claude": "bedrock"}
	}`)

	// Default agent picks up claude's profile.
	var out, errw bytes.Buffer
	if rc := hostEnv(nil, &out, &errw); rc != 0 {
		t.Fatalf("rc=%d %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("default agent did not compose claude's profile:\n%s", out.String())
	}

	// A different agent, with no profile of its own, composes none of it.
	out.Reset()
	if rc := hostEnv([]string{"--agent", "pi"}, &out, &errw); rc != 0 {
		t.Fatalf("rc=%d %s", rc, errw.String())
	}
	if strings.Contains(out.String(), "CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("--agent pi composed claude's profile:\n%s", out.String())
	}
}

// TestHostUsageDocumentsTheAgentDefaultItActuallyHas guards the divergence itself: the
// help is what a user acts on, so a default it names must be the one the code applies.
func TestHostUsageDocumentsTheAgentDefaultItActuallyHas(t *testing.T) {
	if strings.Contains(hostUsage, "every configured one") {
		t.Error("hostUsage promises --agent defaults to every configured agent; hostEnv " +
			"composes for exactly one")
	}
	if !strings.Contains(hostUsage, "default: claude") {
		t.Error("hostUsage does not name the default --agent the code applies")
	}
}

// hostExportKeys returns the KEYS of the `export K=...` lines hostEnv printed, in the
// order it printed them — which is hostEnvVars' composition order, the thing the two
// tests below pin.
func hostExportKeys(out string) []string {
	var keys []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "export ") {
			continue
		}
		rest := line[len("export "):]
		if i := strings.IndexByte(rest, '='); i > 0 {
			keys = append(keys, rest[:i])
		}
	}
	return keys
}

// TestHostEnvPackEnvVarsSortedBeforeEnvSources pins the CALL SITE of §5.4 source 1: the
// block in hostEnvVars that asks loadedHostPacks for the selected packs and folds their
// kind:"env" contributions in FIRST, sorted.
//
// internal/packload's own tests prove packload.EnvVarsFor merges what it is handed; this
// proves `yolo host` actually HANDS it anything. Measured before this test existed:
// deleting the whole `if packs, err := loadedHostPacks(); ...` block from hostEnvVars
// compiled and passed the entire suite — meaning no pack's env vars were reaching a host
// launch and nothing noticed. audio is the one shipped pack with a real kind:"env"
// contribution (PIPEWIRE_REMOTE, PULSE_SERVER), so selecting it by bare name is the
// shortest config that exercises the block through the real embedded-pack loader.
//
// Three properties, one per assertion: PRESENCE (both vars with audio's literal values),
// ORDER vs the secret channel (the whole pack block precedes every env_sources
// assignment), and STABILITY (the order is identical on every run — the sorting exists
// because "an argv that reshuffles between runs is a diff nobody can read", and map
// iteration order would reshuffle it; a random-order implementation survives any ONE run
// half the time, so the loop demands 25 identical compositions).
func TestHostEnvPackEnvVarsSortedBeforeEnvSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{
	  "packs": ["audio"],
	  "env_sources": [{"SECRET_TOKEN": "hunter2"}]
	}`)

	run := func(t *testing.T) string {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := hostEnv(nil, &out, &errw); rc != 0 {
			t.Fatalf("hostEnv rc = %d, stderr = %s", rc, errw.String())
		}
		return out.String()
	}

	got := run(t)
	for _, want := range []string{
		`export PIPEWIRE_REMOTE='/run/pipewire/pipewire-0'`,
		`export PULSE_SERVER='unix:/run/pulse/native'`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("pack env contribution missing from the composition: %q\n%s", want, got)
		}
	}

	keys := hostExportKeys(got)
	pos := map[string]int{}
	for i, k := range keys {
		pos[k] = i
	}
	for _, k := range []string{"PIPEWIRE_REMOTE", "PULSE_SERVER", "SECRET_TOKEN"} {
		if _, ok := pos[k]; !ok {
			t.Fatalf("key %s absent from %v\n%s", k, keys, got)
		}
	}
	if !(pos["PIPEWIRE_REMOTE"] < pos["PULSE_SERVER"] && pos["PULSE_SERVER"] < pos["SECRET_TOKEN"]) {
		t.Errorf("pack env vars are not a SORTED block ahead of env_sources: "+
			"PIPEWIRE_REMOTE@%d PULSE_SERVER@%d SECRET_TOKEN@%d in %v",
			pos["PIPEWIRE_REMOTE"], pos["PULSE_SERVER"], pos["SECRET_TOKEN"], keys)
	}

	// The stability half of the sorting contract: one canonical order, every run.
	for i := 0; i < 24; i++ {
		if again := run(t); again != got {
			t.Fatalf("composition order reshuffled between runs (run %d):\nfirst:\n%s\nagain:\n%s", i+1, got, again)
		}
	}
}

// TestApplyHostWrappersRemovedWhenPacksIsEmptied pins the CALL SITE in applyHost's
// `len(entries) == 0` early-return branch: applyHostWrappers(pr, errw, home, nil, write).
//
// The three existing wrapper tests all configure packs:["claude"], so every one of them
// reaches the MAIN path's wrapper call — none ever entered this branch, and neutering
// THIS call left orphan executables at the front of the user's PATH with the suite green
// (adversarial review finding #13). Emptying `packs` is the MOST complete drop there is:
// every wrapper is an orphan pointing at a program nothing will reinstall, so the branch's
// own comment says it must clean up. The opt-in key deliberately STAYS ON — turning it off
// instead would take the !enabled path inside applyHostWrappers and test the other branch.
func TestApplyHostWrappersRemovedWhenPacksIsEmptied(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"packs": ["claude"], "host_wrappers": true}`)
	dir := paths.WrapDirUnder(home)

	// Setup, exactly as the lifecycle test does it: an asserting apply generates the
	// claude wrapper.
	var out, errw bytes.Buffer
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	if _, err := os.ReadFile(filepath.Join(dir, "claude")); err != nil {
		t.Fatalf("setup: no wrapper generated: %v", err)
	}

	// Empty `packs`, opt-in unchanged. This apply takes the early-return branch, whose
	// wrapper call is the only thing left that would retire the wrapper.
	userCfg(t, home, `{"packs": [], "host_wrappers": true}`)
	out.Reset()
	applyHost(&out, &errw, false, true, strings.NewReader(""))
	if _, err := os.Stat(filepath.Join(dir, "claude")); !os.IsNotExist(err) {
		t.Error("a wrapper survived emptying `packs` — an orphan EXECUTABLE still first " +
			"on the user's PATH, pointing at a program nothing will reinstall")
	}
}

// TestHostEnvResolvesConfigRelativeEntriesBesideTheConfig pins the ruling's host-notch
// half (envsource-relative-paths.md OQ-E1, 2026-08-30): a relative env_sources entry in
// the USER config resolves beside the config that declared it — because the loader
// anchors it at load time (config.AnchorEnvSources), NOT against the current directory,
// which a workspace controls. The cwd's same-named file must not leak; the config-dir
// file must apply; and nothing refuses the entry, because under the ruling it is legal.
func TestHostEnvResolvesConfigRelativeEntriesBesideTheConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	t.Chdir(ws)
	userCfg(t, home, `{
	  "env_sources": ["rel.env", {"KEEP": "yes"}]
	}`)
	// The trap the anchoring kills: the same-named file in a directory the WORKSPACE
	// controls. Under pre-ruling workspace resolution (the jail's rule, never the host's)
	// this is the file a `yolo host` launch would have read.
	if err := os.WriteFile(filepath.Join(ws, "rel.env"), []byte("PWNED=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The ruling's target: beside the declaring config.
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "rel.env"),
		[]byte("FROM_CONFIG_DIR=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var warnings []string
	env, _, err := composeHostEnv("claude", "", func(msg string) { warnings = append(warnings, msg) })
	if err != nil {
		t.Fatal(err)
	}
	if hasEnv(env, "PWNED=yes") {
		t.Error("a workspace-controlled dotenv reached a host process composition")
	}
	if !hasEnv(env, "FROM_CONFIG_DIR=yes") {
		t.Error("the config-dir file beside the declaring config must apply")
	}
	if !hasEnv(env, "KEEP=yes") {
		t.Error("the inline entry beside the file entry must still apply")
	}
	for _, w := range warnings {
		if strings.Contains(w, "is relative") {
			t.Errorf("an anchored entry must not be refused, got: %s", w)
		}
	}
}

// TestHostEnvRefusesUnanchoredRelativeEntries pins the BACKSTOP: hostScopedEnvSources
// still refuses a relative entry that reaches hostEnvVars unanchored. Every loader
// anchors at load time, so in practice this means a hand-built config or a pre-ruling
// artifact — exactly the sources whose cwd a workspace may control, which is why the
// refusal stays even though the ruling made config-relative entries legal.
//
// Fails if the hostScopedEnvSources call is removed from hostEnvVars (the hand-built
// entry then resolves against the cwd and PWNED leaks).
func TestHostEnvRefusesUnanchoredRelativeEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	ws := t.TempDir()
	t.Chdir(ws)
	if err := os.WriteFile(filepath.Join(ws, "rel.env"), []byte("PWNED=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := jsonx.NewOrderedMap()
	cfg.Set("env_sources", []any{"rel.env", map[string]any{"KEEP": "yes"}})

	var warnings []string
	vars, err := hostEnvVars(cfg, ws, "claude", "", func(msg string) { warnings = append(warnings, msg) })
	if err != nil {
		t.Fatalf("hostEnvVars: %v", err)
	}
	var env []string
	for _, v := range vars {
		env = append(env, v.Key+"="+v.Value)
	}
	if hasEnv(env, "PWNED=yes") {
		t.Error("an unanchored relative entry resolved against the cwd")
	}
	refused := false
	for _, w := range warnings {
		if strings.Contains(w, `"rel.env" is relative`) {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the backstop refusal must fire for unanchored entries, got warnings %q", warnings)
	}
}

// TestHostEnvStillResolvesAbsoluteEnvSourceFiles: the refusal is about RELATIVE
// resolution, not about files — an absolute path (and ~/…) in the user config names
// where it names, independent of the cwd, and keeps working.
func TestHostEnvStillResolvesAbsoluteEnvSourceFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	absEnv := filepath.Join(home, "absolute.env")
	if err := os.WriteFile(absEnv, []byte("FROM_ABSOLUTE=yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	userCfg(t, home, `{"env_sources": ["`+absEnv+`"]}`)

	env, _, err := composeHostEnv("claude", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if !hasEnv(env, "FROM_ABSOLUTE=yes") {
		t.Error("an absolute env_sources entry in the user config stopped resolving")
	}
}

// writeZaiLocalPack is hostprovidershape_test.go's fixture: the conventional local pack
// shipping a provider whose credential is a variable NAME the user hydrates, plus a
// variant naming it — the shape packs/zai would have (zai-plumbing.md §7). It installs no
// CLI, which is the ordinary provider pack.

// The host half of the selected-pack credential pre-flight (§6.2, OQ-13): selecting a
// provider pack with no key hydrated refuses the exec, naming the variable, the provider
// and where it looked. rc 1 is the pre-flight's own exit; rc 127 below is PATH resolution
// failing, which is how the two are told apart.
func TestHostExecRefusesASelectedPackWithNoKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{}`)

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("rc = %d, want the pre-flight's 1 (stderr: %s)", rc, errw.String())
	}
	got := errw.String()
	// The pack is named by its STAGING name — the conventional local pack dir is `local`,
	// whatever the manifest's own name field says — which is the identity every other
	// launch-time message uses for it too.
	for _, want := range []string{"ZAI_API_KEY", `provider "zai"`, "pack local", "consulted for credentials"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
}

// The positive half, and the ordering pin: once the key IS hydrated the pre-flight passes
// and hostExec goes on to PATH resolution, which fails with 127 for a binary that does not
// exist. A refusal here would mean the check stopped reading the composed environment.
func TestHostExecProceedsOnceTheKeyIsHydrated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{"env_sources": [{"ZAI_API_KEY": "sk-zai"}]}`)

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil); rc != 127 {
		t.Fatalf("rc = %d, want the PATH miss's 127 — the pre-flight must have passed (stderr: %s)",
			rc, errw.String())
	}
}

// The escape hatch lifts the host refusal too, loudly — same var, same discipline as the
// jail notch, because the two notches refuse on the same fact.
func TestHostExecHatchLiftsTheRefusal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv(paths.AllowMissingProvidersEnv, "1")
	t.Cleanup(func() { os.Unsetenv(paths.AllowMissingProvidersEnv) })
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{}`)

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil); rc != 127 {
		t.Fatalf("rc = %d, want 127 — the hatch must let the launch reach PATH resolution (stderr: %s)",
			rc, errw.String())
	}
	if got := errw.String(); !strings.Contains(got, paths.AllowMissingProvidersEnv+" is set") {
		t.Errorf("the override must say what it is suppressing:\n%s", got)
	}
}

// `yolo host env` is an OBSERVE verb, and stays one: it shares the composition the exec
// half pre-flights, and must keep answering even when the answer is "this launch is
// missing a key" — that is exactly the situation it exists to debug. An unrelated entry
// gives it something to print, so the assertion is that the answer came back rather than
// that it happened to be empty.
func TestHostEnvIsNotGatedByTheCredentialPreflight(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("ZAI_API_KEY", "")
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{"env_sources": [{"KEEP": "yes"}]}`)

	var out, errw bytes.Buffer
	if rc := hostEnv(nil, &out, &errw); rc != 0 {
		t.Fatalf("host env rc = %d (stderr: %s)", rc, errw.String())
	}
	if !strings.Contains(out.String(), "export KEEP='yes'") {
		t.Errorf("host env stopped composing:\n%s", out.String())
	}
	if strings.Contains(errw.String(), "ZAI_API_KEY") {
		t.Errorf("the observe verb must not inherit the exec half's refusal:\n%s", errw.String())
	}
}

// The composition refusal reaches the HOST notch too. composedHostProviders is this
// notch's one provider composition, and a user `base_url` over the local zai pack's
// `endpoints` is the pair per-field composition manufactures out of two legal inputs
// (docs/reference/providers.md, OQ-PT2) — here handed to agentenv, which reads
// endpoints only, with the derives preferring the shorthand. The exec refuses rather
// than run on a table the launch cannot resolve consistently.
func TestHostExecRefusesAManufacturedAddressPair(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("ZAI_API_KEY", "sk-zai") // the credential is fine; the ADDRESS is what refuses
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{"providers": {"zai": {"base_url": "https://my.proxy.example/v1"}}}`)

	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--", "no-such-agent-binary"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("rc = %d, want 1\nstderr:\n%s", rc, errw.String())
	}
	got := errw.String()
	for _, want := range []string{
		`"zai"`,
		"pack local",                    // the staged name of the pack that shipped the endpoints
		"providers.zai.base_url",        // where the shorthand came from
		"endpoints.<protocol>.base_url", // the override that still works
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}
}
