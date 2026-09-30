package cli

// hostprofilekey_test.go pins the HOST notch's call sites for the config `profile` key
// (docs/design/providers-and-profiles-redesign.md PP-D10): `yolo host -- <cmd>` selects the
// command's own entry, else "*" (or the string form) when a selected pack installs the command,
// and a typed -p above both; `yolo host apply` folds "*" over every agent its packs install.
// Each test drives the verb itself (hostMain), so deleting the key's read from
// composeHostVarsWith's selection, or the pack set from host apply's, fails here.

import (
	"bytes"
	"strings"
	"testing"
)

const hostProfileKeyPacks = `"packs": ["claude", "zai"], "env_sources": [{"ZAI_API_KEY": "tok-key"}]`

// The string form and "*" each select zai for claude, whose zai profile claims ZAI_API_KEY, so
// the host launch hands claude the key; with no selection it withholds it. The withheld
// control is what makes the key's delivery the thing under test.
func TestHostLaunchSelectsTheProfileKeysDefault(t *testing.T) {
	for name, sel := range map[string]string{
		"string form": `"zai"`,
		"\"*\"":       `{"*": "zai"}`,
		"named":       `{"claude": "zai"}`,
	} {
		env, errs := hostGateLaunchWith(t, `{`+hostProfileKeyPacks+`, "profile": `+sel+`}`, nil, nil, "claude")
		if env["ZAI_API_KEY"] != "tok-key" {
			t.Errorf("%s: `yolo host -- claude` did not select zai: ZAI_API_KEY = %q\n%s",
				name, env["ZAI_API_KEY"], errs)
		}
	}
	env, errs := hostGateLaunchWith(t, `{`+hostProfileKeyPacks+`}`, nil, nil, "claude")
	if env["ZAI_API_KEY"] != "" {
		t.Errorf("control: with no selection claude must not hold zai's key, got %q\n%s", env["ZAI_API_KEY"], errs)
	}
}

// "*" REACHES AGENT CLIs ONLY, like a bare -p: `yolo host -- bash` under a config selecting zai
// for every agent launches (no refusal — nothing names bash) and bash gets no profile, so no
// claimed key. A "*" that keyed the command would either refuse every ad-hoc command or hand
// each one the provider's key.
func TestHostLaunchDefaultDoesNotReachACommandNoPackInstalls(t *testing.T) {
	for name, sel := range map[string]string{"string form": `"zai"`, "\"*\"": `{"*": "zai"}`} {
		env, errs := hostGateLaunchWith(t, `{`+hostProfileKeyPacks+`, "profile": `+sel+`}`, nil, nil, "bash")
		if env["ZAI_API_KEY"] != "" {
			t.Errorf("%s: bash received zai's key through the key's default: %q\n%s", name, env["ZAI_API_KEY"], errs)
		}
	}
}

// Precedence at the host, the jail's: a null entry keeps "*" off its agent, and a typed -p
// beats the key's entry for the one command it composes.
func TestHostLaunchProfileKeyPrecedence(t *testing.T) {
	cfg := `{` + hostProfileKeyPacks + `, "profile": {"*": "zai", "claude": null}}`
	env, errs := hostGateLaunchWith(t, cfg, nil, nil, "claude")
	if env["ZAI_API_KEY"] != "" {
		t.Errorf("a null entry must keep \"*\" off claude: ZAI_API_KEY = %q\n%s", env["ZAI_API_KEY"], errs)
	}
	env, errs = hostGateLaunchWith(t, cfg, nil, []string{"-p", "zai"}, "claude")
	if env["ZAI_API_KEY"] != "tok-key" {
		t.Errorf("a typed -p must beat the key's entry: ZAI_API_KEY = %q\n%s", env["ZAI_API_KEY"], errs)
	}
}

// The retired key refuses a host launch by name, respelling the user's entries, before
// anything is exec'd: the host runs the provider section of validation over user scope.
func TestHostLaunchRefusesTheRetiredUseProfilesKey(t *testing.T) {
	rc, env, errs := hostGateRun(t, `{`+hostProfileKeyPacks+`, "use_profiles": {"claude": "zai"}}`, nil, nil, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("use_profiles must refuse `yolo host -- claude`: rc = %d, reached exec = %v\n%s", rc, env != nil, errs)
	}
	for _, want := range []string{"config.use_profiles: RENAMED", `"profile": {"claude": "zai"}`} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// `yolo host apply` folds "*" over the agents its packs install: pi on the codex profile
// through "*" writes pi's provider and model into the real home, as the named entry does in
// TestYoloHostApplyAssertWritesTheComputedLayer. Handing composeHostInputs' fold no packs
// leaves "*" reaching nothing, and pi's settings unwritten.
func TestYoloHostApplyFoldsTheProfileKeysDefault(t *testing.T) {
	for name, sel := range map[string]string{"string form": `"codex"`, "\"*\"": `{"*": "codex"}`} {
		home := hostComputedHome(t, `{"packs":["pi"], "profile":`+sel+`}`)
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
			t.Fatalf("%s: yolo host apply --assert rc=%d\n%s%s", name, rc, out.String(), errw.String())
		}
		settings := readJSONAt(t, home, ".pi/agent/settings.json")
		if settings["defaultProvider"] != "openai-codex" {
			t.Errorf("%s: \"*\" did not select pi's codex provider at the host: %v", name, settings)
		}
	}
}
