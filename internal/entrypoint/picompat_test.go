package entrypoint

// picompat_test.go pins the PROVIDER-DECLARED compat facts (roadmap 📦 row 2, ruled
// 2026-09-18): what a local server does and does not support is a SERVICE fact, so the
// provider states it and each agent's derive translates it — the OQ-CS4 shape, where the
// provider declares the knob and the consumer decides what it means.
//
// Two rejected options are what these tests actually defend, and each has a case here:
//
//   - "every loopback URL gets the facts" — DETECTION. It assumes every local server is
//     llama.cpp-shaped and silently downgrades one that is not, which
//     docs/research/local-model-endpoints.md names a live example of (bedrock-mantle is a
//     working local provider). TestPiDeriveEmitsNoCompatForALoopbackProviderThatDeclaresNone
//     is the pin.
//   - "only llamacpp's provider" — a hardcoded VENDOR in an agent's derive, the shape
//     packs/pi/derive.lua already carries once for Kilo and that roadmap row 27 exists to
//     rule on. TestPiDeriveTranslatesCompatFactsForAnyProviderThatDeclaresThem runs the
//     whole block through a provider named nothing like llama.cpp at a URL that is not
//     local.
//
// The derives run through the REAL packs — packs/llamacpp's manifest for the facts,
// packs/pi's derive.lua for the translation — and the end-to-end case runs the BOOT entry
// (ConfigurePackSurfaces) rather than the derive alone, so deleting the call site in the
// models derive turns it red instead of leaving it measuring a function nothing invokes.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// llamaCompatWire is the block pi reads, spelled the way it must reach models.json: pi's
// OWN field names (ProviderCompatSchema's openai-completions member,
// pi-coding-agent/dist/core/model-config.js) carrying JSON BOOLEANS.
//
// THE TYPE IS HALF THE ASSERTION. A yolo option value is always a string, so these facts
// arrive at the derive as "true"/"false"; emitting them unconverted would put the STRING
// "false" in models.json, and every non-empty string is truthy in JavaScript — pi would
// read a disabled capability as enabled, with a config that validates and a jail that
// boots green. That is the same silent-no-op class as a misspelled field name, and it is
// why the comparison below is on typed values rather than on fmt output.
var llamaCompatWire = map[string]any{
	"supportsStore":            false,
	"supportsDeveloperRole":    false,
	"supportsReasoningEffort":  false,
	"supportsUsageInStreaming": true,
	"supportsStrictMode":       false,
	"maxTokensField":           "max_tokens",
}

// llamaCompatOptions is the same six facts in the CANONICAL spelling a provider declares
// them in — snake_case option names with string values, the only shape an options map may
// hold. It is what packs/llamacpp/pack.json ships and what a user writes for ollama, vLLM
// or any other local server of their own.
var llamaCompatOptions = map[string]any{
	"supports_store":              "false",
	"supports_developer_role":     "false",
	"supports_reasoning_effort":   "false",
	"supports_usage_in_streaming": "true",
	"supports_strict_mode":        "false",
	"max_tokens_field":            "max_tokens",
}

// requireCompat compares one provider entry's compat block against want, field by field,
// so a failure names the flag rather than printing two maps.
func requireCompat(t *testing.T, where string, entry map[string]any, want map[string]any) {
	t.Helper()
	got, ok := entry["compat"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no compat block — the declared facts did not reach pi: %#v", where, entry)
	}
	for field, wantValue := range want {
		gotValue, present := got[field]
		if !present {
			t.Errorf("%s: compat.%s is missing. pi's compat schemas set no "+
				"additionalProperties, so a field pi does not read is accepted silently — "+
				"check the spelling against OpenAICompletionsCompatSchema", where, field)
			continue
		}
		if gotValue != wantValue {
			t.Errorf("%s: compat.%s = %#v, want %#v (a %T where pi wants a %T reads as the "+
				"wrong capability, not as an error)", where, field, gotValue, wantValue,
				gotValue, wantValue)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s: compat = %#v, want exactly %d fields — a flag nothing declared is a "+
			"fact yolo invented", where, got, len(want))
	}
}

