package cli

// hostactiveset_test.go pins the host notch's half of docs/design/active-provider-sets.md
// (§4.9: `yolo host -- <agent>` and `yolo host env --agent <agent>`; the ACTIVE SET, a term that
// doc coins, is the ordered list of profiles one agent runs on for one launch). Each cell runs
// hostMain or hostEnv to the exec or the script, over the SHIPPED packs, so the grammar, the
// composition's set checks and the gate's set delivery are all asked through the front door.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// setHostCfg selects the shipped claude, pi, zai and openrouter packs and hydrates both
// providers' keys.
const setHostCfg = `{"packs": ["claude", "pi", "zai", "openrouter"], "env_sources": [` +
	`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`

// §9's last bullet, the exec half: `yolo host -p pi=zai,openrouter -- pi` hands pi both keys
// and names the set before it runs.
func TestHostRunsPiOnItsWholeSet(t *testing.T) {
	env, errs := hostGateLaunchWith(t, setHostCfg, nil, []string{"-p", "pi=zai,openrouter"}, "pi")
	if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "tok-router" {
		t.Errorf("pi on [zai, openrouter] must receive both keys: ZAI=%q OPENROUTER=%q",
			env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"])
	}
	for _, want := range []string{"Active set for pi: zai, openrouter",
		"ZAI_API_KEY (provider zai): pi only", "OPENROUTER_API_KEY (provider openrouter): pi only"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, errs)
		}
	}
	// The same set from use_profiles, a config list.
	env, _ = hostGateLaunchWith(t, `{"packs": ["claude", "pi", "zai", "openrouter"], `+
		`"use_profiles": {"pi": ["zai", "openrouter"]}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`, nil, nil, "pi")
	if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "tok-router" {
		t.Errorf("use_profiles' list must deliver both keys too: ZAI=%q OPENROUTER=%q",
			env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"])
	}
}

// §9's last bullet, the script half: `yolo host env --agent pi -p zai,openrouter` exports what
// the exec composes — both keys — since pi takes a bare list whole.
func TestHostEnvComposesPisWholeSet(t *testing.T) {
	hostGateHome(t, setHostCfg, nil)
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--agent", "pi", "-p", "zai,openrouter"}, &out, &errw); rc != 0 {
		t.Fatalf("hostEnv rc = %d, stderr:\n%s", rc, errw.String())
	}
	for _, want := range []string{"export ZAI_API_KEY='tok-zai'", "export OPENROUTER_API_KEY='tok-router'"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the script must carry %q:\n%s", want, out.String())
		}
	}
}

// OQ-AP2 at the host: a list named at claude refuses before anything runs, naming the fix.
func TestHostRefusesAListNamedAtClaude(t *testing.T) {
	rc, env, errs := hostGateRun(t, setHostCfg, nil, []string{"-p", "claude=zai,openrouter"}, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("a list named at claude must refuse before the exec (rc=%d)\n%s", rc, errs)
	}
	for _, want := range []string{"whose pack does not declare provider_sets", "`-p claude=zai`"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// OQ-AP3 at the host: a BARE list given to claude starts it on the first entry and says what it
// ignores; openrouter's key, which only the ignored entry claims, is not handed over.
func TestHostNarrowsABareListForClaudeAndSaysSo(t *testing.T) {
	env, errs := hostGateLaunchWith(t, setHostCfg, nil, []string{"-p", "zai,openrouter"}, "claude")
	if env["ZAI_API_KEY"] != "tok-zai" {
		t.Errorf("claude must run on zai, the list's first entry: ZAI_API_KEY = %q", env["ZAI_API_KEY"])
	}
	if env["OPENROUTER_API_KEY"] != "" {
		t.Errorf("claude ignores openrouter, so its key must not reach it: %q", env["OPENROUTER_API_KEY"])
	}
	for _, want := range []string{"claude takes one profile (its pack does not declare provider_sets)", "on zai alone", "ignores openrouter"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, errs)
		}
	}
}

// AP-D3 at the host: an undeclared SECOND entry of pi's set refuses before the exec, naming it,
// and pi never starts on the declared first. Cutting the composition's declaration loop to the
// set's first entry passes `typo` through to the exec.
func TestHostRefusesAnUndeclaredLaterEntry(t *testing.T) {
	rc, env, errs := hostGateRun(t, setHostCfg, nil, []string{"-p", "pi=zai,typo"}, "pi")
	if rc == 0 || env != nil {
		t.Fatalf("pi on [zai, typo] must refuse before the exec (rc=%d)\n%s", rc, errs)
	}
	if !strings.Contains(errs, `profile "typo" selected for pi`) {
		t.Errorf("the refusal must name the undeclared second entry:\n%s", errs)
	}
}

