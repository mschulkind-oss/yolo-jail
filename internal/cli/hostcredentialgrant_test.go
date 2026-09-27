package cli

// hostcredentialgrant_test.go pins THE HOST'S GRANT for an ad-hoc command
// (docs/design/credential-sources-separation.md §5, ES-D1 to ES-D5): `yolo host -p <profile> --
// <cmd>` makes any command, not only an agent a pack installs, a recipient of that profile's
// claimed env_sources values. Every cell runs `yolo host` through hostMain to the exec, as the
// TestHostGate* cells in hostcredentialgate_test.go do, with `bash` as the command: no selected
// pack installs it, so no pack code runs for it and the grant is the only thing that can hand it
// a claimed value.

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

// ES-D1: `yolo host -p zai -- bash` hands bash zai's claimed key, keeps the unclaimed value, and
// says the key went to bash alone. It fails if ScopeCredentials' agent loop starts asking
// whether a pack installs the name, or if effectiveHostProfiles stops keying the launched
// basename: both are what make a non-agent a recipient.
func TestHostGrantTypedProfileHandsAnAdHocCommandItsClaimedValues(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig, nil, []string{"-p", "zai"}, "bash")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("yolo host -p zai -- bash: ZAI_API_KEY = %q, want the env_sources value — "+
			"a typed -p is the host's grant for any command\n%s", env["ZAI_API_KEY"], errs)
	}
	if env["PORT"] != "8080" {
		t.Errorf("an unclaimed env_sources value reaches every process, bash on zai included: "+
			"PORT = %q\n%s", env["PORT"], errs)
	}
	if !strings.Contains(errs, "ZAI_API_KEY (provider zai): bash only") {
		t.Errorf("the grant is disclosed, naming its one recipient:\n%s", errs)
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

// ES-D2 at `yolo host --`: the withheld line names the one existing remedy, the typed -p that
// would deliver the key, spelled for the command this launch was given. The profile is a
// declared one resolving to the claiming provider — zai's own same-named profile here.
func TestHostGrantWithheldLineNamesTheTypedProfileRemedy(t *testing.T) {
	_, errs := hostGateLaunchWith(t, esGrantConfig, nil, nil, "bash")
	line := scopeLine(t, errs, "ZAI_API_KEY")
	if !strings.Contains(line, "withheld") {
		t.Errorf("the key is still disclosed as withheld: %q", line)
	}
	if !strings.Contains(line, "`yolo host -p zai -- bash`") {
		t.Errorf("the withheld line must name `yolo host -p zai -- bash`, the grant that would "+
			"deliver it (ES-D2): %q", line)
	}
	if strings.Contains(line, "yolo host env") {
		t.Errorf("an exec names the exec spelling for the command it was given, not the env "+
			"verb's: %q", line)
	}
}

// ES-D2 at `yolo host env`: the same remedy, spelled for the slice the verb composes — its own
// --agent spelling for the shell, and the exec spelling for one launch of that agent.
func TestHostEnvWithheldLineNamesTheTypedProfileRemedy(t *testing.T) {
	hostGateHome(t, esGrantConfig, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	line := scopeLine(t, errw.String(), "ZAI_API_KEY")
	for _, want := range []string{"`yolo host env --agent claude -p zai`", "`yolo host -p zai -- claude`"} {
		if !strings.Contains(line, want) {
			t.Errorf("yolo host env's withheld line must name %s (ES-D2): %q", want, line)
		}
	}
}

// ES-D2's other arm: a provider the user declared under `providers` has no profile until the
// user declares one, and -p takes a profile name, so the line says to declare one rather than
// naming a -p that would refuse. The §1 incident's deepseek line.
func TestHostGrantWithheldLineSaysToDeclareAProfileWhenNoneSelectsTheProvider(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude"], `+
		`"providers": {"deepseek": {"base_url": "https://api.deepseek.example", `+
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil, nil, "bash")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	for _, want := range []string{"No declared profile selects deepseek", "`profiles`",
		`"deepseek": {"provider": "deepseek"}`, "`yolo host -p deepseek -- bash`"} {
		if !strings.Contains(line, want) {
			t.Errorf("with no profile selecting deepseek the line must say to declare one (%q "+
				"missing): %q", want, line)
		}
	}
}

// With a user profile declared over that provider, the remedy names it: a declared profile
// that resolves to the claimant, whatever it is called.
func TestHostGrantWithheldLineNamesAUserProfileOverTheProvider(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude"], `+
		`"providers": {"deepseek": {"base_url": "https://api.deepseek.example", `+
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}, `+
		`"profiles": {"ds": {"provider": "deepseek"}}, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil, nil, "bash")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	if !strings.Contains(line, "`yolo host -p ds -- bash`") {
		t.Errorf("the remedy must name the user's profile over deepseek: %q", line)
	}
}

