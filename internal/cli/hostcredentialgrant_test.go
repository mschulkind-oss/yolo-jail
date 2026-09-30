package cli

// hostcredentialgrant_test.go pins WHO A HOST PROFILE REACHES and the remedies the credential
// disclosure names (docs/design/credential-sources-separation.md §5, ES-D2 to ES-D5, and
// docs/plans/notch-convergence.md OQ-NC5): a bare `-p <profile>` reaches agent CLIs only, at
// every notch, so `yolo host -p <profile> -- <cmd>` for a command no selected pack installs is
// refused, naming `--with-credentials`, the one grant that hands an ad-hoc command a provider's
// claimed env_sources values. Every cell runs `yolo host` through hostMain to the exec, as the
// TestHostGate* cells in hostcredentialgate_test.go do, with `bash` as the ad-hoc command: no
// selected pack installs it, so no pack code runs for it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// esGrantConfig is the design's measured setup (§3.1): packs claude, pi and zai, and
// env_sources carrying zai's key beside an unclaimed PORT.
const esGrantConfig = `{"packs": ["claude", "pi", "zai"], "env_sources": [` +
	`{"ZAI_API_KEY": "tok-es", "PORT": "8080"}]}`

// OQ-NC5 at the host: a bare -p reaches agent CLIs only, so `yolo host -p zai -- env` (a command
// no selected pack installs) is REFUSED before the exec, and never hands the command zai's key.
// ES-D1 made it the host's grant for any command; the grant is `--with-credentials` now, and the
// refusal names it, spelled for the command as typed. The pair spelling naming the command is
// the same -p and refuses the same way. The named grant is run, and delivers the key.
func TestHostBareProfileNeverKeysAnAdHocCommand(t *testing.T) {
	for _, flags := range [][]string{{"-p", "zai"}, {"-p", "env=zai"}, {"--profile=zai"}} {
		rc, env, errs := hostGateRun(t, esGrantConfig, nil, flags, "env")
		if rc == 0 || env != nil {
			t.Fatalf("yolo host %v -- env must refuse before the exec: rc = %d, reached exec = %v\n%s",
				flags, rc, env != nil, errs)
		}
		if strings.Contains(errs, "tok-es") {
			t.Errorf("the refusal leaked the key's value:\n%s", errs)
		}
		line := refusalLine(t, errs)
		for _, want := range []string{"agent CLIs only", `no selected pack installs "env"`,
			"`yolo host --with-credentials zai -- env`"} {
			if !strings.Contains(line, want) {
				t.Errorf("yolo host %v -- env: the refusal must say %q: %q", flags, want, line)
			}
		}
		assertRemediesRun(t, line, "ZAI_API_KEY", "tok-es")
	}
}

// refusalLine is the launch's one "refusing to launch" line, or `yolo host env`'s error line.
func refusalLine(t *testing.T, errs string) string {
	t.Helper()
	for _, l := range strings.Split(errs, "\n") {
		if strings.Contains(l, "refusing to launch") || strings.HasPrefix(l, "yolo host env: ") {
			return l
		}
	}
	t.Fatalf("no refusal line:\n%s", errs)
	return ""
}

// The same rule for an agent is no refusal: `yolo host -p zai -- pi` is pi on zai, since a
// selected pack installs pi. The contrast is what makes the cell above the agent-only rule
// rather than a refusal of -p.
func TestHostBareProfileStillReachesAnAgentCLI(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig, nil, []string{"-p", "zai"}, "pi")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("yolo host -p zai -- pi: ZAI_API_KEY = %q, want zai's key\n%s", env["ZAI_API_KEY"], errs)
	}
}

// The same command with no -p is not a recipient: zai's key is withheld from it and the
// unclaimed value still arrives. The contrast is what makes the cell above a grant rather
// than a leak.
func TestHostGrantAbsentWithholdsTheClaimedValueAndKeepsTheRest(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig, nil, nil, "bash")
	if env["ZAI_API_KEY"] != "" {
		t.Errorf("yolo host -- bash selected nothing and was handed ZAI_API_KEY=%q", env["ZAI_API_KEY"])
	}
	if env["PORT"] != "8080" {
		t.Errorf("PORT = %q, want the unclaimed env_sources value\n%s", env["PORT"], errs)
	}
	if !strings.Contains(errs, "ZAI_API_KEY (provider zai): withheld") {
		t.Errorf("the withheld key is disclosed:\n%s", errs)
	}
}

