package cli

// hostactiveset_test.go pins the host notch's half of docs/design/active-provider-sets.md
// (§4.9: `yolo host -- <agent>` and `yolo host env --agent <agent>`; the ACTIVE SET, a term that
// doc coins, is the ordered list of profiles one agent runs on for one launch). Each cell runs
// hostMain or hostEnv to the exec or the script, over the SHIPPED packs, so the grammar, the
// composition's set checks and the gate's set delivery are all asked through the front door.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

	// THE -p MOVES PI ONTO THE SET (docs/design/model-lists-and-pickers.md MM-D30): pi's own
	// flags, its session's provider and model and the set's scope, right after argv[0] and ahead
	// of the user's own; the two list files in the variables pi's extensions read first; and
	// ~/.pi/agent/settings.json, which only `yolo host apply` writes, untouched.
	home := hostGateHome(t, setHostCfg, nil)
	own := `{"defaultProvider": "openai-codex", "defaultModel": "gpt-6.1-sol"}`
	writeFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), own)
	run := hostSelectionRun(t, home, []string{"-p", "pi=zai,openrouter"}, "pi", "--continue")
	if len(run.argv) < 8 || run.argv[1] != "--provider" || run.argv[2] != "zai" || run.argv[3] != "--model" ||
		run.argv[5] != "--models" || run.argv[len(run.argv)-1] != "--continue" {
		t.Fatalf("pi got %q, want --provider zai --model <id> --models <scope> ahead of its own argv\n%s",
			run.argv, run.errs)
	}
	if scope := run.argv[6]; !strings.HasPrefix(scope, "zai/") || !strings.Contains(scope, "openrouter/*") {
		t.Errorf("--models %q must lead with zai's models and span openrouter", scope)
	}
	for _, name := range []string{"YOLO_PI_OPENAI_CODEX_MODELS", "YOLO_PI_MODEL_LISTS"} {
		if _, set := run.env[name]; !set {
			t.Errorf("pi's environment lacks %s, the list its extension reads first", name)
		}
		if !strings.Contains(run.errs, "  "+name+": ~/.pi/agent/") {
			t.Errorf("setting %s was not disclosed:\n%s", name, run.errs)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json")); string(got) != own {
		t.Errorf("pi's settings.json changed:\n%s", got)
	}
	// From the profile key, with no -p, pi starts on its file: nothing is handed.
	hostGateHome(t, `{"packs": ["claude", "pi", "zai", "openrouter"], `+
		`"profile": {"pi": ["zai", "openrouter"]}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router"}]}`, nil)
	bare := hostSelectionRun(t, "", nil, "pi")
	if !reflect.DeepEqual(bare.argv, []string{"pi"}) || bare.env["YOLO_PI_MODEL_LISTS"] != "" {
		t.Errorf("with no -p pi got %q and lists %q, want its own argv and its files", bare.argv,
			bare.env["YOLO_PI_MODEL_LISTS"])
	}
}

// hostSelectionResult is one launch's exec: the argv and environment it was handed, and stderr.
type hostSelectionResult struct {
	argv []string
	env  map[string]string
	errs string
}

