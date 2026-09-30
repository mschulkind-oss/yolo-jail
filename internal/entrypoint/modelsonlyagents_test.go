package entrypoint

// modelsonlyagents_test.go pins what the agents other than claude render for a provider list a
// `models` contribution narrowed with an `only` (docs/design/model-lists-and-pickers.md §14.1),
// each through its production call site: the boot render over a providers table
// packload.ComposeProviders composed and a profile table packload.ResolveProfiles resolved, the
// env derive through packload.AgentEnv.

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// OH-OMP UNDER AN `only` (§14.1, oh-omp; MM-D8): the selected provider's narrowed list becomes
// omp's `enabledModels` scope in ~/.oh-omp/agent/config.yml, the default entry first. omp then
// shows only the scope, and starts a fresh session on the model its own `modelRoles.default`
// remembers when that is in the scope, else on the scope's first entry — so a valid saved choice
// is never steered, and an off-list one never starts (OQ-ML2).
func TestOmpScopesANarrowedList(t *testing.T) {
	providers, profiles := zaiNarrowed(t, "omp", nil)
	omp, err := embeddedPack("omp")
	if err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    providers,
		"YOLO_USE_PROFILES": `{"oh-omp":"zai"}`,
		"YOLO_PROFILES":     profiles,
	}}
	withCtxRoot(t, t.TempDir(), "omp")
	ConfigurePackSurfaces(e, []*packload.Pack{omp})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	raw, err := os.ReadFile(filepath.Join(e.Home, ".oh-omp", "agent", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := (codec.YAML{}).Decode(raw)
	if err != nil {
		t.Fatalf("config.yml is not YAML: %v\n%s", err, raw)
	}
	cfg, _ := decoded.(map[string]any)
	if got, want := cfg["enabledModels"], []any{"zai/glm-5.3", "zai/glm-5.3-flash"}; !reflect.DeepEqual(got, want) {
		t.Errorf("enabledModels = %v, want %v, the default first", got, want)
	}
	if _, present := cfg["selection"]; present {
		t.Errorf("config.yml carries yolo's reserved selection key:\n%s", raw)
	}

	// The same launch with the list not narrowed writes no scope: a list that only adds sits
	// beside omp's catalog as models.yml rows (OQ-MM1).
	plain := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    zaiReachableJSON,
		"YOLO_USE_PROFILES": `{"oh-omp":"zai"}`,
		"YOLO_PROFILES":     `{"zai":{"provider":"zai"}}`,
	}}
	ConfigurePackSurfaces(plain, []*packload.Pack{omp})
	if raw, err := os.ReadFile(filepath.Join(plain.Home, ".oh-omp", "agent", "config.yml")); err == nil {
		if decoded, _ := (codec.YAML{}).Decode(raw); decoded != nil {
			if m, _ := decoded.(map[string]any); m["enabledModels"] != nil {
				t.Errorf("an unnarrowed list wrote enabledModels = %v", m["enabledModels"])
			}
		}
	}
}

// OH-OMP'S SCOPE LEADS WITH THE DEFAULT ENTRY EVEN WHEN IT IS NOT FIRST IN LIST ORDER, because
// omp starts on the scope's first entry whenever its remembered model is off the scope (MM-D12):
// the first slot is the start rule, not presentation. zai narrowed to glm-4.6 and glm-5.3 puts
// the declared default, glm-5.3, second by id; a profile naming glm-4.6 puts it first; and one
// naming glm-5.3-flash, on a list keeping all three, puts the last entry first. A scope built in
// plain list order passes TestOmpScopesANarrowedList, whose default already sorts first.
func TestOmpScopeLeadsWithADefaultNotFirstInOrder(t *testing.T) {
	omp, err := embeddedPack("omp")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, only, profile string
		user                map[string]packload.UserProfile
		want                []any
	}{
		{"the declared default, second by id", `["glm-4.6","glm-5.3"]`, "zai", nil,
			[]any{"zai/glm-5.3", "zai/glm-4.6"}},
		{"a profile's model, last by id", `["glm-4.6","glm-5.3","glm-5.3-flash"]`, "zai-flash",
			map[string]packload.UserProfile{"zai-flash": {Provider: "zai", Options: map[string]string{"model": "glm-5.3-flash"}}},
			[]any{"zai/glm-5.3-flash", "zai/glm-4.6", "zai/glm-5.3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providers, profiles := zaiNarrowedTo(t, "omp", tc.user, tc.only)
			var errw bytes.Buffer
			e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
				"YOLO_PROVIDERS":    providers,
				"YOLO_USE_PROFILES": `{"oh-omp":"` + tc.profile + `"}`,
				"YOLO_PROFILES":     profiles,
			}}
			withCtxRoot(t, t.TempDir(), "omp")
			ConfigurePackSurfaces(e, []*packload.Pack{omp})
			if fails := e.GenFailures(); len(fails) != 0 {
				t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
			}
			raw, err := os.ReadFile(filepath.Join(e.Home, ".oh-omp", "agent", "config.yml"))
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := (codec.YAML{}).Decode(raw)
			if err != nil {
				t.Fatalf("config.yml is not YAML: %v\n%s", err, raw)
			}
			cfg, _ := decoded.(map[string]any)
			if got := cfg["enabledModels"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("enabledModels = %v, want %v: the default entry first, the rest in list order", got, tc.want)
			}
		})
	}
}