// ES-D2 at `yolo host --`, for an ad-hoc command: the withheld line names the grant that would
// deliver the key, spelled for the command this launch was given — never a -p, which reaches
// agent CLIs only (OQ-NC5).
func TestHostGrantWithheldLineNamesTheGrantRemedy(t *testing.T) {
	_, errs := hostGateLaunchWith(t, esGrantConfig, nil, nil, "bash")
	line := scopeLine(t, errs, "ZAI_API_KEY")
	if !strings.Contains(line, "withheld") {
		t.Errorf("the key is still disclosed as withheld: %q", line)
	}
	if !strings.Contains(line, "`yolo host --with-credentials zai -- bash`") {
		t.Errorf("the withheld line must name `yolo host --with-credentials zai -- bash`, the "+
			"grant that would deliver it (ES-D2, OQ-NC5): %q", line)
	}
	if strings.Contains(line, "-p ") {
		t.Errorf("an ad-hoc command's remedy never names a -p (OQ-NC5): %q", line)
	}
	assertRemediesRun(t, line, "ZAI_API_KEY", "tok-es")
	if strings.Contains(line, "yolo host env") {
		t.Errorf("an exec names the exec spelling for the command it was given, not the env "+
			"verb's: %q", line)
	}
}

// ES-D2 at `yolo host env`: the same remedy, spelled for what the verb is for. The shell
// spelling is the grant, `--with-credentials` (OQ-NC5), never the default agent's slice:
// `--agent claude -p zai` would export claude's whole zai shape into the shell, the zai key
// riding again under ANTHROPIC_AUTH_TOKEN, and every later process the shell starts would
// inherit it undisclosed (CN-D13). The exec spelling is for one launch of the agent. The named
// shell command is run, and must print the key and nothing of claude's.
func TestHostEnvWithheldLineNamesTheGrantRemedy(t *testing.T) {
	hostGateHome(t, esGrantConfig, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	line := scopeLine(t, errw.String(), "ZAI_API_KEY")
	for _, want := range []string{"`eval \"$(yolo host env --with-credentials zai)\"`", "`yolo host -p zai -- claude`"} {
		if !strings.Contains(line, want) {
			t.Errorf("yolo host env's withheld line must name %s (ES-D2): %q", want, line)
		}
	}
	if strings.Contains(line, "--agent claude") {
		t.Errorf("the shell spelling must not be the default agent's slice: %q", line)
	}
	var shell []string
	for _, argv := range remedyCommands(line) {
		if len(argv) > 0 && argv[0] == "env" {
			shell = argv
		}
	}
	if shell == nil {
		t.Fatalf("the line names no `yolo host env` spelling: %q", line)
	}
	rc, script, _, errs := runRemedy(t, shell)
	if rc != 0 || !strings.Contains(script, "export ZAI_API_KEY='tok-es'") {
		t.Errorf("yolo host %s: rc = %d, want the key exported\n%s\n%s", strings.Join(shell, " "), rc, script, errs)
	}
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"} {
		if strings.Contains(script, k) {
			t.Errorf("the shell spelling exported claude's %s:\n%s", k, script)
		}
	}
}

// remedyCommands returns each `yolo host …` command a disclosure line names, as the argv
// hostMain takes: the `yolo host` prefix dropped and an `eval "$(…)"` wrapper unwrapped. The
// names in these cells need no shell quoting, so a field split is the whole parse.
func remedyCommands(line string) [][]string {
	var out [][]string
	parts := strings.Split(line, "`")
	for i := 1; i < len(parts); i += 2 {
		s := parts[i]
		if inner, ok := strings.CutPrefix(s, `eval "$(`); ok {
			s = strings.TrimSuffix(inner, `)"`)
		}
		if f := strings.Fields(s); len(f) >= 2 && f[0] == "yolo" && f[1] == "host" {
			out = append(out, f[2:])
		}
	}
	return out
}

