package config

// activeprovider_test.go pins the one selection namespace a launch reads
// (providersemantic_selection.go; docs/design/config-user.md §5, readable-config.md OQ-RC3,
// OQ-RC14, OQ-RC20, RC-D4, RC-D11): `active_provider` and the legacy `profile` key lower to one
// ProfileSelection, "native" leaves an agent to its own login, and a provider, its alias and its
// variants are selectable by name through the same packload.ResolveProfiles every reader calls.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// activeProviderPacks is two agent packs (assistant holds provider sets, helper does not) and one
// provider pack shipping `zai` with a same-named profile and `bedrock` with none, so a test can
// tell a profile name, a bare provider name and a variant apart.
func activeProviderPacks(t *testing.T) []*packload.Pack {
	t.Helper()
	decode := func(body string) *packdecl.Manifest {
		m, problems := packdecl.Decode([]byte(body))
		if len(problems) != 0 {
			t.Fatalf("fixture manifest: %v", problems)
		}
		return m
	}
	return []*packload.Pack{
		{Name: "assistant-pack", Decl: decode(`{"contributes":[
			{"kind":"program","bin":"assistant","via":"npm","package":"assistant","protocols":["openai-responses"],"provider_sets":true}]}`)},
		{Name: "helper-pack", Decl: decode(`{"contributes":[
			{"kind":"program","bin":"helper","via":"npm","package":"helper","protocols":["openai-responses"]}]}`)},
		{Name: "providers-pack", Decl: decode(`{"contributes":[
			{"kind":"provider","name":"zai","platform":"zai"},
			{"kind":"profile","name":"zai","provider":"zai"},
			{"kind":"provider","name":"bedrock","platform":"aws-bedrock"}]}`)},
	}
}

// foldOf is the table a launch with no -p folds cfg's selection to over packs.
func foldOf(cfg *jsonx.OrderedMap, packs []*packload.Pack) map[string]any {
	return tableOf(FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(cfg)).Table)
}

// `default` reaches every CLI the selected packs install and nothing else: not a CLI named only
// in the key that no pack installs (checkProfileTargets refuses that by name), and not the command
// after `--`, which is no receiver. A named entry wins over default for its agent (RC-D4).
func TestActiveProviderDefaultReachesDeclaredReceiversOnly(t *testing.T) {
	packs := activeProviderPacks(t)
	cfg := decode(t, `{"active_provider": {"default": "zai", "helper": "bedrock"}}`)
	got := foldOf(cfg, packs)
	want := map[string]any{"assistant": "zai", "helper": "bedrock"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("active_provider folded to %v, want %v", got, want)
	}
	// Only the assistant pack selected: helper is no receiver, so default does not reach it.
	if got := foldOf(decode(t, `{"active_provider": {"default": "zai"}}`), packs[:1]); !reflect.DeepEqual(got,
		map[string]any{"assistant": "zai"}) {
		t.Fatalf("default reached a CLI no selected pack installs: %v", got)
	}
}

