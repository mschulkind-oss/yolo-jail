package cli

// hostwithcredentials_test.go pins THE HOST'S EXPLICIT GRANT, `yolo host --with-credentials
// <provider[,provider...]|all> -- <cmd>` (docs/design/credential-sources-separation.md OQ-ES5,
// ruled for the host 2026-09-27): the one command receives the named providers' CLAIMED
// env_sources values, keys only, disclosed by name on every run. Every cell runs `yolo host`
// through hostMain to the exec (hostGateRun), as hostcredentialgrant_test.go's cells do, so a
// cell fails when the flag's parse, its hand-off to the composition or the gate's delivery is
// deleted, not only when a helper changes.

import (
	"bytes"
	"strings"
	"testing"
)

// wcConfig is the maintainer's case: several providers' keys in the ONE env_sources store,
// beside an unclaimed value, and no profile selecting any of them. kilo is selected and claims
// KILO_API_KEY, which the store does not hold.
const wcConfig = `{"packs": ["claude", "zai", "cerebras", "openrouter", "kilo"], "env_sources": [` +
	`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "OPENROUTER_API_KEY": "tok-o", "PORT": "8080"}]}`

// wcBlank blanks the names these cells read that hostGateNames does not, so what the command
// receives is what yolo composed.
var wcBlank = map[string]string{"CEREBRAS_API_KEY": "", "OPENROUTER_API_KEY": "", "KILO_API_KEY": "",
	"YOLO_ALLOW_MISSING_PROVIDERS": ""}

func wcShell(extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range wcBlank {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// The case the ruling is for: an ad-hoc command (the usage bar) asks for named providers and
// receives exactly their claimed keys, the unclaimed value as always, and nothing of the
// providers it did not name.
func TestWithCredentialsHandsTheCommandTheNamedProvidersKeys(t *testing.T) {
	env, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil),
		[]string{"--with-credentials", "zai,cerebras"}, "bash")
	for k, want := range map[string]string{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "PORT": "8080"} {
		if env[k] != want {
			t.Errorf("yolo host --with-credentials zai,cerebras -- bash: %s = %q, want %q\n%s", k, env[k], want, errs)
		}
	}
	if env["OPENROUTER_API_KEY"] != "" {
		t.Errorf("openrouter was not named, yet bash was handed OPENROUTER_API_KEY=%q", env["OPENROUTER_API_KEY"])
	}
	if line := scopeLine(t, errs, "ZAI_API_KEY"); !strings.HasSuffix(line, "): bash only") {
		t.Errorf("the gate's line names the grant's one recipient: %q", line)
	}
	if line := scopeLine(t, errs, "OPENROUTER_API_KEY"); !strings.Contains(line, "withheld") {
		t.Errorf("an unnamed provider's key stays withheld: %q", line)
	}
}

// KEYS ONLY: a grant selects no profile, so no derive runs and nothing is re-pointed — claude
// granted zai's key gets that key and none of the zai profile's shape (no base URL, no token
// under claude's own name).
func TestWithCredentialsIsKeysOnlyNoShape(t *testing.T) {
	env, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil), []string{"--with-credentials", "zai"}, "claude")
	if env["ZAI_API_KEY"] != "tok-z" {
		t.Errorf("claude granted zai: ZAI_API_KEY = %q\n%s", env["ZAI_API_KEY"], errs)
	}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"} {
		if env[k] != "" {
			t.Errorf("a grant re-points nothing, yet claude was handed %s=%q", k, env[k])
		}
	}
}

// `all` is every composed provider that claims a value env_sources holds — and only those:
// kilo claims a name the store lacks, so `all` does not name it and nothing is reported for it.
func TestWithCredentialsAllIsEveryProviderClaimingAValue(t *testing.T) {
	env, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil), []string{"--with-credentials=all"}, "bash")
	for k, want := range map[string]string{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "OPENROUTER_API_KEY": "tok-o"} {
		if env[k] != want {
			t.Errorf("--with-credentials all: %s = %q, want %q\n%s", k, env[k], want, errs)
		}
	}
	for _, want := range []string{"Credential grant (--with-credentials all)", "  cerebras: CEREBRAS_API_KEY",
		"  openrouter: OPENROUTER_API_KEY", "  zai: ZAI_API_KEY"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the grant's disclosure must carry %q:\n%s", want, errs)
		}
	}
	if strings.Contains(errs, "  kilo:") {
		t.Errorf("kilo claims no value env_sources holds, so `all` does not name it:\n%s", errs)
	}
}