// remedyProfile prefers the profile named after a claiming provider, and otherwise takes the
// first by name that resolves to any claimant.
func TestRemedyProfilePrefersTheProvidersOwnName(t *testing.T) {
	resolved := map[string]packload.ResolvedProfile{
		"a-zai": {Provider: "zai"}, "zai": {Provider: "zai"}, "b-ds": {Provider: "deepseek"},
		"a-ds": {Provider: "deepseek"}, "other": {Provider: "openrouter"},
	}
	for _, tc := range []struct {
		claimants []string
		want      string
	}{
		{[]string{"zai"}, "zai"},
		{[]string{"deepseek"}, "a-ds"},
		{[]string{"deepseek", "zai"}, "zai"},
		{[]string{"cerebras"}, ""},
	} {
		if got := remedyProfile(resolved, tc.claimants); got != tc.want {
			t.Errorf("remedyProfile(%v) = %q, want %q", tc.claimants, got, tc.want)
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
	if strings.Contains(line, "yolo host -p") {
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
	if strings.Contains(out.String(), "ZAI_API_KEY") {
		t.Errorf("claude's slice still adds nothing for zai's key:\n%s", out.String())
	}
	line := scopeLine(t, errw.String(), "ZAI_API_KEY")
	if strings.Contains(line, "withheld") || !strings.Contains(line, "not added by yolo") {
		t.Errorf("yolo host env must say the shell's own ZAI_API_KEY passes through (ES-D4): %q", line)
	}
}

// ES-D4 asks what the process ends up holding, not only what the shell exported: a value yolo
// composed over the shell's (here a local pack's static env of the same name) means the
// shell's value does NOT pass through, so the line must not say it does.
func TestHostEnvShellValueComposedOverIsNotDisclosedAsPassingThrough(t *testing.T) {
	home := hostGateHome(t, esGrantConfig, map[string]string{"ZAI_API_KEY": "tok-shell"})
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
	if line := scopeLine(t, errw.String(), "ZAI_API_KEY"); strings.Contains(line, "not added by yolo") {
		t.Errorf("the shell's value is overridden, so it does not pass through: %q", line)
	}
}

// With the grant typed, the env_sources value beats the shell's and the line is the grant's
// ("bash only"): ES-D4 rewords only a withheld line.
func TestHostGrantTypedProfileBeatsTheShellsValue(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig,
		map[string]string{"ZAI_API_KEY": "tok-shell"}, []string{"-p", "zai"}, "bash")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("a typed -p delivers the env_sources value over the shell's: ZAI_API_KEY = %q",
			env["ZAI_API_KEY"])
	}
	if line := scopeLine(t, errs, "ZAI_API_KEY"); !strings.HasSuffix(line, "ZAI_API_KEY (provider zai): bash only") {
		t.Errorf("the grant is disclosed as the grant: %q", line)
	}
}

// esUseProfilesBash is the design's ES-D5 case: a use_profiles entry keyed by a command no pack
// installs, which `yolo check` and every jail launch already refuse.
const esUseProfilesBash = `{"packs": ["claude", "pi", "zai"], "use_profiles": {"bash": "zai"}, ` +
	`"env_sources": [{"ZAI_API_KEY": "tok-es", "PORT": "8080"}]}`

// ES-D5: only a typed -p keys a command no pack installs. The use_profiles entry is refused at
// `yolo host --` before anything is exec'd, with the validator's own message and the -p
// spelling that is legal; it used to deliver here only because the host skips validation.
func TestHostGrantRefusesAUseProfilesKeyNoPackInstalls(t *testing.T) {
	rc, env, errs := hostGateRun(t, esUseProfilesBash, nil, nil, "bash")
	if rc == 0 || env != nil {
		t.Fatalf("use_profiles {bash: zai} must refuse `yolo host -- bash` before the exec; "+
			"rc = %d, reached exec = %v\n%s", rc, env != nil, errs)
	}
	want, unknown := config.UnknownUseProfileKey("bash")
	if !unknown {
		t.Fatal("fixture: the validator must refuse a use_profiles key for bash")
	}
	for _, s := range []string{want, "`yolo host -p zai -- bash`"} {
		if !strings.Contains(errs, s) {
			t.Errorf("the refusal must carry %q:\n%s", s, errs)
		}
	}
}

// The same file with the grant typed still delivers: the refusal is about what the ENTRY
// selects, and a typed -p is the legal spelling it names.
func TestHostGrantTypedProfileStillDeliversBesideARefusedEntry(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esUseProfilesBash, nil, []string{"-p", "zai"}, "bash")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("a typed -p zai delivers whatever use_profiles says: ZAI_API_KEY = %q\n%s",
			env["ZAI_API_KEY"], errs)
	}
}