// "native" names the agent and selects no profile, so no provider row is resolved for it, and the
// provider variables its pack declares are cleared exactly as ClearNativeProviderEnvForReceiver
// clears them: an agent with no selection takes the launch's PP-D14 clearing, which is the same
// ProviderEnvFor list with no platform.
func TestNativeClearsProviderEnvAndWritesNoRows(t *testing.T) {
	packs := activeProviderPacks(t)
	got := foldOf(decode(t, `{"active_provider": {"default": "zai", "assistant": "native"}}`), packs)
	if v, named := got["assistant"]; !named || v != nil {
		t.Fatalf("native did not name assistant with no profile: %v", got)
	}
	if got["helper"] != "zai" {
		t.Fatalf("native for assistant took helper's default too: %v", got)
	}
	if got := foldOf(decode(t, `{"active_provider": {"default": "native"}}`), packs); !reflect.DeepEqual(got,
		map[string]any{"assistant": nil, "helper": nil}) {
		t.Fatalf("default native must leave every agent to its own login: %v", got)
	}
	// -p native is the same word on the command line, over the key.
	flag := ProfileSelection{Named: map[string][]string{"helper": {NativeProvider}}}
	table := FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(decode(t, `{"active_provider": {"default": "zai"}}`)), flag).Table
	if v, _ := table.Get("helper"); v != nil {
		t.Fatalf("-p helper=native still selects %v", v)
	}
	if rows := packload.ProfileTable(table); rows["helper"] != "" {
		t.Fatalf("native wrote a profile row for helper: %v", rows)
	}

	manifest, problems := packdecl.Decode([]byte(`{
		"provider_env":{"assistant":{"surface":"assistant/settings","pointer":"/env"}},
		"provider_keys":{"assistant":[{"surface":"assistant/settings","pointer":"/env/MANAGED_PROVIDER"}]},
		"contributes":[{"kind":"program","bin":"assistant","via":"npm","package":"assistant","protocols":["openai-responses"]}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	envPacks := []*packload.Pack{{Name: "assistant-pack", Decl: manifest}}
	environ := []string{"MANAGED_PROVIDER=x", "UNRELATED=keep"}
	launch, launchCleared := packload.ClearProviderEnv(environ, packload.ProviderEnvFor(envPacks, "assistant", ""), nil)
	semantic, err := ComposeConfiguredProviderSemantics(decode(t, `{"active_provider": {"assistant": "native"}}`), nil, envPacks, nil)
	if err != nil {
		t.Fatal(err)
	}
	native, nativeCleared := ClearNativeProviderEnvForReceiver(semantic, envPacks, "assistant", environ, nil)
	if !reflect.DeepEqual(launch, []string{"UNRELATED=keep"}) || !reflect.DeepEqual(launch, native) ||
		!reflect.DeepEqual(launchCleared, nativeCleared) {
		t.Fatalf("the launch's clearing for a native agent (%v, %v) is not native's (%v, %v)",
			launch, launchCleared, native, nativeCleared)
	}
}

// OQ-RC20: "native" is reserved, so a provider or alias named it is fatal, and the refusal says
// what to do.
func TestProviderNamedNativeIsFatal(t *testing.T) {
	packs := activeProviderPacks(t)
	for name, body := range map[string]string{
		"provider": `{"active_provider": {}, "providers": {"native": {"platform": "x"}}}`,
		"alias":    `{"active_provider": {}, "providers": {"bedrock": {"alias": "native"}}}`,
	} {
		_, err := SelectableProfiles(nil, decode(t, body), packs)
		if err == nil || !strings.Contains(err.Error(), "reserved") || !strings.Contains(err.Error(), "Rename") {
			t.Errorf("%s named native: err = %v, want the reserved-word refusal naming the fix", name, err)
		}
	}
	if _, err := LoadSelectableProfiles(nil, decode(t, `{"active_provider": {}, "providers": {"native": {"platform": "x"}}}`), packs); err == nil {
		t.Error("the launch's loader accepted a provider named native")
	}
}

// RC-D19's dual-reader window: a JSONC user config's `profile` key still selects, through the one
// reader, in every form; `active_provider` is read instead when the config has it.
func TestLegacyProfileKeyStillSelectsFromJSONC(t *testing.T) {
	h := newMountsHost(t)
	h.user(t, `{"profile": {"*": "zai", "helper": "bedrock", "assistant": null}}`)
	sel := ConfigActiveSelection(UserScopeConfigOrEmpty())
	if sel.Key != "" || !reflect.DeepEqual(sel.Default, []string{"zai"}) ||
		!reflect.DeepEqual(sel.Named, map[string][]string{"helper": {"bedrock"}, "assistant": nil}) {
		t.Fatalf("the profile key lowered to %+v", sel)
	}
	if got := foldOf(UserScopeConfigOrEmpty(), activeProviderPacks(t)); !reflect.DeepEqual(got,
		map[string]any{"assistant": nil, "helper": "bedrock"}) {
		t.Fatalf("the profile key folded to %v", got)
	}

	p := filepath.Join(h.home, ".config", "yolo-jail", paths.UserConfigTOMLName)
	if err := os.Remove(filepath.Join(h.home, ".config", "yolo-jail", "config.jsonc")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("[active_provider]\ndefault = \"zai\"\nhelper = \"native\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sel = ConfigActiveSelection(UserScopeConfigOrEmpty())
	if sel.Key != ActiveProviderKey || !reflect.DeepEqual(sel.Default, []string{"zai"}) {
		t.Fatalf("a TOML active_provider lowered to %+v", sel)
	}
	if got := foldOf(UserScopeConfigOrEmpty(), activeProviderPacks(t)); !reflect.DeepEqual(got,
		map[string]any{"assistant": "zai", "helper": nil}) {
		t.Fatalf("the TOML active_provider folded to %v", got)
	}
}

// OQ-RC3, OQ-RC14: a provider is selectable by its own name and starts on its model, a variant as
// provider/variant, and an alias names its provider; each resolves through the resolution every
// reader calls. A profile already holding a name keeps it.
func TestActiveProviderSelectsProviderVariantAndAlias(t *testing.T) {
	packs := activeProviderPacks(t)
	cfg := decode(t, `{
	  "providers": {"bedrock": {"alias": "br", "model": "sonnet",
	    "variants": {"opus": {"model": "opus", "via": "wire-bridge"}}}},
	  "active_provider": {"default": "br", "assistant": ["bedrock/opus", "zai"]}}`)
	profiles, err := SelectableProfiles(map[string]packload.UserProfile{}, cfg, packs)
	if err != nil {
		t.Fatal(err)
	}
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveShippedProfiles(packs, profiles, providers)
	if err != nil {
		t.Fatal(err)
	}
	fold := FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(cfg))
	if problems := UndeclaredProfileProblems(fold, 0, profiles, nil, packs, nil); len(problems) != 0 {
		t.Fatalf("a provider, alias or variant name was refused as undeclared: %v", problems)
	}
	sets := packload.ProfileSets(fold.Table)
	if !reflect.DeepEqual(sets["assistant"], []string{"bedrock/opus", "zai"}) || !reflect.DeepEqual(sets["helper"], []string{"br"}) {
		t.Fatalf("selection folded to %v", sets)
	}
	for name, want := range map[string]packload.ResolvedProfile{
		"br":           {Provider: "bedrock", Options: map[string]string{"model": "sonnet"}},
		"bedrock":      {Provider: "bedrock", Options: map[string]string{"model": "sonnet"}},
		"bedrock/opus": {Provider: "bedrock", Options: map[string]string{"model": "opus"}, Via: "wire-bridge"},
		"zai":          {Provider: "zai", Options: map[string]string{}},
	} {
		got := resolved[name]
		if got.Provider != want.Provider || got.Via != want.Via || !reflect.DeepEqual(got.Options, want.Options) {
			t.Errorf("%s resolved to %+v, want %+v", name, got, want)
		}
	}
	// A name no profile, provider, alias or variant holds is still refused, and the refusal lists
	// the implied names among the declared ones.
	bad := FoldProfiles(ReceiversOf(packs), ConfigActiveSelection(decode(t, `{"active_provider": {"default": "bedrok"}}`)))
	problems := UndeclaredProfileProblems(bad, 0, profiles, nil, packs, nil)
	if len(problems) == 0 || !strings.Contains(problems[0], "bedrock/opus") {
		t.Fatalf("an unknown name was not refused naming the declared ones: %v", problems)
	}
}

// RC-D11: a declaration never selects. Declaring providers and variants adds names a selection
// may use, and with no selection the fold is empty.
func TestDeclarationAloneSelectsNothing(t *testing.T) {
	packs := activeProviderPacks(t)
	cfg := decode(t, `{"providers": {"bedrock": {"model": "sonnet", "variants": {"opus": {"model": "opus"}}}}}`)
	if got := foldOf(cfg, packs); len(got) != 0 {
		t.Fatalf("declarations selected %v", got)
	}
	if !ConfigActiveSelection(cfg).IsZero() {
		t.Fatal("declarations lowered to a selection")
	}
	if got := foldOf(decode(t, `{"active_provider": {}}`), packs); len(got) != 0 {
		t.Fatalf("an empty active_provider selected %v", got)
	}
}

// W1-V C7: a selected provider's model facts come from its rows over the offline catalog, and a
// fact neither states is absent from what its receiver is handed, never a zero: no catalog row
// means no context_window or max_tokens, and a catalog row fills only what it has.
func TestLaunchModelFactsInheritCatalogWithoutInventedZeros(t *testing.T) {
	packs := activeProviderPacks(t)
	declarations := []packdecl.ProviderContribution{{
		Name:       "bedrock",
		ModelRows:  map[string]packdecl.ProviderModel{"claude-known": {Vendor: "anthropic", Price: completePrice(1, 2, .1, .2)}, "claude-unknown": {Vendor: "anthropic", Price: completePrice(1, 2, .1, .2)}},
		ModelOrder: []string{"claude-known", "claude-unknown"},
	}}
	cfg := decode(t, `{"active_provider": {"default": "bedrock"}}`)
	for name, catalog := range map[string]map[string]map[string]packdecl.ProviderModel{
		"a catalog row for one model": {"bedrock": {"claude-known": {Context: 200000, MaxTokens: 8192}}},
		"no catalog at all":           {},
	} {
		semantic, err := ComposeConfiguredProviderSemantics(cfg, declarations, packs, catalog)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := semantic.Selection.ByReceiver["assistant"]; len(got) != 1 || got[0].Provider != "bedrock" {
			t.Fatalf("%s: assistant selected %+v", name, got)
		}
		entry := jsonx.PlainMap(semantic.LegacyDerives["bedrock"].ProviderEntry)
		options, _ := entry["model_options"].(map[string]any)
		for id, row := range options {
			facts, _ := row.(map[string]any)
			for _, fact := range []string{"context_window", "max_tokens"} {
				if v, present := facts[fact]; present && (v == "0" || v == "") {
					t.Errorf("%s: %s.%s was invented as %q", name, id, fact, v)
				}
			}
			if id == "claude-unknown" {
				if _, present := facts["context_window"]; present {
					t.Errorf("%s: a model no catalog row describes was given a context window: %v", name, facts)
				}
			}
		}
		known, _ := options["claude-known"].(map[string]any)
		wantContext := name == "a catalog row for one model"
		if _, present := known["context_window"]; present != wantContext {
			t.Errorf("%s: known's context window present=%v, want %v (%v)", name, present, wantContext, known)
		}
		if wantContext && (known["context_window"] != "200000" || known["max_tokens"] != "8192") {
			t.Errorf("%s: the catalog's facts did not reach the receiver: %v", name, known)
		}
	}
}
