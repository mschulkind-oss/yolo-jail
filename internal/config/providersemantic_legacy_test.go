package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/luahook"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestNormalizeLegacyProviderModelsKeepsExactSnapshotAndBuildsCanonicalRows(t *testing.T) {
	price := func(s string) *string { return &s }
	input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{"m": "m"}, ModelOptions: packdecl.ModelFacts{
		"m": {"vendor": price("acme"), "name": price("Model"), "context_window": price("100000"), "reasoning": price("false"), "input": price("text,image"),
			"cost_input": price("1.0"), "cost_output": price("2"), "cost_cache_read": price("0.1"), "cost_cache_write": price("0")},
	}}
	got := NormalizeLegacyProviderModels(input, nil)
	if got.Canonical == nil || len(got.Differences) != 0 {
		t.Fatalf("representable legacy models refused: canonical=%#v differences=%#v", got.Canonical, got.Differences)
	}
	row := got.Canonical.ModelRows["m"]
	if got.Canonical.ModelOrder == nil || !reflect.DeepEqual(got.Canonical.ModelOrder, []string{"m"}) || row.Vendor != "acme" || row.Context != 100000 || row.Reasoning == nil || *row.Reasoning || !reflect.DeepEqual(row.Accepts, []string{"text", "image"}) || row.Price == nil || row.Price.Input == nil || *row.Price.Input != 1 {
		t.Fatalf("canonical provider row = %#v, order=%v", row, got.Canonical.ModelOrder)
	}
	// Normalized numeric equality does not replace source provenance: restore the original lexeme.
	restored := got.RestoreLegacyProviderModels(input)
	if !reflect.DeepEqual(restored.Models, input.Models) || !reflect.DeepEqual(restored.ModelOptions, input.ModelOptions) || *restored.ModelOptions["m"]["cost_input"] != "1.0" {
		t.Fatalf("legacy declaration did not restore value/order/lexeme: %#v", restored)
	}
	*got.Snapshot.ModelOptions["m"]["name"] = "mutated snapshot"
	if *input.ModelOptions["m"]["name"] != "Model" {
		t.Fatal("snapshot aliases the caller's nested fact pointer")
	}
}

func TestNormalizeLegacyProviderModelsRefusesOpaqueTombstoneAliasAndConflictingWireFacts(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name     string
		models   map[string]string
		options  packdecl.ModelFacts
		wantPath string
	}{
		{"opaque", map[string]string{"m": "m"}, packdecl.ModelFacts{"m": {"vendor": str("acme"), "future_fact": str("x")}}, "model_options.m.future_fact"},
		{"tombstone", map[string]string{"m": "m"}, packdecl.ModelFacts{"m": {"vendor": str("acme"), "name": nil}}, "model_options.m.name"},
		{"alias is not canonical id", map[string]string{"short": "wire"}, packdecl.ModelFacts{"short": {"vendor": str("acme")}}, "models.short"},
		{"same wire facts conflict", map[string]string{"a": "wire", "b": "wire"}, packdecl.ModelFacts{"a": {"vendor": str("acme")}, "b": {"vendor": str("other")}}, "models.b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := packdecl.ProviderContribution{Name: "p", Models: tc.models, ModelOptions: tc.options}
			got := NormalizeLegacyProviderModels(input, nil)
			if got.Canonical != nil {
				t.Fatalf("lossy declaration produced canonical rows: %#v", got.Canonical)
			}
			found, foundConflict := false, false
			for _, diff := range got.Differences {
				if diff.Path == tc.wantPath {
					found = true
				}
				if tc.name == "same wire facts conflict" && diff.Path == "models.b" && strings.Contains(diff.Reason, "complete model facts differ") {
					foundConflict = true
				}
			}
			if !found {
				t.Fatalf("differences %#v do not name %s", got.Differences, tc.wantPath)
			}
			if tc.name == "same wire facts conflict" && !foundConflict {
				t.Errorf("same-wire facts conflict was not explicit: %#v", got.Differences)
			}
			// Refusal never drops legacy maps or nil fact pointers.
			restored := got.RestoreLegacyProviderModels(input)
			if !reflect.DeepEqual(restored.Models, input.Models) || !reflect.DeepEqual(restored.ModelOptions, input.ModelOptions) {
				t.Fatalf("legacy state lost: %#v", restored)
			}
		})
	}
}

func TestNormalizeLegacyProviderModelsCoalescesOnlyIdenticalFactsForSharedWireIDs(t *testing.T) {
	str := func(s string) *string { return &s }
	input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{"a": "wire", "b": "wire"}, ModelOptions: packdecl.ModelFacts{
		"a": {"vendor": str("acme"), "name": str("Model")}, "b": {"vendor": str("acme"), "name": str("Model")},
	}}
	got := NormalizeLegacyProviderModels(input, nil)
	for _, diff := range got.Differences {
		if strings.Contains(diff.Reason, "complete model facts differ") {
			t.Fatalf("identical shared-wire facts were treated as conflict: %#v", got.Differences)
		}
	}
	if len(got.Snapshot.Models) != 2 || len(got.Snapshot.ModelOptions) != 2 {
		t.Fatalf("aliases were not preserved independently: %#v", got.Snapshot)
	}
}

func TestNormalizeLegacyProviderModelsPreservesNilEmptyRowsAndTombstonesOnRestore(t *testing.T) {
	str := "1.00"
	input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{}, ModelOptions: packdecl.ModelFacts{"nil-row": nil, "empty-row": {}, "tombstone": {"cost_input": nil, "future": &str}}}
	got := NormalizeLegacyProviderModels(input, nil)
	restored := got.RestoreLegacyProviderModels(input)
	if got.Snapshot.Models == nil || restored.Models == nil || restored.ModelOptions["nil-row"] != nil || restored.ModelOptions["empty-row"] == nil || restored.ModelOptions["tombstone"]["cost_input"] != nil || *restored.ModelOptions["tombstone"]["future"] != "1.00" {
		t.Fatalf("nil/empty/tombstone state changed: %#v / %#v", got.Snapshot, restored.ModelOptions)
	}
	if restored.ModelOptions["tombstone"]["future"] == input.ModelOptions["tombstone"]["future"] {
		t.Fatal("restored fact pointer is not independently owned")
	}
}