// COPILOT UNDER AN `only` (§14.1, copilot): one COPILOT_MODEL, since copilot's
// environment-variable setup carries one, and it is the narrowed list's default entry (§7.2):
// the profile's `model` when the list holds it, else the `default` alias, else the list's first
// entry. Here the only drops zai's declared default, glm-5.3, so a copilot that looked only at
// the profile's model composed nothing and fell back to its GitHub login. The whole list in
// copilot's menu is providers.json's (MM-D10), after its four measurements.
func TestCopilotStartsOnANarrowedListsDefault(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"zai","only":["glm-5.3-flash","glm-4.6"]}`)
	packs := append(testPacksForAgent(t, "copilot", "zai"), company)
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	vars, err := packload.AgentEnv(packs, table, map[string]string{"copilot": "zai"}, "copilot", "zai",
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Key] = v.Value
	}
	// The narrowed list keeps the provider's order, which for zai, declaring none, is by id.
	if got["COPILOT_MODEL"] != "glm-4.6" {
		t.Errorf("COPILOT_MODEL = %q, want glm-4.6, the narrowed list's first entry (env %v)", got["COPILOT_MODEL"], got)
	}
	if got["COPILOT_PROVIDER_BASE_URL"] == "" {
		t.Errorf("copilot was not pointed at zai: %v", got)
	}
}

// CODEX UNDER AN `only` (§14.1, codex): until the prelaunch catalog (MM-D9) is built, what an
// `only` gives codex is its selection, and that selection is the narrowed list's default entry
// even when the only dropped the model the profile would otherwise start on.
func TestCodexSelectsANarrowedListsDefault(t *testing.T) {
	company := companyModelsPack(t, `{"kind":"models","provider":"openrouter","add":[
	    {"id":"x-1","vendor":"x"},{"id":"x-2","vendor":"x"}]},
	  {"kind":"models","provider":"openrouter","only":["x-2"]}`)
	packs := append(testPacksForAgent(t, "codex", "openrouter"), company)
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	cfg := renderCodexConfig(t, mustCompactJSON(t, table), `{"codex":"openrouter"}`,
		mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	if cfg["model_provider"] != "openrouter" || cfg["model"] != "x-2" {
		t.Errorf("codex selection = %v/%v, want openrouter/x-2, the narrowed list's only entry",
			cfg["model_provider"], cfg["model"])
	}
}

// zaiNarrowed composes zai's shipped provider narrowed by a company pack's `only`, and resolves
// the profiles (zai's own and the user's).
func zaiNarrowed(t *testing.T, agent string, user map[string]packload.UserProfile) (providers, profiles string) {
	t.Helper()
	return zaiNarrowedTo(t, agent, user, `["glm-5.3","glm-5.3-flash"]`)
}

// zaiNarrowedTo is zaiNarrowed with the `only` list the company pack keeps, as a JSON array.
func zaiNarrowedTo(t *testing.T, agent string, user map[string]packload.UserProfile, only string) (providers, profiles string) {
	t.Helper()
	company := companyModelsPack(t, `{"kind":"models","provider":"zai","only":`+only+`}`)
	packs := append(testPacksForAgent(t, agent, "zai"), company)
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, user, table)
	if err != nil {
		t.Fatal(err)
	}
	return mustCompactJSON(t, table), mustCompactJSON(t, packload.ProfilesWireTable(resolved))
}

// OPENCODE UNDER AN `only` (§14.1, opencode native; MM-D7): opencode starts zai from its own
// catalog entry of the same id, so a config row adds beside about 18 models and cannot narrow;
// its `whitelist` is the one lever, and it refuses every other model too, so it renders only
// while the profile's switch is on (MM-D5). Off, the menu is not narrowed.
func TestOpencodeWhitelistsANarrowedList(t *testing.T) {
	off := false
	for _, tc := range []struct {
		name     string
		user     map[string]packload.UserProfile
		profile  string
		enforced bool
	}{
		{"enforced by default", nil, "zai", true},
		{"with enforce_models off", map[string]packload.UserProfile{"zai-open": {Provider: "zai", EnforceModels: &off}}, "zai-open", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providers, profiles := zaiNarrowed(t, "opencode", tc.user)
			r := newPioencodeRender(t, providers)
			r.wireProfiles(profiles)
			r.render(t, `{"opencode":"`+tc.profile+`"}`)
			provider, _ := r.ocConfig(t)["provider"].(map[string]any)
			zai, _ := provider["zai"].(map[string]any)
			models, _ := zai["models"].(map[string]any)
			var ids []string
			for id := range models {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			if want := []string{"glm-5.3", "glm-5.3-flash"}; !reflect.DeepEqual(ids, want) {
				t.Errorf("provider.zai.models = %v, want the narrowed list %v", ids, want)
			}
			whitelist, has := zai["whitelist"]
			switch {
			case tc.enforced && !reflect.DeepEqual(whitelist, []any{"glm-5.3", "glm-5.3-flash"}):
				t.Errorf("provider.zai.whitelist = %v, want the narrowed ids", whitelist)
			case !tc.enforced && has:
				t.Errorf("provider.zai.whitelist = %v with enforce_models off, want none: it refuses too", whitelist)
			}
		})
	}
}

// OPENCODE STARTS ON A NARROWED LIST'S DEFAULT ENTRY (§7.2) when the only dropped the profile's
// model: the profile's `model` when the list holds it, else the `default` alias, else the list's
// first entry. zai declares `model: glm-5.3` and no `default` alias, so narrowed to glm-4.6 and
// glm-5.3-flash its default entry is glm-4.6, the first by id. Without the rule the derive
// resolved nothing and wrote neither `model` nor `enabled_providers`, so opencode started on its
// own persisted choice and its menu no longer followed the selection (OQ-CN4).
func TestOpencodeStartsOnANarrowedListsDefault(t *testing.T) {
	providers, profiles := zaiNarrowedTo(t, "opencode", nil, `["glm-4.6","glm-5.3-flash"]`)
	r := newPioencodeRender(t, providers)
	r.wireProfiles(profiles)
	r.render(t, `{"opencode":"zai"}`)
	cfg := r.ocConfig(t)
	if cfg["model"] != "zai/glm-4.6" || cfg["small_model"] != "zai/glm-4.6" {
		t.Errorf("opencode model/small_model = %v/%v, want zai/glm-4.6 for both, the narrowed list's first entry",
			cfg["model"], cfg["small_model"])
	}
	if got, want := cfg["enabled_providers"], []any{"zai"}; !reflect.DeepEqual(got, want) {
		t.Errorf("enabled_providers = %v, want %v: the menu follows the selection", got, want)
	}
}

// PI STARTS ON A NARROWED LIST'S DEFAULT ENTRY too, by the same rule: with no enabledModels under
// an only, a defaultModel is the one thing that decides pi's start, and without it pi started on
// its own saved or built-in choice.
func TestPiStartsOnANarrowedListsDefault(t *testing.T) {
	providers, profiles := zaiNarrowedTo(t, "pi", nil, `["glm-4.6","glm-5.3-flash"]`)
	r := newPioencodeRender(t, providers)
	r.wireProfiles(profiles)
	r.render(t, `{"pi":"zai"}`)
	settings := r.piSettings(t)
	if settings["defaultProvider"] != "zai" || settings["defaultModel"] != "glm-4.6" {
		t.Errorf("pi selection = %v/%v, want zai/glm-4.6, the narrowed list's first entry",
			settings["defaultProvider"], settings["defaultModel"])
	}
	if scope, present := settings["enabledModels"]; present {
		t.Errorf("pi enabledModels = %v, want none: the registration is the exact list", scope)
	}
}

// THE `default` ALIAS OUTRANKS THE FIRST ENTRY: a narrowed list that keeps its `default` starts
// both agents there, not on the entry that sorts first.
func TestOpencodeAndPiPreferANarrowedListsDefaultAlias(t *testing.T) {
	providers := `{"gw":{"endpoints":{"openai":{"base_url":"https://gw.example/v1","wire_api":"openai-chat-completions"}},
	  "api_key_env_name":"GW_KEY","models":{"default":"b-2","a-1":"a-1","b-2":"b-2"},"models_only":true}}`
	r := newPioencodeRender(t, providers)
	r.wireProfiles(`{"gw":{"provider":"gw","model":"gone-9"}}`)
	r.render(t, `{"opencode":"gw","pi":"gw"}`)
	if got := r.ocConfig(t)["model"]; got != "gw/b-2" {
		t.Errorf("opencode model = %v, want gw/b-2, the narrowed list's default alias", got)
	}
	if got := r.piSettings(t)["defaultModel"]; got != "b-2" {
		t.Errorf("pi defaultModel = %v, want b-2, the narrowed list's default alias", got)
	}
}

// OPENCODE'S OWN BEDROCK CLIENT GETS THE WHITELIST TOO. The native `amazon-bedrock` row (Bedrock
// step 2's) lists the narrowed entries, but those rows add beside opencode's own Bedrock catalog
// and cannot narrow it, so without the whitelist an `only` left that menu whole and refused
// nothing. The same switch governs it: off, no whitelist.
func TestOpencodeWhitelistsANarrowedListOnItsOwnBedrockClient(t *testing.T) {
	const opus, sol = "global.anthropic.claude-opus-5-5", "us.openai.gpt-6.1-sol"
	company := companyModelsPack(t, `{"kind":"models","provider":"bedrock","only":["`+sol+`","`+opus+`"]}`)
	packs := append(testPacksForAgent(t, "opencode"), company)
	off := false
	for _, tc := range []struct {
		name     string
		user     map[string]packload.UserProfile
		profile  string
		enforced bool
	}{
		{"enforced by default", nil, "bedrock", true},
		{"with enforce_models off", map[string]packload.UserProfile{"bedrock-open": {Provider: "bedrock", EnforceModels: &off}}, "bedrock-open", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table, err := packload.ComposeProviders(nil, packs)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := packload.ResolveProfiles(packs, tc.user, table)
			if err != nil {
				t.Fatal(err)
			}
			r := newPioencodeRender(t, mustCompactJSON(t, table))
			r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
			r.render(t, `{"opencode":"`+tc.profile+`"}`)
			cfg := r.ocConfig(t)
			rows, _ := cfg["provider"].(map[string]any)
			native, _ := rows["amazon-bedrock"].(map[string]any)
			if native == nil {
				t.Fatalf("no provider[\"amazon-bedrock\"] row: %v", rows)
			}
			models, _ := native["models"].(map[string]any)
			var ids []string
			for id := range models {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			if want := []string{opus, sol}; !reflect.DeepEqual(ids, want) {
				t.Errorf("amazon-bedrock models = %v, want the narrowed list %v", ids, want)
			}
			whitelist, has := native["whitelist"]
			switch {
			case tc.enforced && !reflect.DeepEqual(whitelist, []any{opus, sol}):
				t.Errorf("amazon-bedrock whitelist = %v, want the narrowed ids", whitelist)
			case !tc.enforced && has:
				t.Errorf("amazon-bedrock whitelist = %v with enforce_models off, want none", whitelist)
			}
			if cfg["model"] != "amazon-bedrock/"+opus {
				t.Errorf("model = %v, want amazon-bedrock/%s, the narrowed list's first entry", cfg["model"], opus)
			}
		})
	}
}

// NO WHITELIST FOR A LIST NO `only` NARROWED: what an `add` does to opencode's own catalog is
// OQ-MM1's, and whether a list with no `only` refuses on a gateway serving more is OQ-MM3's.
func TestOpencodeWritesNoWhitelistForAnUnnarrowedList(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.render(t, `{"opencode":"zai"}`)
	provider, _ := r.ocConfig(t)["provider"].(map[string]any)
	if zai, _ := provider["zai"].(map[string]any); zai["whitelist"] != nil {
		t.Errorf("provider.zai.whitelist = %v, want none for a list no only narrowed", zai["whitelist"])
	}
}

// PI UNDER AN `only` (§14.1, pi on a provider pi ships; MM-D6): the narrowed list reaches pi's
// extension as data, keyed by pi's provider id, and pi's selection writes no enabledModels for
// it, since the registration makes pi's "all" view the list (as ML-D2 ruled for openai-codex).
func TestPiGetsANarrowedListToRegister(t *testing.T) {
	providers, profiles := zaiNarrowed(t, "pi", nil)
	r := newPioencodeRender(t, providers)
	r.wireProfiles(profiles)
	r.render(t, `{"pi":"zai"}`)
	file := r.surface(t, ".pi", "agent", "yolo-model-lists.json")
	lists, _ := file["providers"].(map[string]any)
	zai, _ := lists["zai"].(map[string]any)
	rows, _ := zai["models"].([]any)
	var ids []any
	for _, row := range rows {
		ids = append(ids, row.(map[string]any)["id"])
	}
	if want := []any{"glm-5.3", "glm-5.3-flash"}; !reflect.DeepEqual(ids, want) {
		t.Errorf("pi model-lists zai ids = %v, want the narrowed list %v", ids, want)
	}
	if len(lists) != 1 {
		t.Errorf("pi model-lists = %v, want only the narrowed provider", lists)
	}
	// The switch is on by default, so the extension refuses outside the list (MM-D21), on the api
	// of the models.json row the models derive writes for zai.
	if zai["enforce"] != true || zai["api"] != "openai-completions" {
		t.Errorf("pi model-lists zai enforce/api = %v/%v, want true/openai-completions", zai["enforce"], zai["api"])
	}
	settings := r.piSettings(t)
	if settings["defaultProvider"] != "zai" || settings["defaultModel"] != "glm-5.3" {
		t.Errorf("pi selection = %v/%v, want zai/glm-5.3", settings["defaultProvider"], settings["defaultModel"])
	}
	if scope, present := settings["enabledModels"]; present {
		t.Errorf("pi enabledModels = %v, want none: the registration is the exact list", scope)
	}
}

// EACH NARROWED LIST CARRIES THE SWITCH OF THE PROFILE THAT GOVERNS IT (MM-D5 for pi, the rule
// opencode's whitelist follows): the primary's, and for a later active-set entry that entry's own,
// every entry being live (AP-P1). pi's own Bedrock client has no models.json row, so its list
// carries no api and the extension reads one from pi's catalog. Each case sets the switches apart,
// so reading one switch for every list, or none, fails one of them.
func TestPiNarrowedListCarriesItsProfilesModelSwitch(t *testing.T) {
	const sol = "us.openai.gpt-6.1-sol"
	company := companyModelsPack(t,
		`{"kind":"models","provider":"zai","only":["glm-5.3"]},{"kind":"models","provider":"bedrock","only":["`+sol+`"]}`)
	packs := append(testPacksForAgent(t, "pi", "zai"), company)
	off := false
	for _, tc := range []struct {
		name         string
		user         map[string]packload.UserProfile
		use          string
		zai, bedrock bool
	}{
		{"the primary's switch off", map[string]packload.UserProfile{"zai-open": {Provider: "zai", EnforceModels: &off}},
			`{"pi":"zai-open"}`, false, false},
		{"the primary's switch on", nil, `{"pi":"zai"}`, true, true},
		{"a later entry's own switch off",
			map[string]packload.UserProfile{"bedrock-open": {Provider: "bedrock", EnforceModels: &off}},
			`{"pi":["zai","bedrock-open"]}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table, err := packload.ComposeProviders(nil, packs)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := packload.ResolveProfiles(packs, tc.user, table)
			if err != nil {
				t.Fatal(err)
			}
			r := newPioencodeRender(t, mustCompactJSON(t, table))
			r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
			r.render(t, tc.use)
			lists, _ := r.surface(t, ".pi", "agent", "yolo-model-lists.json")["providers"].(map[string]any)
			zai, _ := lists["zai"].(map[string]any)
			if zai["enforce"] != tc.zai {
				t.Errorf("zai enforce = %v, want %v", zai["enforce"], tc.zai)
			}
			bedrock, _ := lists["amazon-bedrock"].(map[string]any)
			if bedrock == nil {
				t.Fatalf("pi model-lists = %v, want yolo's bedrock list as pi's amazon-bedrock", lists)
			}
			if bedrock["enforce"] != tc.bedrock {
				t.Errorf("amazon-bedrock enforce = %v, want %v", bedrock["enforce"], tc.bedrock)
			}
			if api, has := bedrock["api"]; has {
				t.Errorf("amazon-bedrock api = %v, want none: pi's own Bedrock client has no models.json row", api)
			}
		})
	}
}