// piModelsFor runs the real pi models derive over one providers table.
func piModelsFor(t *testing.T, sel surfaceSelection, e *Env, providers map[string]any) map[string]any {
	t.Helper()
	script, s := deriveSurface(t, "pi", "pi/models")
	got, err := deriveComputedLayer(e, s, script, sel, map[string]map[string]any{
		manifest.SourceProviders: providers,
	})
	if err != nil {
		t.Fatal(err)
	}
	provs, ok := got["providers"].(map[string]any)
	if !ok {
		t.Fatalf("pi/models produced no providers table: %#v", got)
	}
	return provs
}

// TestPiDeriveTranslatesCompatFactsForAnyProviderThatDeclaresThem is the ruling's
// positive half, and it is deliberately NOT llama.cpp: the provider is named `mantle`, its
// URL is neither loopback nor llama-server's port, and it still gets the whole block —
// because it DECLARED it. A derive that gated on a vendor name or a local address would
// emit nothing here.
//
// The neighbour proves the same run is selective rather than unconditional: `hosted`
// reaches pi through the identical code path, declares no compat facts, and must carry no
// compat key at all.
func TestPiDeriveTranslatesCompatFactsForAnyProviderThatDeclaresThem(t *testing.T) {
	provs := piModelsFor(t, surfaceSelection{}, &Env{Vars: map[string]string{}}, map[string]any{
		"mantle": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://mantle.internal.example/v1"}},
			"models":  map[string]any{"default": "mantle-1"},
			"options": llamaCompatOptions,
		},
		"hosted": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://api.example/v1"}},
			"models": map[string]any{"default": "big"},
		},
	})
	requireCompat(t, "mantle", provs["mantle"].(map[string]any), llamaCompatWire)
	if _, present := provs["hosted"].(map[string]any)["compat"]; present {
		t.Errorf("a provider that declares no compat facts must get no compat key — "+
			"stating facts nobody measured is the downgrade this ruling refuses: %#v",
			provs["hosted"])
	}
}

// TestPiDeriveEmitsNoCompatForALoopbackProviderThatDeclaresNone refutes the option the
// ruling rejected. The URL is 127.0.0.1 on llama-server's own default port and the entry
// is otherwise the shape a local server takes — it even gets the unkeyed-local `apiKey`
// treatment, which IS address-derived, so the absence of a compat block here is a fact
// about declaration rather than about the derive never having looked at the URL.
func TestPiDeriveEmitsNoCompatForALoopbackProviderThatDeclaresNone(t *testing.T) {
	provs := piModelsFor(t, surfaceSelection{}, &Env{Vars: map[string]string{}}, map[string]any{
		"local": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "http://127.0.0.1:8080/v1"}},
			"models":  map[string]any{"default": "qwen"},
			"options": map[string]any{"context_window": "32768"},
		},
	})
	entry := provs["local"].(map[string]any)
	if entry["apiKey"] != "local" {
		t.Fatalf("the local-address path did not run, so this case proves nothing: %#v", entry)
	}
	if _, present := entry["compat"]; present {
		t.Errorf("a loopback URL is not a declaration. Assuming every local server is "+
			"llama.cpp-shaped silently downgrades one that is not: %#v", entry["compat"])
	}
}

// TestPiDeriveCompatCarriesOnlyTheFactsDeclared pins the two ways a provider states less
// than everything, because both have to reach pi as SILENCE rather than as a guess.
//
// A partially-declared provider gets exactly its own facts — pi's default for an absent
// flag is the permissive one, and filling the gaps with `false` would turn capabilities
// off on a server nobody measured.
//
// A value that is not a JSON boolean spelling is UNDECLARED, not false, for the same
// reason: `"yes"` is a typo, and the two readings of a typo are "emit nothing" and "emit
// the wrong capability". This is the disposition tonumber already gives an unparseable
// context_window.
func TestPiDeriveCompatCarriesOnlyTheFactsDeclared(t *testing.T) {
	provs := piModelsFor(t, surfaceSelection{}, &Env{Vars: map[string]string{}}, map[string]any{
		"partial": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "http://127.0.0.1:8080/v1"}},
			"options": map[string]any{
				"supports_reasoning_effort": "false",
				"max_tokens_field":          "max_completion_tokens",
			},
		},
		"typo": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "http://127.0.0.1:8081/v1"}},
			"options": map[string]any{
				"supports_store":          "yes",
				"supports_developer_role": "false",
			},
		},
	})
	requireCompat(t, "partial", provs["partial"].(map[string]any), map[string]any{
		"supportsReasoningEffort": false,
		"maxTokensField":          "max_completion_tokens",
	})
	requireCompat(t, "typo", provs["typo"].(map[string]any), map[string]any{
		"supportsDeveloperRole": false,
	})
}