func TestNormalizeLegacyProviderModelsMatchesOldCompositionAndRegisteredOwnerRender(t *testing.T) {
	root := t.TempDir()
	manifest := `{"contributes":[
	 {"kind":"provider","name":"p","models":{"m":"m"},"model_options":{"m":{"vendor":"acme","name":"Model","context_window":"100000","reasoning":"false","input":"text,image","cost_input":"1.0","cost_output":"2","cost_cache_read":"0.1","cost_cache_write":"0"}}},
	 {"kind":"program","bin":"probe","via":"npm","package":"probe","protocols":["openai"],"provider_sets":true},
	 {"kind":"config","config":[{"agent":"probe","name":"snapshot","codec":"json","path":"~/.probe/snapshot.json","mode":"computed"}]}
	]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	script := `yolo.derive("probe","snapshot",function(ctx)
	 local p=ctx.providers.p; local f=p.model_options.m
	 return {id=p.models.m,name=f.name,context=tonumber(f.context_window),reasoning=f.reasoning,input=f.input,price=tonumber(f.cost_input)}
	end)`
	if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(root, "probe")
	if pack == nil || len(problems) > 0 {
		t.Fatalf("load actual legacy fixture: %#v", problems)
	}
	declarations := pack.Decl.Providers()
	if len(declarations) != 1 {
		t.Fatalf("legacy provider declarations=%#v", declarations)
	}
	normalized := NormalizeLegacyProviderModels(declarations[0], nil)
	if normalized.Canonical == nil || len(normalized.Differences) != 0 {
		t.Fatalf("fixture not representable: %#v", normalized.Differences)
	}

	// Baseline executes the same registered owner source against the actual old host composer output.
	legacy, err := packload.ComposeProviders(nil, []*packload.Pack{pack})
	if err != nil {
		t.Fatal(err)
	}
	legacyTables := map[string]any{"providers": jsonx.Plain(legacy)}
	baseline, err := (luahook.GopherLuaVM{}).Derive(packload.DeriveScript(pack), &luahook.DeriveCtx{Agent: "probe", Surface: "snapshot", Tables: toDeriveTables(t, legacyTables)})
	if err != nil {
		t.Fatalf("old composer owner render: %v", err)
	}

	// The candidate side invokes the provider semantic path and the owner's registered yolo.derive.
	input := ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{*normalized.Canonical}, Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: ProviderReceivers([]*packload.Pack{pack})}
	_, outputs, err := ProjectProviderSemantics(input, []ProviderDeriveRequest{{Receiver: "probe", Pack: pack, Surface: "snapshot"}})
	if err != nil || len(outputs) != 1 {
		t.Fatalf("canonical registered owner render: %#v, %v", outputs, err)
	}
	if !reflect.DeepEqual(baseline, outputs[0].Value) {
		t.Fatalf("legacy compose+owner render differs: old=%#v canonical=%#v", baseline, outputs[0].Value)
	}
	if baseline["id"] != "m" || baseline["name"] != "Model" || baseline["input"] != "text,image" || baseline["context"] == nil || baseline["price"] == nil || baseline["reasoning"] != "false" {
		t.Fatalf("unexpected observable legacy output: %#v", baseline)
	}
	if strings.Contains(packload.DeriveScript(pack), "error(") {
		t.Fatal("fixture owner unexpectedly contains an error path")
	}
}

func TestNormalizeLegacyProviderModelsValidatesEveryFlatRowShape(t *testing.T) {
	validOffline := packdecl.ProviderModel{Vendor: "acme", Name: "Offline", Price: completePrice(1, 2, 3, 4)}
	for _, tc := range []struct {
		name  string
		facts packdecl.ModelFacts
	}{
		{name: "nil row", facts: packdecl.ModelFacts{"m": nil}},
		{name: "absent row", facts: nil},
		{name: "empty row", facts: packdecl.ModelFacts{"m": {}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{"m": "m"}, ModelOptions: tc.facts}
			got := NormalizeLegacyProviderModels(input, nil)
			if got.Canonical != nil || !hasProviderDifference(got.Differences, "model_options.m.vendor") || !hasProviderDifference(got.Differences, "model_options.m.price") {
				t.Errorf("incomplete %s row must refuse maker/price rather than infer or invent: canonical=%#v differences=%#v", tc.name, got.Canonical, got.Differences)
			}
			restored := got.RestoreLegacyProviderModels(input)
			if !reflect.DeepEqual(restored.Models, input.Models) || !reflect.DeepEqual(restored.ModelOptions, input.ModelOptions) {
				t.Errorf("%s snapshot was not restored exactly: %#v", tc.name, restored)
			}
			valid := NormalizeLegacyProviderModels(input, map[string]packdecl.ProviderModel{"m": validOffline})
			if valid.Canonical == nil || len(valid.Differences) != 0 {
				t.Errorf("complete offline maker+price facts should support %s row: %#v", tc.name, valid.Differences)
			}
			invalid := NormalizeLegacyProviderModels(input, map[string]packdecl.ProviderModel{"m": {Vendor: "acme", Price: &packdecl.ProviderPrice{Input: float64p(1)}}})
			if invalid.Canonical != nil || !hasProviderDifference(invalid.Differences, "model_options.m.price") {
				t.Errorf("invalid offline facts bypassed final validation for %s: canonical=%#v differences=%#v", tc.name, invalid.Canonical, invalid.Differences)
			}
		})
	}
}

func TestNormalizeLegacyProviderModelsPreservesIdentityWirePresenceThroughShippedPi(t *testing.T) {
	piPack := shippedPiPack(t)
	oldPack, provider := loadLegacyPiProvider(t, "openai", `"models":{"global.openai.gpt-6-luna":"global.openai.gpt-6-luna"},"model_options":{"global.openai.gpt-6-luna":{"vendor":"openai","name":"GPT-6 Luna","cost_input":"1","cost_output":"2","cost_cache_read":"3","cost_cache_write":"4"}}`)
	oldTable, err := packload.ComposeProviders(nil, []*packload.Pack{oldPack})
	if err != nil {
		t.Fatal(err)
	}
	ctx := luahook.DeriveCtx{BuiltInProviders: packload.BuiltInProvidersFor([]*packload.Pack{piPack}, "pi")}
	ctx.Tables = toDeriveTables(t, map[string]any{"providers": jsonx.Plain(oldTable)})
	oldOutput := runShippedPi(t, piPack, "model-lists", oldTable, ctx)
	if registered, ok := oldOutput["providers"]; ok && registered != nil {
		t.Errorf("old identity-only openai declaration unexpectedly registers model-lists: %#v", oldOutput)
	}

	normalized := NormalizeLegacyProviderModels(provider, nil)
	if normalized.Canonical == nil || len(normalized.Differences) != 0 {
		t.Fatalf("identity declaration should be representable: %#v", normalized.Differences)
	}
	input := ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{*normalized.Canonical}, Selection: ActiveProviderSelection{Default: []string{"openai"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}
	_, outputs, err := ProjectProviderSemantics(input, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "model-lists", Context: ctx}})
	if err != nil || len(outputs) != 1 {
		t.Fatalf("project through shipped pi derive: outputs=%#v err=%v", outputs, err)
	}
	if !reflect.DeepEqual(oldOutput, outputs[0].Value) {
		t.Errorf("legacy ComposeProviders→shipped pi model-lists differs from typed projection: old=%#v projected=%#v", oldOutput, outputs[0].Value)
	}
	oldModels := runShippedPi(t, piPack, "models", oldTable, ctx)
	_, modelOutputs, err := ProjectProviderSemantics(input, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "models", Context: ctx}})
	if err != nil || len(modelOutputs) != 1 {
		t.Errorf("project complete shipped pi models surface: outputs=%#v err=%v", modelOutputs, err)
	} else if !reflect.DeepEqual(oldModels, modelOutputs[0].Value) {
		t.Errorf("legacy ComposeProviders→shipped pi models differs from typed projection: old=%#v projected=%#v", oldModels, modelOutputs[0].Value)
	}

	// Presence is distinct from value: an authored wire_id equal to id must still reach pi.
	authoredPack, authored := loadLegacyPiProvider(t, "openai", `"models":{"global.openai.gpt-6-luna":"global.openai.gpt-6-luna"},"model_options":{"global.openai.gpt-6-luna":{"vendor":"openai","wire_id":"global.openai.gpt-6-luna","cost_input":"1","cost_output":"2","cost_cache_read":"3","cost_cache_write":"4"}}`)
	authoredNormalized := NormalizeLegacyProviderModels(authored, nil)
	authoredOld, err := packload.ComposeProviders(nil, []*packload.Pack{authoredPack})
	if err != nil {
		t.Fatal(err)
	}
	oldAuthoredOutput := runShippedPi(t, piPack, "model-lists", authoredOld, ctx)
	_, authoredOutputs, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{*authoredNormalized.Canonical}, Selection: ActiveProviderSelection{Default: []string{"openai"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "model-lists", Context: ctx}})
	if err != nil || len(authoredOutputs) != 1 {
		t.Fatalf("authored wire derive: %#v %v", authoredOutputs, err)
	}
	if !reflect.DeepEqual(oldAuthoredOutput, authoredOutputs[0].Value) {
		t.Errorf("authored identity wire_id presence differs in shipped pi output: old=%#v projected=%#v", oldAuthoredOutput, authoredOutputs[0].Value)
	}
	authoredModels := runShippedPi(t, piPack, "models", authoredOld, ctx)
	_, authoredModelOutputs, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{*authoredNormalized.Canonical}, Selection: ActiveProviderSelection{Default: []string{"openai"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "models", Context: ctx}})
	if err != nil || len(authoredModelOutputs) != 1 {
		t.Errorf("project authored shipped pi models surface: outputs=%#v err=%v", authoredModelOutputs, err)
	} else if !reflect.DeepEqual(authoredModels, authoredModelOutputs[0].Value) {
		t.Errorf("authored wire_id differs in shipped pi models surface: old=%#v projected=%#v", authoredModels, authoredModelOutputs[0].Value)
	}
}

func TestNormalizeLegacyProviderModelsDeeplyOwnsBothReturnedDeclarations(t *testing.T) {
	price := completePrice(1, 2, 3, 4)
	flag := true
	enforce := false
	opaque := 7
	channel := make(chan int)
	input := packdecl.ProviderContribution{
		Name: "p", Aliases: []string{"alias"}, Model: "m", Variants: map[string]packdecl.ProviderVariant{"v": {
			Model: "m", Options: map[string]packdecl.OptionDefault{"temperature": {Defaulted: true, Value: "0.2"}},
			Subagents:     map[string]any{"roles": map[string]any{"worker": "m"}, "opaque": &opaque, "channel": channel, "nil": nil},
			EnforceModels: &enforce, Capabilities: []string{"variant-capability"}, Protocols: []string{"openai"},
		}}, VariantOrder: []string{"v"}, EveryModel: &packdecl.ProviderModel{Vendor: "acme", Reasoning: &flag, Price: price, Accepts: []string{"text"}},
		ModelRows: map[string]packdecl.ProviderModel{"m": {Vendor: "acme", Price: price, Accepts: []string{"text"}}}, ModelOrder: []string{"m"},
		Endpoints:     map[string]packdecl.ProviderEndpoint{"openai": {BaseURL: "https://example.test/v1", WireAPI: "openai-responses"}},
		APIKeyEnvName: packdecl.EnvNames{"API_KEY"}, RegionEnvName: []string{"AWS_REGION"}, RegionFile: &packdecl.RegionFile{Path: ".aws/config", Key: "region"},
		Models: nil, ModelOptions: nil, Options: map[string]packdecl.OptionDefault{"context_window": {Defaulted: true, Value: "500"}}, Capabilities: []string{"provider-capability"},
	}
	got := NormalizeLegacyProviderModels(input, nil)
	if got.Canonical == nil {
		t.Fatalf("canonical clone fixture was refused: %#v", got.Differences)
	}
	first := got.RestoreLegacyProviderModels(input)
	second := got.RestoreLegacyProviderModels(input)
	if got.Canonical.Capabilities == nil || got.Canonical.VariantOrder == nil || got.Canonical.ModelOrder == nil || got.Canonical.RegionEnvName == nil || got.Canonical.APIKeyEnvName == nil || got.Canonical.Variants["v"].Capabilities == nil {
		t.Errorf("explicit empty slices collapsed to nil: %#v", got.Canonical)
	}

	variant := got.Canonical.Variants["v"]
	variant.Options["temperature"] = packdecl.OptionDefault{Defaulted: true, Value: "9"}
	variant.Subagents["roles"].(map[string]any)["worker"] = "changed"
	*variant.Subagents["opaque"].(*int) = 9
	*variant.EnforceModels = true
	variant.Capabilities[0] = "canonical-mutated"
	variant.Protocols[0] = "changed"
	got.Canonical.Variants["v"] = variant
	got.Canonical.VariantOrder[0] = "canonical-mutated"
	got.Canonical.RegionFile.Path = "changed"
	got.Canonical.Options["context_window"] = packdecl.OptionDefault{Defaulted: true, Value: "9"}
	got.Canonical.Endpoints["openai"] = packdecl.ProviderEndpoint{BaseURL: "changed"}
	got.Canonical.ModelOrder[0] = "canonical-mutated"
	modelRow := got.Canonical.ModelRows["m"]
	modelRow.Accepts[0] = "image"
	*modelRow.Price.Input = 9
	got.Canonical.ModelRows["m"] = modelRow
	*got.Canonical.EveryModel.Reasoning = false
	*got.Canonical.EveryModel.Price.Output = 9
	got.Canonical.Capabilities[0] = "canonical-mutated"
	got.Canonical.APIKeyEnvName[0] = "canonical-mutated"
	got.Canonical.RegionEnvName[0] = "canonical-mutated"
	if got.Canonical.Variants["v"].Subagents["channel"] != channel || got.Canonical.Variants["v"].Subagents["nil"] != nil {
		t.Error("non-JSON or nil subagent values were not preserved")
	}
	if input.Variants["v"].Options["temperature"].Value != "0.2" || input.Variants["v"].Subagents["roles"].(map[string]any)["worker"] != "m" || opaque != 7 || *input.Variants["v"].EnforceModels || input.Variants["v"].Capabilities[0] != "variant-capability" || input.Variants["v"].Protocols[0] != "openai" || input.VariantOrder[0] != "v" || input.RegionFile.Path != ".aws/config" || input.Options["context_window"].Value != "500" || input.Endpoints["openai"].BaseURL != "https://example.test/v1" || input.ModelRows["m"].Accepts[0] != "text" || *input.ModelRows["m"].Price.Input != 1 || *input.EveryModel.Reasoning != true || *input.EveryModel.Price.Output != 2 || input.EveryModel.Accepts[0] != "text" || input.ModelOrder[0] != "m" || input.Capabilities[0] != "provider-capability" || input.APIKeyEnvName[0] != "API_KEY" || input.RegionEnvName[0] != "AWS_REGION" {
		t.Errorf("canonical return aliases original declaration: %#v", input)
	}
	if first.Variants["v"].Options["temperature"].Value != "0.2" || second.Variants["v"].Options["temperature"].Value != "0.2" || *first.ModelRows["m"].Price.Input != 1 || *second.ModelRows["m"].Price.Input != 1 || first.VariantOrder[0] != "v" || second.VariantOrder[0] != "v" || first.Variants["v"].Capabilities[0] != "variant-capability" || second.Variants["v"].Capabilities[0] != "variant-capability" || first.Variants["v"].Protocols[0] != "openai" || second.Variants["v"].Protocols[0] != "openai" || first.RegionFile.Path != ".aws/config" || second.RegionFile.Path != ".aws/config" || first.ModelOrder[0] != "m" || second.ModelOrder[0] != "m" || first.ModelRows["m"].Accepts[0] != "text" || second.ModelRows["m"].Accepts[0] != "text" || first.Capabilities[0] != "provider-capability" || second.Capabilities[0] != "provider-capability" || first.APIKeyEnvName[0] != "API_KEY" || second.APIKeyEnvName[0] != "API_KEY" || first.RegionEnvName[0] != "AWS_REGION" || second.RegionEnvName[0] != "AWS_REGION" {
		t.Errorf("canonical return aliases one or both restore returns: first=%#v second=%#v", first, second)
	}
	firstVariant := first.Variants["v"]
	*firstVariant.EnforceModels = true
	firstVariant.Capabilities[0] = "first-mutated"
	firstVariant.Protocols[0] = "first-mutated"
	first.Variants["v"] = firstVariant
	first.VariantOrder[0] = "first-mutated"
	first.RegionFile.Path = "first-mutated"
	first.ModelOrder[0] = "first-mutated"
	first.ModelRows["m"] = func() packdecl.ProviderModel {
		row := first.ModelRows["m"]
		row.Accepts[0] = "first-mutated"
		return row
	}()
	first.Capabilities[0] = "first-mutated"
	first.APIKeyEnvName[0] = "first-mutated"
	first.RegionEnvName[0] = "first-mutated"
	if *input.Variants["v"].EnforceModels || *second.Variants["v"].EnforceModels || !*got.Canonical.Variants["v"].EnforceModels || input.Variants["v"].Capabilities[0] != "variant-capability" || second.Variants["v"].Capabilities[0] != "variant-capability" || got.Canonical.Variants["v"].Capabilities[0] != "canonical-mutated" || input.Variants["v"].Protocols[0] != "openai" || second.Variants["v"].Protocols[0] != "openai" || got.Canonical.Variants["v"].Protocols[0] != "changed" || input.VariantOrder[0] != "v" || second.VariantOrder[0] != "v" || got.Canonical.VariantOrder[0] != "canonical-mutated" || input.RegionFile.Path != ".aws/config" || second.RegionFile.Path != ".aws/config" || got.Canonical.RegionFile.Path != "changed" {
		t.Errorf("first restore return aliases input, canonical, or sibling: first=%#v", first)
	}
	secondVariant := second.Variants["v"]
	*secondVariant.EnforceModels = true
	secondVariant.Capabilities[0] = "second-mutated"
	secondVariant.Protocols[0] = "second-mutated"
	second.Variants["v"] = secondVariant
	second.VariantOrder[0] = "second-mutated"
	second.RegionFile.Path = "second-mutated"
	second.ModelOrder[0] = "second-mutated"
	secondRow := second.ModelRows["m"]
	secondRow.Accepts[0] = "second-mutated"
	second.ModelRows["m"] = secondRow
	second.Capabilities[0] = "second-mutated"
	second.APIKeyEnvName[0] = "second-mutated"
	second.RegionEnvName[0] = "second-mutated"
	if *input.Variants["v"].EnforceModels || !*first.Variants["v"].EnforceModels || !*got.Canonical.Variants["v"].EnforceModels || input.Variants["v"].Capabilities[0] != "variant-capability" || first.Variants["v"].Capabilities[0] != "first-mutated" || got.Canonical.Variants["v"].Capabilities[0] != "canonical-mutated" || input.Variants["v"].Protocols[0] != "openai" || first.Variants["v"].Protocols[0] != "first-mutated" || got.Canonical.Variants["v"].Protocols[0] != "changed" || input.VariantOrder[0] != "v" || first.VariantOrder[0] != "first-mutated" || got.Canonical.VariantOrder[0] != "canonical-mutated" || input.RegionFile.Path != ".aws/config" || first.RegionFile.Path != "first-mutated" || got.Canonical.RegionFile.Path != "changed" {
		t.Errorf("second restore return aliases input, canonical, or sibling: second=%#v", second)
	}
}

func TestNormalizeLegacyProviderModelsRestoreReturnsOwnEveryNestedCategory(t *testing.T) {
	price := completePrice(1, 2, 3, 4)
	reasoning := true
	enforce := false
	opaque := 7
	input := packdecl.ProviderContribution{
		Name: "p", Variants: map[string]packdecl.ProviderVariant{"v": {
			Options:       map[string]packdecl.OptionDefault{"temperature": {Value: "0.2"}},
			Subagents:     map[string]any{"roles": map[string]any{"worker": "m"}, "opaque": &opaque},
			EnforceModels: &enforce, Capabilities: []string{"variant-capability"}, Protocols: []string{"openai"},
		}}, VariantOrder: []string{"v"},
		EveryModel: &packdecl.ProviderModel{Vendor: "acme", Reasoning: &reasoning, Price: price, Accepts: []string{"text"}},
		ModelRows:  map[string]packdecl.ProviderModel{"m": {Vendor: "acme", Price: price, Accepts: []string{"text"}}}, ModelOrder: []string{"m"},
		Endpoints:    map[string]packdecl.ProviderEndpoint{"openai": {BaseURL: "https://example.test/v1"}},
		Options:      map[string]packdecl.OptionDefault{"context_window": {Value: "500"}},
		Capabilities: []string{"provider-capability"}, APIKeyEnvName: packdecl.EnvNames{"API_KEY"}, RegionEnvName: []string{"AWS_REGION"},
		RegionFile: &packdecl.RegionFile{Path: ".aws/config"},
	}
	normalized := NormalizeLegacyProviderModels(input, nil)
	if normalized.Canonical == nil {
		t.Fatalf("canonical declaration: %#v", normalized.Differences)
	}
	first := normalized.RestoreLegacyProviderModels(input)
	second := normalized.RestoreLegacyProviderModels(input)
	base := ownershipProbeValues(input)
	canonicalProbe := mutateOwnershipProbe(normalized.Canonical, "canonical")
	if reflect.DeepEqual(ownershipProbeValues(*normalized.Canonical), base) || !reflect.DeepEqual(ownershipProbeValues(input), base) || !reflect.DeepEqual(ownershipProbeValues(first), base) || !reflect.DeepEqual(ownershipProbeValues(second), base) {
		t.Errorf("Canonical mutation was not isolated across all deep categories")
	}
	firstProbe := mutateOwnershipProbe(&first, "first")
	if reflect.DeepEqual(ownershipProbeValues(first), base) || !reflect.DeepEqual(ownershipProbeValues(input), base) || !reflect.DeepEqual(ownershipProbeValues(second), base) || !reflect.DeepEqual(ownershipProbeValues(*normalized.Canonical), canonicalProbe) {
		t.Errorf("first Restore mutation aliases input, canonical, or second Restore")
	}
	secondProbe := mutateOwnershipProbe(&second, "second")
	if reflect.DeepEqual(secondProbe, base) || !reflect.DeepEqual(ownershipProbeValues(input), base) || !reflect.DeepEqual(ownershipProbeValues(first), firstProbe) || !reflect.DeepEqual(ownershipProbeValues(*normalized.Canonical), canonicalProbe) {
		t.Errorf("second Restore mutation aliases input, canonical, or first Restore")
	}
}

type providerOwnershipProbe struct {
	variantOption, subagent, variantCapability, variantProtocol, variantOrder string
	opaque, providerEnforce                                                   int
	regionFile, providerOption, endpoint                                      string
	modelOrder, modelAccepts                                                  string
	modelPrice, everyReasoning, everyPrice                                    float64
	providerCapability, apiKeyEnv, regionEnv                                  string
}

func ownershipProbeValues(provider packdecl.ProviderContribution) providerOwnershipProbe {
	variant := provider.Variants["v"]
	row := provider.ModelRows["m"]
	return providerOwnershipProbe{
		variantOption:     variant.Options["temperature"].Value,
		subagent:          variant.Subagents["roles"].(map[string]any)["worker"].(string),
		variantCapability: variant.Capabilities[0], variantProtocol: variant.Protocols[0], variantOrder: provider.VariantOrder[0],
		opaque: *variant.Subagents["opaque"].(*int), providerEnforce: boolAsInt(variant.EnforceModels),
		regionFile: provider.RegionFile.Path, providerOption: provider.Options["context_window"].Value, endpoint: provider.Endpoints["openai"].BaseURL,
		modelOrder: provider.ModelOrder[0], modelAccepts: row.Accepts[0], modelPrice: *row.Price.Input,
		everyReasoning: boolAsFloat(provider.EveryModel.Reasoning), everyPrice: *provider.EveryModel.Price.Output,
		providerCapability: provider.Capabilities[0], apiKeyEnv: string(provider.APIKeyEnvName[0]), regionEnv: provider.RegionEnvName[0],
	}
}

func mutateOwnershipProbe(provider *packdecl.ProviderContribution, marker string) providerOwnershipProbe {
	variant := provider.Variants["v"]
	option := variant.Options["temperature"]
	option.Value = marker
	variant.Options["temperature"] = option
	variant.Subagents["roles"].(map[string]any)["worker"] = marker
	*variant.Subagents["opaque"].(*int)++
	*variant.EnforceModels = !*variant.EnforceModels
	variant.Capabilities[0] = marker
	variant.Protocols[0] = marker
	provider.Variants["v"] = variant
	provider.VariantOrder[0] = marker
	provider.RegionFile.Path = marker
	option = provider.Options["context_window"]
	option.Value = marker
	provider.Options["context_window"] = option
	endpoint := provider.Endpoints["openai"]
	endpoint.BaseURL = marker
	provider.Endpoints["openai"] = endpoint
	provider.ModelOrder[0] = marker
	row := provider.ModelRows["m"]
	row.Accepts[0] = marker
	*row.Price.Input++
	provider.ModelRows["m"] = row
	*provider.EveryModel.Reasoning = !*provider.EveryModel.Reasoning
	*provider.EveryModel.Price.Output++
	provider.Capabilities[0] = marker
	provider.APIKeyEnvName[0] = marker
	provider.RegionEnvName[0] = marker
	return ownershipProbeValues(*provider)
}

func boolAsInt(value *bool) int {
	if value != nil && *value {
		return 1
	}
	return 0
}

func boolAsFloat(value *bool) float64 {
	if value != nil && *value {
		return 1
	}
	return 0
}

func TestNormalizeLegacyProviderModelsPreservesCapabilityReplacementPresenceForReceiver(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		providerCapabilities []string
		variantCapabilities  []string
		selector             string
		want                 []string
	}{
		{name: "inherit absent provider and variant capabilities", providerCapabilities: nil, variantCapabilities: nil, selector: "p/v", want: nil},
		{name: "preserve explicitly empty provider capabilities", providerCapabilities: []string{}, variantCapabilities: nil, selector: "p", want: []string{}},
		{name: "inherit provider capabilities", providerCapabilities: []string{"provider-capability"}, variantCapabilities: nil, selector: "p/v", want: []string{"provider-capability"}},
		{name: "replace with explicitly empty variant capabilities", providerCapabilities: []string{"provider-capability"}, variantCapabilities: []string{}, selector: "p/v", want: []string{}},
		{name: "replace with present variant capabilities", providerCapabilities: []string{"provider-capability"}, variantCapabilities: []string{"variant-capability"}, selector: "p/v", want: []string{"variant-capability"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			declaration := packdecl.ProviderContribution{Name: "p", Capabilities: tc.providerCapabilities, Variants: map[string]packdecl.ProviderVariant{"v": {Capabilities: tc.variantCapabilities}}}
			normalized := NormalizeLegacyProviderModels(declaration, nil)
			if normalized.Canonical == nil {
				t.Fatalf("canonical clone failed: %#v", normalized.Differences)
			}
			resolved, err := ResolveActiveProviders([]packdecl.ProviderContribution{*normalized.Canonical}, ActiveProviderSelection{Default: []string{tc.selector}}, []ProviderReceiver{{Bin: "pi"}})
			if err != nil {
				t.Fatal(err)
			}
			got := resolved.ByReceiver["pi"][0].LegacyCompatibility().Capabilities
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("receiver capabilities = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestNormalizeLegacyProviderModelsRefusesUnresolvedHybridPrecedence(t *testing.T) {
	valid := packdecl.ProviderModel{Vendor: "acme", Price: completePrice(1, 2, 3, 4)}
	t.Run("empty legacy map and canonical rows coexist", func(t *testing.T) {
		input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{}, ModelRows: map[string]packdecl.ProviderModel{"existing": valid}, ModelOrder: []string{"existing"}}
		got := NormalizeLegacyProviderModels(input, nil)
		if got.Canonical != nil || !hasProviderDifference(got.Differences, "model_rows") {
			t.Errorf("explicitly empty legacy Models silently replaced canonical rows: %#v", got)
		}
		restored := got.RestoreLegacyProviderModels(input)
		if !reflect.DeepEqual(restored.Models, input.Models) || !reflect.DeepEqual(restored.ModelRows, input.ModelRows) || !reflect.DeepEqual(restored.ModelOrder, input.ModelOrder) {
			t.Errorf("hybrid refusal changed raw rows/order: %#v", restored)
		}
	})
	t.Run("every model facts are not old composer facts", func(t *testing.T) {
		falseValue := false
		input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{"m": "m"}, EveryModel: &packdecl.ProviderModel{Reasoning: &falseValue}, ModelOptions: packdecl.ModelFacts{"m": {"vendor": stringPointer("acme"), "cost_input": stringPointer("1"), "cost_output": stringPointer("2"), "cost_cache_read": stringPointer("3"), "cost_cache_write": stringPointer("4")}}}
		got := NormalizeLegacyProviderModels(input, nil)
		if got.Canonical != nil || !hasProviderDifference(got.Differences, "every_model") {
			t.Errorf("retained every_model silently activates facts old composer did not emit: %#v", got)
		}
		if !reflect.DeepEqual(got.RestoreLegacyProviderModels(input).ModelOptions, input.ModelOptions) || got.Snapshot.ModelOptions["m"]["reasoning"] != nil {
			t.Errorf("hybrid difference changed original declaration/snapshot: %#v", got)
		}
	})
	t.Run("ordered supported nonhybrid control", func(t *testing.T) {
		input := packdecl.ProviderContribution{Name: "p", Models: map[string]string{"z": "z", "a": "a"}, ModelOptions: packdecl.ModelFacts{
			"z": {"vendor": stringPointer("acme"), "order": stringPointer("0"), "cost_input": stringPointer("1"), "cost_output": stringPointer("2"), "cost_cache_read": stringPointer("3"), "cost_cache_write": stringPointer("4")},
			"a": {"vendor": stringPointer("acme"), "order": stringPointer("1"), "cost_input": stringPointer("1"), "cost_output": stringPointer("2"), "cost_cache_read": stringPointer("3"), "cost_cache_write": stringPointer("4")},
		}}
		got := NormalizeLegacyProviderModels(input, nil)
		if got.Canonical == nil || !reflect.DeepEqual(got.Canonical.ModelOrder, []string{"z", "a"}) {
			t.Errorf("supported [z,a] control failed: %#v %#v", got.Canonical, got.Differences)
		}
	})
}

func TestNormalizeLegacyProviderModelsRefusesZeroContextThatChangesPiFallback(t *testing.T) {
	piPack := shippedPiPack(t)
	oldPack, provider := loadLegacyPiProvider(t, "p", `"options":{"context_window":"500"},"endpoints":{"openai":{"base_url":"https://example.test/v1","wire_api":"openai-responses"}},"models":{"m":"m"},"model_options":{"m":{"vendor":"acme","context_window":"0","cost_input":"1","cost_output":"2","cost_cache_read":"3","cost_cache_write":"4"}}`)
	got := NormalizeLegacyProviderModels(provider, nil)
	if got.Canonical != nil || !hasProviderDifference(got.Differences, "model_options.m.context_window") {
		t.Errorf("authored zero context must be an explicit nonrepresentable difference: %#v", got)
	}
	if got.RestoreLegacyProviderModels(provider).ModelOptions["m"]["context_window"] == nil || *got.RestoreLegacyProviderModels(provider).ModelOptions["m"]["context_window"] != "0" {
		t.Errorf("refusal did not preserve original zero spelling")
	}
	legacyTable, err := packload.ComposeProviders(nil, []*packload.Pack{oldPack})
	if err != nil {
		t.Fatal(err)
	}
	ctx := luahook.DeriveCtx{BuiltInProviders: packload.BuiltInProvidersFor([]*packload.Pack{piPack}, "pi")}
	ctx.Tables = toDeriveTables(t, map[string]any{"providers": jsonx.Plain(legacyTable)})
	oldOutput := runShippedPi(t, piPack, "models", legacyTable, ctx)
	attempted := provider
	attempted.Models, attempted.ModelOptions = nil, nil
	attempted.ModelRows = map[string]packdecl.ProviderModel{"m": {Vendor: "acme", Context: 0, Price: completePrice(1, 2, 3, 4)}}
	attempted.ModelOrder = []string{"m"}
	_, outputs, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{attempted}, Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "models", Context: ctx}})
	if err != nil || len(outputs) != 1 {
		t.Fatalf("attempted typed pi render: %#v %v", outputs, err)
	}
	if reflect.DeepEqual(oldOutput, outputs[0].Value) {
		t.Errorf("actual pi zero→fallback behavior unexpectedly equal: old=%#v typed=%#v", oldOutput, outputs[0].Value)
	}
	if piRenderedContext(oldOutput) != "0" || piRenderedContext(outputs[0].Value) != "500" {
		t.Errorf("expected actual shipped pi context 0→500 fallback, got old=%#v typed=%#v", oldOutput, outputs[0].Value)
	}
}

func TestNormalizeLegacyProviderModelsUsesPiExactReasoningGrammar(t *testing.T) {
	piPack := shippedPiPack(t)
	for _, spelling := range []string{"1", "TRUE", "t", "true", "false"} {
		t.Run(spelling, func(t *testing.T) {
			extra := `"endpoints":{"openai":{"base_url":"https://example.test/v1","wire_api":"openai-responses"}},"models":{"m":"m"},"model_options":{"m":{"vendor":"acme","reasoning":"` + spelling + `","cost_input":"1","cost_output":"2","cost_cache_read":"3","cost_cache_write":"4"}}`
			oldPack, provider := loadLegacyPiProvider(t, "p", extra)
			got := NormalizeLegacyProviderModels(provider, nil)
			oldTable, err := packload.ComposeProviders(nil, []*packload.Pack{oldPack})
			if err != nil {
				t.Fatal(err)
			}
			ctx := luahook.DeriveCtx{BuiltInProviders: packload.BuiltInProvidersFor([]*packload.Pack{piPack}, "pi")}
			ctx.Tables = toDeriveTables(t, map[string]any{"providers": jsonx.Plain(oldTable)})
			oldOutput := runShippedPi(t, piPack, "models", oldTable, ctx)
			if spelling == "1" || spelling == "TRUE" || spelling == "t" {
				if got.Canonical != nil || !hasProviderDifference(got.Differences, "model_options.m.reasoning") {
					t.Errorf("unsupported Pi boolean spelling %q lacks explicit difference: %#v", spelling, got)
				}
				if got.RestoreLegacyProviderModels(provider).ModelOptions["m"]["reasoning"] == nil || *got.RestoreLegacyProviderModels(provider).ModelOptions["m"]["reasoning"] != spelling {
					t.Errorf("raw reasoning spelling was not restored")
				}
				attempted := provider
				attempted.Models, attempted.ModelOptions = nil, nil
				b := true
				attempted.ModelRows = map[string]packdecl.ProviderModel{"m": {Vendor: "acme", Reasoning: &b, Price: completePrice(1, 2, 3, 4)}}
				attempted.ModelOrder = []string{"m"}
				_, outputs, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{attempted}, Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "models", Context: ctx}})
				if err != nil || len(outputs) != 1 {
					t.Fatalf("attempted bool render: %#v %v", outputs, err)
				}
				if reflect.DeepEqual(oldOutput, outputs[0].Value) || piRenderedReasoning(oldOutput) != nil || piRenderedReasoning(outputs[0].Value) != true {
					t.Errorf("actual pi boolean grammar difference missing: old=%#v projected=%#v", oldOutput, outputs[0].Value)
				}
			} else {
				if got.Canonical == nil || len(got.Differences) != 0 {
					t.Errorf("exact Pi boolean %q should remain supported: %#v", spelling, got.Differences)
				}
				input := ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{*got.Canonical}, Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}
				_, outputs, err := ProjectProviderSemantics(input, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "models", Context: ctx}})
				if err != nil || len(outputs) != 1 {
					t.Fatalf("supported boolean render: %#v %v", outputs, err)
				}
				if !reflect.DeepEqual(oldOutput, outputs[0].Value) {
					t.Errorf("exact Pi boolean %q changed owner output: old=%#v typed=%#v", spelling, oldOutput, outputs[0].Value)
				}
			}
		})
	}
}

func TestNormalizeLegacyProviderModelsRefusesPiUnconsumedPositiveIntegerAliases(t *testing.T) {
	for _, tc := range []struct {
		key          string
		outputField  string
		oldListValue string
		oldListFound bool
	}{
		{key: "max_context_tokens", outputField: "contextWindow", oldListValue: "500", oldListFound: true},
		{key: "max_output_tokens", outputField: "maxTokens", oldListFound: false},
	} {
		t.Run(tc.key+"/normalization", func(t *testing.T) {
			_, provider := legacyPiIntegerAliasFixture(t, tc.key, "123")
			got := NormalizeLegacyProviderModels(provider, nil)
			if got.Canonical != nil {
				t.Errorf("unconsumed alternate %s produced canonical facts: %#v", tc.key, got.Canonical.ModelRows)
			}
			if !hasProviderDifference(got.Differences, "model_options."+piLegacyTargetID+"."+tc.key) {
				t.Errorf("alternate field difference must identify its exact authored spelling: %#v", got.Differences)
			}
		})
		t.Run(tc.key+"/restore", func(t *testing.T) {
			_, provider := legacyPiIntegerAliasFixture(t, tc.key, "123")
			got := NormalizeLegacyProviderModels(provider, nil)
			restored := got.RestoreLegacyProviderModels(provider)
			if value := restored.ModelOptions[piLegacyTargetID][tc.key]; value == nil || *value != "123" || got.Snapshot.ModelOptions[piLegacyTargetID][tc.key] == nil || *got.Snapshot.ModelOptions[piLegacyTargetID][tc.key] != "123" {
				t.Errorf("alternate spelling did not survive snapshot/restore: snapshot=%#v restored=%#v", got.Snapshot.ModelOptions, restored.ModelOptions)
			}
		})
		for _, surface := range []string{"models", "model-lists"} {
			t.Run(tc.key+"/shipped-pi-"+surface, func(t *testing.T) {
				assertLegacyPiAlternateDifference(t, tc.key, tc.outputField, tc.oldListValue, tc.oldListFound, "123", surface)
			})
		}
	}
}

func TestNormalizeLegacyProviderModelsSupportsEquivalentPrimaryIntegerSpellings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts string
	}{
		{name: "primary fields", facts: `"context_window":"123","max_tokens":"123"`},
		{name: "alternate plus equal primary", facts: `"context_window":"123","max_context_tokens":"123","max_tokens":"123","max_output_tokens":"123"`},
	} {
		t.Run(tc.name+"/normalization", func(t *testing.T) {
			_, provider := legacyPiIntegerFactFixture(t, tc.facts)
			got := NormalizeLegacyProviderModels(provider, nil)
			if got.Canonical == nil || len(got.Differences) != 0 {
				t.Errorf("consumed primary/equivalent integer facts should remain supported: %#v", got.Differences)
			}
		})
		t.Run(tc.name+"/actual-pi", func(t *testing.T) {
			assertLegacyPiEquivalentFacts(t, tc.facts)
		})
	}
}

func TestNormalizeLegacyProviderModelsRefusesZeroIntegerFieldsBeforePiFallback(t *testing.T) {
	for _, tc := range []struct {
		key         string
		primary     string
		outputField string
	}{
		{key: "context_window", primary: "context_window", outputField: "contextWindow"},
		{key: "max_context_tokens", primary: "context_window", outputField: "contextWindow"},
		{key: "max_tokens", primary: "max_tokens", outputField: "maxTokens"},
		{key: "max_output_tokens", primary: "max_tokens", outputField: "maxTokens"},
	} {
		t.Run(tc.key+"/normalization", func(t *testing.T) {
			_, provider := legacyPiIntegerAliasFixture(t, tc.key, "0")
			got := NormalizeLegacyProviderModels(provider, nil)
			if got.Canonical != nil {
				t.Errorf("authored zero %s produced canonical facts: %#v", tc.key, got.Canonical.ModelRows)
			}
			if !hasProviderDifference(got.Differences, "model_options."+piLegacyTargetID+"."+tc.key) {
				t.Errorf("zero refusal must name authored field %s: %#v", tc.key, got.Differences)
			}
		})
		t.Run(tc.key+"/restore", func(t *testing.T) {
			_, provider := legacyPiIntegerAliasFixture(t, tc.key, "0")
			got := NormalizeLegacyProviderModels(provider, nil)
			restored := got.RestoreLegacyProviderModels(provider)
			if value := restored.ModelOptions[piLegacyTargetID][tc.key]; value == nil || *value != "0" {
				t.Errorf("raw zero %s did not restore: %#v", tc.key, restored.ModelOptions)
			}
		})
		t.Run(tc.key+"/shipped-pi-zero-to-fallback", func(t *testing.T) {
			assertLegacyPiZeroFallback(t, tc.key, tc.primary, tc.outputField)
		})
	}
}

const piLegacyTargetID = "global.openai.gpt-6-luna"
const piLegacyOtherID = "global.openai.gpt-6-astral"

func legacyPiIntegerAliasFixture(t *testing.T, key, value string) (*packload.Pack, packdecl.ProviderContribution) {
	t.Helper()
	return legacyPiIntegerFactFixture(t, `"`+key+`":"`+value+`"`)
}

func legacyPiIntegerFactFixture(t *testing.T, integerFacts string) (*packload.Pack, packdecl.ProviderContribution) {
	return legacyPiIntegerProviderFixture(t, "openai", piLegacyTargetID, piLegacyOtherID, integerFacts)
}

func legacyPiIntegerProviderFixture(t *testing.T, providerName, targetID, otherID, integerFacts string) (*packload.Pack, packdecl.ProviderContribution) {
	t.Helper()
	fields := `"options":{"context_window":"500","max_tokens":"500"},"endpoints":{"openai":{"base_url":"https://example.test/v1","wire_api":"openai-responses"}},"models":{"` + targetID + `":"` + targetID + `","` + otherID + `":"` + otherID + `"},"model_options":{"` + targetID + `":{"vendor":"openai","name":"GPT-6 Luna","wire_id":"` + targetID + `",` + integerFacts + `,"cost_input":"1","cost_output":"2","cost_cache_read":"3","cost_cache_write":"4"},"` + otherID + `":{"vendor":"openai","name":"GPT-6 Astral","wire_id":"` + otherID + `","context_window":"100","max_tokens":"50","cost_input":"1","cost_output":"2","cost_cache_read":"3","cost_cache_write":"4"}}`
	return loadLegacyPiProvider(t, providerName, fields)
}

func legacyPiNarrowedProviderTable(t *testing.T, provider *packload.Pack, providerName, targetID string) *jsonx.OrderedMap {
	t.Helper()
	root := t.TempDir()
	manifest := `{"contributes":[{"kind":"models","provider":"` + providerName + `","only":["` + targetID + `"]}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0600); err != nil {
		t.Fatalf("write models-only pack: %v", err)
	}
	narrow, problems := packload.LoadDir(root, "pi-models-only")
	if narrow == nil || len(problems) != 0 {
		t.Fatalf("load models-only pack: %#v", problems)
	}
	table, err := packload.ComposeProviders(nil, []*packload.Pack{provider, narrow})
	if err != nil {
		t.Fatalf("old packload.ComposeProviders with only contribution: %v", err)
	}
	entry, ok := table.Get(providerName)
	if !ok {
		t.Fatalf("composed old provider table omits %s: %#v", providerName, table)
	}
	providerEntry, ok := entry.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("old openai entry is %T", entry)
	}
	models, ok := providerEntry.Get("models")
	modelMap, mapOK := models.(*jsonx.OrderedMap)
	if !ok || !mapOK || !reflect.DeepEqual(modelMap.Keys(), []string{targetID}) {
		t.Fatalf("old models-only contribution did not narrow old table: %#v", models)
	}
	if only, ok := providerEntry.Get(packload.ModelsOnlyKey); !ok || only != true {
		t.Fatalf("old composed table lacks models-only presence: %#v", providerEntry)
	}
	return table
}