// `yolo host env` composes the same way, so it refuses the entry too, and its own typed -p
// prints the key for a shell to eval — the design's `eval "$(yolo host env --agent bash -p zai)"`.
func TestHostEnvRefusesTheEntryAndPrintsTheTypedGrant(t *testing.T) {
	hostGateHome(t, esUseProfilesBash, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "bash"}, &out, &errw, false, nil); rc == 0 {
		t.Errorf("yolo host env --agent bash must refuse use_profiles {bash: zai}:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "`yolo host env --agent bash -p zai`") {
		t.Errorf("the refusal names the env verb's typed spelling:\n%s", errw.String())
	}
	out.Reset()
	errw.Reset()
	if rc := hostMain([]string{"env", "--agent", "bash", "-p", "zai"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env --agent bash -p zai: rc = %d\n%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "export ZAI_API_KEY='tok-es'") {
		t.Errorf("the typed grant prints the key for the shell:\n%s", out.String())
	}
}

// The validator's rule, not a narrower one: a use_profiles key naming a shipped pack's CLI is
// accepted by `yolo check` whether or not that pack is selected, so the host accepts it too
// rather than refusing what the validator passes.
func TestHostGrantAcceptsAUseProfilesKeyTheValidatorAccepts(t *testing.T) {
	env, errs := hostGateLaunchWith(t, `{"packs": ["claude", "zai"], "use_profiles": {"codex": "zai"}, `+
		`"env_sources": [{"ZAI_API_KEY": "tok-es"}]}`, nil, nil, "codex")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("use_profiles {codex: zai} is valid config, so codex is keyed: ZAI_API_KEY = %q\n%s",
			env["ZAI_API_KEY"], errs)
	}
}

// ES-D3: `yolo host --help` describes -p as what it is — a selection for the wrapped COMMAND,
// whatever it is, handing an ad-hoc one that profile's claimed env_sources values — and no
// longer as a preset "for the wrapped agent". Read through hostMain's help arm, the call site
// a user reaches.
func TestHostHelpDescribesProfileAsApplyingToTheWrappedCommand(t *testing.T) {
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
	for _, want := range []string{"wrapped COMMAND", "ad-hoc", "claimed env_sources"} {
		if !strings.Contains(flag, want) {
			t.Errorf("the -p entry must say %q:\n%s", want, flag)
		}
	}
	if strings.Contains(help, "for the wrapped agent") {
		t.Errorf("-p is not agent-only at the host (ES-D1); the help still says so:\n%s", help)
	}
	if !strings.Contains(help, "yolo host env --agent bash -p zai") {
		t.Errorf("the help shows the shell spelling of the grant:\n%s", help)
	}
}
