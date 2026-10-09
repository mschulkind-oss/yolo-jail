package packload

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func emptyCheckPack(t *testing.T, modelListCheck, providerFields, extraContributions, extraPrograms string) *Pack {
	t.Helper()
	program := `{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic","protocols":["openai"]}`
	if extraPrograms != "" {
		program += "," + extraPrograms
	}
	provider := `{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}}`
	if providerFields != "" {
		provider += "," + providerFields
	}
	provider += "}"
	contributions := program + "," + provider + `,{"kind":"profile","name":"selected","provider":"target"}`
	if extraContributions != "" {
		contributions += "," + extraContributions
	}
	manifest := `{"model_list_check":{"synthetic":` + modelListCheck + `},"contributes":[` + contributions + `]}`
	return &Pack{Name: "synthetic-owner", Decl: declFrom(t, manifest)}
}

func emptyCheckInput(t *testing.T, p *Pack) EmptyModelListInput {
	t.Helper()
	return emptyCheckInputWithUser(t, p, nil)
}

func emptyCheckInputWithUser(t *testing.T, p *Pack, user *jsonx.OrderedMap) EmptyModelListInput {
	t.Helper()
	packs := []*Pack{p}
	presence := map[string]bool{}
	providers, err := ComposeProviders(user, packs, WithModelListPresence(func(name string, supplied bool) {
		presence[name] = supplied
	}))
	if err != nil {
		t.Fatalf("ComposeProviders: %v", err)
	}
	resolved, err := ResolveProfiles(packs, nil, providers)
	if err != nil {
		t.Fatalf("ResolveProfiles: %v", err)
	}
	return EmptyModelListInput{Packs: packs, Providers: providers,
		Profiles: map[string]string{"synthetic": "selected"},
		Sets:     map[string][]string{"synthetic": {"selected"}},
		Resolved: resolved, Presence: presence}
}

func userModelsEntry(provider string, value any) *jsonx.OrderedMap {
	models := jsonx.NewOrderedMap()
	models.Set("models", value)
	entry := jsonx.NewOrderedMap()
	entry.Set(provider, models)
	return entry
}

func TestComposeProvidersReportsSuppliedModelListPresenceOutOfBand(t *testing.T) {
	base := func(models string) *Pack {
		return &Pack{Name: "p", Decl: declFrom(t, `{"contributes":[{"kind":"provider","name":"target"`+models+`}]}`)}
	}
	withRows := base(`,"models":{"one":"one"}`)
	withoutRows := base("")
	empty := base(`,"models":{}`)
	capture := func(user *jsonx.OrderedMap, packs ...*Pack) (map[string]bool, *jsonx.OrderedMap) {
		t.Helper()
		got := map[string]bool{}
		table, err := ComposeProviders(user, packs, WithModelListPresence(func(name string, supplied bool) {
			got[name] = supplied
		}))
		if err != nil {
			t.Fatal(err)
		}
		if table != nil {
			if entry := providerEntry(table, "target"); entry != nil {
				for _, key := range entry.Keys() {
					if key == "model_list_check" || key == "models_supplied" {
						t.Fatalf("presence leaked into provider wire table under %q", key)
					}
				}
			}
		}
		return got, table
	}

	for _, tc := range []struct {
		name  string
		user  *jsonx.OrderedMap
		packs []*Pack
		want  map[string]bool
	}{
		{"absent provider field", nil, []*Pack{withoutRows}, map[string]bool{"target": false}},
		{"explicit empty provider map", nil, []*Pack{empty}, map[string]bool{"target": true}},
		{"later winning provider omits list", nil, []*Pack{withRows, withoutRows}, map[string]bool{"target": false}},
		{"later winning provider declares empty list", nil, []*Pack{withRows, empty}, map[string]bool{"target": true}},
		{"empty user map", userModelsEntry("target", jsonx.NewOrderedMap()), []*Pack{withoutRows}, map[string]bool{"target": true}},
		{"user null list clears previous list", userModelsEntry("target", nil), []*Pack{withRows}, map[string]bool{"target": false}},
		{"user null provider removes callback", userProvidersForPresence(t, `{"target":null}`), []*Pack{withRows}, map[string]bool{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := capture(tc.user, tc.packs...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("presence = %v, want %v", got, tc.want)
			}
		})
	}

	// A final per-alias null leaves a supplied, empty map; only's composed mark preserves
	// presence even when an explicit user null removes the models key itself.
	aliasDelete := userModelsEntry("target", mustOrdered(t, `{"one":null}`))
	got, _ := capture(aliasDelete, withRows)
	if !got["target"] {
		t.Errorf("deleting the final alias cleared presence: %v", got)
	}
	only := &Pack{Name: "only", Decl: declFrom(t, `{"contributes":[{"kind":"models","provider":"target","only":["not-added"]}]}`)}
	got, table := capture(userModelsEntry("target", nil), withoutRows, only)
	if !got["target"] {
		t.Errorf("a user-null after only cleared its supplied presence: %v", got)
	}
	if v, ok := providerEntry(table, "target").Get(ModelsOnlyKey); !ok || v != true {
		t.Errorf("only marker = (%v,%t), want true", v, ok)
	}

	// model_options alone does not supply a model list.
	orphanFacts := jsonx.NewOrderedMap()
	orphanFacts.Set("orphan", mustOrdered(t, `{"vendor":"blocked"}`))
	entry := jsonx.NewOrderedMap()
	entry.Set("model_options", orphanFacts)
	if rows := callableModelRows(entry); len(rows) != 0 {
		t.Errorf("model_options alone synthesized callable rows: %+v", rows)
	}
}