func unsafeTypedIntegerAttempt(provider packdecl.ProviderContribution, key, value, targetID string) packdecl.ProviderContribution {
	provider.Models, provider.ModelOptions = nil, nil
	row := packdecl.ProviderModel{Vendor: "openai", Name: "GPT-6 Luna", WireID: targetID, Price: completePrice(1, 2, 3, 4)}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	switch key {
	case "context_window", "max_context_tokens":
		row.Context = parsed
	case "max_tokens", "max_output_tokens":
		row.MaxTokens = parsed
	}
	provider.ModelRows = map[string]packdecl.ProviderModel{targetID: row}
	provider.ModelOrder = []string{targetID}
	return provider
}

func assertLegacyPiAlternateDifference(t *testing.T, key, field, oldListValue string, oldListFound bool, value, surface string) {
	t.Helper()
	providerName, targetID, otherID := "openai", piLegacyTargetID, piLegacyOtherID
	if surface == "models" {
		providerName, targetID, otherID = "p", "m", "n"
	}
	piPack := shippedPiPack(t)
	oldPack, provider := legacyPiIntegerProviderFixture(t, providerName, targetID, otherID, `"`+key+`":"`+value+`"`)
	oldTable := legacyPiNarrowedProviderTable(t, oldPack, providerName, targetID)
	ctx := luahook.DeriveCtx{BuiltInProviders: packload.BuiltInProvidersFor([]*packload.Pack{piPack}, "pi"), Tables: toDeriveTables(t, map[string]any{"providers": jsonx.Plain(oldTable)})}
	oldOutput := runShippedPi(t, piPack, surface, oldTable, ctx)
	attempt := unsafeTypedIntegerAttempt(provider, key, value, targetID)
	input := ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{attempt}, Selection: ActiveProviderSelection{Default: []string{providerName}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}
	_, outputs, err := ProjectProviderSemantics(input, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: surface, Context: ctx}})
	if err != nil || len(outputs) != 1 {
		t.Errorf("public ProjectProviderSemantics %s attempt: outputs=%#v err=%v", surface, outputs, err)
		return
	}
	oldValue, oldFound := piRenderedModelField(oldOutput, targetID, field)
	newValue, newFound := piRenderedModelField(outputs[0].Value, targetID, field)
	if !newFound || (surface == "models" && !oldFound) {
		t.Errorf("actual shipped pi %s row field %s missing: old=%#v new=%#v", surface, field, oldOutput, outputs[0].Value)
		return
	}
	if surface == "model-lists" {
		if oldFound != oldListFound || (oldListFound && fmt.Sprint(oldValue) != oldListValue) || fmt.Sprint(newValue) != value {
			t.Errorf("narrowed Pi model-lists did not expose %s legacy fallback/absence→typed difference: old=%v (found=%t) new=%v; outputs old=%#v new=%#v", key, oldValue, oldFound, newValue, oldOutput, outputs[0].Value)
		}
	} else if fmt.Sprint(oldValue) != value || fmt.Sprint(newValue) != value {
		t.Errorf("Pi models consumed %s differently than its authored value: old=%v new=%v; outputs old=%#v new=%#v", key, oldValue, newValue, oldOutput, outputs[0].Value)
	}
}