// runRemedy runs one command a disclosure line named through hostMain, in the fixture home
// already set up, with the exec replaced as hostGateRun replaces it: the exit code, what it
// printed on stdout (`yolo host env`'s script), the environment an exec was handed (nil when
// none was reached) and stderr.
func runRemedy(t *testing.T, argv []string) (int, string, map[string]string, string) {
	t.Helper()
	if i := indexOf(argv, "--"); i >= 0 && i+1 < len(argv) {
		bin := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, argv[i+1]), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	var got []string
	origExec := hostSyscallExec
	hostSyscallExec = func(_ string, _, env []string) error {
		got = env
		return nil
	}
	defer func() { hostSyscallExec = origExec }()
	var out, errw bytes.Buffer
	rc := hostMain(argv, &out, &errw, false, nil)
	var env map[string]string
	if got != nil {
		env = map[string]string{}
		for _, kv := range got {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
	}
	return rc, out.String(), env, errw.String()
}

// A named -p on an AGENT is a provider switch, not one more key: the withheld name belongs to a
// provider the agent's profile did not select, so the -p replaces that profile and re-points
// the agent's backend. The line says so, naming the profile it replaces, and never words it as
// handing the agent the key.
func TestHostGrantAgentRemedyIsWordedAsAProfileSwitch(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude", "zai"], "profile": {"claude": "bedrock"}, `+
		`"env_sources": [{"ZAI_API_KEY": "tok-es", "AWS_PROFILE": "dev"}]}`, nil, nil, "claude")
	line := scopeLine(t, errs, "ZAI_API_KEY")
	want := "To run claude on the zai profile for one launch, replacing its bedrock profile: " +
		"`yolo host -p zai -- claude`"
	if !strings.Contains(line, want) {
		t.Errorf("the remedy for an agent must say it switches the agent's profile (%q): %q", want, line)
	}
	if strings.Contains(line, "hand it to claude") {
		t.Errorf("a -p on an agent re-points its backend; it does not hand it one key: %q", line)
	}
}

// An ad-hoc command's remedy is the grant, whichever provider claims the name: bedrock's key
// pair is handed to bash by `--with-credentials bedrock`, never by a -p (OQ-NC5).
func TestHostGrantAdHocRemedyIsTheGrantNeverAProfile(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude", "zai"], "env_sources": [`+
		`{"ZAI_API_KEY": "tok-es", "AWS_ACCESS_KEY_ID": "AKIA-host"}]}`, nil, nil, "bash")
	line := scopeLine(t, errs, "AWS_ACCESS_KEY_ID")
	want := "To hand it to bash for one launch: `yolo host --with-credentials bedrock -- bash`"
	if !strings.Contains(line, want) {
		t.Errorf("the remedy for an ad-hoc command must be the grant (%q): %q", want, line)
	}
	assertRemediesRun(t, line, "AWS_ACCESS_KEY_ID", "AKIA-host")
}

// At `yolo host env` the one-launch spelling for the agent is worded as the switch too.
func TestHostEnvAgentRemedyIsWordedAsAProfileSwitch(t *testing.T) {
	hostGateHome(t, esGrantConfig, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	line := scopeLine(t, errw.String(), "ZAI_API_KEY")
	if want := "to run claude on the zai profile for one launch: `yolo host -p zai -- claude`"; !strings.Contains(line, want) {
		t.Errorf("yolo host env's one-launch spelling must say it runs claude on zai (%q): %q", want, line)
	}
}

// A remedy is a command the user will run, so it must be one that runs: every `yolo host …`
// the line names, run through hostMain in the same home, has to exit 0. cerebras speaks only
// openai and claude only anthropic, and at the host no `needs` joins wire-bridge, so
// `yolo host -p cerebras -- claude` refuses on the protocol pairing. The line must hand the
// key to an ad-hoc command instead, and say why claude is not named.
func TestHostGrantRemedyNeverNamesAProfileTheAgentRefuses(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude", "cerebras"], `+
		`"env_sources": [{"CEREBRAS_API_KEY": "tok-c"}]}`, nil, nil, "claude")
	line := scopeLine(t, errs, "CEREBRAS_API_KEY")
	if strings.Contains(line, "-- claude`") {
		t.Errorf("claude cannot run on cerebras here, so no named command may launch it: %q", line)
	}
	for _, want := range []string{"`yolo host --with-credentials cerebras -- bash`", "claude cannot run on the cerebras profile"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line must hand the key to an ad-hoc command and say why (%q missing): %q", want, line)
		}
	}
	assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
}