// hostSelectionRun runs `yolo host [flags] -- <agent> [args]` to the exec in the home hostGateHome
// already made (home is only for the caller's files), with a stub agent first on PATH, and returns
// what the exec was handed.
func hostSelectionRun(t *testing.T, home string, flags []string, agent string, args ...string) hostSelectionResult {
	t.Helper()
	_ = home
	bin := filepath.Join(t.TempDir(), "bin")
	writeFile(t, filepath.Join(bin, agent), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(bin, agent), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var r hostSelectionResult
	origExec := hostSyscallExec
	hostSyscallExec = func(_ string, argv, env []string) error {
		r.argv = append([]string{}, argv...)
		r.env = map[string]string{}
		for _, kv := range env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				r.env[k] = v
			}
		}
		return nil
	}
	t.Cleanup(func() { hostSyscallExec = origExec })
	var out, errw bytes.Buffer
	cmd := append(append(append([]string{}, flags...), "--", agent), args...)
	if rc := hostMain(cmd, &out, &errw, false, nil); rc != 0 || r.argv == nil {
		t.Fatalf("yolo host %q: rc=%d, exec reached=%v\n%s", cmd, rc, r.argv != nil, errw.String())
	}
	r.errs = errw.String()
	return r
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
	if hostExports(out.String(), "OPENROUTER_API_KEY") {
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
// report names the selection as the set. This and the two other `yolo host apply` tests in this
// file declare `host_management: "own"`: the unset key is `none` since the `assert` retirement
// (OQ-CO14), and the verb refuses under it.
func TestHostApplyRendersPisSet(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi","zai","openrouter"],"host_management":"own",
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
	home := hostComputedHome(t, `{"packs":["pi","zai"],"host_management":"own",
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
	// With no -p opencode starts on its file: the profile key's set hands nothing.
	if _, set := env["OPENCODE_CONFIG_CONTENT"]; set {
		t.Errorf("with no -p opencode was handed OPENCODE_CONFIG_CONTENT=%s", env["OPENCODE_CONFIG_CONTENT"])
	}

	// THE -p MOVES OPENCODE ONTO THE SET (docs/design/model-lists-and-pickers.md MM-D30): its
	// selection and the rows enabled_providers names, in OPENCODE_CONFIG_CONTENT, disclosed by
	// name, merged over the user's own value of it; opencode.json, which only `yolo host apply`
	// writes, untouched.
	home := hostGateHome(t, cfg, map[string]string{"OPENCODE_CONFIG_CONTENT": `{"theme": "mine"}`})
	own := `{"model": "openai/gpt-6.1-sol"}`
	writeFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), own)
	run := hostSelectionRun(t, home, []string{"-p", "opencode=zai,openrouter"}, "opencode")
	var doc map[string]any
	if err := json.Unmarshal([]byte(run.env["OPENCODE_CONFIG_CONTENT"]), &doc); err != nil {
		t.Fatalf("OPENCODE_CONFIG_CONTENT is not a document: %q (%v)\n%s", run.env["OPENCODE_CONFIG_CONTENT"],
			err, run.errs)
	}
	// Both providers are opencode's own (pi-codex-provider-shadowing.md OQ-3): named by its own ids,
	// zai as `zai-coding-plan`, with no row of yolo's over either.
	if !reflect.DeepEqual(doc["enabled_providers"], []any{"zai-coding-plan", "openrouter"}) || doc["theme"] != "mine" {
		t.Errorf("OPENCODE_CONFIG_CONTENT = %v, want the set's providers over the user's own theme", doc)
	}
	if rows, _ := doc["provider"].(map[string]any); rows["zai"] != nil || rows["openrouter"] != nil {
		t.Errorf("OPENCODE_CONFIG_CONTENT carries rows %v over opencode's own providers", doc["provider"])
	}
	for _, want := range []string{"yolo host: yolo SET variables for opencode, from pack opencode",
		"  OPENCODE_CONFIG_CONTENT: enabled_providers=zai-coding-plan,openrouter, model=", "(merged over your own value"} {
		if !strings.Contains(run.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, run.errs)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json")); string(got) != own {
		t.Errorf("opencode.json changed:\n%s", got)
	}
	if !reflect.DeepEqual(run.argv, []string{"opencode"}) {
		t.Errorf("the env form hands no argv, but opencode got %q", run.argv)
	}
}

// `yolo host env -p` CARRIES WHAT A SCRIPT CAN (MM-D30): opencode's document is exported, and a
// selection that needs pi's argv is not, the line naming the launch that carries it.
func TestHostEnvHandsTheSelectionAScriptCanCarry(t *testing.T) {
	hostGateHome(t, setHostCfg, nil)
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--agent", "pi", "-p", "zai"}, &out, &errw); rc != 0 {
		t.Fatalf("hostEnv rc = %d\n%s", rc, errw.String())
	}
	for _, want := range []string{"-p zai moves pi through its command line (--provider zai",
		"To run pi on it for one launch: `yolo host -p zai -- pi`"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("yolo host env must say %q:\n%s", want, errw.String())
		}
	}
	if hostExports(out.String(), "YOLO_PI_MODEL_LISTS") {
		t.Errorf("half of pi's selection was exported:\n%s", out.String())
	}

	hostGateHome(t, `{"packs": ["claude", "opencode", "zai"], "env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`, nil)
	out.Reset()
	errw.Reset()
	if rc := hostEnv([]string{"--agent", "opencode", "-p", "zai"}, &out, &errw); rc != 0 {
		t.Fatalf("hostEnv rc = %d\n%s", rc, errw.String())
	}
	if !hostExports(out.String(), "OPENCODE_CONFIG_CONTENT") || !strings.Contains(out.String(), `"enabled_providers":["zai-coding-plan"]`) {
		t.Errorf("opencode's selection must be exported:\n%s", out.String())
	}
	if !strings.Contains(errw.String(), "yolo host env: yolo SET variables for opencode") {
		t.Errorf("the exported selection must be disclosed:\n%s", errw.String())
	}
}

// `yolo host apply` renders the profile key's set into opencode's own file (§4.9), through the
// real command: `enabled_providers` names both entries, the primary first, and `model` is the
// primary's.
func TestHostApplyRendersOpencodesSet(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["opencode","zai","openrouter"],"host_management":"own",
		"profile":{"opencode":["zai","openrouter"]}}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	// Both are opencode's own providers, zai's plan its zai-coding-plan, so each is named by
	// opencode's own id (docs/design/pi-codex-provider-shadowing.md OQ-3).
	cfg := readJSONAt(t, home, ".config/opencode/opencode.json")
	if m, _ := cfg["model"].(string); !strings.HasPrefix(m, "zai-coding-plan/") {
		t.Errorf("the start model must be the primary's: model = %v", cfg["model"])
	}
	got, _ := cfg["enabled_providers"].([]any)
	if len(got) != 2 || got[0] != "zai-coding-plan" || got[1] != "openrouter" {
		t.Errorf("enabled_providers = %v, want [zai-coding-plan openrouter]", cfg["enabled_providers"])
	}
}

// setRemedyCfg selects pi with three providers' keys hydrated, so a launch on two of them
// withholds the third's.
const setRemedyCfg = `{"packs": ["claude", "pi", "zai", "openrouter", "cerebras"], "env_sources": [` +
	`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router", "CEREBRAS_API_KEY": "tok-c"}]}`

// THE ADDITIVE REMEDY (AP-D19): pi runs on a set, so the withheld line for a key only cerebras
// claims names the launch that ADDS cerebras to the set, in the pair form that replaces pi's set
// whole for the launch (AP-D4). That launch runs and keeps every key the set delivered, where the
// switch the line used to name, `yolo host -p cerebras -- pi`, handed pi cerebras's key and dropped
// zai's and openrouter's.
func TestHostSetRemedyAddsTheProfileToTheSet(t *testing.T) {
	_, errs := hostGateLaunchWith(t, setRemedyCfg, nil, []string{"-p", "pi=zai,openrouter"}, "pi")
	line := scopeLine(t, errs, "CEREBRAS_API_KEY")
	want := "To add the cerebras profile to pi's active set for one launch, keeping zai, openrouter: " +
		"`yolo host -p pi=zai,openrouter,cerebras -- pi`"
	if !strings.Contains(line, want) {
		t.Errorf("the remedy must add cerebras to pi's set (%q): %q", want, line)
	}
	if strings.Contains(line, "replacing") {
		t.Errorf("an additive remedy replaces nothing: %q", line)
	}
	for name, value := range map[string]string{"CEREBRAS_API_KEY": "tok-c", "ZAI_API_KEY": "tok-zai",
		"OPENROUTER_API_KEY": "tok-router"} {
		assertRemediesRun(t, line, name, value)
	}
}

// The same set from the profile key's list: the remedy names the pair that adds cerebras to it.
func TestHostSetRemedyAddsToTheProfileKeysSet(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude", "pi", "zai", "openrouter", "cerebras"], `+
		`"profile": {"pi": ["zai", "openrouter"]}, "env_sources": [`+
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router", "CEREBRAS_API_KEY": "tok-c"}]}`,
		nil, nil, "pi")
	line := scopeLine(t, errs, "CEREBRAS_API_KEY")
	if !strings.Contains(line, "`yolo host -p pi=zai,openrouter,cerebras -- pi`") {
		t.Errorf("the remedy must add cerebras to the key's set: %q", line)
	}
	assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
	assertRemediesRun(t, line, "OPENROUTER_API_KEY", "tok-router")
}

// At `yolo host env` the additive launch follows the shell's grant, as the switch did.
func TestHostEnvSetRemedyAddsTheProfileToTheSet(t *testing.T) {
	hostGateHome(t, setRemedyCfg, nil)
	var out, errw bytes.Buffer
	if rc := hostEnv([]string{"--agent", "pi", "-p", "zai,openrouter"}, &out, &errw); rc != 0 {
		t.Fatalf("hostEnv rc = %d\n%s", rc, errw.String())
	}
	line := scopeLine(t, errw.String(), "CEREBRAS_API_KEY")
	for _, want := range []string{"`eval \"$(yolo host env --with-credentials cerebras)\"`; " +
		"to add the cerebras profile to pi's active set for one launch, keeping zai, openrouter: " +
		"`yolo host -p pi=zai,openrouter,cerebras -- pi`"} {
		if !strings.Contains(line, want) {
			t.Errorf("yolo host env's line must name the shell grant and then the additive launch (%q): %q", want, line)
		}
	}
	assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
}

// AP-D12 refuses a set naming a regional platform twice, so a widened set that would is never
// named: the line falls back to the switch, and says the switch replaces the whole set.
func TestHostSetRemedyFallsBackToTheSwitchForASecondRegionalEntry(t *testing.T) {
	cfg := `{"packs": ["claude", "pi", "zai", "bedrock"], ` +
		`"providers": {"bedrock": {"region": "us-east-1"}, "bedrock-west": {"platform": "aws-bedrock", ` +
		`"region": "us-west-2", "endpoints": {"openai": {"base_url": "https://west.example/v1"}}, ` +
		`"api_key_env_name": "WEST_KEY"}}, ` +
		`"profiles": {"bedrock-west": {"provider": "bedrock-west"}}, "env_sources": [` +
		`{"ZAI_API_KEY": "tok-zai", "AWS_PROFILE": "dev", "WEST_KEY": "tok-w"}]}`
	_, errs := hostGateLaunchWith(t, cfg, nil, []string{"-p", "pi=zai,bedrock"}, "pi")
	line := scopeLine(t, errs, "WEST_KEY")
	if strings.Contains(line, "pi=zai,bedrock,bedrock-west") {
		t.Errorf("a set with two Bedrock entries refuses (AP-D12), so the line may not name it: %q", line)
	}
	if !strings.Contains(line, "replacing its active set (zai, bedrock)") {
		t.Errorf("the switch replaces pi's whole set and must say so: %q", line)
	}
	assertRemediesRun(t, line, "WEST_KEY", "tok-w")
}

// An entry pi cannot speak is refused at every position of a set as it is alone, so neither the
// widened set nor the switch is named: the key goes to an ad-hoc command through the grant.
func TestHostSetRemedyHandsAnUnspeakableProfilesKeyToTheGrant(t *testing.T) {
	cfg := `{"packs": ["claude", "pi", "zai", "openrouter"], ` +
		`"providers": {"anth": {"endpoints": {"anthropic": {"base_url": "https://anth.example"}}, ` +
		`"api_key_env_name": "ANTH_KEY"}}, ` +
		`"profiles": {"anth": {"provider": "anth"}}, "env_sources": [` +
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router", "ANTH_KEY": "tok-a"}]}`
	_, errs := hostGateLaunchWith(t, cfg, nil, []string{"-p", "pi=zai,openrouter"}, "pi")
	line := scopeLine(t, errs, "ANTH_KEY")
	if strings.Contains(line, "-- pi`") {
		t.Errorf("pi speaks no anthropic, so no named command may launch it: %q", line)
	}
	if !strings.Contains(line, "`yolo host --with-credentials anth -- bash`") {
		t.Errorf("the key must go to an ad-hoc command through the grant: %q", line)
	}
	assertRemediesRun(t, line, "ANTH_KEY", "tok-a")
}

// oh-omp holds a set too (AP-D18), so its withheld line adds a profile its set can take.
func TestHostOmpSetRemedyAddsTheProfileToTheSet(t *testing.T) {
	const cfg = `{"packs": ["claude", "omp", "zai", "openrouter", "cerebras"], "env_sources": [` +
		`{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router", "CEREBRAS_API_KEY": "tok-c"}]}`
	_, errs := hostGateLaunchWith(t, cfg, nil, []string{"-p", "oh-omp=zai,openrouter"}, "oh-omp")
	line := scopeLine(t, errs, "CEREBRAS_API_KEY")
	if !strings.Contains(line, "`yolo host -p oh-omp=zai,openrouter,cerebras -- oh-omp`") {
		t.Errorf("the remedy must add cerebras to oh-omp's set: %q", line)
	}
	assertRemediesRun(t, line, "CEREBRAS_API_KEY", "tok-c")
	assertRemediesRun(t, line, "ZAI_API_KEY", "tok-zai")
}

// The declare-a-profile arm adds too: with no profile over deepseek, the line says to declare one
// and then names the launch adding it to pi's set, which runs once the example is declared.
func TestHostSetRemedyAddsTheProfileItTellsTheUserToDeclare(t *testing.T) {
	provider := `"providers": {"deepseek": {"endpoints": {"openai": {"base_url": "https://api.deepseek.example/v1"}}, ` +
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}`
	keys := `"env_sources": [{"ZAI_API_KEY": "tok-zai", "OPENROUTER_API_KEY": "tok-router", "DEEPSEEK_API_KEY": "tok-ds"}]`
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude", "pi", "zai", "openrouter"], `+provider+`, `+keys+`}`,
		nil, []string{"-p", "pi=zai,openrouter"}, "pi")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	for _, want := range []string{"No declared profile selects deepseek", `"deepseek": {"provider": "deepseek"}`,
		"then to add the deepseek profile to pi's active set for one launch, keeping zai, openrouter: " +
			"`yolo host -p pi=zai,openrouter,deepseek -- pi`"} {
		if !strings.Contains(line, want) {
			t.Errorf("the declare arm must add the profile it names (%q missing): %q", want, line)
		}
	}
	hostGateHome(t, `{"packs": ["claude", "pi", "zai", "openrouter"], `+provider+`, `+
		`"profiles": {"deepseek": {"provider": "deepseek"}}, `+keys+`}`, nil)
	assertRemediesRun(t, line, "DEEPSEEK_API_KEY", "tok-ds")
	assertRemediesRun(t, line, "OPENROUTER_API_KEY", "tok-router")
}

