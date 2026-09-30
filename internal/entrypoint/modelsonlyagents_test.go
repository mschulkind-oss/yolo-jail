package entrypoint

// modelsonlyagents_test.go pins what the agents other than claude render for a provider list a
// `models` contribution narrowed with an `only` (docs/design/model-lists-and-pickers.md §14.1),
// each through its production call site: the boot render over a providers table
// packload.ComposeProviders composed and a profile table packload.ResolveProfiles resolved, the
// env derive through packload.AgentEnv.

import (
	"reflect"
	"sort"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

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
