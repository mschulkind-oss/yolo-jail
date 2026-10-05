package cli

// hostprofilekey_test.go pins the HOST notch's call sites for the config `profile` key
// (docs/design/providers-and-profiles-redesign.md PP-D10): `yolo host -- <cmd>` selects the
// command's own entry, else "*" (or the string form) when a selected pack installs the command,
// and a typed -p above both; `yolo host apply` folds "*" over every agent its packs install.
// Each test drives the verb itself (hostMain), so deleting the key's read from
// composeHostVarsWith's selection, or the pack set from host apply's, fails here.

import (
	"bytes"
	"path/filepath"
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
// leaves "*" reaching nothing, and pi's settings unwritten. The config declares
// `host_management: "own"`, without which (OQ-CO14's unset `none`) the apply writes nothing.
func TestYoloHostApplyFoldsTheProfileKeysDefault(t *testing.T) {
	for name, sel := range map[string]string{"string form": `"codex"`, "\"*\"": `{"*": "codex"}`} {
		home := hostComputedHome(t, `{"packs":["pi"], "host_management": "own", "profile":`+sel+`}`)
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

// THE RETIRED KEY REFUSES EVERY HOST RENDER, not only a host launch: `yolo host apply` (both
// postures), the automatic apply a wrapped launch runs, and `yolo config render --at host` read
// the selection off the new key alone, so a `use_profiles` they did not refuse was one they
// silently ignored, and an --assert then deselected the profile an earlier apply had written
// into the real home. Each case starts from a home the key applied pi's codex profile into, then
// respells the selection under the old key; the refusal must name it and the home must keep
// what the earlier apply wrote. Both configs declare `host_management: "own"`: under the unset
// key (`none` since OQ-CO14) nothing renders to refuse, and the earlier apply writes nothing.
func TestEveryHostRenderRefusesTheRetiredUseProfilesKey(t *testing.T) {
	const retired = `{"packs":["pi"], "host_management": "own", "host_apply_on_launch": true, ` +
		`"use_profiles": {"pi": "codex"}}`
	applied := func(t *testing.T) string {
		t.Helper()
		home := hostComputedHome(t, `{"packs":["pi"], "host_management": "own", "host_apply_on_launch": true, `+
			`"profile": {"pi": "codex"}}`)
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
			t.Fatalf("fixture: yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
		}
		if got := readJSONAt(t, home, ".pi/agent/settings.json")["defaultProvider"]; got != "openai-codex" {
			t.Fatalf("fixture: the profile key did not select pi's codex provider: %v", got)
		}
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), retired)
		return home
	}
	for name, run := range map[string]func() (int, string){
		"yolo host apply --assert": func() (int, string) {
			var out, errw bytes.Buffer
			rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n"))
			return rc, out.String() + errw.String()
		},
		"yolo host apply (dry run)": func() (int, string) {
			var out, errw bytes.Buffer
			rc := hostMain([]string{"apply"}, &out, &errw, false, nil)
			return rc, out.String() + errw.String()
		},
		"yolo host -- pi (the wrapper's automatic apply)": func() (int, string) {
			var out, errw bytes.Buffer
			rc := hostMain([]string{"--", "pi"}, &out, &errw, false, nil)
			return rc, out.String() + errw.String()
		},
		"yolo config render --at host": func() (int, string) {
			rc, out, errs := runConfigVerb(t, "render", "pi/settings", "--at", "host")
			return rc, out + errs
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := applied(t)
			rc, report := run()
			if rc == 0 {
				t.Errorf("%s accepted the retired key (rc=0):\n%s", name, report)
			}
			if !strings.Contains(report, "config.use_profiles: RENAMED") {
				t.Errorf("%s must refuse the retired key by name:\n%s", name, report)
			}
			if strings.Contains(report, "synchronized") {
				t.Errorf("%s rendered before refusing:\n%s", name, report)
			}
			// The launch gate asks before its observe pass, so a wrapped launch says one thing:
			// not "could not check … launching pi anyway" and then a refusal.
			if strings.Contains(report, "anyway") {
				t.Errorf("%s said it was launching and then refused:\n%s", name, report)
			}
			if got := readJSONAt(t, home, ".pi/agent/settings.json")["defaultProvider"]; got != "openai-codex" {
				t.Errorf("%s deselected the profile an earlier apply wrote: defaultProvider = %v\n%s",
					name, got, report)
			}
		})
	}
}

// THE HOST LAUNCH'S SELECTION CLOSURE folds the key over the set it is handed
// (hostLaunchSelection): a via profile selected through the string form or "*" joins its
// service's pack at a host launch exactly as the named entry does, and says so. Handing that
// fold no packs leaves the default reaching no agent, so the via pack silently drops out of the
// host launch's set while the named twin still joins it.
func TestHostLaunchClosureJoinsTheViaOfTheProfileKeysDefault(t *testing.T) {
	const want = "yolo host env: + wire-bridge (via of profile bedrock-bridge, active for pi)"
	for name, sel := range map[string]string{
		"string form": `"bedrock-bridge"`,
		"\"*\"":       `{"*": "bedrock-bridge"}`,
		"named":       `{"pi": "bedrock-bridge"}`,
	} {
		hostGateHome(t, `{"packs": ["pi", "bedrock"], "profile": `+sel+`}`, nil)
		var out, errw bytes.Buffer
		if rc := hostMain([]string{"env", "--agent", "pi"}, &out, &errw, false, nil); rc != 0 {
			t.Fatalf("%s: yolo host env --agent pi rc=%d\n%s", name, rc, errw.String())
		}
		if !strings.Contains(errw.String(), want) {
			t.Errorf("%s: the host launch's selection must join the via's pack and say %q:\n%s",
				name, want, errw.String())
		}
	}
}