// The §1 incident's own config at `yolo host env`, whose agent defaults to claude: every
// withheld line's named commands run, openai-only providers (cerebras, kilo) included.
func TestHostEnvRemediesAllRunOverTheIncidentConfig(t *testing.T) {
	hostGateHome(t, `{"packs": ["claude", "pi", "zai", "openrouter", "cerebras", "kilo"], `+
		`"env_sources": [{"ZAI_API_KEY": "z", "OPENROUTER_API_KEY": "o", "CEREBRAS_API_KEY": "c", `+
		`"KILO_API_KEY": "k"}]}`, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	for name, value := range map[string]string{"ZAI_API_KEY": "z", "OPENROUTER_API_KEY": "o",
		"CEREBRAS_API_KEY": "c", "KILO_API_KEY": "k"} {
		assertRemediesRun(t, scopeLine(t, errw.String(), name), name, value)
	}
	for _, name := range []string{"CEREBRAS_API_KEY", "KILO_API_KEY"} {
		if line := scopeLine(t, errw.String(), name); strings.Contains(line, "-- claude`") {
			t.Errorf("claude speaks no openai, so %s's line may not name a claude launch: %q", name, line)
		}
	}
}

// The declare-a-profile arm is asked the same question, of the profile it tells the user to
// declare: a user provider claude cannot speak to gets the ad-hoc spelling, and once the
// example entry is declared, the named command runs.
func TestHostGrantDeclareRemedyNamesACommandThatRunsOnceDeclared(t *testing.T) {
	provider := `"providers": {"deepseek": {"endpoints": {"openai": {"base_url": "https://api.deepseek.example/v1"}}, ` +
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}`
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude"], `+provider+`, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil, nil, "claude")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	if !strings.Contains(line, "`yolo host --with-credentials deepseek -- bash`") || strings.Contains(line, "-- claude`") {
		t.Errorf("claude speaks no openai, so the declared profile's remedy must be ad-hoc: %q", line)
	}
	hostGateHome(t, `{"packs": ["claude"], `+provider+`, "profiles": {"deepseek": {"provider": "deepseek"}}, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil)
	assertRemediesRun(t, line, "DEEPSEEK_API_KEY", "tok-ds")
}

// With two providers claiming one name, the remedy names the first profile the agent can run
// on, not merely the first by preference: `aaa` sorts ahead of zai but speaks only openai.
func TestHostGrantRemedyPrefersAProfileTheAgentRunsOn(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude", "zai"], `+
		`"providers": {"aaa": {"endpoints": {"openai": {"base_url": "https://aaa.example/v1"}}, `+
		`"api_key_env_name": "ZAI_API_KEY"}}, "profiles": {"aaa": {"provider": "aaa"}}, `+
		`"env_sources": [{"ZAI_API_KEY": "tok-es"}]}`, nil, nil, "claude")
	line := scopeLine(t, errs, "ZAI_API_KEY")
	if !strings.Contains(line, "`yolo host -p zai -- claude`") {
		t.Errorf("claude runs on zai, which also claims the key, so the line must name it: %q", line)
	}
	assertRemediesRun(t, line, "ZAI_API_KEY", "tok-es")
}

// assertRemediesRun runs every `yolo host …` command line names, in the current fixture home,
// and asserts each exits 0 and hands its process, or prints for its shell, name=value.
func assertRemediesRun(t *testing.T, line, name, value string) {
	t.Helper()
	cmds := remedyCommands(line)
	if len(cmds) == 0 {
		t.Fatalf("the line names no command: %q", line)
	}
	for _, argv := range cmds {
		rc, script, env, errs := runRemedy(t, argv)
		if rc != 0 {
			t.Errorf("the remedy `yolo host %s` exits %d:\n%s", strings.Join(argv, " "), rc, errs)
			continue
		}
		if argv[0] == "env" {
			if !strings.Contains(script, "export "+name+"='"+value+"'") {
				t.Errorf("`yolo host %s` does not export %s:\n%s", strings.Join(argv, " "), name, script)
			}
			continue
		}
		if env[name] != value {
			t.Errorf("`yolo host %s` hands %s=%q, want %q", strings.Join(argv, " "), name, env[name], value)
		}
	}
}