// The grant is disclosed on every run, by name and never by value, and says what a grant is.
func TestWithCredentialsIsDisclosedByNameNeverByValue(t *testing.T) {
	_, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil), []string{"--with-credentials", "zai"}, "bash")
	for _, want := range []string{"yolo host: Credential grant (--with-credentials zai): bash receives",
		"keys only", "re-points nothing", "every process bash starts inherits them", "  zai: ZAI_API_KEY"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the grant's disclosure must say %q:\n%s", want, errs)
		}
	}
	for _, v := range []string{"tok-z", "tok-c", "tok-o"} {
		if strings.Contains(errs, v) {
			t.Errorf("the disclosure printed a credential VALUE (%s):\n%s", v, errs)
		}
	}
	// Even a grant that delivers nothing is disclosed.
	_, errs = hostGateLaunchWith(t, `{"packs": ["claude", "zai"], "env_sources": [{"PORT": "1"}]}`,
		wcShell(nil), []string{"--with-credentials", "all"}, "bash")
	if !strings.Contains(errs, "Credential grant (--with-credentials all)") ||
		!strings.Contains(errs, "nothing granted") {
		t.Errorf("a grant that delivers nothing is still disclosed, saying so:\n%s", errs)
	}
}

// A named provider env_sources holds no value for is REPORTED, never silently skipped, and the
// launch still runs with what the rest of the grant delivered.
func TestWithCredentialsReportsANamedProviderWithNoValue(t *testing.T) {
	env, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil), []string{"--with-credentials", "zai,kilo"}, "bash")
	if env["ZAI_API_KEY"] != "tok-z" {
		t.Errorf("zai's key still arrives: %q\n%s", env["ZAI_API_KEY"], errs)
	}
	want := "  kilo: nothing granted — env_sources holds no value for the names it claims (KILO_API_KEY)"
	if !strings.Contains(errs, want) {
		t.Errorf("kilo was named and delivered nothing, which must be reported (%q):\n%s", want, errs)
	}
}

// An unknown provider name REFUSES before anything is exec'd, naming the known ones.
func TestWithCredentialsRefusesAnUnknownProviderNamingTheKnownOnes(t *testing.T) {
	rc, env, errs := hostGateRun(t, wcConfig, wcShell(nil), []string{"--with-credentials", "zai,zia"}, "bash")
	if rc == 0 || env != nil {
		t.Fatalf("--with-credentials zia must refuse before the exec: rc = %d, reached exec = %v\n%s", rc, env != nil, errs)
	}
	for _, want := range []string{"refusing to launch", `"zia"`, "cerebras", "kilo", "openrouter", "zai", "`all`"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must carry %q:\n%s", want, errs)
		}
	}
}

// An empty value is a mistake, refused at the parse rather than read as a grant of nothing.
func TestWithCredentialsRefusesAnEmptyValue(t *testing.T) {
	for _, flags := range [][]string{{"--with-credentials", ""}, {"--with-credentials=zai,"}, {"--with-credentials"}} {
		rc, env, errs := hostGateRun(t, wcConfig, wcShell(nil), flags, "bash")
		if rc != 2 || env != nil {
			t.Errorf("%q: rc = %d (want 2), reached exec = %v\n%s", flags, rc, env != nil, errs)
		}
	}
}

// Repeated, the flag's lists merge.
func TestWithCredentialsIsRepeatable(t *testing.T) {
	env, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil),
		[]string{"--with-credentials=zai", "--with-credentials", "openrouter"}, "bash")
	if env["ZAI_API_KEY"] != "tok-z" || env["OPENROUTER_API_KEY"] != "tok-o" {
		t.Errorf("both occurrences grant: ZAI=%q OPENROUTER=%q\n%s", env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"], errs)
	}
}

