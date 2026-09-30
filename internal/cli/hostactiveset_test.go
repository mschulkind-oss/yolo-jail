package cli

// hostactiveset_test.go pins the host notch's half of docs/design/active-provider-sets.md
// (§4.9: `yolo host -- <agent>` and `yolo host env --agent <agent>`; the ACTIVE SET, a term that
// doc coins, is the ordered list of profiles one agent runs on for one launch). Each cell runs
// hostMain or hostEnv to the exec or the script, over the SHIPPED packs, so the grammar, the
// composition's set checks and the gate's set delivery are all asked through the front door.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	// Every entry says where it landed, the second as well as the primary (profileLines, over the
	// whole set), beside the set's own line and each key's disclosure.
	for _, want := range []string{"Active set for pi: zai, openrouter", "Profile zai:", "Profile openrouter:",
		"ZAI_API_KEY (provider zai): pi only", "OPENROUTER_API_KEY (provider openrouter): pi only"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, errs)
		}
	}
	// The same set from the profile key, a config list.
	env, _ = hostGateLaunchWith(t, `{"packs": ["claude", "pi", "zai", "openrouter"], `+
		`"profile": {"pi": ["zai", "openrouter"]}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`, nil, nil, "pi")
	if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "tok-router" {
		t.Errorf("the profile key's list must deliver both keys too: ZAI=%q OPENROUTER=%q",
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

	// Checked in order, so a list whose first entry is undeclared names that one first.
	rc, _, errs = hostGateRun(t, cfg, nil, []string{"-p", "nosuch,typo"}, "claude")
	if rc == 0 || !strings.Contains(errs, `profile "nosuch" (entry 1 of the bare -p list nosuch,typo)`) {
		t.Errorf("an undeclared first entry must be named first (rc=%d):\n%s", rc, errs)
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

// THE REGION PRE-FLIGHT AT THE HOST asks every entry of the set (AP-P1): pi on [zai, bedrock]
// with no region anywhere refuses naming bedrock and pi, where reading the primary alone sees zai
// and asks nothing (regionGaps). With the provider's region set, pi runs and its environment
// carries that region as AWS_REGION, relayed by pi's derive for its second entry.
func TestHostAsksTheRegionOfALaterEntry(t *testing.T) {
	const cfg = `{"packs": ["pi", "zai", "bedrock"], "env_sources": [` +
		`{"ZAI_API_KEY": "tok-zai", "AWS_PROFILE": "work"}]}`
	rc, env, errs := hostGateRun(t, cfg, map[string]string{"AWS_REGION": ""}, []string{"-p", "pi=zai,bedrock"}, "pi")
	if rc == 0 || env != nil {
		t.Fatalf("pi's second entry on bedrock with no region must refuse (rc=%d)\n%s", rc, errs)
	}
	if !strings.Contains(errs, `requires a region for provider "bedrock" (platform "aws-bedrock"), selected for pi`) {
		t.Errorf("the refusal must name bedrock and pi:\n%s", errs)
	}

	const withRegion = `{"packs": ["pi", "zai", "bedrock"], "providers": {"bedrock": {"region": "eu-west-7"}},
	  "env_sources": [{"ZAI_API_KEY": "tok-zai", "AWS_PROFILE": "work"}]}`
	env, errs = hostGateLaunchWith(t, withRegion, map[string]string{"AWS_REGION": ""},
		[]string{"-p", "pi=zai,bedrock"}, "pi")
	if env["AWS_REGION"] != "eu-west-7" {
		t.Errorf("pi on [zai, bedrock] must receive the provider's region: AWS_REGION = %q\n%s", env["AWS_REGION"], errs)
	}
}

// widgetSetPack is a local pack declaring a provider of platform widget-plat pi can reach, a
// profile over it, and a pointer gated on that platform that WIDGET_TOKEN overrides: aws-auth's
// shape in made-up names, for a set whose SECOND entry is the gated platform.
const widgetSetPack = `{"name": "local", "contributes": [
  {"kind": "provider", "name": "widgetprovider", "platform": "widget-plat",
   "endpoints": {"openai": {"base_url": "https://widget.example/v1"}}},
  {"kind": "profile", "name": "widget", "provider": "widgetprovider"},
  {"kind": "env", "platform": "widget-plat", "vars": {"WIDGET_POINTER": "https://widget.example/creds"},
   "overridden_by": [{"vars": ["WIDGET_TOKEN"], "because": "the widget client reads WIDGET_TOKEN first"}]}]}`

// A PLATFORM GATE A LATER ENTRY FIRES is checked for an override at the host (AP-P1): pi on
// [zai, widget] receives the widget-plat pointer, and WIDGET_TOKEN delivered beside it overrides
// it, so the launch refuses. envOverrideFindings builds its selection over the whole set
// (SelectionOfSets); read over the primary alone it sees zai, fires no gate, and pi runs.
func TestHostRefusesAnOverrideOfAPointerALaterEntryGates(t *testing.T) {
	home := hostGateHome(t, `{"packs": ["pi", "zai"], "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai", "WIDGET_TOKEN": "frozen"}]}`, nil)
	t.Setenv("WIDGET_TOKEN", "")
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"), widgetSetPack)
	rc, reached, errw := hostExecRun(t, "pi", "-p", "pi=zai,widget")
	if rc != 1 || reached {
		t.Fatalf("pi on [zai, widget] beside WIDGET_TOKEN must refuse: rc=%d reached=%v\n%s", rc, reached, errw)
	}
	for _, want := range []string{"WIDGET_TOKEN is delivered by " + packload.FromEnvSources, "WIDGET_POINTER"} {
		if !strings.Contains(errw, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errw)
		}
	}
	// The control: pi on zai alone receives no pointer, so nothing is overridden.
	if rc, reached, errw := hostExecRun(t, "pi", "-p", "pi=zai"); rc != 0 || !reached {
		t.Fatalf("pi on zai alone must launch: rc=%d\n%s", rc, errw)
	}
}

// `yolo host apply` renders the profile key's set into pi's own files (§4.9), through the real
// command: the start pair is the primary's and the scoped list spans both providers, and the
// report names the selection as the set.
func TestHostApplyRendersPisSet(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi","zai","openrouter"],
		"profile":{"pi":["zai","openrouter"]}}`)
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
	if !strings.Contains(c.summary(), "profile selection (the profile key): pi → zai,openrouter") {
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
		"profile":{"pi":["zai","zai-fast"]}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	report := out.String() + errw.String()
	for _, want := range []string{"profile pi → zai, zai-fast is not applied at the host",
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

// OQ-AP3 ON THE PROFILE KEY at the host (PP-D10's list form): the key's bare list — its list
// form, or a list under "*" — reaches claude as its first entry alone and says so with the
// key's own spelling of a per-agent list, while pi takes it whole. Through hostGateLaunchWith, so
// cutting the note from profileLines or the fold's narrowing from composeHostVarsWith fails it.
func TestHostNarrowsTheProfileKeysBareListForClaudeAndSaysSo(t *testing.T) {
	for _, key := range []string{`["zai", "openrouter"]`, `{"*": ["zai", "openrouter"]}`} {
		cfg := `{"packs": ["claude", "pi", "zai", "openrouter"], "profile": ` + key + `, ` +
			`"env_sources": [{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`
		env, errs := hostGateLaunchWith(t, cfg, nil, nil, "claude")
		if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "" {
			t.Errorf("%s: claude must run on zai alone: ZAI=%q OPENROUTER=%q", key,
				env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"])
		}
		for _, want := range []string{"(the profile key's list, naming no agent)", "on zai alone",
			"ignores openrouter", `"profile": {"<agent>": ["zai", "openrouter"]}`} {
			if !strings.Contains(errs, want) {
				t.Errorf("%s: the launch must say %q:\n%s", key, want, errs)
			}
		}
		env, errs = hostGateLaunchWith(t, cfg, nil, nil, "pi")
		if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "tok-router" {
			t.Errorf("%s: pi takes the key's list whole: ZAI=%q OPENROUTER=%q", key,
				env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"])
		}
		if strings.Contains(errs, "naming no agent") {
			t.Errorf("%s: pi narrowed nothing, so nothing is said:\n%s", key, errs)
		}
	}
}

// AP-D3 for the key's bare list at the host: the entries claude ignores must be declared, as
// they are for a bare -p.
func TestHostRefusesAnUndeclaredEntryOfTheProfileKeysBareList(t *testing.T) {
	const cfg = `{"packs": ["claude", "zai"], "profile": ["zai", "typo"], ` +
		`"env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`
	rc, env, errs := hostGateRun(t, cfg, nil, nil, "claude")
	if rc == 0 || env != nil {
		t.Fatalf("the key's list with an undeclared entry must refuse before the exec (rc=%d)\n%s", rc, errs)
	}
	if !strings.Contains(errs, `profile "typo" (entry 2 of the profile key's list zai,typo)`) {
		t.Errorf("yolo host must name the ignored entry nothing declares:\n%s", errs)
	}
}

// OQ-AP3 at `yolo host apply`: the profile key's bare list renders pi's whole set and claude's
// first entry, and the apply names what claude ignores, as a launch does, rather than writing
// part of the list in silence (AP-P2).
func TestHostApplySaysWhatTheProfileKeysBareListNarrowed(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["claude","pi","zai","openrouter"],
		"profile":["zai","openrouter"]}`)
	c, err := composeHostInputs(config.UserScopeConfigOrEmpty(), selectConfiguredHostPacks().packs, home)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.summary(); !strings.Contains(got, "pi → zai,openrouter") || !strings.Contains(got, "claude → zai") {
		t.Errorf("the detail line must name pi's whole list and claude's first entry: %s", got)
	}
	said := strings.Join(c.omitted, "\n")
	for _, want := range []string{"(the profile key's list, naming no agent)", "claude takes one profile",
		"ignores openrouter"} {
		if !strings.Contains(said, want) {
			t.Errorf("the apply must say %q:\n%s", want, said)
		}
	}
}

// opencode holds a set too (docs/design/active-provider-sets.md §8 step 3, AP-D15), at the host as
// in a jail: `yolo host -p opencode=zai,openrouter -- opencode` hands opencode both keys and names
// the set before it runs, and the profile key's list does the same.
func TestHostRunsOpencodeOnItsWholeSet(t *testing.T) {
	const cfg = `{"packs": ["claude", "opencode", "zai", "openrouter"], "env_sources": [` +
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`
	env, errs := hostGateLaunchWith(t, cfg, nil, []string{"-p", "opencode=zai,openrouter"}, "opencode")
	if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "tok-router" {
		t.Errorf("opencode on [zai, openrouter] must receive both keys: ZAI=%q OPENROUTER=%q",
			env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"])
	}
	for _, want := range []string{"Active set for opencode: zai, openrouter",
		"ZAI_API_KEY (provider zai): opencode only", "OPENROUTER_API_KEY (provider openrouter): opencode only"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, errs)
		}
	}
	env, _ = hostGateLaunchWith(t, `{"packs": ["claude", "opencode", "zai", "openrouter"], `+
		`"profile": {"opencode": ["zai", "openrouter"]}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`, nil, nil, "opencode")
	if env["ZAI_API_KEY"] != "tok-zai" || env["OPENROUTER_API_KEY"] != "tok-router" {
		t.Errorf("the profile key's list must deliver both keys too: ZAI=%q OPENROUTER=%q",
			env["ZAI_API_KEY"], env["OPENROUTER_API_KEY"])
	}
}

// `yolo host apply` renders the profile key's set into opencode's own file (§4.9), through the
// real command: `enabled_providers` names both entries, the primary first, and `model` is the
// primary's.
func TestHostApplyRendersOpencodesSet(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["opencode","zai","openrouter"],
		"profile":{"opencode":["zai","openrouter"]}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	cfg := readJSONAt(t, home, ".config/opencode/opencode.json")
	if m, _ := cfg["model"].(string); !strings.HasPrefix(m, "zai/") {
		t.Errorf("the start model must be the primary's: model = %v", cfg["model"])
	}
	got, _ := cfg["enabled_providers"].([]any)
	if len(got) != 2 || got[0] != "zai" || got[1] != "openrouter" {
		t.Errorf("enabled_providers = %v, want [zai openrouter]", cfg["enabled_providers"])
	}
}