// narrowedPiRender renders pi on provider narrowed by a company pack whose contributes are the
// given JSON, under the provider's own profile, and returns the render.
func narrowedPiRender(t *testing.T, provider, contributes string) *pioencodeRender {
	t.Helper()
	company := companyModelsPack(t, contributes)
	packs := append(testPacksForAgent(t, "pi", provider), company)
	table, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, table))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, `{"pi":"`+provider+`"}`)
	return r
}

// piRowIDs is the model ids of provider's models.json row.
func piRowIDs(t *testing.T, r *pioencodeRender, provider string) []any {
	t.Helper()
	rows, _ := r.surface(t, ".pi", "agent", "models.json")["providers"].(map[string]any)
	row, _ := rows[provider].(map[string]any)
	models, _ := row["models"].([]any)
	var ids []any
	for _, m := range models {
		ids = append(ids, m.(map[string]any)["id"])
	}
	return ids
}

// A NARROWED LIST REGISTERS THE IDS PI SENDS: on Kilo, pi's models.json row and its selection spell
// a bare `deepseek-…` id as Kilo's gateway names it, `deepseek/deepseek-…` (normalizeKiloModel), so
// the registration that replaces the row's list must spell it the same way. Otherwise pi's menu
// sends the bare id Kilo does not serve, yolo's own default is not on the menu, and with the
// switch on the refusal turns away the one spelling the row and the selection use (MM-D6, MM-D21).
func TestPiNarrowedKiloListRegistersTheIdsItsRowSends(t *testing.T) {
	r := narrowedPiRender(t, "kilo",
		`{"kind":"models","provider":"kilo","add":[{"id":"deepseek-v4.1-flash","vendor":"deepseek"},`+
			`{"id":"x-ai/grok-5","vendor":"xai"}]},`+
			`{"kind":"models","provider":"kilo","only":["deepseek-v4.1-flash","x-ai/grok-5"]}`)
	lists, _ := r.surface(t, ".pi", "agent", "yolo-model-lists.json")["providers"].(map[string]any)
	kilo, _ := lists["kilo"].(map[string]any)
	models, _ := kilo["models"].([]any)
	var registered []any
	for _, m := range models {
		registered = append(registered, m.(map[string]any)["id"])
	}
	row := piRowIDs(t, r, "kilo")
	if len(row) == 0 || !reflect.DeepEqual(registered, row) {
		t.Errorf("kilo's registered ids = %v, want its models.json row's %v", registered, row)
	}
	settings := r.piSettings(t)
	found := false
	for _, id := range registered {
		found = found || id == settings["defaultModel"]
	}
	if !found {
		t.Errorf("pi's selection starts on kilo/%v, which the registered list %v does not hold",
			settings["defaultModel"], registered)
	}
}

