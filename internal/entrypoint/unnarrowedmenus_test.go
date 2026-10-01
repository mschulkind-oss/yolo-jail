package entrypoint

// unnarrowedmenus_test.go holds packs/opencode's `exact_menu_refuses` declaration to what its
// derive does (docs/design/model-lists-and-pickers.md MM-D29). `yolo check` says a profile's
// `enforce_models` off leaves an agent's menu unnarrowed from the declaration alone
// (packload.UnnarrowedMenus); the derive decides where the `whitelist` goes. The two are two
// statements of one rule, so this renders the shipped derive through the boot render, with the
// switch on and off, and fails wherever the check would name a menu the switch does not decide, or
// miss one it does.

import (
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// opencodeRowOf is the opencode provider id the derive writes yolo's provider under.
var opencodeRowOf = map[string]string{"zai": "zai", "bedrock": "amazon-bedrock", "openai-codex": "openai",
	"anthropic": "anthropic"}

// renderOpencodeOver renders opencode over the table packs compose under the user's own
// `providers` (userProviders, JSON, "" for none) with user's profiles resolved, for the selection
// use, returning which of providers' rows carry a whitelist and what packload.UnnarrowedMenus
// says of opencode over the same inputs.
func renderOpencodeOver(t *testing.T, packs []*packload.Pack, userProviders string,
	user map[string]packload.UserProfile, use string, providers []string) (whitelisted, reported map[string]bool) {
	t.Helper()
	var own *jsonx.OrderedMap
	if userProviders != "" {
		decoded, err := jsonx.Decode([]byte(userProviders))
		if err != nil {
			t.Fatal(err)
		}
		own, _ = decoded.(*jsonx.OrderedMap)
	}
	table, err := packload.ComposeProviders(own, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, user, table)
	if err != nil {
		t.Fatal(err)
	}
	r := newPioencodeRender(t, mustCompactJSON(t, table))
	r.wireProfiles(mustCompactJSON(t, packload.ProfilesWireTable(resolved)))
	r.render(t, use)
	rows := ocRows(t, r.ocConfig(t))
	whitelisted = map[string]bool{}
	for _, p := range providers {
		row, _ := rows[opencodeRowOf[p]].(map[string]any)
		_, has := row["whitelist"]
		whitelisted[p] = has
	}
	decoded, err := jsonx.Decode([]byte(use))
	if err != nil {
		t.Fatal(err)
	}
	selection, _ := decoded.(*jsonx.OrderedMap)
	reported = map[string]bool{}
	for _, m := range packload.UnnarrowedMenus(packs, table, resolved,
		packload.ProfileTable(selection), packload.ProfileSets(selection)) {
		if m.Agent != "opencode" || m.Pack != "opencode" {
			t.Errorf("UnnarrowedMenus named %+v, want only opencode's own menus", m)
		}
		reported[m.Provider] = true
	}
	return whitelisted, reported
}

// THE CHECK NAMES A MENU EXACTLY WHERE THE SWITCH TAKES THE WHITELIST AWAY: for each provider, the
// line appears with the switch off if and only if the derive writes that provider's whitelist with
// it on and none with it off. The cases cover both halves of the declaration (a list an `only`
// narrowed, on a generic row and on opencode's own Bedrock row; the subscription's whole list, the
// one provider it names), a list no `only` narrowed, which neither the derive nor the check
// narrows, and an active set, whose entries each carry their own profile's switch.
func TestTheUnnarrowedMenuLineAgreesWithOpencodesWhitelist(t *testing.T) {
	off := false
	user := map[string]packload.UserProfile{
		"zai-open":     {Provider: "zai", EnforceModels: &off},
		"bedrock-open": {Provider: "bedrock", EnforceModels: &off},
		"codex-open":   {Provider: "openai-codex", EnforceModels: &off},
		// A provider naming no endpoint and no platform is the agent's own first-party API
		// (docs/reference/protocol-resolution.md OQ-PR2), which opencode's derive writes no row for.
		"anthropic-on":   {Provider: "anthropic"},
		"anthropic-open": {Provider: "anthropic", EnforceModels: &off},
	}
	const firstParty = `{"anthropic": {"models": {"claude-x": "claude-x", "claude-y": "claude-y"}}}`
	const opus, sol = "global.anthropic.claude-opus-5-5", "us.openai.gpt-6.1-sol"
	narrowed := companyModelsPack(t, `{"kind":"models","provider":"zai","only":["glm-5.3"]},`+
		`{"kind":"models","provider":"bedrock","only":["`+opus+`","`+sol+`"]},`+
		`{"kind":"models","provider":"anthropic","only":["claude-x"]}`)
	added := companyModelsPack(t, `{"kind":"models","provider":"zai","add":[{"id":"glm-6","vendor":"zai"}]}`)
	// An `only` naming nothing any pack added keeps no model: the entry is still marked narrowed,
	// and the derive writes no whitelist for an empty list, with the switch on or off.
	emptied := companyModelsPack(t, `{"kind":"models","provider":"zai","only":["glm-nothing-added"]}`)
	withNarrowed := append(testPacksForAgent(t, "opencode", "zai"), narrowed)
	withAdded := append(testPacksForAgent(t, "opencode", "zai"), added)
	withEmptied := append(testPacksForAgent(t, "opencode", "zai"), emptied)

	for _, tc := range []struct {
		name      string
		packs     []*packload.Pack
		own       string
		on, off   string
		providers []string
		// want is the providers the check must name with the switch off: the agreement below
		// would also hold if the derive and the check both said nothing anywhere.
		want []string
	}{
		{"a narrowed list on a generic row", withNarrowed, "", `{"opencode":"zai"}`, `{"opencode":"zai-open"}`,
			[]string{"zai"}, []string{"zai"}},
		{"a narrowed list on opencode's own Bedrock row", withNarrowed, "", `{"opencode":"bedrock"}`,
			`{"opencode":"bedrock-open"}`, []string{"bedrock"}, []string{"bedrock"}},
		{"the subscription's whole list", withNarrowed, "", `{"opencode":"codex"}`, `{"opencode":"codex-open"}`,
			[]string{"openai-codex"}, []string{"openai-codex"}},
		{"a list no only narrowed", withAdded, "", `{"opencode":"zai"}`, `{"opencode":"zai-open"}`,
			[]string{"zai"}, nil},
		{"an only that keeps no model", withEmptied, "", `{"opencode":"zai"}`, `{"opencode":"zai-open"}`,
			[]string{"zai"}, nil},
		{"an active set whose second entry has the switch off", withNarrowed, "", `{"opencode":["zai","bedrock"]}`,
			`{"opencode":["zai","bedrock-open"]}`, []string{"zai", "bedrock"}, []string{"bedrock"}},
		{"a narrowed list on the agent's own first-party provider", withNarrowed, firstParty,
			`{"opencode":"anthropic-on"}`, `{"opencode":"anthropic-open"}`, []string{"anthropic"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onRows, onReported := renderOpencodeOver(t, tc.packs, tc.own, user, tc.on, tc.providers)
			if len(onReported) != 0 {
				t.Errorf("with the switch on the check named %v", onReported)
			}
			offRows, reported := renderOpencodeOver(t, tc.packs, tc.own, user, tc.off, tc.providers)
			var named []string
			for _, p := range tc.providers {
				if taken := onRows[p] && !offRows[p]; reported[p] != taken {
					t.Errorf("provider %s: the check names it %v, but the switch takes opencode's whitelist "+
						"away %v (on: %v, off: %v)", p, reported[p], taken, onRows[p], offRows[p])
				}
				if reported[p] {
					named = append(named, p)
				}
			}
			sort.Strings(named)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(named, ",") != strings.Join(want, ",") {
				t.Errorf("the check named %v with the switch off, want %v", named, want)
			}
		})
	}
}