// ES-D2's other arm, for an agent: a provider the user declared under `providers` has no
// profile until the user declares one, and -p takes a profile name, so the line says to declare
// one rather than naming a -p that would refuse. The §1 incident's deepseek line, on pi, which
// speaks openai. An ad-hoc command needs no profile at all: its remedy is the grant.
func TestHostGrantWithheldLineSaysToDeclareAProfileWhenNoneSelectsTheProvider(t *testing.T) {
	cfg := `{"packs": ["pi"], ` +
		`"providers": {"deepseek": {"endpoints": {"openai": {"base_url": "https://api.deepseek.example"}}, ` +
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}, ` +
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`
	_, errs := hostGateLaunchWith(t, cfg, nil, nil, "bash")
	if line := scopeLine(t, errs, "DEEPSEEK_API_KEY"); strings.Contains(line, "No declared profile") ||
		!strings.Contains(line, "`yolo host --with-credentials deepseek -- bash`") {
		t.Errorf("an ad-hoc command's remedy is the grant, needing no profile: %q", line)
	}
	_, errs = hostGateLaunchWith(t, cfg, nil, nil, "pi")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	for _, want := range []string{"No declared profile selects deepseek", "`profiles`",
		`"deepseek": {"provider": "deepseek"}`, "`yolo host -p deepseek -- pi`"} {
		if !strings.Contains(line, want) {
			t.Errorf("with no profile selecting deepseek the line must say to declare one (%q "+
				"missing): %q", want, line)
		}
	}
}