// IT COMBINES WITH -p: an agent keeps its profile — its provider's keys and the shape its
// derive composes — and additionally receives the granted keys. The grant re-points nothing,
// so the agent's backend is still the profile's.
func TestWithCredentialsCombinesWithAProfile(t *testing.T) {
	cfg := `{"packs": ["claude", "zai", "cerebras"], "env_sources": [` +
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "AWS_ACCESS_KEY_ID": "AKIA-host"}]}`
	env, errs := hostGateLaunchWith(t, cfg, wcShell(nil),
		[]string{"-p", "bedrock", "--with-credentials", "zai"}, "claude")
	for k, want := range map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_ACCESS_KEY_ID": "AKIA-host", "ZAI_API_KEY": "tok-z"} {
		if env[k] != want {
			t.Errorf("claude on bedrock granted zai: %s = %q, want %q\n%s", k, env[k], want, errs)
		}
	}
	if env["CEREBRAS_API_KEY"] != "" || strings.Contains(env["ANTHROPIC_BASE_URL"], "z.ai") {
		t.Errorf("the grant named zai's key only and re-points nothing: CEREBRAS=%q BASE_URL=%q",
			env["CEREBRAS_API_KEY"], env["ANTHROPIC_BASE_URL"])
	}
	if !strings.Contains(errs, "claude keeps its bedrock profile") {
		t.Errorf("the grant's disclosure says the agent keeps its profile:\n%s", errs)
	}
}

// NOTHING ELSE IMPLIES IT: not -p, not profile, not any YOLO_ALLOW_* variable, and no
// variable spelling of the flag. Without the typed flag no grant line is printed and an
// unselected provider's key stays withheld.
func TestWithCredentialsIsImpliedByNothingElse(t *testing.T) {
	shell := wcShell(map[string]string{"YOLO_ALLOW_MISSING_PROVIDERS": "1", "YOLO_ALLOW_SOURCE_SKEW": "1",
		"YOLO_ALLOW_UNREACHABLE_SERVICES": "1", "YOLO_ALLOW_ATTACH_SKEW": "1", "YOLO_WITH_CREDENTIALS": "all",
		"YOLO_ALLOW_ALL_CREDENTIALS": "1"})
	cfg := `{"packs": ["claude", "zai", "cerebras"], "profile": {"claude": "zai"}, "env_sources": [` +
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c"}]}`
	for _, tc := range []struct {
		flags []string
		cmd   string
	}{
		{nil, "claude"}, // profile selects zai for claude
		{nil, "bash"},   // an ad-hoc command, which no profile reaches (OQ-NC5)
		{[]string{"-p", "zai"}, "claude"},
	} {
		env, errs := hostGateLaunchWith(t, cfg, shell, tc.flags, tc.cmd)
		if env["CEREBRAS_API_KEY"] != "" {
			t.Errorf("%v -- %s: nothing named cerebras, yet CEREBRAS_API_KEY=%q", tc.flags, tc.cmd, env["CEREBRAS_API_KEY"])
		}
		if strings.Contains(errs, "Credential grant") {
			t.Errorf("%v -- %s: no --with-credentials was typed, yet a grant was disclosed:\n%s", tc.flags, tc.cmd, errs)
		}
	}
}

// `eval "$(yolo host env --with-credentials all)"` exports the keys into the current shell, and
// only the keys: with no --agent the script is the ad-hoc slice, so claude's zai profile —
// selected by profile — sends none of its shape into the shell, and the grant's disclosure
// goes to stderr with the verb's prefix.
func TestHostEnvWithCredentialsExportsTheKeysOnly(t *testing.T) {
	hostGateHome(t, `{"packs": ["claude", "zai", "cerebras"], "profile": {"claude": "zai"}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "PORT": "8080"}]}`, wcShell(nil))
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--with-credentials", "all"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env --with-credentials all: rc = %d\n%s", rc, errw.String())
	}
	script := out.String()
	for _, want := range []string{"export ZAI_API_KEY='tok-z'", "export CEREBRAS_API_KEY='tok-c'", "export PORT='8080'"} {
		if !strings.Contains(script, want) {
			t.Errorf("the script must carry %q:\n%s", want, script)
		}
	}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL"} {
		if strings.Contains(script, k) {
			t.Errorf("a grant re-points nothing, yet the script exports claude's %s:\n%s", k, script)
		}
	}
	errs := errw.String()
	if !strings.Contains(errs, "yolo host env: Credential grant (--with-credentials all): the script exports") {
		t.Errorf("the grant is disclosed on stderr with the verb's prefix:\n%s", errs)
	}
	if strings.Contains(script, "Credential grant") {
		t.Errorf("the disclosure must not reach the eval'd stdout:\n%s", script)
	}
}

// With --agent, `yolo host env` composes that agent's slice — its profile included — and adds
// the granted keys beside it, as the exec does.
func TestHostEnvWithCredentialsKeepsANamedAgentsProfile(t *testing.T) {
	hostGateHome(t, `{"packs": ["claude", "zai", "cerebras"], "env_sources": [`+
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c"}]}`, wcShell(nil))
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--agent", "claude", "-p", "zai", "--with-credentials", "cerebras"},
		&out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc = %d\n%s", rc, errw.String())
	}
	for _, want := range []string{"export CEREBRAS_API_KEY='tok-c'", "export ANTHROPIC_BASE_URL="} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("claude's zai slice plus cerebras's key must carry %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(errw.String(), "claude's slice keeps its zai profile") {
		t.Errorf("the disclosure says the slice keeps its profile:\n%s", errw.String())
	}
}