// TestPiDeriveCompatFallsBackToTheActiveProfile pins the second source a declared option
// resolves from — the profile active at pi's CLI name, read for the provider that profile
// selects, which is the fallback the context-window and max-tokens reads beside it already
// take. A provider that declares the facts nowhere and a profile that carries them is the
// shape a user gets when they state the facts on the profile rather than on the provider.
func TestPiDeriveCompatFallsBackToTheActiveProfile(t *testing.T) {
	e := &Env{Vars: map[string]string{
		"YOLO_PROFILES": `{"local":{"provider":"local",` +
			`"supports_store":"false","max_tokens_field":"max_tokens"}}`,
	}}
	provs := piModelsFor(t, surfaceSelection{Profile: "local", Provider: "local"}, e, map[string]any{
		"local": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "http://127.0.0.1:8080/v1"}},
			"models": map[string]any{"default": "qwen"},
		},
		// Not the selected provider, so the profile speaks for `local` only — a fallback
		// that leaked across providers would state one server's facts about another's.
		"other": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "http://127.0.0.1:8081/v1"}},
		},
	})
	requireCompat(t, "local", provs["local"].(map[string]any), map[string]any{
		"supportsStore":  false,
		"maxTokensField": "max_tokens",
	})
	if _, present := provs["other"].(map[string]any)["compat"]; present {
		t.Errorf("the active profile speaks for the provider it selects and no other: %#v",
			provs["other"])
	}
}

// TestShippedLlamacppCompatFactsReachPiModelsJSON is the end-to-end, and the two halves it
// joins are the point: packs/llamacpp/pack.json's DECLARATION and packs/pi/derive.lua's
// TRANSLATION, composed by the real provider composition and rendered by the BOOT entry
// (ConfigurePackSurfaces) into the file pi actually reads.
//
// Nothing here is a fixture. The providers table is composed from the shipped manifests,
// so a renamed option, a re-spelled pi field, a dropped emit in the models derive, or a
// declaration that never reaches the composed table each turn this red — where a test that
// called the derive with a hand-written table would stay green through three of the four.
//
// zai rides along as the selectivity half, and it too is a shipped manifest: it declares
// none of these options, so its catalog row must carry no compat key while llamacpp's
// carries the whole block. One render, both answers.
func TestShippedLlamacppCompatFactsReachPiModelsJSON(t *testing.T) {
	llamacpp, err := embeddedPack("llamacpp")
	if err != nil {
		t.Fatalf("embedded llamacpp: %v", err)
	}
	zai, err := embeddedPack("zai")
	if err != nil {
		t.Fatalf("embedded zai: %v", err)
	}
	composed, err := packload.ComposeProviders(nil, []*packload.Pack{llamacpp, zai})
	if err != nil {
		t.Fatalf("composing the shipped providers: %v", err)
	}
	providersJSON, err := jsonx.DumpsCompact(composed)
	if err != nil {
		t.Fatalf("encoding the composed table: %v", err)
	}

	r := newPioencodeRender(t, providersJSON)
	r.wireProfiles(`{"llamacpp":{"provider":"llamacpp","model":"default"}}`)
	r.render(t, `{"pi":"llamacpp"}`)

	provs, ok := r.piModels(t)["providers"].(map[string]any)
	if !ok {
		t.Fatalf("models.json has no providers table: %#v", r.piModels(t))
	}
	entry, ok := provs["llamacpp"].(map[string]any)
	if !ok {
		t.Fatalf("models.json has no llamacpp row: %#v", provs)
	}
	requireCompat(t, "llamacpp", entry, llamaCompatWire)

	other, ok := provs["zai"].(map[string]any)
	if !ok {
		t.Fatalf("models.json has no zai row, so the selectivity half measures nothing: %#v", provs)
	}
	if _, present := other["compat"]; present {
		t.Errorf("zai declares no compat facts and must get none — the facts are the "+
			"provider's, not every provider's: %#v", other["compat"])
	}
}