// With a user profile declared over that provider, the remedy names it: a declared profile
// that resolves to the claimant, whatever it is called.
func TestHostGrantWithheldLineNamesAUserProfileOverTheProvider(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["pi"], `+
		`"providers": {"deepseek": {"endpoints": {"openai": {"base_url": "https://api.deepseek.example"}}, `+
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}, `+
		`"profiles": {"ds": {"provider": "deepseek"}}, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil, nil, "pi")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	if !strings.Contains(line, "`yolo host -p ds -- pi`") {
		t.Errorf("the remedy must name the user's profile over deepseek: %q", line)
	}
}

// remedyProfiles prefers the profile named after a claiming provider, then every other that
// resolves to any claimant, by name; the remedy names the first of them its command runs on.
func TestRemedyProfilePrefersTheProvidersOwnName(t *testing.T) {
	resolved := map[string]packload.ResolvedProfile{
		"a-zai": {Provider: "zai"}, "zai": {Provider: "zai"}, "b-ds": {Provider: "deepseek"},
		"a-ds": {Provider: "deepseek"}, "other": {Provider: "openrouter"},
	}
	for _, tc := range []struct {
		claimants []string
		want      string
	}{
		{[]string{"zai"}, "zai a-zai"},
		{[]string{"deepseek"}, "a-ds b-ds"},
		{[]string{"deepseek", "zai"}, "zai a-ds a-zai b-ds"},
		{[]string{"cerebras"}, ""},
	} {
		if got := strings.Join(remedyProfiles(resolved, tc.claimants), " "); got != tc.want {
			t.Errorf("remedyProfiles(%v) = %q, want %q", tc.claimants, got, tc.want)
		}
	}
}

// scopeLine returns the credential-scope disclosure line whose name list holds name, failing
// when there is none.
func scopeLine(t *testing.T, errs, name string) string {
	t.Helper()
	for _, l := range strings.Split(errs, "\n") {
		head, _, ok := strings.Cut(l, " (provider ")
		if ok && strings.Contains(", "+strings.TrimSpace(head)+", ", ", "+name+", ") {
			return l
		}
	}
	t.Fatalf("no disclosure line names %s:\n%s", name, errs)
	return ""
}

// ES-D4: a withheld name the invoking shell also exports reaches the command anyway, from the
// shell (CN-D13), so the line says yolo did not add it and never calls it withheld; nor does it
// offer a remedy for a value the command already has.
func TestHostGrantShellHeldNameIsDisclosedAsNotAddedNeverWithheld(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig,
		map[string]string{"ZAI_API_KEY": "tok-shell"}, nil, "bash")
	if env["ZAI_API_KEY"] != "tok-shell" {
		t.Errorf("the invoking shell's value passes through untouched: ZAI_API_KEY = %q", env["ZAI_API_KEY"])
	}
	line := scopeLine(t, errs, "ZAI_API_KEY")
	if strings.Contains(line, "withheld") {
		t.Errorf("bash holds the shell's ZAI_API_KEY, so the line must not say it was withheld "+
			"(ES-D4): %q", line)
	}
	for _, want := range []string{"not added by yolo", "invoking shell"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line must say yolo did not add it and the shell's value passes (%q "+
				"missing): %q", want, line)
		}
	}
	if strings.Contains(line, "yolo host -p") || strings.Contains(line, "--with-credentials") {
		t.Errorf("a value the command already holds needs no remedy: %q", line)
	}
}

// The same at `yolo host env`: the eval'ing shell is the one that already holds the value.
func TestHostEnvShellHeldNameIsDisclosedAsNotAdded(t *testing.T) {
	hostGateHome(t, esGrantConfig, map[string]string{"ZAI_API_KEY": "tok-shell"})
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	if hostExports(out.String(), "ZAI_API_KEY") {
		t.Errorf("claude's slice still adds nothing for zai's key:\n%s", out.String())
	}
	line := scopeLine(t, errw.String(), "ZAI_API_KEY")
	if strings.Contains(line, "withheld") || !strings.Contains(line, "not added by yolo") {
		t.Errorf("yolo host env must say the shell's own ZAI_API_KEY passes through (ES-D4): %q", line)
	}
}

// ES-D4 asks what the process ends up holding, not only what the shell exported: a value yolo
// composed over the shell's (here a local pack's static env of the same name) means the
// shell's value does NOT pass through, so the line must not say it does. Nor may it say the
// name was withheld from every process, which the exported script contradicts: the
// env_sources value is not delivered, and the process holds one yolo composed from another
// source. The same with no shell value at all. The remedy stays, since a granted
// env_sources value beats the pack's.
func TestHostEnvShellValueComposedOverIsNotDisclosedAsPassingThrough(t *testing.T) {
	for _, shell := range []map[string]string{{"ZAI_API_KEY": "tok-shell"}, nil} {
		home := hostGateHome(t, esGrantConfig, shell)
		dir := filepath.Join(home, ".config", "yolo-jail", "local")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name":"local",`+
			`"contributes":[{"kind":"env","vars":{"ZAI_API_KEY":"tok-pack"}}]}`), 0o644); err != nil {
			t.Fatal(err)
		}
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"env", "--agent", "bash"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
		}
		if !strings.Contains(out.String(), "export ZAI_API_KEY='tok-pack'") {
			t.Fatalf("the fixture must compose the pack's value over the shell's:\n%s", out.String())
		}
		line := scopeLine(t, errw.String(), "ZAI_API_KEY")
		if strings.Contains(line, "not added by yolo") {
			t.Errorf("shell %v: the shell's value is overridden, so it does not pass through: %q", shell, line)
		}
		if strings.Contains(line, "withheld") {
			t.Errorf("shell %v: the process holds ZAI_API_KEY, so no line may call it withheld: %q", shell, line)
		}
		for _, want := range []string{"not delivered from env_sources", "composed from another source",
			"yolo host env --with-credentials zai"} {
			if !strings.Contains(line, want) {
				t.Errorf("shell %v: the line must say %q: %q", shell, want, line)
			}
		}
	}
}