// OQ-AP3 with AP-D3 at the host: claude takes a bare list's first entry alone, and the entries it
// ignores must still be declared, at `yolo host --` and `yolo host env` alike. They used to go
// unchecked, so `-p zai,typo -- claude` ran claude on zai.
func TestHostRefusesAnUndeclaredEntryOfABareList(t *testing.T) {
	const cfg = `{"packs": ["claude", "zai"], "env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`
	const says = `profile "typo" (entry 2 of the bare -p list zai,typo)`
	rc, env, errs := hostGateRun(t, cfg, nil, []string{"-p", "zai,typo"}, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("a bare list with an undeclared entry must refuse before the exec (rc=%d)\n%s", rc, errs)
	}
	if !strings.Contains(errs, says) {
		t.Errorf("yolo host must name the ignored entry nothing declares:\n%s", errs)
	}

	hostGateHome(t, cfg, nil)
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"-p", "zai,typo"}, &out, &errw); rc == 0 {
		t.Fatalf("yolo host env must refuse the same list (rc=0):\n%s", out.String())
	}
	if !strings.Contains(errw.String(), says) {
		t.Errorf("yolo host env must name the ignored entry nothing declares:\n%s", errw.String())
	}
}

// OQ-AP3 at `yolo host env`: with no --agent the verb composes claude's slice, and a bare list is
// narrowed to its first entry for claude, with the one line saying what it ignores. Without the
// narrowing the same value is a list at claude, which refuses (OQ-AP2).
func TestHostEnvNarrowsABareListForClaudeAndSaysSo(t *testing.T) {
	hostGateHome(t, setHostCfg, nil)
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"-p", "zai,openrouter"}, &out, &errw); rc != 0 {
		t.Fatalf("hostEnv rc = %d, stderr:\n%s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "export ZAI_API_KEY='tok-zai'") {
		t.Errorf("claude's slice must carry zai's key, the list's first entry:\n%s", out.String())
	}
	if strings.Contains(out.String(), "OPENROUTER_API_KEY") {
		t.Errorf("claude ignores openrouter, so its key must not be exported:\n%s", out.String())
	}
	for _, want := range []string{"claude takes one profile", "ignores openrouter"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("yolo host env must say %q:\n%s", want, errw.String())
		}
	}
}

// `yolo host apply` renders the use_profiles set into pi's own files (§4.9), through the real
// command: the start pair is the primary's and the scoped list spans both providers, and the
// report names the selection as the set.
func TestHostApplyRendersPisSet(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi","zai","openrouter"],
		"use_profiles":{"pi":["zai","openrouter"]}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	settings := readJSONAt(t, home, ".pi/agent/settings.json")
	if settings["defaultProvider"] != "zai" {
		t.Errorf("the start pair must be the primary's: defaultProvider = %v", settings["defaultProvider"])
	}
	var zai, router bool
	for _, e := range settings["enabledModels"].([]any) {
		s, _ := e.(string)
		zai = zai || strings.HasPrefix(s, "zai/")
		router = router || strings.HasPrefix(s, "openrouter/")
	}
	if !zai || !router {
		t.Errorf("enabledModels must span both entries of pi's set: %v", settings["enabledModels"])
	}
	// The composition's detail line names the selection as the whole set.
	c, err := composeHostInputs(config.UserScopeConfigOrEmpty(), selectConfiguredHostPacks().packs, home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.summary(), "profile selection (use_profiles): pi → zai,openrouter") {
		t.Errorf("the detail line must name pi's whole set: %s", c.summary())
	}
}

// `yolo host apply` LEAVES OUT a set every launch notch refuses, rather than writing its primary
// into the user's real files: two entries on one provider (a user's zai-fast beside zai) is
// named on the report, and pi's settings.json carries no selection from it. Deleting the
// omission branch in composeHostInputs writes defaultProvider zai.
func TestHostApplyLeavesOutASetItCannotRender(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi","zai"],
		"profiles":{"zai-fast":{"provider":"zai"}},
		"use_profiles":{"pi":["zai","zai-fast"]}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{"use_profiles pi → zai, zai-fast is not applied at the host",
		`both resolve to provider "zai"`} {
		if !strings.Contains(report, want) {
			t.Errorf("the apply must say %q:\n%s", want, report)
		}
	}
	settings := readJSONAt(t, home, ".pi/agent/settings.json")
	for _, key := range []string{"defaultProvider", "defaultModel", "enabledModels"} {
		if v, ok := settings[key]; ok {
			t.Errorf("a set the apply left out must write no selection, but %s = %v", key, v)
		}
	}
}

// The credential pre-flight at the host demands every entry's key and names the missing one's
// position; the agent is never started on the rest.
func TestHostRefusesASetMissingAnEntrysKey(t *testing.T) {
	rc, env, errs := hostGateRun(t, `{"packs": ["pi", "zai", "openrouter"], "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai"}]}`, nil, []string{"-p", "pi=zai,openrouter"}, "pi")
	if rc == 0 || env != nil {
		t.Fatalf("a set missing openrouter's key must refuse before the exec (rc=%d)\n%s", rc, errs)
	}
	if !strings.Contains(errs, "profile openrouter is entry 2 of pi's profiles (zai, openrouter)") {
		t.Errorf("the refusal must name openrouter's position in pi's set:\n%s", errs)
	}
}