// `yolo host env` refuses an unknown provider too, as an error rather than a script.
func TestHostEnvWithCredentialsRefusesAnUnknownProvider(t *testing.T) {
	hostGateHome(t, wcConfig, wcShell(nil))
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env", "--with-credentials", "nope"}, &out, &errw, false, nil); rc == 0 {
		t.Fatalf("yolo host env --with-credentials nope must refuse:\n%s", out.String())
	}
	if out.Len() != 0 || !strings.Contains(errw.String(), `"nope"`) {
		t.Errorf("stdout = %q, stderr = %q", out.String(), errw.String())
	}
}

// The help documents the flag at both front doors, and says a jail launch takes it too (OQ-ES5's
// jail half: the HOST ONLY sentence it carried until 2026-10-05 is gone).
func TestHostHelpDocumentsWithCredentials(t *testing.T) {
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"--help"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if strings.Contains(out.String(), "HOST ONLY") {
		t.Errorf("yolo host --help still calls the grant host-only:\n%s", out.String())
	}
	for _, want := range []string{"--with-credentials <provider[,provider...]|all>", "KEYS ONLY",
		"A jail launch\n                                takes the same flag",
		`eval "$(yolo host env --with-credentials all)"`,
		// -p's own help names the grant as the one route to an ad-hoc command (OQ-NC5).
		"a provider's key, --with-credentials below."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("yolo host --help must say %q", want)
		}
	}
}

// A TYPED -p KEEPS ITS SLICE UNDER A GRANT (ES-D21): `yolo host env -p zai --with-credentials
// cerebras` is `yolo host env -p zai`, which composes claude's zai slice, with cerebras's key
// added. The grant only adds keys. So it must not move the script to another agent's slice and
// drop the profile's shape. Only a run with neither --agent nor -p takes the ad-hoc slice
// (ES-D16).
func TestHostEnvWithCredentialsKeepsATypedProfilesSlice(t *testing.T) {
	hostGateHome(t, `{"packs": ["claude", "zai", "cerebras"], "env_sources": [`+
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c"}]}`, wcShell(nil))
	script := func(args ...string) (string, string) {
		t.Helper()
		var out, errw bytes.Buffer
		if rc := hostMain(append([]string{"env"}, args...), &out, &errw, false, nil); rc != 0 {
			t.Fatalf("yolo host env %v: rc = %d\n%s", args, rc, errw.String())
		}
		return out.String(), errw.String()
	}
	plain, _ := script("-p", "zai")
	granted, errs := script("-p", "zai", "--with-credentials", "cerebras")
	if !strings.Contains(plain, "export ANTHROPIC_BASE_URL=") {
		t.Fatalf("the fixture's -p zai slice carries the profile's shape:\n%s", plain)
	}
	for _, line := range strings.Split(strings.TrimSpace(plain), "\n") {
		if !strings.Contains(granted, line) {
			t.Errorf("the grant dropped %q from the -p zai slice:\n%s", line, granted)
		}
	}
	if !strings.Contains(granted, "export CEREBRAS_API_KEY='tok-c'") {
		t.Errorf("the granted key is added beside the slice:\n%s", granted)
	}
	if !strings.Contains(errs, "claude's slice keeps its zai profile") {
		t.Errorf("the disclosure names the slice the typed -p composes:\n%s", errs)
	}
}

// Through the host's own disclosure path: the rule line heading `--with-credentials zai --
// usage-bar`'s scope block names the grant, since usage-bar, which no profile selects, is the
// next line's recipient (ES-D22).
func TestWithCredentialsScopeHeaderNamesTheGrant(t *testing.T) {
	_, errs := hostGateLaunchWith(t, wcConfig, wcShell(nil), []string{"--with-credentials", "zai"}, "usage-bar")
	want := "yolo host: Credential scope: a provider's credential reaches only the processes whose " +
		"profile selects it or whose --with-credentials grant names it."
	if !strings.Contains(errs, want) || !strings.Contains(errs, "ZAI_API_KEY (provider zai): usage-bar only") {
		t.Errorf("the rule line must be true of its usage-bar recipient (%q):\n%s", want, errs)
	}
	if strings.Contains(errs, "reaches only the agents whose profile selects it.") {
		t.Errorf("the no-grant rule line is false of a grant-only recipient:\n%s", errs)
	}
}

// ON A RUN GIVEN A GRANT, A WITHHELD LINE NAMES THE GRANT WIDENED (ES-D23). A named -p would
// replace the typed one and silently drop the grant: `yolo host -p cerebras -- usage-bar` loses
// ZAI_API_KEY. The additive command is the same run with the claimant added to the grant, and
// every command the line names runs and still delivers the key the grant already carried.
func TestWithCredentialsRemedyWidensTheGrant(t *testing.T) {
	cfg := `{"packs": ["claude", "zai", "cerebras"], "env_sources": [` +
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "AWS_ACCESS_KEY_ID": "AKIA-host"}]}`
	for _, tc := range []struct {
		flags []string
		cmd   string
		want  string
		keeps map[string]string
	}{
		{[]string{"--with-credentials", "zai"}, "usage-bar",
			"To add it to this launch's grant: `yolo host --with-credentials zai,cerebras -- usage-bar`",
			map[string]string{"ZAI_API_KEY": "tok-z"}},
		{[]string{"-p", "bedrock", "--with-credentials", "zai"}, "claude",
			"To add it to this launch's grant: `yolo host -p bedrock --with-credentials zai,cerebras -- claude`",
			map[string]string{"ZAI_API_KEY": "tok-z", "CLAUDE_CODE_USE_BEDROCK": "1"}},
	} {
		_, errs := hostGateLaunchWith(t, cfg, wcShell(nil), tc.flags, tc.cmd)
		line := scopeLine(t, errs, "CEREBRAS_API_KEY")
		if !strings.Contains(line, tc.want) {
			t.Errorf("%v -- %s: the remedy must widen the grant (%q):\n%s", tc.flags, tc.cmd, tc.want, line)
		}
		if strings.Contains(line, "`yolo host -p cerebras") {
			t.Errorf("%v -- %s: a named -p drops the grant, so it is not the remedy on a grant run:\n%s",
				tc.flags, tc.cmd, line)
		}
		assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
		for _, argv := range remedyCommands(line) {
			_, _, env, _ := runRemedy(t, argv)
			for k, v := range tc.keeps {
				if env[k] != v {
					t.Errorf("the remedy `yolo host %s` lost %s (= %q, want %q)", strings.Join(argv, " "), k, env[k], v)
				}
			}
		}
	}
}