// The same at `yolo host --`: the exec'd process holds the pack's value, and the line says so.
func TestHostGrantComposedNameIsNotDisclosedAsWithheld(t *testing.T) {
	home := hostGateHome(t, esGrantConfig, nil)
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name":"local",`+
		`"contributes":[{"kind":"env","vars":{"ZAI_API_KEY":"tok-pack"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rc, _, env, errs := runRemedy(t, []string{"--", "bash"})
	if rc != 0 || env["ZAI_API_KEY"] != "tok-pack" {
		t.Fatalf("yolo host -- bash: rc = %d, ZAI_API_KEY = %q, want the pack's value\n%s", rc, env["ZAI_API_KEY"], errs)
	}
	line := scopeLine(t, errs, "ZAI_API_KEY")
	if strings.Contains(line, "withheld") || !strings.Contains(line, "composed from another source") {
		t.Errorf("bash holds the pack's ZAI_API_KEY, so the line must say so, never withheld: %q", line)
	}
}

// With the grant typed, the env_sources value beats the shell's and the line is the grant's
// ("bash only"): ES-D4 rewords only a withheld line.
func TestHostGrantBeatsTheShellsValue(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig,
		map[string]string{"ZAI_API_KEY": "tok-shell"}, []string{"--with-credentials", "zai"}, "bash")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("the grant delivers the env_sources value over the shell's: ZAI_API_KEY = %q",
			env["ZAI_API_KEY"])
	}
	if line := scopeLine(t, errs, "ZAI_API_KEY"); !strings.HasSuffix(line, "ZAI_API_KEY (provider zai): bash only") {
		t.Errorf("the grant is disclosed as the grant: %q", line)
	}
}

// esUseProfilesBash is the design's ES-D5 case: a profile entry keyed by a command no pack
// installs, which `yolo check` and every jail launch already refuse.
const esUseProfilesBash = `{"packs": ["claude", "pi", "zai"], "profile": {"bash": "zai"}, ` +
	`"env_sources": [{"ZAI_API_KEY": "tok-es", "PORT": "8080"}]}`

// ES-D5: no profile keys a command no pack installs. The profile entry is refused at
// `yolo host --` before anything is exec'd, with the validator's own message and the grant
// spelling that is legal (OQ-NC5); it used to deliver here only because the host skips
// validation.
func TestHostGrantRefusesAUseProfilesKeyNoPackInstalls(t *testing.T) {
	rc, env, errs := hostGateRun(t, esUseProfilesBash, nil, nil, "bash")
	if rc == 0 || env != nil {
		t.Fatalf("profile {bash: zai} must refuse `yolo host -- bash` before the exec; "+
			"rc = %d, reached exec = %v\n%s", rc, env != nil, errs)
	}
	want, unknown := config.UnknownProfileKey("bash")
	if !unknown {
		t.Fatal("fixture: the validator must refuse a profile key for bash")
	}
	for _, s := range []string{want, "`yolo host --with-credentials zai -- bash`"} {
		if !strings.Contains(errs, s) {
			t.Errorf("the refusal must carry %q:\n%s", s, errs)
		}
	}
}

// The same file with the grant typed REFUSES too, in the validator's words: the host runs the
// provider and profile section of validation (notch-convergence item 13, row A8), so a config
// every jail launch refuses — `yolo -p zai -- claude` included — refuses every host launch too.
// ES-D9 exempted a typed -p while the host ran only ES-D5's one-key check. Without the entry the
// typed -p still refuses, since it reaches agent CLIs only (OQ-NC5), and the grant delivers.
func TestHostGrantTypedProfileRefusesBesideARefusedEntry(t *testing.T) {
	rc, env, errs := hostGateRun(t, esUseProfilesBash, nil, []string{"-p", "zai"}, "bash")
	want, _ := config.UnknownProfileKey("bash")
	if rc == 0 || env != nil || !strings.Contains(errs, want) {
		t.Errorf("a typed -p beside a profile key every launch refuses must refuse with the "+
			"validator's message: rc = %d\n%s", rc, errs)
	}
	clean := strings.Replace(esUseProfilesBash, `"profile": {"bash": "zai"}, `, "", 1)
	if rc, env, errs := hostGateRun(t, clean, nil, []string{"-p", "zai"}, "bash"); rc == 0 || env != nil {
		t.Errorf("a typed -p zai reaches agent CLIs only, so -- bash refuses: rc = %d\n%s", rc, errs)
	}
	env, errs = hostGateLaunchWith(t, clean, nil, []string{"--with-credentials", "zai"}, "bash")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("--with-credentials zai delivers the key: ZAI_API_KEY = %q\n%s", env["ZAI_API_KEY"], errs)
	}
}

// `yolo host env` composes the same way, so it refuses the entry too, typed -p or not, naming the
// grant's shell spelling. With the entry gone its typed -p still refuses for bash (OQ-NC5), and
// the grant prints the key for a shell to eval: `eval "$(yolo host env --with-credentials zai)"`.
func TestHostEnvRefusesTheEntryAndPrintsTheTypedGrant(t *testing.T) {
	home := hostGateHome(t, esUseProfilesBash, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "bash"}, &out, &errw, false, nil); rc == 0 {
		t.Errorf("yolo host env --agent bash must refuse profile {bash: zai}:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "`eval \"$(yolo host env --with-credentials zai)\"`") {
		t.Errorf("the refusal names the env verb's grant spelling:\n%s", errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"env", "--agent", "bash", "-p", "zai"}, &out, &errw, false, nil); rc == 0 {
		t.Errorf("yolo host env --agent bash -p zai must refuse the entry too:\n%s", out.String())
	}
	userCfg(t, home, strings.Replace(esUseProfilesBash, `"profile": {"bash": "zai"}, `, "", 1))
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"env", "--agent", "bash", "-p", "zai"}, &out, &errw, false, nil); rc == 0 {
		t.Errorf("yolo host env --agent bash -p zai: a bare -p reaches agent CLIs only (OQ-NC5):\n%s", out.String())
	} else if !strings.Contains(errw.String(), "`eval \"$(yolo host env --with-credentials zai)\"`") {
		t.Errorf("the refusal names the grant's shell spelling:\n%s", errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"env", "--with-credentials", "zai"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env --with-credentials zai: rc = %d\n%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "export ZAI_API_KEY='tok-es'") {
		t.Errorf("the grant prints the key for the shell:\n%s", out.String())
	}
}