// OH-OMP'S SCOPE MOVES WITH A -p UNDER AN `only` (docs/design/model-lists-and-pickers.md MM-D30):
// its one selection key, enabledModels, is written only for a narrowed list, and a -p hands it as
// --models, which oh-omp 0.15.3 reads in its place. A -p over a list no `only` narrowed composes no
// scope, as with no -p, and hands nothing.
func TestHostMovesOhOmpsScopeUnderAnOnly(t *testing.T) {
	home := hostGateHome(t, `{"packs": ["claude", "omp", "zai"], "env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`, nil)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"name":"acme","contributes":[{"kind":"models","provider":"zai","only":["glm-5.3","glm-4.6"]}]}`)
	run := hostSelectionRun(t, home, []string{"-p", "oh-omp=zai"}, "oh-omp")
	// The narrowed list, its default entry (zai's model, glm-5.3) first, as the derive orders it.
	if want := []string{"oh-omp", "--models", "zai/glm-5.3,zai/glm-4.6"}; !reflect.DeepEqual(run.argv, want) {
		t.Errorf("oh-omp got %q, want %q\n%s", run.argv, want, run.errs)
	}
	hostGateHome(t, `{"packs": ["claude", "omp", "zai"], "env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`, nil)
	if plain := hostSelectionRun(t, "", []string{"-p", "oh-omp=zai"}, "oh-omp"); !reflect.DeepEqual(plain.argv, []string{"oh-omp"}) {
		t.Errorf("with no `only` oh-omp got %q, want its own argv", plain.argv)
	}
}