// A NARROWED PROVIDER PI CANNOT USE IS NOT REGISTERED: pi refuses a registration for a provider
// it has no address or credential for ("no authentication method configured"), which would fail
// the extension's whole load.
func TestPiRegistersNoNarrowedListItCannotUse(t *testing.T) {
	r := newPioencodeRender(t, `{"anthropic_only":{"endpoints":{"anthropic":{"base_url":"https://a.example"}},
	  "models":{"m-1":"m-1"},"models_only":true}}`)
	r.render(t, `{}`)
	if file := r.surface(t, ".pi", "agent", "yolo-model-lists.json"); len(file) != 0 {
		t.Errorf("pi model-lists = %v, want nothing for a provider pi cannot reach", file)
	}
}

// A LIST NO `only` NARROWED GIVES PI NOTHING TO REGISTER: it stays a models.json row beside pi's
// own catalog (what an `add` does to that catalog is OQ-MM1's), and the scope stays as before.
func TestPiRegistersNothingForAnUnnarrowedList(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.render(t, `{"pi":"zai"}`)
	if file := r.surface(t, ".pi", "agent", "yolo-model-lists.json"); len(file) != 0 {
		t.Errorf("pi model-lists = %v, want nothing for a list no only narrowed", file)
	}
	if _, present := r.piSettings(t)["enabledModels"]; !present {
		t.Error("pi enabledModels went missing for an unnarrowed list")
	}
}