// The validator's rule, not a narrower one: a profile key naming a shipped pack's CLI is
// accepted by `yolo check` whether or not that pack is selected, so the host accepts it too
// rather than refusing what the validator passes.
func TestHostGrantAcceptsAUseProfilesKeyTheValidatorAccepts(t *testing.T) {
	env, errs := hostGateLaunchWith(t, `{"packs": ["claude", "zai"], "profile": {"codex": "zai"}, `+
		`"env_sources": [{"ZAI_API_KEY": "tok-es"}]}`, nil, nil, "codex")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("profile {codex: zai} is valid config, so codex is keyed: ZAI_API_KEY = %q\n%s",
			env["ZAI_API_KEY"], errs)
	}
}

// OQ-NC5 in `yolo host --help`: -p is described as reaching agent CLIs only, at every notch, an
// ad-hoc command's key coming from --with-credentials, and the examples say so. Read through
// hostMain's help arm, the call site a user reaches.
func TestHostHelpDescribesProfileAsReachingAgentCLIsOnly(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--help"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host --help: rc = %d\n%s", rc, errw.String())
	}
	help := out.String()
	_, flag, ok := strings.Cut(help, "--profile <name>, -p <name>")
	if !ok {
		t.Fatalf("yolo host --help documents no -p:\n%s", help)
	}
	flag, _, _ = strings.Cut(flag, "--help, -h")
	for _, want := range []string{"agent CLI", "refused", "--with-credentials"} {
		if !strings.Contains(flag, want) {
			t.Errorf("the -p entry must say %q:\n%s", want, flag)
		}
	}
	for _, gone := range []string{"yolo host -p zai -- curl", "yolo host env --agent bash -p zai",
		"Any command, not only an agent"} {
		if strings.Contains(help, gone) {
			t.Errorf("-p reaches agent CLIs only (OQ-NC5); the help still shows %q:\n%s", gone, help)
		}
	}
	for _, want := range []string{"yolo host --with-credentials zai -- curl",
		`eval "$(yolo host env --with-credentials zai)"`} {
		if !strings.Contains(help, want) {
			t.Errorf("the help shows the grant's spelling %q:\n%s", want, help)
		}
	}
	// ONE RECIPIENT RULE, NAMED AT BOTH NOTCHES (notch-convergence item 11's done-when): the
	// jail's `yolo run --help` names the same rule for its -p.
	var rc int
	jail, _ := captureBoth(t, func() { rc = runRun([]string{"run", "--help"}) })
	_, jailFlag, _ := strings.Cut(jail, "--profile <sel>")
	jailFlag, _, _ = strings.Cut(jailFlag, "--timing")
	for _, want := range []string{"agent CLI", "never the command after `--`", "--with-credentials"} {
		if rc != 0 || !strings.Contains(jailFlag, want) {
			t.Errorf("yolo run --help's -p entry must say %q (rc %d):\n%s", want, rc, jailFlag)
		}
	}
}