// `yolo host env` names the shell spelling of the widened grant, keeping the flags that chose the
// slice: none for the ad-hoc default, the typed -p, and --agent where it is not the default.
func TestHostEnvWithCredentialsRemedyWidensTheGrant(t *testing.T) {
	cfg := `{"packs": ["claude", "zai", "cerebras", "openrouter"], "env_sources": [` +
		`{"ZAI_API_KEY": "tok-z", "CEREBRAS_API_KEY": "tok-c", "OPENROUTER_API_KEY": "tok-o"}]}`
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--with-credentials", "zai"},
			"`eval \"$(yolo host env --with-credentials zai,cerebras)\"`"},
		{[]string{"-p", "zai", "--with-credentials", "openrouter"},
			"`eval \"$(yolo host env -p zai --with-credentials openrouter,cerebras)\"`"},
		{[]string{"--agent", "claude", "--with-credentials", "zai"},
			"`eval \"$(yolo host env --agent claude --with-credentials zai,cerebras)\"`"},
	} {
		hostGateHome(t, cfg, wcShell(nil))
		var out, errw bytes.Buffer
		if rc := hostMain(append([]string{"env"}, tc.args...), &out, &errw, false, nil); rc != 0 {
			t.Fatalf("yolo host env %v: rc = %d\n%s", tc.args, rc, errw.String())
		}
		line := scopeLine(t, errw.String(), "CEREBRAS_API_KEY")
		if !strings.Contains(line, "To add it to this shell's grant: "+tc.want) {
			t.Errorf("yolo host env %v: the remedy must be %s:\n%s", tc.args, tc.want, line)
		}
		assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
		for _, argv := range remedyCommands(line) {
			rc, script, _, errs := runRemedy(t, argv)
			for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
				if rc != 0 || !strings.Contains(script, l) {
					t.Errorf("the remedy `yolo host %s` must export everything the original did (%q "+
						"missing):\n%s\n%s", strings.Join(argv, " "), l, script, errs)
				}
			}
		}
	}
}