func assertLegacyPiEquivalentFacts(t *testing.T, integerFacts string) {
	t.Helper()
	for _, surface := range []string{"models", "model-lists"} {
		providerName, targetID, otherID := "openai", piLegacyTargetID, piLegacyOtherID
		if surface == "models" {
			providerName, targetID, otherID = "p", "m", "n"
		}
		piPack := shippedPiPack(t)
		oldPack, provider := legacyPiIntegerProviderFixture(t, providerName, targetID, otherID, integerFacts)
		oldTable := legacyPiNarrowedProviderTable(t, oldPack, providerName, targetID)
		normalized := NormalizeLegacyProviderModels(provider, nil)
		if normalized.Canonical == nil {
			t.Errorf("primary/equivalent fields have no canonical candidate: %#v", normalized.Differences)
			continue
		}
		canonical := *normalized.Canonical
		canonical.ModelRows = map[string]packdecl.ProviderModel{targetID: canonical.ModelRows[targetID]}
		canonical.ModelOrder = []string{targetID}
		ctx := luahook.DeriveCtx{BuiltInProviders: packload.BuiltInProvidersFor([]*packload.Pack{piPack}, "pi"), Tables: toDeriveTables(t, map[string]any{"providers": jsonx.Plain(oldTable)})}
		oldOutput := runShippedPi(t, piPack, surface, oldTable, ctx)
		_, outputs, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{canonical}, Selection: ActiveProviderSelection{Default: []string{providerName}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: surface, Context: ctx}})
		if err != nil || len(outputs) != 1 {
			t.Errorf("ProjectProviderSemantics %s parity attempt: %#v %v", surface, outputs, err)
			continue
		}
		if !reflect.DeepEqual(oldOutput, outputs[0].Value) {
			t.Errorf("old ComposeProviders→shipped pi %s differs for consumed exact/equivalent facts: old=%#v projected=%#v", surface, oldOutput, outputs[0].Value)
		}
	}
}