// A SUBCOMMAND RUNS AS TYPED (MM-D30, packdecl.LaunchSelection.Subcommands): pi and oh-omp read a
// subcommand only as their first word, so words handed right after argv[0] would turn `pi update`
// or `oh-omp commit` into a session whose first prompt is the subcommand. A -p that would move
// either hands nothing in front of one, argv or variables, and says so, naming the launch that
// starts a session on the -p's selection; a first word that is not a subcommand is still moved.
func TestHostLeavesASubcommandWhereTheUserTypedIt(t *testing.T) {
	hostGateHome(t, setHostCfg, nil)
	pi := hostSelectionRun(t, "", []string{"-p", "pi=zai,openrouter"}, "pi", "update")
	if !reflect.DeepEqual(pi.argv, []string{"pi", "update"}) {
		t.Errorf("`yolo host -p pi=zai,openrouter -- pi update` exec'd %q, want it as typed\n%s", pi.argv, pi.errs)
	}
	for _, name := range []string{"YOLO_PI_MODEL_LISTS", "YOLO_PI_OPENAI_CODEX_MODELS"} {
		if _, set := pi.env[name]; set {
			t.Errorf("`pi update` was handed %s, half of a selection it does not run on", name)
		}
	}
	for _, want := range []string{"yolo host: `pi update` runs a subcommand of pi, and pi reads one only as " +
		"its first word", "To start pi itself on that selection: `yolo host -p zai,openrouter -- pi`"} {
		if !strings.Contains(pi.errs, want) {
			t.Errorf("the launch must say %q:\n%s", want, pi.errs)
		}
	}
	if strings.Contains(pi.errs, "yolo CHANGED the command") || strings.Contains(pi.errs, "yolo SET variables for pi") {
		t.Errorf("`pi update` disclosed a selection it was not handed:\n%s", pi.errs)
	}
	// A prompt that only starts with the word is no subcommand, and is moved.
	prompt := hostSelectionRun(t, "", []string{"-p", "pi=zai,openrouter"}, "pi", "update the readme")
	if len(prompt.argv) < 3 || prompt.argv[1] != "--provider" || prompt.argv[len(prompt.argv)-1] != "update the readme" {
		t.Errorf("a prompt was not moved: pi got %q\n%s", prompt.argv, prompt.errs)
	}

	home := hostGateHome(t, `{"packs": ["claude", "omp", "zai"], "env_sources": [{"ZAI_API_KEY": "tok-zai"}]}`, nil)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "local", "pack.json"),
		`{"name":"acme","contributes":[{"kind":"models","provider":"zai","only":["glm-5.3","glm-4.6"]}]}`)
	omp := hostSelectionRun(t, home, []string{"-p", "oh-omp=zai"}, "oh-omp", "commit", "--push")
	if !reflect.DeepEqual(omp.argv, []string{"oh-omp", "commit", "--push"}) {
		t.Errorf("`yolo host -p oh-omp=zai -- oh-omp commit --push` exec'd %q, want it as typed\n%s", omp.argv, omp.errs)
	}
	if !strings.Contains(omp.errs, "`oh-omp commit` runs a subcommand of oh-omp") {
		t.Errorf("the launch must say why oh-omp commit was not moved:\n%s", omp.errs)
	}
}
