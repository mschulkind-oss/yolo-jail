package config

import (
	"math"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestResolveActiveProvidersLeavesDeclarationsInactiveAndLimitsDefaultToReceivers(t *testing.T) {
	providers := []packdecl.ProviderContribution{
		{Name: "bedrock", Model: "sonnet", Options: map[string]packdecl.OptionDefault{"region": {Defaulted: true, Value: "us-east-1"}, "thinking": {}}, Variants: map[string]packdecl.ProviderVariant{
			"opus": {Model: "opus", Via: "wire-bridge", Options: map[string]packdecl.OptionDefault{"thinking": {}},
				Subagents:    map[string]any{"default": "cheap", "roles": map[string]any{"worker": "cheap"}},
				Capabilities: []string{"messages"}, Protocols: []string{"anthropic"}},
		}},
		{Name: "openai-codex", Aliases: []string{"codex"}, Model: "gpt-6"},
	}
	inactive, err := ResolveActiveProviders(providers, ActiveProviderSelection{}, []ProviderReceiver{{Bin: "claude"}, {Bin: "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(inactive.ByReceiver) != 0 {
		t.Fatalf("declarations selected by themselves: %#v", inactive.ByReceiver)
	}
	selected, err := ResolveActiveProviders(providers, ActiveProviderSelection{
		Default: []string{"bedrock"}, Named: map[string][]string{"claude": {"bedrock/opus"}, "codex": {"native"}},
	}, []ProviderReceiver{{Bin: "claude"}, {Bin: "pi"}, {Bin: "codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.ByReceiver) != 2 || selected.ByReceiver["claude"][0].Via != "wire-bridge" ||
		selected.ByReceiver["claude"][0].Capabilities[0] != "messages" || selected.ByReceiver["claude"][0].Protocols[0] != "anthropic" ||
		selected.ByReceiver["claude"][0].Options["region"].Value != "us-east-1" || selected.ByReceiver["claude"][0].Options["thinking"].Defaulted {
		t.Fatalf("variant fields not retained: %#v", selected.ByReceiver)
	}
	if selected.ByReceiver["claude"][0].Subagents.Default != "cheap" || selected.ByReceiver["claude"][0].Subagents.Roles["worker"] != "cheap" {
		t.Fatalf("typed subagent contract lost: %#v", selected.ByReceiver["claude"][0].Subagents)
	}
	legacy := selected.ByReceiver["claude"][0].LegacyCompatibility()
	if legacy.Profile.Provider != "bedrock" || legacy.Profile.Options["model"] != "opus" || legacy.Profile.Options["region"] != "us-east-1" || legacy.Profile.Via != "wire-bridge" ||
		legacy.Profile.Subagents.Default != "cheap" || legacy.Capabilities[0] != "messages" || legacy.Protocols[0] != "anthropic" {
		t.Fatalf("legacy profile/wire compatibility projection lost typed fields: %#v", legacy)
	}
	if legacy.Profile.Options["thinking"] != "" {
		t.Fatal("defaultless options must remain declared without inventing a value")
	}
	if len(selected.ByReceiver["pi"]) != 1 || selected.ByReceiver["pi"][0].Provider != "bedrock" {
		t.Fatalf("default did not reach receiver: %#v", selected.ByReceiver)
	}
	if _, exists := selected.ByReceiver["codex"]; exists {
		t.Fatal("native selection was incorrectly emitted as a provider")
	}
	if !selected.ByReceiver["claude"][0].Target.IsVariant() || !selected.NativeReceivers["codex"] {
		t.Fatalf("target identities lost: %#v", selected)
	}
}

func TestProviderReceiversComeFromExplicitPackContractsNotEveryInstalledBin(t *testing.T) {
	manifest, problems := packdecl.Decode([]byte(`{"contributes":[
		{"kind":"program","bin":"assistant","via":"npm","package":"assistant","protocols":["anthropic"],"provider_sets":true},
		{"kind":"program","bin":"fd","via":"npm","package":"fd"}
	]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	receivers := ProviderReceivers([]*packload.Pack{{Name: "fixture", Decl: manifest}})
	if len(receivers) != 1 || receivers[0].Bin != "assistant" || !receivers[0].HoldsProviderSets {
		t.Fatalf("provider-capable receivers = %v; utility program must not receive active_provider.default", receivers)
	}
	selection, err := ResolveActiveProviders([]packdecl.ProviderContribution{{Name: "bedrock"}},
		ActiveProviderSelection{Default: []string{"bedrock"}}, receivers)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := selection.ByReceiver["fd"]; exists {
		t.Fatal("default selected an unrelated installed program")
	}
}

func TestVariantOptionsUseProviderDeclaredNamesAndPreserveLiteralStrings(t *testing.T) {
	declarations := []packdecl.ProviderContribution{{Name: "p", Options: map[string]packdecl.OptionDefault{"mode": {}}, Variants: map[string]packdecl.ProviderVariant{
		"fast": {Options: map[string]packdecl.OptionDefault{"mode": {Defaulted: true, Value: "off"}}},
	}}}
	selected, err := ResolveActiveProviders(declarations, ActiveProviderSelection{Default: []string{"p/fast"}}, []ProviderReceiver{{Bin: "assistant"}})
	if err != nil {
		t.Fatal(err)
	}
	if option := selected.ByReceiver["assistant"][0].Options["mode"]; !option.Defaulted || option.Value != "off" {
		t.Fatalf("literal option value was interpreted as a sentinel: %#v", option)
	}
	declarations[0].Variants["fast"] = packdecl.ProviderVariant{Options: map[string]packdecl.OptionDefault{"invented": {Defaulted: true, Value: "x"}}}
	if _, err := ResolveActiveProviders(declarations, ActiveProviderSelection{Default: []string{"p/fast"}}, []ProviderReceiver{{Bin: "assistant"}}); err == nil || !strings.Contains(err.Error(), "invented") {
		t.Fatalf("variant option outside provider's option census must refuse: %v", err)
	}
}

func TestProviderSelectionPreservesProviderSetNarrowingAndRefusal(t *testing.T) {
	providers := []packdecl.ProviderContribution{{Name: "p"}, {Name: "q"}}
	got, err := ResolveActiveProviders(providers, ActiveProviderSelection{Default: []string{"p", "q"}}, []ProviderReceiver{
		{Bin: "single"}, {Bin: "multi", HoldsProviderSets: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ByReceiver["single"]) != 1 || got.ByReceiver["single"][0].Provider != "p" || len(got.ByReceiver["multi"]) != 2 ||
		len(got.NarrowedReceivers) != 1 || got.NarrowedReceivers[0] != "single" || len(got.WholeReceivers) != 1 || got.WholeReceivers[0] != "multi" {
		t.Fatalf("default list projection = %#v", got)
	}
	_, err = ResolveActiveProviders(providers, ActiveProviderSelection{Named: map[string][]string{"single": {"p", "q"}}}, []ProviderReceiver{{Bin: "single"}})
	if err == nil || !strings.Contains(err.Error(), "provider_sets") {
		t.Fatalf("explicit multi-select without capability must refuse: %v", err)
	}
	if _, err := ResolveActiveProviders(providers, ActiveProviderSelection{Named: map[string][]string{"multi": {"p", "q"}}}, []ProviderReceiver{{Bin: "multi", HoldsProviderSets: true}}); err != nil {
		t.Fatalf("declared provider set receiver must accept explicit multiple choices: %v", err)
	}
}

func TestParseActiveProviderFlagUsesTheProfileFlagGrammarWithoutChangingLegacyParser(t *testing.T) {
	bare, err := ParseActiveProviderFlag("bedrock/opus")
	if err != nil || len(bare.Default) != 1 || bare.Default[0] != "bedrock/opus" || len(bare.Named) != 0 {
		t.Fatalf("provider -p value = %#v, %v", bare, err)
	}
	named, err := ParseActiveProviderFlag("claude=bedrock/opus,codex=native")
	if err != nil || named.Named["claude"][0] != "bedrock/opus" || named.Named["codex"][0] != "native" {
		t.Fatalf("provider named -p values = %#v, %v", named, err)
	}
	legacyBare, legacyNamed, err := ParseProfileFlag("pi=old-profile")
	if err != nil || legacyBare != nil || legacyNamed["pi"][0] != "old-profile" {
		t.Fatalf("legacy profile -p parser changed: %v, %v, %v", legacyBare, legacyNamed, err)
	}
}

func TestProviderReceiversRequireSpellingCompatibleNames(t *testing.T) {
	for _, tc := range []struct {
		name        string
		declaration packdecl.ProviderContribution
		selection   string
	}{
		{"empty provider", packdecl.ProviderContribution{}, ""},
		{"provider whitespace", packdecl.ProviderContribution{Name: "p q"}, "p"},
		{"provider comma", packdecl.ProviderContribution{Name: "p,q"}, "p"},
		{"alias equals", packdecl.ProviderContribution{Name: "p", Aliases: []string{"a=b"}}, "p"},
		{"variant whitespace", packdecl.ProviderContribution{Name: "p", Variants: map[string]packdecl.ProviderVariant{"a b": {}}}, "p"},
		{"unspellable selector", packdecl.ProviderContribution{Name: "p"}, "p,q"},
		{"equals selector", packdecl.ProviderContribution{Name: "p"}, "p=q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveActiveProviders([]packdecl.ProviderContribution{tc.declaration}, ActiveProviderSelection{Default: []string{tc.selection}}, []ProviderReceiver{{Bin: "agent"}})
			if err == nil {
				t.Fatalf("expected spelling refusal for %#v / %q", tc.declaration, tc.selection)
			}
		})
	}
}

func TestComposeProviderSemanticsIsTheTypedPublicCompositionEntryPoint(t *testing.T) {
	inactive, err := ComposeProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{
		{Name: "unused", ModelRows: map[string]packdecl.ProviderModel{"a": {TwinOf: "b"}, "b": {TwinOf: "a"}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "twin") {
		t.Fatalf("inactive malformed twin graph must be rejected: %#v, %v", inactive, err)
	}
	price := completePrice(1, 2, .1, .2)
	input := ProviderSemanticInput{
		Declarations: []packdecl.ProviderContribution{{Name: "bedrock", Aliases: []string{"br"}, Model: "opus", ModelOrder: []string{"opus"}, ModelRows: map[string]packdecl.ProviderModel{"opus": {Name: "Opus", Vendor: "anthropic", Price: price}}}},
		Selection:    ActiveProviderSelection{Named: map[string][]string{"assistant": {"br"}}},
		Receivers:    []ProviderReceiver{{Bin: "assistant"}},
		Catalog:      map[string]map[string]packdecl.ProviderModel{"bedrock": {"opus": {Context: 100000}}},
	}
	got, err := ComposeProviderSemantics(input)
	if err != nil {
		t.Fatal(err)
	}
	if got.Selection.ByReceiver["assistant"][0].Provider != "bedrock" || got.Models["bedrock"].ByID["opus"].Facts.Context != 100000 || got.Models["bedrock"].ByID["opus"].Facts.Name != "Opus" {
		t.Fatalf("public semantic composition = %#v", got)
	}
	if _, exists := got.Models["unused"]; exists {
		t.Fatal("unselected model catalog was projected")
	}
}

func TestResolveActiveProvidersValidatesSharedNameNamespaceAndActionableSelections(t *testing.T) {
	providers := []packdecl.ProviderContribution{{Name: "bedrock", Aliases: []string{"br"}, Variants: map[string]packdecl.ProviderVariant{"opus": {}}}}
	for _, p := range [][]packdecl.ProviderContribution{
		{{Name: "native"}}, {{Name: "p", Aliases: []string{"native"}}}, {{Name: "same"}, {Name: "other", Aliases: []string{"same"}}}, {{Name: "p", Aliases: []string{"q"}}, {Name: "q"}},
	} {
		if _, err := ResolveActiveProviders(p, ActiveProviderSelection{}, nil); err == nil || !strings.Contains(err.Error(), "collision") && !strings.Contains(err.Error(), "reserved") {
			t.Errorf("declarations %#v: expected reserved/collision error, got %v", p, err)
		}
	}
	_, err := ResolveActiveProviders(providers, ActiveProviderSelection{Default: []string{"bedrok"}}, []ProviderReceiver{{Bin: "claude"}})
	if err == nil || !strings.Contains(err.Error(), "bedrock") || !strings.Contains(err.Error(), "bedrock/opus") || !strings.Contains(err.Error(), "br") {
		t.Fatalf("unknown selection should name declared values: %v", err)
	}
	resolved, err := ResolveActiveProviders(providers, ActiveProviderSelection{Default: []string{"br"}}, []ProviderReceiver{{Bin: "claude"}})
	if err != nil || resolved.ByReceiver["claude"][0].Provider != "bedrock" {
		t.Fatalf("provider alias did not resolve: %#v, %v", resolved, err)
	}
	pairs := []packdecl.ProviderContribution{{Name: "p", Variants: map[string]packdecl.ProviderVariant{"opus": {Model: "p-opus"}, "native": {Model: "p-native"}}},
		{Name: "q", Variants: map[string]packdecl.ProviderVariant{"opus": {Model: "q-opus"}}}}
	both, err := ResolveActiveProviders(pairs, ActiveProviderSelection{Default: []string{"p/opus", "q/opus"}}, []ProviderReceiver{{Bin: "assistant", HoldsProviderSets: true}})
	if err != nil || len(both.ByReceiver["assistant"]) != 2 || both.ByReceiver["assistant"][0].Model != "p-opus" || both.ByReceiver["assistant"][1].Model != "q-opus" {
		t.Fatalf("qualified variants should coexist: %#v, %v", both, err)
	}
	for _, selectors := range [][]string{{"p", "p/opus"}, {"p", "alias-p"}} {
		duplicates := []packdecl.ProviderContribution{{Name: "p", Aliases: []string{"alias-p"}, Variants: map[string]packdecl.ProviderVariant{"opus": {}}}, {Name: "q", Variants: map[string]packdecl.ProviderVariant{"opus": {}}}}
		if _, err := ResolveActiveProviders(duplicates, ActiveProviderSelection{Default: selectors}, []ProviderReceiver{{Bin: "assistant", HoldsProviderSets: true}}); err == nil || !strings.Contains(err.Error(), "keep one") {
			t.Errorf("selectors %v resolving to duplicate provider should refuse actionably: %v", selectors, err)
		}
	}
	nativeVariant, err := ResolveActiveProviders(pairs, ActiveProviderSelection{Default: []string{"p/native"}}, []ProviderReceiver{{Bin: "assistant"}})
	if err != nil || nativeVariant.ByReceiver["assistant"][0].Target.Native || nativeVariant.ByReceiver["assistant"][0].Target.Variant != "native" {
		t.Fatalf("only bare native is reserved: %#v, %v", nativeVariant, err)
	}
}

func TestComposeProviderModelsFoldsCatalogSharedAndExplicitFactsAndTwinIdentity(t *testing.T) {
	price := completePrice(2, 10, .1, .2)
	partial := &packdecl.ProviderPrice{Input: floatp(3)}
	p := packdecl.ProviderContribution{Name: "bedrock", EveryModel: &packdecl.ProviderModel{
		API: "openai-responses", Thinking: []string{"low", "high"}, Accepts: []string{"text", "image"},
		MaxTokens: 1000, Price: price, WireID: "shared-wire",
		Images: &packdecl.ProviderImage{MaxWidth: 2048, MaxHeight: 2048, MaxBytes: 4_000_000, JPEGQuality: 80},
	}, ModelOrder: []string{"base", "compact"}, ModelRows: map[string]packdecl.ProviderModel{
		"base":    {Name: "Base", Context: 10000, MaxTokens: 1500, Price: partial},
		"compact": {TwinOf: "base", CompactAt: 2000, Context: 8000},
	}}
	catalog := map[string]packdecl.ProviderModel{"base": {Vendor: "anthropic", Context: 9000, Reasoning: boolp(true)}}
	rows, err := ComposeProviderModels(p, catalog)
	if err != nil {
		t.Fatal(err)
	}
	base, compact := rows.ByID["base"], rows.ByID["compact"]
	if base.Facts.API != "openai-responses" || base.Facts.Context != 10000 || base.Facts.Vendor != "anthropic" || base.WireID != "shared-wire" || base.Facts.Price.Input == nil || *base.Facts.Price.Input != 3 || *base.Facts.Price.Output != 10 {
		t.Fatalf("base facts = %#v", base)
	}
	if compact.Facts.Name != "Base" || compact.Facts.API != "openai-responses" || compact.Facts.Context != 8000 || compact.Facts.MaxTokens != 1500 || compact.Facts.Vendor != "anthropic" || compact.WireID != "shared-wire" || compact.CompactAt != 2000 || *compact.Facts.Price.Input != 3 || compact.Facts.Images == nil || compact.Facts.Images.JPEGQuality != 80 || len(compact.Facts.Accepts) != 2 || compact.Facts.Reasoning == nil || !*compact.Facts.Reasoning {
		t.Fatalf("twin must inherit complete target facts and identity before row overrides: %#v", compact)
	}
	compact.Facts.Name = "local override"
	if rows.ByID["base"].Facts.Name != "Base" {
		t.Fatal("twin facts alias source mutated")
	}
	*compact.Facts.Price.Input = 99
	if *base.Facts.Price.Input != 3 || *price.Input != 2 {
		t.Fatal("price pointers must be deep-copied between rows and input")
	}
}

func TestComposeProviderModelsInfersOnlyRecognizableMakers(t *testing.T) {
	price := completePrice(1, 2, 3, 4)
	p := packdecl.ProviderContribution{Name: "p", ModelRows: map[string]packdecl.ProviderModel{
		"global.anthropic.opus-5": {Price: price},
		"openai/gpt-6":            {Price: price},
	}}
	got, err := ComposeProviderModels(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.ByID["global.anthropic.opus-5"].Facts.Vendor != "anthropic" || got.ByID["openai/gpt-6"].Facts.Vendor != "openai" {
		t.Fatalf("maker inference = %#v", got.ByID)
	}
}

// With no catalog entry for a row, and nothing authored for a fact, the fact stays absent: no zero
// is read as a real value and no default is invented. A catalog that does not name the row leaks
// nothing into it either.
func TestComposeProviderModelsMissingCatalogInventsNoFacts(t *testing.T) {
	p := packdecl.ProviderContribution{Name: "p", ModelRows: map[string]packdecl.ProviderModel{
		"openai/gpt-6": {Price: completePrice(1, 2, 3, 4)},
	}}
	elsewhere := map[string]packdecl.ProviderModel{"other": {Context: 9000, MaxTokens: 500, Reasoning: boolp(true), CompactAt: 100}}
	for name, catalog := range map[string]map[string]packdecl.ProviderModel{"nil catalog": nil, "catalog naming another row": elsewhere} {
		rows, err := ComposeProviderModels(p, catalog)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		row := rows.ByID["openai/gpt-6"]
		f := row.Facts
		if f.Context != 0 || f.MaxTokens != 0 || f.Reasoning != nil || len(f.Thinking) != 0 || len(f.Accepts) != 0 ||
			f.Images != nil || f.API != "" || f.Name != "" || row.CompactAt != 0 {
			t.Fatalf("%s: facts were invented for a row with no catalog entry: %#v", name, row)
		}
	}
}

func TestComposeProviderModelsRefusesInvalidFactsTwinsAndPrices(t *testing.T) {
	for name, models := range map[string]map[string]packdecl.ProviderModel{
		"missing": {"a": {TwinOf: "missing"}}, "cycle": {"a": {TwinOf: "b"}, "b": {TwinOf: "a"}}, "self": {"a": {TwinOf: "a"}},
	} {
		_, err := ComposeProviderModels(packdecl.ProviderContribution{Name: "p", ModelRows: models}, nil)
		if err == nil || !strings.Contains(err.Error(), "twin") {
			t.Errorf("%s twin error = %v", name, err)
		}
	}
	for name, price := range map[string]*packdecl.ProviderPrice{
		"partial": {Input: floatp(1)}, "negative": completePrice(-1, 2, 3, 4), "nan": completePrice(math.NaN(), 2, 3, 4), "infinite": completePrice(math.Inf(1), 2, 3, 4),
	} {
		_, err := ComposeProviderModels(packdecl.ProviderContribution{Name: "p", EveryModel: &packdecl.ProviderModel{Vendor: "anthropic"}, ModelRows: map[string]packdecl.ProviderModel{"a": {Price: price}}}, nil)
		if err == nil || !strings.Contains(err.Error(), "price") {
			t.Errorf("%s price error = %v", name, err)
		}
	}
	validPrice := completePrice(1, 2, 3, 4)
	for _, model := range []packdecl.ProviderModel{
		{Price: validPrice, Images: &packdecl.ProviderImage{MaxWidth: 10, MaxHeight: 10, MaxBytes: 100, JPEGQuality: 101}},
		{Price: validPrice, Context: -1}, {Price: validPrice, MaxTokens: -1}, {Price: validPrice, CompactAt: -1},
		{Price: validPrice, API: "   "}, {Price: validPrice, Accepts: []string{"audio"}}, {Price: validPrice, Thinking: []string{"low", "low"}},
		{Price: validPrice, Vendor: "BadMaker"},
	} {
		_, err := ComposeProviderModels(packdecl.ProviderContribution{Name: "p", ModelRows: map[string]packdecl.ProviderModel{"a": model}}, nil)
		if err == nil {
			t.Errorf("invalid model facts accepted: %#v", model)
		}
	}
	unknownMaker := packdecl.ProviderContribution{Name: "p", ModelRows: map[string]packdecl.ProviderModel{"opaque-id": {Price: validPrice}}}
	if _, err := ComposeProviderModels(unknownMaker, nil); err == nil || !strings.Contains(err.Error(), "vendor") {
		t.Fatalf("ambiguous maker must be authored: %v", err)
	}
}

func TestComposeProviderSemanticsPreservesAuthoredOrderThroughPiModelDerive(t *testing.T) {
	packs := packload.Embedded()
	defer packload.ReleaseEmbedded()
	var piPack *packload.Pack
	for _, pack := range packs {
		if pack.Name == "pi" {
			piPack = pack
			break
		}
	}
	provider := packdecl.ProviderContribution{Name: "semantic", ModelOrder: []string{"z", "a"}, ModelRows: map[string]packdecl.ProviderModel{
		"z": {Name: "Zed", Vendor: "anthropic", Price: completePrice(1, 2, 3, 4), OpenRouterRouting: map[string]any{"order": []any{"anthropic"}}},
		"a": {Name: "Alpha", Vendor: "anthropic", Price: completePrice(1, 2, 3, 4), OpenRouterRouting: map[string]any{"order": []any{"anthropic"}}},
	}}
	_, derives, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{provider},
		Selection: ActiveProviderSelection{Default: []string{"semantic"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})},
		[]ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "model-lists", Context: luahook.DeriveCtx{Tables: map[string]map[string]any{"providers": {"semantic": map[string]any{"endpoints": map[string]any{"openai": map[string]any{"base_url": "https://models.example/v1", "wire_api": "openai-responses"}}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	providers, ok := derives[0].Value["providers"].(map[string]any)
	if !ok {
		t.Fatalf("pi derive providers = %#v", derives[0].Value)
	}
	semantic, ok := providers["semantic"].(map[string]any)
	if !ok {
		t.Fatalf("pi derive semantic provider = %#v", providers)
	}
	rows, ok := semantic["models"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("pi derive semantic models = %#v", semantic)
	}
	ids := []string{rows[0].(map[string]any)["id"].(string), rows[1].(map[string]any)["id"].(string)}
	if strings.Join(ids, ",") != "z,a" {
		t.Fatalf("pi model list order = %v, want authored order [z a]", ids)
	}
}

func TestComposeProviderSemanticsProjectsThroughTheDeclaredPiModelDerive(t *testing.T) {
	packs := packload.Embedded()
	defer packload.ReleaseEmbedded()
	var piPack *packload.Pack
	for _, pack := range packs {
		if pack.Name == "pi" {
			piPack = pack
			break
		}
	}
	if piPack == nil || packload.DeriveScript(piPack) == "" {
		t.Fatal("embedded pi pack's declared derive is missing")
	}
	provider := packdecl.ProviderContribution{Name: "semantic", ModelOrder: []string{"client-model"}, ModelRows: map[string]packdecl.ProviderModel{
		"client-model": {Name: "Semantic Model", Description: "composed once", Vendor: "anthropic", Context: 8192, MaxTokens: 1024,
			Reasoning: boolp(true), Accepts: []string{"text", "image"}, Price: completePrice(1, 2, 3, 4),
			Base: "catalog-base", WireID: "wire-model", OpenRouterRouting: map[string]any{"order": []any{"anthropic"}}},
	}}
	providerInput := ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{provider},
		Selection: ActiveProviderSelection{Default: []string{"semantic"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}
	semantic, derives, err := ProjectProviderSemantics(providerInput, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "model-lists",
		Context: luahook.DeriveCtx{Tables: map[string]map[string]any{"providers": map[string]any{"semantic": map[string]any{"endpoints": map[string]any{"openai": map[string]any{"base_url": "https://models.example/v1", "wire_api": "openai-responses"}}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(derives) != 1 || derives[0].Pack != "pi" || derives[0].Surface != "model-lists" {
		t.Fatalf("declared derive outputs = %#v", derives)
	}
	projection := semantic.LegacyDerives["semantic"]
	if len(projection.Unsupported) != 0 {
		t.Fatalf("supported fixture facts reported as unsupported: %#v", projection.Unsupported)
	}
	got := derives[0].Value
	providers, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("pi derive providers = %#v", got)
	}
	list, ok := providers["semantic"].(map[string]any)
	if !ok {
		t.Fatalf("pi derive semantic row = %#v", providers)
	}
	rows, ok := list["models"].([]any)
	if !ok || len(rows) != 1 {
		t.Fatalf("pi derived models = %#v", list["models"])
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		t.Fatalf("pi derived model row = %#v", rows[0])
	}
	cost, _ := row["cost"].(map[string]any)
	input, _ := row["input"].([]any)
	routing, _ := row["openrouter_routing"].(map[string]any)
	numeric := func(value any) float64 {
		switch n := value.(type) {
		case int:
			return float64(n)
		case int64:
			return float64(n)
		case float64:
			return n
		default:
			return math.NaN()
		}
	}
	if row["id"] != "client-model" || row["wireId"] != "wire-model" || row["base"] != "catalog-base" || row["name"] != "Semantic Model" ||
		numeric(row["contextWindow"]) != 8192 || numeric(row["maxTokens"]) != 1024 || row["reasoning"] != true || len(input) != 2 || input[1] != "image" || numeric(cost["input"]) != 1 || len(routing["order"].([]any)) != 1 {
		t.Fatalf("canonical model projection not consumed by the declared pi derive: %#v", row)
	}
}

func TestProjectProviderModelsReportsUnsupportedCanonicalFactsByName(t *testing.T) {
	provider := packdecl.ProviderContribution{Name: "p", ModelRows: map[string]packdecl.ProviderModel{
		"openai/model-x": {Vendor: "openai", API: "openai-responses", Thinking: []string{"minimal"},
			Images:    &packdecl.ProviderImage{MaxWidth: 100, MaxHeight: 100, MaxBytes: 1000, JPEGQuality: 80},
			CompactAt: 100, Price: completePrice(1, 2, 3, 4)},
	}}
	composed, err := ComposeProviderModels(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := ProjectProviderModelsForLegacyDerives(composed)
	if err != nil {
		t.Fatal(err)
	}
	// compact_at reaches every derive as the flat context_window (RC-D29), so it is not unsupported.
	want := []string{"api", "images", "thinking"}
	options, _ := projection.ProviderEntry.Get("model_options")
	row, _ := options.(*jsonx.OrderedMap).Get("openai/model-x")
	if got, _ := row.(*jsonx.OrderedMap).Get("context_window"); got != "100" {
		t.Fatalf("compact_at must lower to the flat context_window, got %#v", got)
	}
	got := projection.Unsupported["openai/model-x"]
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unsupported canonical facts = %v, want %v", got, want)
	}
	if err := projection.RequireSupportedFor("pi"); err == nil || !strings.Contains(err.Error(), "openai/model-x") || !strings.Contains(err.Error(), "omit those facts") {
		t.Fatalf("unsupported fields refusal must name the model and a remedy: %v", err)
	}
	packs := packload.Embedded()
	defer packload.ReleaseEmbedded()
	var piPack *packload.Pack
	for _, pack := range packs {
		if pack.Name == "pi" {
			piPack = pack
			break
		}
	}
	if piPack == nil {
		t.Fatal("embedded pi pack missing")
	}
	_, _, err = ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{provider},
		Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})},
		[]ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "model-lists", Context: luahook.DeriveCtx{Tables: map[string]map[string]any{"providers": map[string]any{"p": map[string]any{"endpoints": map[string]any{"openai": map[string]any{"base_url": "https://models.example/v1", "wire_api": "openai-responses"}}}}}}}})
	if err == nil || !strings.Contains(err.Error(), "packs/pi derive model-lists") || !strings.Contains(err.Error(), "selected receiver pi") || !strings.Contains(err.Error(), "api") || !strings.Contains(err.Error(), "omit those facts") {
		t.Fatalf("unsupported canonical facts must refuse at actual declared owner with remedy: %v", err)
	}
}

func TestComposeProviderModelsPreservesEmptyAuthoredListsAndDoesNotInventModel(t *testing.T) {
	price := completePrice(1, 2, 3, 4)
	provider := packdecl.ProviderContribution{Name: "p", Model: "", EveryModel: &packdecl.ProviderModel{Vendor: "openai", Thinking: []string{"high"}, Accepts: []string{"text"}},
		ModelOrder: []string{"openai/model-x"}, ModelRows: map[string]packdecl.ProviderModel{"openai/model-x": {Thinking: []string{}, Accepts: []string{}, Price: price}}}
	rows, err := ComposeProviderModels(provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	facts := rows.ByID["openai/model-x"].Facts
	if facts.Thinking == nil || len(facts.Thinking) != 0 || facts.Accepts == nil || len(facts.Accepts) != 0 {
		t.Fatalf("explicit empty lists did not clear shared facts: %#v", facts)
	}
	projection, err := ProjectProviderModelsForLegacyDerives(rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(projection.Unsupported["openai/model-x"], ",") != "accepts,thinking" {
		t.Fatalf("empty facts must be reported rather than inherited/defaulted: %#v", projection.Unsupported)
	}
	semantics, err := ComposeProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{{Name: "p"}},
		Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: []ProviderReceiver{{Bin: "pi"}}})
	if err != nil {
		t.Fatal(err)
	}
	selected := semantics.Selection.ByReceiver["pi"][0]
	if selected.Model != "" || selected.LegacyCompatibility().Profile.Options["model"] != "" || len(semantics.Models["p"].Order) != 0 {
		t.Fatalf("empty provider.Model must not invent a first/default model: %#v", semantics)
	}
}

func TestVariantEmptyCapabilitiesAndProtocolsRemainExplicitAcrossCompatibilityView(t *testing.T) {
	provider := packdecl.ProviderContribution{Name: "p", Capabilities: []string{"base-capability"}, Variants: map[string]packdecl.ProviderVariant{
		"v": {Capabilities: []string{}, Protocols: []string{}},
	}}
	selected, err := ResolveActiveProviders([]packdecl.ProviderContribution{provider}, ActiveProviderSelection{Default: []string{"p/v"}}, []ProviderReceiver{{Bin: "pi"}})
	if err != nil {
		t.Fatal(err)
	}
	resolved := selected.ByReceiver["pi"][0]
	compat := resolved.LegacyCompatibility()
	if resolved.Capabilities == nil || resolved.Protocols == nil || compat.Capabilities == nil || compat.Protocols == nil ||
		len(resolved.Capabilities) != 0 || len(resolved.Protocols) != 0 || len(compat.Capabilities) != 0 || len(compat.Protocols) != 0 {
		t.Fatalf("explicit empty capability/protocol sets became absent: %#v %#v", resolved, compat)
	}
}

func TestModelGraphRejectsDuplicateOrderEntries(t *testing.T) {
	provider := packdecl.ProviderContribution{Name: "p", ModelRows: map[string]packdecl.ProviderModel{"a": {}, "b": {}}, ModelOrder: []string{"a", "a"}}
	if err := ValidateProviderModelGraph(provider); err == nil || !strings.Contains(err.Error(), "repeats") {
		t.Fatalf("duplicate model order was not specifically refused: %v", err)
	}
}

func completePrice(input, output, cacheRead, cacheWrite float64) *packdecl.ProviderPrice {
	return &packdecl.ProviderPrice{Input: floatp(input), Output: floatp(output), CacheRead: floatp(cacheRead), CacheWrite: floatp(cacheWrite)}
}
func floatp(v float64) *float64 { return &v }
func boolp(v bool) *bool        { return &v }