func assertLegacyPiZeroFallback(t *testing.T, key, primary, field string) {
	t.Helper()
	piPack := shippedPiPack(t)
	oldPack, provider := legacyPiIntegerProviderFixture(t, "p", "m", "n", `"`+key+`":"0"`)
	oldTable := legacyPiNarrowedProviderTable(t, oldPack, "p", "m")
	ctx := luahook.DeriveCtx{BuiltInProviders: packload.BuiltInProvidersFor([]*packload.Pack{piPack}, "pi"), Tables: toDeriveTables(t, map[string]any{"providers": jsonx.Plain(oldTable)})}
	oldOutput := runShippedPi(t, piPack, "models", oldTable, ctx)
	attempt := unsafeTypedIntegerAttempt(provider, primary, "0", "m")
	_, outputs, err := ProjectProviderSemantics(ProviderSemanticInput{Declarations: []packdecl.ProviderContribution{attempt}, Selection: ActiveProviderSelection{Default: []string{"p"}}, Receivers: ProviderReceivers([]*packload.Pack{piPack})}, []ProviderDeriveRequest{{Receiver: "pi", Pack: piPack, Surface: "models", Context: ctx}})
	if err != nil || len(outputs) != 1 {
		t.Errorf("public ProjectProviderSemantics zero %s fallback attempt: %#v %v", key, outputs, err)
		return
	}
	oldValue, oldFound := piRenderedModelField(oldOutput, "m", field)
	newValue, newFound := piRenderedModelField(outputs[0].Value, "m", field)
	if !oldFound || !newFound || fmt.Sprint(oldValue) != "0" || fmt.Sprint(newValue) != "500" {
		t.Errorf("actual Pi zero→provider fallback for %s = old(%v,%t), typed(%v,%t), outputs old=%#v new=%#v", key, oldValue, oldFound, newValue, newFound, oldOutput, outputs[0].Value)
	}
}

