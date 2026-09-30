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

// zaiNarrowed composes zai's shipped provider narrowed by a company pack's `only`, and resolves
// the profiles (zai's own and the user's).
func zaiNarrowed(t *testing.T, agent string, user map[string]packload.UserProfile) (providers, profiles string) {
	t.Helper()
	company := companyModelsPack(t, `{"kind":"models","provider":"zai","only":["glm-5.3","glm-5.3-flash"]}`)
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
	settings := r.piSettings(t)
	if settings["defaultProvider"] != "zai" || settings["defaultModel"] != "glm-5.3" {
		t.Errorf("pi selection = %v/%v, want zai/glm-5.3", settings["defaultProvider"], settings["defaultModel"])
	}
	if scope, present := settings["enabledModels"]; present {
		t.Errorf("pi enabledModels = %v, want none: the registration is the exact list", scope)
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