func userProvidersForPresence(t *testing.T, json string) *jsonx.OrderedMap {
	t.Helper()
	m := mustOrdered(t, json)
	return m
}

func mustOrdered(t *testing.T, raw string) *jsonx.OrderedMap {
	t.Helper()
	v, err := json5.Decode([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decoded %T, want object", v)
	}
	return m
}

func TestEmptyModelListsLeavesProgramsWithoutPackOwnedFactsAlone(t *testing.T) {
	legacy := &Pack{Name: "legacy-owner", Decl: declFrom(t, `{"contributes":[`+
		`{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic","protocols":["openai"]},`+
		`{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}},"models":{}},`+
		`{"kind":"profile","name":"selected","provider":"target"}]}`)}
	if got := EmptyModelLists(emptyCheckInput(t, legacy)); len(got) != 0 {
		t.Fatalf("program without check-only metadata should be silent: %+v", got)
	}
}

func TestEmptyModelListsDistinguishesAbsentEmptyFilteredAndVendorlessLists(t *testing.T) {
	for _, tc := range []struct {
		name, modelCheck, providerFields string
		want                             EmptyModelListReason
	}{
		{"absent list", `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`, "", ""},
		{"explicit empty list", `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`, `"models":{}`, EmptyModelListSource},
		{"only another maker", `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`, `"models":{"model":"model"},"model_options":{"model":{"vendor":"blocked"}}`, EmptyModelListFiltered},
		{"mixed makers keep a callable row", `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`, `"models":{"blocked":"blocked","allowed":"allowed"},"model_options":{"blocked":{"vendor":"blocked"},"allowed":{"vendor":"allowed"}}`, ""},
		{"vendorless row is callable", `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`, `"models":{"model":"model"}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := emptyCheckInput(t, emptyCheckPack(t, tc.modelCheck, tc.providerFields, "", ""))
			got := EmptyModelLists(input)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("EmptyModelLists = %+v, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0].Reason != tc.want || got[0].Program != "synthetic" ||
				got[0].Pack != "synthetic-owner" || got[0].Provider != "target" || got[0].Profile != "selected" {
				t.Fatalf("EmptyModelLists = %+v, want one %q row with identity", got, tc.want)
			}
		})
	}
}

func TestEmptyModelListsAppliesOnlyThenFinalUserAlias(t *testing.T) {
	only := `{"kind":"models","provider":"target","only":["not-added"]}`
	p := emptyCheckPack(t, `{}`, `"models":{"base":"base"}`, only, "")
	input := emptyCheckInput(t, p)
	if got := EmptyModelLists(input); len(got) != 1 || got[0].Reason != EmptyModelListSource {
		t.Fatalf("empty intersection = %+v, want one source-empty result", got)
	}
	userModels := jsonx.NewOrderedMap()
	userModels.Set("restored", "restored-model")
	providers := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	entry.Set("models", userModels)
	providers.Set("target", entry)
	table, err := ComposeProviders(providers, input.Packs, WithModelListPresence(func(name string, supplied bool) {
		input.Presence[name] = supplied
	}))
	if err != nil {
		t.Fatal(err)
	}
	input.Providers = table
	input.Resolved, err = ResolveProfiles(input.Packs, nil, table)
	if err != nil {
		t.Fatal(err)
	}
	if got := EmptyModelLists(input); len(got) != 0 {
		t.Fatalf("final vendorless user alias did not restore callability: %+v", got)
	}
}

func TestEmptyModelListsAppliesCanonicalAliasMakerBeforeOtherAliases(t *testing.T) {
	p := emptyCheckPack(t, `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`,
		`"models":{"model":"model","z-alias":"model"},"model_options":{"model":{"vendor":"allowed"},"z-alias":{"vendor":"blocked"}}`, "", "")
	if got := EmptyModelLists(emptyCheckInput(t, p)); len(got) != 0 {
		t.Fatalf("canonical id's allowed maker should win over conflicting alias: %+v", got)
	}
	orphanOptions := jsonx.NewOrderedMap()
	orphanOptions.Set("orphan", mustOrdered(t, `{"vendor":"blocked"}`))
	orphanEntry := jsonx.NewOrderedMap()
	orphanEntry.Set("model_options", orphanOptions)
	if rows := callableModelRows(orphanEntry); len(rows) != 0 {
		t.Fatalf("model_options-only orphan invented a row: %+v", rows)
	}
}

func TestEmptyModelListsHonorsNativeEmptyExceptionAndViaRuleSelection(t *testing.T) {
	p := emptyCheckPack(t, `{"rules":[{"platform":"fixture-platform","via":false,"makers":["allowed"],"native_empty_ok":true}]}`,
		`"models":{}`, "", "")
	input := emptyCheckInput(t, p)
	if got := EmptyModelLists(input); len(got) != 0 {
		t.Fatalf("native source-empty exception should suppress only source-empty: %+v", got)
	}
	input.Presence["target"] = true
	provider := providerEntry(input.Providers, "target")
	models := jsonx.NewOrderedMap()
	models.Set("model", "model")
	provider.Set("models", models)
	options := jsonx.NewOrderedMap()
	facts := jsonx.NewOrderedMap()
	facts.Set("vendor", "blocked")
	options.Set("model", facts)
	provider.Set("model_options", options)
	if got := EmptyModelLists(input); len(got) != 1 || got[0].Reason != EmptyModelListFiltered {
		t.Fatalf("native exception hid maker filtering: %+v", got)
	}
	profile := input.Resolved["selected"]
	profile.Options = map[string]string{"model": "literal-outside-the-list"}
	enforceOff := false
	profile.EnforceModels = &enforceOff
	input.Resolved["selected"] = profile
	if got := EmptyModelLists(input); len(got) != 1 || got[0].Reason != EmptyModelListFiltered {
		t.Fatalf("literal model and enforcement-off changed the list-only diagnostic: %+v", got)
	}
	viaProfile := input.Resolved["selected"]
	viaProfile.Via, viaProfile.ViaBase = "synthetic-route", "https://route.invalid"
	input.Resolved["selected"] = viaProfile
	if got := EmptyModelLists(input); len(got) != 0 {
		t.Fatalf("the via path did not use the unrestricted fallback rule: %+v", got)
	}
}

func TestEmptyModelListsMergesObjectFactsAndDropsRepointedVendor(t *testing.T) {
	check := `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`
	p := emptyCheckPack(t, check, `"models":{}`, "", "")
	for _, tc := range []struct {
		name, vendor string
		want         EmptyModelListReason
	}{
		{"object alias with blocked maker", "blocked", EmptyModelListFiltered},
		{"object alias with allowed maker", "allowed", ""},
		{"object alias without maker", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := jsonx.NewOrderedMap()
			facts.Set("id", "object-model")
			if tc.vendor != "" {
				facts.Set("vendor", tc.vendor)
			}
			models := jsonx.NewOrderedMap()
			models.Set("object-alias", facts)
			input := emptyCheckInputWithUser(t, p, userModelsEntry("target", models))
			got := EmptyModelLists(input)
			if tc.want == "" && len(got) != 0 || tc.want != "" && (len(got) != 1 || got[0].Reason != tc.want) {
				t.Errorf("object-form alias result = %+v, want reason %q", got, tc.want)
			}
		})
	}

	// Changing the id behind an existing alias invalidates its old vendor fact during
	// composition; the final row has no maker and remains callable.
	repointed := emptyCheckPack(t, check,
		`"models":{"alias":"original-id"},"model_options":{"alias":{"vendor":"blocked"}}`, "", "")
	replacement := jsonx.NewOrderedMap()
	replacement.Set("alias", "replacement-id")
	if got := EmptyModelLists(emptyCheckInputWithUser(t, repointed, userModelsEntry("target", replacement))); len(got) != 0 {
		t.Fatalf("repointed alias retained stale maker facts: %+v", got)
	}
}

func TestEmptyModelListsSkipsUnknownAndUnreachableProfiles(t *testing.T) {
	p := emptyCheckPack(t, `{}`, `"models":{}`, "", "")
	input := emptyCheckInput(t, p)
	input.Profiles["synthetic"] = "unknown"
	input.Sets["synthetic"] = []string{"unknown"}
	if got := EmptyModelLists(input); len(got) != 0 {
		t.Fatalf("unknown profile was reported as an empty list: %+v", got)
	}

	unpaired := emptyCheckInput(t, p)
	entry := providerEntry(unpaired.Providers, "target")
	endpoints := jsonx.NewOrderedMap()
	endpoints.Set("anthropic", mustOrdered(t, `{"base_url":"https://fixture.invalid/v1"}`))
	entry.Set("endpoints", endpoints)
	if got := EmptyModelLists(unpaired); len(got) != 0 {
		t.Fatalf("unusable protocol profile was reported as an empty list: %+v", got)
	}

	// With no live via/carrier, the profile has no route and must not be reported just because
	// the provider's list is supplied. The program owner is intentionally separate from the
	// provider pack and neither declares a native client for this platform.
	owner := &Pack{Name: "program-owner", Decl: declFrom(t, `{"model_list_check":{"synthetic":{}},"contributes":[`+
		`{"kind":"program","bin":"synthetic","via":"npm","package":"example/synthetic","protocols":["openai"]},`+
		`{"kind":"profile","name":"selected","provider":"target"}]}`)}
	providerPack := &Pack{Name: "provider-pack", Decl: declFrom(t, `{"contributes":[`+
		`{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}},"models":{}}]}`)}
	packs := []*Pack{owner, providerPack}
	presence := map[string]bool{}
	providers, err := ComposeProviders(nil, packs, WithModelListPresence(func(name string, supplied bool) {
		presence[name] = supplied
	}))
	if err != nil {
		t.Fatal(err)
	}
	endpoint := jsonx.NewOrderedMap()
	endpoint.Set("base_url", "https://fixture.invalid/v1")
	endpoint.Set(ForViaKey, "wire-bridge")
	viaEndpoints := jsonx.NewOrderedMap()
	viaEndpoints.Set("openai", endpoint)
	providerEntry(providers, "target").Set("endpoints", viaEndpoints)
	withoutVia := EmptyModelListInput{Packs: packs, Providers: providers, Presence: presence,
		Profiles: map[string]string{"synthetic": "selected"}, Sets: map[string][]string{"synthetic": {"selected"}},
		Resolved: map[string]ResolvedProfile{"selected": {Provider: "target"}}}
	if got := EmptyModelLists(withoutVia); len(got) != 0 {
		t.Fatalf("profile with no live via/carrier or native client was reported as empty: %+v", got)
	}

	removed := emptyCheckInputWithUser(t, p, mustOrdered(t, `{"target":null}`))
	if got := EmptyModelLists(removed); len(got) != 0 {
		t.Fatalf("removed provider was reported as an empty list: %+v", got)
	}
}

func TestEmptyModelListsUsesFirstReachableProfileAndDeduplicatesProviders(t *testing.T) {
	p := emptyCheckPack(t, `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`,
		`"models":{},`, ``, `{"kind":"profile","name":"second","provider":"target"},`+
			`{"kind":"provider","name":"later","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}},"models":{}},`+
			`{"kind":"profile","name":"later-profile","provider":"later"}`)
	input := emptyCheckInput(t, p)
	input.Profiles["synthetic"] = "selected"
	input.Sets["synthetic"] = []string{"unresolved", "selected", "second", "later-profile", "selected"}
	input.Resolved["second"] = input.Resolved["selected"]
	got := EmptyModelLists(input)
	if len(got) != 2 || got[0].Profile != "selected" || got[0].Provider != "target" ||
		got[1].Profile != "later-profile" || got[1].Provider != "later" {
		t.Fatalf("first reachable entry should govern each distinct provider once: %+v", got)
	}
}

func TestEmptyModelListsRetainsEmptyAliasAndPreservesFactPrecedence(t *testing.T) {
	p := emptyCheckPack(t, `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`,
		`"models":{}`, "", "")
	for _, tc := range []struct {
		name, userProviders string
		want                EmptyModelListReason
	}{
		{
			name:          "empty alias supplies a vendorless row",
			userProviders: `{"target":{"models":{"":"callable-model"}}}`,
		},
		{
			name: "empty alias wins sorted same-id facts before later aliases",
			userProviders: `{"target":{"models":{"":"shared-model","z-alias":"shared-model"},` +
				`"model_options":{"":{"vendor":"blocked"},"z-alias":{"vendor":"allowed"}}}}`,
			want: EmptyModelListFiltered,
		},
		{
			name: "canonical allowed fact precedes empty alias",
			userProviders: `{"target":{"models":{"":"shared-model","shared-model":"shared-model"},` +
				`"model_options":{"":{"vendor":"blocked"},"shared-model":{"vendor":"allowed"}}}}`,
		},
		{
			name: "canonical blocked fact precedes empty alias",
			userProviders: `{"target":{"models":{"":"shared-model","shared-model":"shared-model"},` +
				`"model_options":{"":{"vendor":"allowed"},"shared-model":{"vendor":"blocked"}}}}`,
			want: EmptyModelListFiltered,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := emptyCheckInputWithUser(t, p, mustOrdered(t, tc.userProviders))
			if !input.Presence["target"] {
				t.Fatal("empty-key model alias must remain a supplied list")
			}
			got := EmptyModelLists(input)
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("EmptyModelLists = %+v, want no warning", got)
				}
				return
			}
			if len(got) != 1 || got[0].Reason != tc.want || got[0].Program != "synthetic" ||
				got[0].Provider != "target" || got[0].Profile != "selected" || got[0].Pack != "synthetic-owner" {
				t.Fatalf("EmptyModelLists = %+v, want one %q row with identity", got, tc.want)
			}
		})
	}
}