func piRenderedModelField(output map[string]any, id, field string) (any, bool) {
	var search func(any) (any, bool)
	search = func(value any) (any, bool) {
		switch node := value.(type) {
		case map[string]any:
			if node["id"] == id {
				result, ok := node[field]
				return result, ok
			}
			for _, child := range node {
				if result, ok := search(child); ok {
					return result, true
				}
			}
		case []any:
			for _, child := range node {
				if result, ok := search(child); ok {
					return result, true
				}
			}
		}
		return nil, false
	}
	return search(output)
}

func hasProviderDifference(differences []ProviderModelDifference, path string) bool {
	for _, difference := range differences {
		if difference.Path == path {
			return true
		}
	}
	return false
}

func shippedPiPack(t *testing.T) *packload.Pack {
	t.Helper()
	for _, pack := range packload.Embedded() {
		if pack.Name == "pi" {
			t.Cleanup(packload.ReleaseEmbedded)
			if packload.DeriveScript(pack) == "" {
				t.Fatal("shipped pi derive is missing")
			}
			return pack
		}
	}
	t.Fatal("shipped pi pack missing")
	return nil
}

func loadLegacyPiProvider(t *testing.T, name, providerFields string) (*packload.Pack, packdecl.ProviderContribution) {
	t.Helper()
	root := t.TempDir()
	quotedName, err := json.Marshal(name)
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"contributes":[{"kind":"provider","name":` + string(quotedName) + `,` + providerFields + `}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(root, "legacy-fixture")
	if pack == nil || len(problems) > 0 {
		t.Fatalf("load old provider fixture: pack=%#v problems=%#v manifest=%s", pack, problems, manifest)
	}
	providers := pack.Decl.Providers()
	if len(providers) != 1 {
		t.Fatalf("legacy provider declarations=%#v", providers)
	}
	return pack, providers[0]
}

func runShippedPi(t *testing.T, pack *packload.Pack, surface string, providers *jsonx.OrderedMap, context luahook.DeriveCtx) map[string]any {
	t.Helper()
	context.Agent, context.Surface = "pi", surface
	context.Tables = toDeriveTables(t, map[string]any{"providers": jsonx.Plain(providers)})
	output, err := (luahook.GopherLuaVM{}).Derive(packload.DeriveScript(pack), &context)
	if err != nil {
		t.Fatalf("shipped pi %s derive: %v", surface, err)
	}
	return output
}

func piRenderedContext(output map[string]any) string {
	providers, _ := output["providers"].(map[string]any)
	for _, provider := range providers {
		entry, _ := provider.(map[string]any)
		models, _ := entry["models"].([]any)
		for _, model := range models {
			row, _ := model.(map[string]any)
			if row["id"] == "m" {
				return fmt.Sprint(row["contextWindow"])
			}
		}
	}
	return ""
}

func piRenderedReasoning(output map[string]any) any {
	providers, _ := output["providers"].(map[string]any)
	for _, provider := range providers {
		entry, _ := provider.(map[string]any)
		models, _ := entry["models"].([]any)
		for _, model := range models {
			row, _ := model.(map[string]any)
			if row["id"] == "m" {
				return row["reasoning"]
			}
		}
	}
	return nil
}

func stringPointer(value string) *string { return &value }
func float64p(value float64) *float64    { return &value }

func toDeriveTables(t *testing.T, values map[string]any) map[string]map[string]any {
	t.Helper()
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for k, v := range decoded {
		if row, ok := v.(map[string]any); ok {
			out[k] = row
		}
	}
	return out
}
