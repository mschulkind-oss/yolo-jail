package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestConfiguredProviderSemanticsConsumesReadableSelectionAtOwnedBoundary(t *testing.T) {
	manifest, problems := packdecl.Decode([]byte(`{"contributes":[
		{"kind":"program","bin":"assistant","via":"npm","package":"assistant","protocols":["openai-responses"],"provider_sets":true},
		{"kind":"program","bin":"codex","via":"npm","package":"codex","protocols":["openai-responses"]},
		{"kind":"program","bin":"fd","via":"npm","package":"fd"}
	]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	cfg := jsonx.NewOrderedMap()
	selection := jsonx.NewOrderedMap()
	selection.Set("default", []any{"bedrock/opus"})
	selection.Set("codex", "native")
	cfg.Set("active_provider", selection)
	declarations := []packdecl.ProviderContribution{{
		Name: "bedrock", Aliases: []string{"br"}, Model: "sonnet",
		Variants: map[string]packdecl.ProviderVariant{"opus": {Model: "opus"}},
	}}

	composition, err := ConfigProviderConsumer(cfg, declarations,
		[]*packload.Pack{{Name: "assistant-pack", Decl: manifest}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if composition.Readable == nil {
		t.Fatal("active_provider did not select the readable consumer path")
	}
	got := *composition.Readable
	selected := got.Selection.ByReceiver["assistant"]
	if len(selected) != 1 || selected[0].Target.Provider != "bedrock" || selected[0].Target.Variant != "opus" || selected[0].Model != "opus" {
		t.Fatalf("readable selector did not reach the declared receiver: %#v", got.Selection)
	}
	if !got.Selection.NativeReceivers["codex"] {
		t.Fatalf("explicit native selection was lost: %#v", got.Selection)
	}
	if _, exists := got.Selection.ByReceiver["fd"]; exists {
		t.Fatalf("provider default widened to an unrelated program: %#v", got.Selection)
	}
}

func TestConfigActiveProviderSelectionOwnsParsedSetsAndReturnsAuthoredPath(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	active := jsonx.NewOrderedMap()
	selectors := []any{"provider/fast", "native"}
	active.Set("default", selectors)
	cfg.Set("active_provider", active)
	parsed, err := ConfigActiveProviderSelection(cfg)
	if err != nil {
		t.Fatal(err)
	}
	selectors[0] = "mutated"
	if parsed.Default[0] != "provider/fast" || parsed.Default[1] != "native" {
		t.Fatalf("selection aliases authored input: %#v", parsed)
	}
	active.Set("default", []any{"provider", 7})
	if _, err := ConfigActiveProviderSelection(cfg); err == nil || !strings.Contains(err.Error(), "config.active_provider.default[1]") {
		t.Fatalf("malformed selector did not preserve its authored path: %v", err)
	}
}

func TestClearNativeProviderEnvUsesOnlyDeclaredVarsAndPreservesOtherValues(t *testing.T) {
	manifest, problems := packdecl.Decode([]byte(`{
		"provider_keys":{"assistant":[
			{"surface":"assistant/settings","pointer":"/env/MANAGED_PROVIDER"},
			{"surface":"assistant/settings","pointer":"/env/BEDROCK_MODEL","platform":"aws-bedrock","matches":"^br\\."},
			{"surface":"assistant/settings","pointer":"/env/USER_MODEL","platform":"aws-bedrock","matches":"^br\\."}
		]},
		"provider_env":{"assistant":{"surface":"assistant/settings","pointer":"/env"}},
		"contributes":[{"kind":"program","bin":"assistant","via":"npm","package":"assistant","protocols":["openai-responses"]}]
	}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	packs := []*packload.Pack{{Name: "assistant-pack", Decl: manifest}}
	cfg := jsonx.NewOrderedMap()
	selection := jsonx.NewOrderedMap()
	selection.Set("assistant", "native")
	cfg.Set("active_provider", selection)
	composition, err := ConfigProviderConsumer(cfg, nil, packs, nil)
	if err != nil || composition.Readable == nil {
		t.Fatalf("native selection failed: composition=%#v err=%v", composition, err)
	}
	environ := []string{
		"MANAGED_PROVIDER=native",
		"BEDROCK_MODEL=br.claude",
		"USER_MODEL=subscription",
		"UNRELATED=keep",
	}
	cleared, removed := ClearNativeProviderEnvForReceiver(*composition.Readable, packs, "assistant", environ, nil)
	if !reflect.DeepEqual(removed, []string{"MANAGED_PROVIDER", "BEDROCK_MODEL"}) {
		t.Fatalf("native selection cleared vars=%v, want unconditional and matching declared vars", removed)
	}
	if !reflect.DeepEqual(cleared, []string{"USER_MODEL=subscription", "UNRELATED=keep"}) {
		t.Fatalf("native clearing changed unrelated/nonmatching environment: %v", cleared)
	}
	other, removed := ClearNativeProviderEnvForReceiver(ProviderSemanticResult{}, packs, "assistant", environ, nil)
	if len(removed) != 0 || !reflect.DeepEqual(other, environ) {
		t.Fatalf("environment cleared without semantic native selection: env=%v removed=%v", other, removed)
	}
}

func TestConfigProviderConsumerPreservesLegacyBoundaryAndRejectsMixedSelectors(t *testing.T) {
	cfg := jsonx.NewOrderedMap()
	cfg.Set(ProfileKey, "legacy-profile")
	got, err := ConfigProviderConsumer(cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Readable != nil || !reflect.DeepEqual(got.Legacy, legacyProfileSelection(cfg)) {
		t.Fatalf("legacy selector behavior changed: got=%#v want=%#v", got, legacyProfileSelection(cfg))
	}
	cfg.Set("active_provider", jsonx.NewOrderedMap())
	if _, err := ConfigProviderConsumer(cfg, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "cannot be composed together") {
		t.Fatalf("mixed selector forms were silently prioritized: %v", err)
	}
}

func TestConfiguredProviderSemanticsLeavesDeclarationsInactiveAndRejectsBadSelectionPath(t *testing.T) {
	declarations := []packdecl.ProviderContribution{{Name: "bedrock"}}
	inactive, err := ComposeConfiguredProviderSemantics(jsonx.NewOrderedMap(), declarations, nil, nil)
	if err != nil || len(inactive.Selection.ByReceiver) != 0 {
		t.Fatalf("declaration inferred a selection: result=%#v err=%v", inactive, err)
	}

	cfg := jsonx.NewOrderedMap()
	active := jsonx.NewOrderedMap()
	active.Set("default", []any{"bedrock", "native"})
	cfg.Set("active_provider", active)
	_, err = ComposeConfiguredProviderSemantics(cfg, declarations, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "active_provider.default") || !strings.Contains(err.Error(), "native") {
		t.Fatalf("bad selection lacks its authored path and reason: %v", err)
	}
}

func TestNormalizeLegacyProviderConsumersUsesCompleteCompositionBoundaries(t *testing.T) {
	legacyEntry := jsonx.NewOrderedMap()
	options := jsonx.NewOrderedMap()
	options.Set("mode", "default")
	options.Set("region", nil)
	legacyEntry.Set("options", options)
	legacyEntry.Set("capabilities", []any{"chat"})
	legacyTable := jsonx.NewOrderedMap()
	legacyTable.Set("p", legacyEntry)
	composed, err := packload.ComposeProviders(legacyTable, nil)
	if err != nil {
		t.Fatal(err)
	}
	falseValue := false
	legacyProfiles := map[string]packload.ResolvedProfile{
		"default": {Provider: "p", Options: map[string]string{"mode": "default"}},
		"fast":    {Provider: "p", Options: map[string]string{"mode": "fast", "region": "west", "model": "fast-model"}, Via: "gateway", ViaBase: "http://gateway", EnforceModels: &falseValue, Subagents: luahookSubagentsFixture()},
	}

	got := NormalizeLegacyProviderConsumers(composed, legacyProfiles, nil)
	if len(got.Differences) != 0 || len(got.Canonical) != 1 || got.ProfileSelectors["default"] != "p/default" || got.ProfileSelectors["fast"] != "p/fast" {
		t.Fatalf("representable legacy consumer set was not fully normalized: %#v", got)
	}
	fast := got.Canonical[0].Variants["fast"]
	if fast.Model != "fast-model" || fast.Via != "gateway" || fast.Options["mode"] != (packdecl.OptionDefault{Defaulted: true, Value: "fast"}) ||
		fast.Options["region"] != (packdecl.OptionDefault{Defaulted: true, Value: "west"}) || fast.EnforceModels == nil || *fast.EnforceModels || fast.Subagents == nil {
		t.Fatalf("profile fields were not represented by the variant: %#v", fast)
	}
	resolved, err := ResolveActiveProviders(got.Canonical, ActiveProviderSelection{Default: []string{"p/fast"}}, []ProviderReceiver{{Bin: "assistant"}})
	if err != nil {
		t.Fatal(err)
	}
	selected := resolved.ByReceiver["assistant"][0]
	if selected.Model != "fast-model" || selected.Via != "gateway" || selected.Options["mode"].Value != "fast" ||
		selected.Options["region"].Value != "west" || selected.EnforceModels == nil || *selected.EnforceModels || selected.Subagents.Default != "cheap" {
		t.Fatalf("canonical consumer result differs from resolved legacy profile: %#v", selected)
	}
	if _, exists := legacyEntry.Get("options"); !exists {
		t.Fatal("normalization mutated its input composition")
	}
	got.Canonical[0].Options["mode"] = packdecl.OptionDefault{Defaulted: true, Value: "mutated"}
	if defaultValue, _ := options.Get("mode"); defaultValue != "default" {
		t.Fatal("canonical output aliases the caller's effective provider table")
	}
}

func TestNormalizeLegacyProviderConsumersMatchesActualShippedOptionModelSemantics(t *testing.T) {
	packsByName := map[string]*packload.Pack{}
	for _, pack := range packload.Embedded() {
		if pack.Name == "zai" || pack.Name == "cerebras" || pack.Name == "llamacpp" || pack.Name == "openrouter" || pack.Name == "kilo" {
			packsByName[pack.Name] = pack
		}
	}
	t.Cleanup(packload.ReleaseEmbedded)
	for _, name := range []string{"zai", "cerebras", "llamacpp", "openrouter", "kilo"} {
		pack := packsByName[name]
		if pack == nil {
			t.Fatalf("shipped legacy %s pack missing", name)
		}
		providers, err := packload.ComposeProviders(nil, []*packload.Pack{pack})
		if err != nil {
			t.Fatal(err)
		}
		profiles, err := resolveShippedProfiles([]*packload.Pack{pack}, map[string]packload.UserProfile{
			"custom": {Provider: name, Options: map[string]string{"model": "user-choice"}},
		}, providers)
		if err != nil {
			t.Fatal(err)
		}
		legacy, exists := profiles[name]
		if !exists {
			t.Fatalf("shipped legacy profile %q was not resolved", name)
		}
		wantModel := map[string]string{"zai": "glm-5.3", "cerebras": "default", "llamacpp": "default"}[name]
		if wantModel != "" && legacy.Options["model"] != wantModel {
			t.Errorf("%s legacy model default = %q, want %q", name, legacy.Options["model"], wantModel)
		}
		if wantModel == "" {
			if _, exists := legacy.Options["model"]; exists {
				t.Errorf("%s defaultless model option unexpectedly selected a model: %#v", name, legacy.Options)
			}
		}
		normalized := NormalizeLegacyProviderConsumers(providers, profiles, nil)
		switch name {
		case "openrouter", "kilo":
			if len(normalized.Differences) != 0 || len(normalized.Canonical) != 1 || normalized.ProfileSelectors[name] != name {
				t.Errorf("%s defaultless model declaration did not normalize exactly: %#v", name, normalized)
				continue
			}
			modelOption, declared := normalized.Canonical[0].Options["model"]
			if !declared || modelOption.Defaulted {
				t.Errorf("%s model's defaultless declaration was lost: %#v", name, normalized.Canonical[0].Options)
			}
			if normalized.ProfileSelectors["custom"] != name+"/custom" {
				t.Errorf("%s custom model selection identity was not preserved: %#v", name, normalized.ProfileSelectors)
			}
			selected, err := ResolveActiveProviders(normalized.Canonical, ActiveProviderSelection{Default: []string{normalized.ProfileSelectors["custom"]}}, []ProviderReceiver{{Bin: "pi"}})
			if err != nil {
				t.Fatal(err)
			}
			compat := selected.ByReceiver["pi"][0].LegacyCompatibility().Profile
			if !reflect.DeepEqual(compat.Options, profiles["custom"].Options) || compat.Options["model"] != "user-choice" {
				t.Errorf("%s explicit model selection compatibility differs: legacy=%#v canonical=%#v", name, profiles["custom"].Options, compat.Options)
			}
		default:
			if normalized.Canonical != nil || len(normalized.ProfileSelectors) != 0 || len(normalized.Differences) == 0 {
				t.Errorf("%s model aliases/facts requiring unsupported inference were partially accepted: %#v", name, normalized)
				continue
			}
			knownDifference := map[string]string{
				"zai":      "providers.zai.model_options.glm-4.6.price",
				"cerebras": "providers.cerebras.models.default",
				"llamacpp": "providers.llamacpp.models.default",
			}[name]
			found := false
			for _, difference := range normalized.Differences {
				if difference.Path == knownDifference && difference.Reason != "" {
					found = true
				}
			}
			if !found {
				t.Errorf("%s missing precise known legacy-model difference %q: %#v", name, knownDifference, normalized.Differences)
			}
		}
	}
}

func TestNormalizeLegacyProviderConsumersMatchesComposerAndResolverWithoutLosingProfileIdentity(t *testing.T) {
	manifest, problems := packdecl.Decode([]byte(`{"contributes":[{"kind":"provider","name":"local-provider","options":{"model":"base","mode":"default"}}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	var bridge *packload.Pack
	for _, pack := range packload.Embedded() {
		if pack.Name == "wire-bridge" {
			bridge = pack
			break
		}
	}
	if bridge == nil {
		t.Fatal("wire-bridge service pack missing")
	}
	t.Cleanup(packload.ReleaseEmbedded)
	packs := []*packload.Pack{{Name: "local-provider", Decl: manifest}, bridge}
	falseValue := false
	userProfiles := map[string]packload.UserProfile{
		"plain-a": {Provider: "local-provider", Options: map[string]string{"model": "base", "mode": "default"}},
		"plain-b": {Provider: "local-provider", Options: map[string]string{"model": "base", "mode": "default"}},
		"through": {Provider: "local-provider", Options: map[string]string{"model": "custom", "mode": "fast"}, Via: "wire-bridge", EnforceModels: &falseValue, Subagents: luahookSubagentsFixture()},
	}
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	legacyProfiles, err := resolveShippedProfiles(packs, userProfiles, providers)
	if err != nil {
		t.Fatal(err)
	}
	normalized := NormalizeLegacyProviderConsumers(providers, legacyProfiles, nil)
	if len(normalized.Differences) != 0 || len(normalized.Canonical) != 1 ||
		normalized.ProfileSelectors["plain-a"] != "local-provider/plain-a" ||
		normalized.ProfileSelectors["plain-b"] != "local-provider/plain-b" ||
		normalized.ProfileSelectors["through"] != "local-provider/through" {
		t.Fatalf("legacy profiles did not normalize completely and distinctly: %#v", normalized)
	}
	for name, selector := range normalized.ProfileSelectors {
		selected, err := ResolveActiveProviders(normalized.Canonical, ActiveProviderSelection{Default: []string{selector}}, []ProviderReceiver{{Bin: "pi"}})
		if err != nil {
			t.Fatal(err)
		}
		compat := selected.ByReceiver["pi"][0].LegacyCompatibility().Profile
		legacy := legacyProfiles[name]
		if compat.Provider != legacy.Provider || !reflect.DeepEqual(compat.Options, legacy.Options) || compat.Via != legacy.Via ||
			!reflect.DeepEqual(compat.Subagents, legacy.Subagents) || !reflect.DeepEqual(compat.EnforceModels, legacy.EnforceModels) {
			t.Errorf("%s legacy and canonical profiles differ: legacy=%#v canonical=%#v", name, legacy, compat)
		}
		if legacy.Via != "" {
			base, found := packload.ViaServiceAddress(packs, compat.Via)
			if !found || base != legacy.ViaBase {
				t.Errorf("%s via address changed: legacy=%q canonical=%q found=%t", name, legacy.ViaBase, base, found)
			}
		}
	}
}

func TestNormalizeLegacyProviderConsumersReportsExactUnsupportedFieldsWithoutPartialAcceptance(t *testing.T) {
	profiles := map[string]packload.ResolvedProfile{
		"arbitrary": {Provider: "p", Options: map[string]string{"unlisted": "value"}},
		"carried":   {Provider: "p", Carrier: "bridge", CarrierBase: "http://bridge", Carried: []string{"assistant"}},
	}
	legacy := jsonx.NewOrderedMap()
	legacy.Set("p", jsonx.NewOrderedMap())
	composed, err := packload.ComposeProviders(legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := NormalizeLegacyProviderConsumers(composed, profiles, nil)
	if got.Canonical != nil || len(got.ProfileSelectors) != 0 {
		t.Fatalf("partial canonical conversion escaped: %#v", got)
	}
	want := map[string]bool{
		"profiles.arbitrary.options.unlisted": false,
		"profiles.carried.carrier":            false,
		"profiles.carried.carrier_base":       false,
		"profiles.carried.carried":            false,
	}
	for _, difference := range got.Differences {
		if _, exists := want[difference.Path]; exists && difference.Reason != "" {
			want[difference.Path] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("missing precise unsupported difference %q: %#v", path, got.Differences)
		}
	}
}

func TestNormalizeLegacyProviderConsumersRejectsModelOmissionAndUnmappedEffectiveFields(t *testing.T) {
	legacy := jsonx.NewOrderedMap()
	entry := jsonx.NewOrderedMap()
	options := jsonx.NewOrderedMap()
	options.Set("model", "base")
	entry.Set("options", options)
	entry.Set("model_shared", func() *jsonx.OrderedMap {
		shared := jsonx.NewOrderedMap()
		shared.Set("group", jsonx.NewOrderedMap())
		return shared
	}())
	entry.Set("models_only", true)
	legacy.Set("p", entry)
	composed, err := packload.ComposeProviders(legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	composedEntry, _ := composed.Get("p")
	composedProvider := composedEntry.(*jsonx.OrderedMap)
	composedProvider.Set("models_only", true)
	got := NormalizeLegacyProviderConsumers(composed, map[string]packload.ResolvedProfile{
		"no-model": {Provider: "p"},
	}, nil)
	if got.Canonical != nil {
		t.Fatalf("unmapped effective provider fields or omitted profile model were accepted: %#v", got)
	}
	paths := map[string]bool{}
	for _, difference := range got.Differences {
		paths[difference.Path] = true
	}
	for _, path := range []string{"providers.p.model_shared", "providers.p.models_only", "profiles.no-model.options.model"} {
		if !paths[path] {
			t.Errorf("missing unsupported difference %s: %#v", path, got.Differences)
		}
	}
}

func TestNormalizeLegacyProviderConsumersCarriesOpenRouterRoutingIntoCanonicalRows(t *testing.T) {
	legacy := jsonx.NewOrderedMap()
	provider := jsonx.NewOrderedMap()
	models := jsonx.NewOrderedMap()
	models.Set("m", "m")
	provider.Set("models", models)
	modelOptions := jsonx.NewOrderedMap()
	modelFacts := jsonx.NewOrderedMap()
	modelFacts.Set("vendor", "openai")
	modelFacts.Set("cost_input", "0.1")
	modelFacts.Set("cost_output", "0.2")
	modelFacts.Set("cost_cache_read", "0.03")
	modelFacts.Set("cost_cache_write", "0.04")
	modelOptions.Set("m", modelFacts)
	provider.Set("model_options", modelOptions)
	routingRows := jsonx.NewOrderedMap()
	routing := jsonx.NewOrderedMap()
	routing.Set("order", "openai")
	routing.Set("allow_fallbacks", false)
	routingRows.Set("m", routing)
	provider.Set("model_routing", routingRows)
	legacy.Set("p", provider)
	composed, err := packload.ComposeProviders(legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := NormalizeLegacyProviderConsumers(composed, nil, nil)
	if len(result.Differences) != 0 || len(result.Canonical) != 1 {
		t.Fatalf("representable OpenRouter routing was rejected: %#v", result)
	}
	modelsResult, err := ComposeProviderModels(result.Canonical[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := ProjectProviderModelsForLegacyDerives(modelsResult)
	if err != nil {
		t.Fatal(err)
	}
	projectedRouting, ok := projection.ProviderEntry.Get("model_routing")
	if !ok || !reflect.DeepEqual(jsonx.Plain(projectedRouting), jsonx.Plain(routingRows)) {
		t.Fatalf("OpenRouter model routing did not survive canonical projection: %#v", projection.ProviderEntry)
	}
}

func luahookSubagentsFixture() luahook.Subagents {
	return luahook.Subagents{Default: "cheap", Roles: map[string]string{"worker": "cheap"}}
}
