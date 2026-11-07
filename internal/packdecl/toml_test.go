package packdecl

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestDecodeProviderTablePreservesMixedLegacyModelIDsWithoutMutatingInput(t *testing.T) {
	decoded, err := json5.Decode([]byte(`{"providers":{"p":{"models":{"base":"base-wire","row":{"context":200}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	root := decoded.(*jsonx.OrderedMap)
	providerTable, err := DecodeProviderTable(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(providerTable.Declarations) != 1 {
		t.Fatalf("declarations = %#v", providerTable.Declarations)
	}
	provider := providerTable.Declarations[0]
	if provider.Models["base"] != "base-wire" || provider.ModelRows["row"].Context != 200 ||
		!reflect.DeepEqual(provider.ModelOrder, []string{"row"}) {
		t.Fatalf("mixed legacy and canonical model facts = %#v", provider)
	}
	providersRaw, _ := root.Get("providers")
	providers := providersRaw.(*jsonx.OrderedMap)
	entryRaw, _ := providers.Get("p")
	entry := entryRaw.(*jsonx.OrderedMap)
	modelsRaw, _ := entry.Get("models")
	models := modelsRaw.(*jsonx.OrderedMap)
	if base, _ := models.Get("base"); base != "base-wire" {
		t.Fatalf("decoder mutated caller-owned model table: %#v", models)
	}
}

func TestDecodeProviderTableKeepsCanonicalOrderAndTombstones(t *testing.T) {
	decoded, err := json5.Decode([]byte(`{"providers":{"disabled":"off","local":{"via":"wire-bridge","every_model":{"api":"openai-responses","context":100},"models":{"zulu":{"context":200},"alpha":{"twin_of":"zulu"}},"variants":{"zeta":{"via":"wire-bridge"},"retired":"off","alpha":{"model":"variant"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	root, ok := decoded.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("root = %T", decoded)
	}
	got, err := DecodeProviderTable(root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Disabled, []string{"disabled"}) ||
		!reflect.DeepEqual(got.DisabledVariants["local"], []string{"retired"}) {
		t.Fatalf("provider tombstones = %#v", got)
	}
	if len(got.Declarations) != 1 {
		t.Fatalf("declarations = %#v", got.Declarations)
	}
	provider := got.Declarations[0]
	if provider.Name != "local" || provider.Via != "wire-bridge" || provider.EveryModel == nil ||
		!reflect.DeepEqual(provider.ModelOrder, []string{"zulu", "alpha"}) ||
		!reflect.DeepEqual(provider.VariantOrder, []string{"zeta", "alpha"}) {
		t.Fatalf("decoded provider = %#v", provider)
	}
}

const groupedManifestFixture = `name = "sample"
description = "grouped manifest"
needs = [{ pack = "wire-bridge" }]

[[program]]
bin = "pi"
via = "npm"
package = "@example/pi"

[[overlay]]
surface = "pi/settings"
set.theme = "dark"
replace = true

[providers.meta]
alias = "muse"
model = "muse-spark-1.3-contributor"
replace = true
[providers.meta.every_model]
api = "openai-responses"
thinking = ["low", "high"]
accepts = ["text", "image"]
max_tokens = 128_000
reasoning = true
context = 128_000
price = { input = 0.1, output = 0.2, cache_read = 0.002, cache_write = 0.0 }
[providers.meta.models."muse-spark-1.3-contributor"]
name = "Spark 1.3"
description = "An explicit catalog description"
vendor = "muse"
base = "muse-spark-base"
wire_id = "muse-spark-wire"
extends = "muse-shared"
openrouter_routing = { order = ["fast"], allow_fallbacks = false }
images = { max_width = 2000, max_height = 2000, max_bytes = 4_718_592, jpeg_quality = 80 }
price = { input = 0.1 }
[providers.meta.models."muse-wonder-1.0-contributor[200k]"]
twin_of = "muse-wonder-1.0-contributor"
compact_at = 200_000
[providers.meta.variants.fast]
model = "muse-spark-1.3-contributor"
options = { model = "spark", thinking = "high" }
subagents = { default = "muse-spark-1.3-contributor", roles = { worker = "muse-spark-1.3-contributor" } }
enforce_models = false
capabilities = ["web_search"]
protocols = ["openai"]
[providers.meta.variants.routed]
via = "wire-bridge"
`

func TestDecodeTOMLGroupedManifestNormalizesAccessorsAndMetadata(t *testing.T) {
	m, problems := DecodeTOML([]byte(groupedManifestFixture))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	if got, want := m.Name, "sample"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if got := len(m.DeclaredNeeds()); got != 1 {
		t.Fatalf("needs = %d, want 1", got)
	}
	programs := m.InstallContributions()
	if len(programs) != 1 || programs[0].Bin != "pi" {
		t.Fatalf("program accessor = %#v", programs)
	}
	overlays := m.ConfigOverlayContributions()
	if len(overlays) != 1 || overlays[0].Surface != "pi/settings" {
		t.Fatalf("overlay accessor = %#v", overlays)
	}
	if m.Origin.Format != ManifestFormatTOML || m.Origin.File != ManifestTOMLName {
		t.Fatalf("manifest origin = %#v", m.Origin)
	}
	contribs := m.Contributions()
	if len(contribs) != 3 || contribs[1].Origin.Format != ManifestFormatTOML {
		t.Fatalf("normalized contributions = %#v", contribs)
	}
	if !contribs[1].Replace {
		t.Fatal("explicit replace=true was not retained")
	}
	if !contribs[2].Replace {
		t.Fatal("provider replace=true was not retained")
	}
	if string(contribs[1].Raw) != `{"managed":{"theme":"dark"}}` {
		t.Fatalf("overlay set did not normalize to config managed body: %s", contribs[1].Raw)
	}
	if got := contribs[1].Location; got.File != ManifestTOMLName || got.Key != "overlay[0]" || got.Line != 10 {
		t.Fatalf("overlay location = %#v, want pack.toml:10 at overlay[0]", got)
	}
	providers := m.Providers()
	if len(providers) != 1 {
		t.Fatalf("providers = %#v", providers)
	}
	p := providers[0]
	if p.Name != "meta" || len(p.Aliases) != 1 || p.Aliases[0] != "muse" || p.Model != "muse-spark-1.3-contributor" {
		t.Fatalf("provider identity/default = %#v", p)
	}
	if p.EveryModel == nil || p.EveryModel.API != "openai-responses" || p.EveryModel.Context != 128_000 || p.EveryModel.MaxTokens != 128_000 || p.EveryModel.Reasoning == nil || !*p.EveryModel.Reasoning {
		t.Fatalf("every_model = %#v", p.EveryModel)
	}
	row := p.ModelRows["muse-spark-1.3-contributor"]
	if row.Name != "Spark 1.3" || row.Description != "An explicit catalog description" || row.Vendor != "muse" || row.Images == nil || row.Images.MaxWidth != 2000 {
		t.Fatalf("model row = %#v", row)
	}
	if row.Base != "muse-spark-base" || row.WireID != "muse-spark-wire" || row.Extends != "muse-shared" || row.OpenRouterRouting["order"] == nil {
		t.Fatalf("legacy model facts were dropped: %#v", row)
	}
	if len(p.ModelOrder) != 2 || p.ModelOrder[0] != "muse-spark-1.3-contributor" || p.ModelOrder[1] != "muse-wonder-1.0-contributor[200k]" {
		t.Fatalf("model source order = %v", p.ModelOrder)
	}
	if len(p.EveryModel.Accepts) != 2 || p.EveryModel.Accepts[0] != "text" || p.EveryModel.Price == nil || *p.EveryModel.Price.Input != 0.1 {
		t.Fatalf("model facts = %#v", row)
	}
	if got := p.ModelRows["muse-wonder-1.0-contributor[200k]"]; got.TwinOf != "muse-wonder-1.0-contributor" || got.CompactAt != 200_000 {
		t.Fatalf("twin model = %#v", got)
	}
	if len(p.Variants) != 2 || p.Variants["fast"].Model != "muse-spark-1.3-contributor" {
		t.Fatalf("variants = %#v", p.Variants)
	}
	variant := p.Variants["fast"]
	if variant.Options["model"].Value != "spark" || variant.Subagents["default"] != "muse-spark-1.3-contributor" || variant.EnforceModels == nil || *variant.EnforceModels || len(variant.Capabilities) != 1 || len(variant.Protocols) != 1 {
		t.Fatalf("variant profile fields = %#v", variant)
	}
	roles, ok := variant.Subagents["roles"].(map[string]any)
	if !ok || roles["worker"] != "muse-spark-1.3-contributor" {
		t.Fatalf("variant subagent roles = %#v", variant.Subagents["roles"])
	}
	if len(p.VariantOrder) != 2 || p.VariantOrder[0] != "fast" || p.VariantOrder[1] != "routed" {
		t.Fatalf("variant source order = %v", p.VariantOrder)
	}
}

func TestDecodeTOMLProviderModelAndVariantOrderFollowsHeaders(t *testing.T) {
	data := []byte(`[providers.meta.models.zulu]
name = "Zulu"
[providers.meta.models.alpha]
name = "Alpha"
[providers.meta.variants.zulu]
model = "zulu-model"
[providers.meta.variants.alpha]
model = "alpha-model"
`)
	m, problems := DecodeTOML(data)
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	providers := m.Providers()
	if len(providers) != 1 {
		t.Fatalf("providers = %#v, want one", providers)
	}
	if got, want := providers[0].ModelOrder, []string{"zulu", "alpha"}; !equalStrings(got, want) {
		t.Errorf("model declaration order = %v, want authored order %v", got, want)
	}
	if got, want := providers[0].VariantOrder, []string{"zulu", "alpha"}; !equalStrings(got, want) {
		t.Errorf("variant declaration order = %v, want authored order %v", got, want)
	}
}

func TestDecodeTOMLProviderMixedInlineAndHeaderOrder(t *testing.T) {
	data := []byte(`[providers.meta]
models.zulu = { name = "Zulu" }
variants.zulu = { model = "zulu-model" }
[providers.meta.models.alpha]
name = "Alpha"
[providers.meta.variants.alpha]
model = "alpha-model"
`)
	m, problems := DecodeTOML(data)
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	providers := m.Providers()
	if len(providers) != 1 {
		t.Fatalf("providers = %#v, want one", providers)
	}
	if got, want := providers[0].ModelOrder, []string{"zulu", "alpha"}; !equalStrings(got, want) {
		t.Errorf("mixed model declaration order = %v, want authored order %v", got, want)
	}
	if got, want := providers[0].VariantOrder, []string{"zulu", "alpha"}; !equalStrings(got, want) {
		t.Errorf("mixed variant declaration order = %v, want authored order %v", got, want)
	}
}

func TestDecodeTOMLDottedProviderRowsKeepAuthoredOrder(t *testing.T) {
	data := []byte(`[providers.meta]
models.z.name = "Z"
models.a.name = "A"
variants.z.model = "z"
variants.a.model = "a"
`)
	m, problems := DecodeTOML(data)
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	providers := m.Providers()
	if len(providers) != 1 {
		t.Fatalf("providers = %#v, want one", providers)
	}
	if got, want := providers[0].ModelOrder, []string{"z", "a"}; !equalStrings(got, want) {
		t.Errorf("dotted model declaration order = %v, want authored order %v", got, want)
	}
	if got, want := providers[0].VariantOrder, []string{"z", "a"}; !equalStrings(got, want) {
		t.Errorf("dotted variant declaration order = %v, want authored order %v", got, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDecodeTOMLProviderVariantProfileValidationMatchesConfig(t *testing.T) {
	decoders := []struct {
		name   string
		decode func([]byte) (*Manifest, []string, []string)
	}{
		{name: "strict", decode: func(data []byte) (*Manifest, []string, []string) {
			m, problems := DecodeTOML(data)
			return m, problems, nil
		}},
		{name: "use", decode: DecodeTOMLForUse},
		{name: "tolerant", decode: DecodeTOMLTolerant},
	}
	valid := []byte(`[providers.meta.variants.worker]
model = "fast"
via = "wire-bridge"
subagents.default = "fast"
subagents.roles.worker = "fast"
`)
	for _, decoder := range decoders {
		t.Run(decoder.name+" valid profile fields", func(t *testing.T) {
			m, problems, skipped := decoder.decode(valid)
			if len(problems) != 0 || len(skipped) != 0 || m == nil {
				t.Fatalf("valid variant: manifest=%#v problems=%v skipped=%v", m, problems, skipped)
			}
			providers := m.Providers()
			if len(providers) != 1 {
				t.Fatalf("providers = %#v, want one", providers)
			}
			variant := providers[0].Variants["worker"]
			if variant.Via != "wire-bridge" || variant.Subagents["default"] != "fast" {
				t.Fatalf("variant profile fields = %#v", variant)
			}
			roles, ok := variant.Subagents["roles"].(map[string]any)
			if !ok || roles["worker"] != "fast" {
				t.Fatalf("variant subagent roles = %#v", variant.Subagents["roles"])
			}
		})
	}

	for name, test := range map[string]struct {
		input string
		want  string
	}{
		"unknown subagents field": {input: `[providers.meta.variants.worker]
model = "fast"
subagents.default_model = "fast"
`, want: "subagents.default_model is unknown"},
		"non-string default": {input: `[providers.meta.variants.worker]
model = "fast"
subagents.default = 7
`, want: "subagents.default must be a non-empty string"},
		"invalid role model": {input: `[providers.meta.variants.worker]
model = "fast"
subagents.roles.worker = 7
`, want: "subagents.roles.worker must be a non-empty string"},
		"empty subagents": {input: `[providers.meta.variants.worker]
model = "fast"
subagents = {}
`, want: "subagents declares nothing"},
		"invalid via name": {input: `[providers.meta.variants.worker]
model = "fast"
via = "bad/name"
`, want: "variants.worker.via must be a valid pack name"},
	} {
		for _, decoder := range decoders {
			t.Run(decoder.name+" rejects "+name, func(t *testing.T) {
				m, problems, skipped := decoder.decode([]byte(test.input))
				joined := strings.Join(problems, "\n")
				if m != nil || !strings.Contains(joined, test.want) || len(skipped) != 0 {
					t.Fatalf("malformed profile shape: problems=%v skipped=%v, want %q", problems, skipped, test.want)
				}
			})
		}
	}
}

func TestDecodeTOMLVariantUnknownFieldKeepsReaderModePolicy(t *testing.T) {
	data := []byte(`[providers.meta.variants.fast]
model = "fast"
newer_field = true
`)
	if _, problems := DecodeTOML(data); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), `unknown provider model field "variants.fast.newer_field"`) {
		t.Fatalf("strict reader should refuse unknown variant field, got %v", problems)
	}
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){"use": DecodeTOMLForUse, "tolerant": DecodeTOMLTolerant} {
		m, problems, skipped := decode(data)
		if m == nil || len(problems) != 0 || len(skipped) != 1 || len(m.Providers()) != 0 {
			t.Errorf("%s reader: manifest=%#v problems=%v skipped=%v", name, m, problems, skipped)
		}
	}
}

func TestDecodeTOMLProviderViaReachesProvidersAccessor(t *testing.T) {
	decoders := []struct {
		name   string
		decode func([]byte) (*Manifest, []string, []string)
	}{
		{name: "strict", decode: func(data []byte) (*Manifest, []string, []string) {
			m, problems := DecodeTOML(data)
			return m, problems, nil
		}},
		{name: "use", decode: DecodeTOMLForUse},
		{name: "tolerant", decode: DecodeTOMLTolerant},
	}
	for _, decoder := range decoders {
		t.Run(decoder.name+" preserves provider via", func(t *testing.T) {
			m, problems, skipped := decoder.decode([]byte(`[providers.meta]
via = "wire-bridge"
`))
			if m == nil || len(problems) != 0 || len(skipped) != 0 {
				t.Fatalf("provider via: manifest=%#v problems=%v skipped=%v", m, problems, skipped)
			}
			providers := m.Providers()
			if len(providers) != 1 || providers[0].Via != "wire-bridge" {
				t.Fatalf("Providers() = %#v, want provider via wire-bridge", providers)
			}
		})
		t.Run(decoder.name+" rejects invalid provider via type", func(t *testing.T) {
			m, problems, skipped := decoder.decode([]byte(`[providers.meta]
via = 7
`))
			if m != nil || len(problems) == 0 || len(skipped) != 0 {
				t.Fatalf("invalid provider via: manifest=%#v problems=%v skipped=%v", m, problems, skipped)
			}
		})
		t.Run(decoder.name+" rejects invalid provider via name", func(t *testing.T) {
			m, problems, skipped := decoder.decode([]byte(`[providers.meta]
via = "bad/name"
`))
			joined := strings.Join(problems, "\n")
			if m != nil || !strings.Contains(joined, "providers.meta.via must be a valid pack name") || len(skipped) != 0 {
				t.Fatalf("invalid provider via name: problems=%v skipped=%v", problems, skipped)
			}
		})
	}
}

func TestDecodeTOMLProviderViaOmissionAndExplicitEmpty(t *testing.T) {
	decoders := []struct {
		name   string
		decode func([]byte) (*Manifest, []string, []string)
	}{
		{name: "strict", decode: func(data []byte) (*Manifest, []string, []string) {
			m, problems := DecodeTOML(data)
			return m, problems, nil
		}},
		{name: "use", decode: DecodeTOMLForUse},
		{name: "tolerant", decode: DecodeTOMLTolerant},
	}
	for _, decoder := range decoders {
		t.Run(decoder.name+" omitted", func(t *testing.T) {
			m, problems, skipped := decoder.decode([]byte("[providers.meta]\n"))
			if m == nil || len(problems) != 0 || len(skipped) != 0 {
				t.Fatalf("omitted provider via: problems=%v skipped=%v", problems, skipped)
			}
			providers := m.Providers()
			if len(providers) != 1 || providers[0].Via != "" {
				t.Fatalf("Providers() = %#v, want omitted optional via", providers)
			}
		})
		t.Run(decoder.name+" rejects explicit empty", func(t *testing.T) {
			m, problems, skipped := decoder.decode([]byte(`[providers.meta]
via = ""
`))
			joined := strings.Join(problems, "\n")
			if m != nil || !strings.Contains(joined, "providers.meta.via must be a valid pack name") || len(skipped) != 0 {
				t.Fatalf("explicit empty provider via: problems=%v skipped=%v", problems, skipped)
			}
		})
	}
}

func TestDecodeTOMLReadersKeepStrictUseAndTolerantContracts(t *testing.T) {
	newer := []byte("[[skills]]\nfrom = \"skills\"\ninto = \".sample/skills\"\nnewer_field = true\n")
	if _, problems := DecodeTOML(newer); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), `unknown field "newer_field"`) {
		t.Fatalf("strict reader should refuse unknown field, got %v", problems)
	} else if !strings.Contains(problems[0], "pack.toml:1:1 (skills[0])") {
		t.Errorf("strict diagnostic lacks grouped source location: %v", problems)
	}
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"use":      DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		m, problems, skipped := decode(newer)
		if len(problems) != 0 || len(skipped) != 1 {
			t.Errorf("%s reader: problems=%v skipped=%v", name, problems, skipped)
			continue
		}
		if len(m.Contributions()) != 0 {
			t.Errorf("%s reader retained a contribution with an unknown field", name)
		}
	}
}

func TestDecodeTOMLRejectsLegacyAuthoringShapeAndValidatesNewModelFacts(t *testing.T) {
	for name, test := range map[string]struct {
		input string
		want  string
	}{
		"old contributes key":   {input: "name = \"p\"\ncontributes = []\n"},
		"bad context":           {input: "[providers.meta.every_model]\ncontext = 0\n"},
		"bad image quality":     {input: "[providers.meta.models.x]\nimages = { jpeg_quality = 101 }\n"},
		"bad variant":           {input: "[providers.meta.variants.fast]\nmodel = \"\"\n"},
		"legacy overlay body":   {input: "[[overlay]]\nsurface = \"pi/settings\"\nconfig.managed.theme = \"dark\"\n", want: `use "set"`},
		"legacy model id":       {input: "[providers.meta.models.x]\nid = \"x\"\n", want: `use "the model table key"`},
		"legacy model input":    {input: "[providers.meta.models.x]\ninput = [\"text\"]\n", want: `use "accepts"`},
		"legacy model cost":     {input: "[providers.meta.models.x]\ncost = { input = 1 }\n", want: `use "price"`},
		"legacy context window": {input: "[providers.meta.models.x]\ncontext_window = 64000\n", want: `use "context"`},
	} {
		_, problems := DecodeTOML([]byte(test.input))
		if len(problems) == 0 {
			t.Errorf("%s: expected a validation refusal", name)
		} else if test.want != "" && !strings.Contains(strings.Join(problems, "\n"), test.want) {
			t.Errorf("%s: diagnostic %v does not name %s", name, problems, test.want)
		}
	}
}

func TestGroupedTOMLRetainsLegacyModelFactShapesWithoutInventedConstraints(t *testing.T) {
	m, problems := DecodeTOML([]byte(`[providers.meta.models.partial]
price = { input = 1 }
compact_at = 200_000
`))
	if len(problems) != 0 {
		t.Fatalf("partial price and standalone compact_at should remain representable: %v", problems)
	}
	row := m.Providers()[0].ModelRows["partial"]
	if row.Price == nil || row.Price.Input == nil || *row.Price.Input != 1 || row.Price.Output != nil || row.CompactAt != 200_000 {
		t.Fatalf("partial legacy facts = %#v", row)
	}

	legacy, problems := Decode([]byte(`{"contributes":[{"kind":"config-overlay","surface":"pi/settings","config":{"managed":{"theme":"dark"}}}]}`))
	if len(problems) != 0 || len(legacy.ConfigOverlayContributions()) != 1 {
		t.Fatalf("legacy JSON overlay spelling changed: manifest=%#v problems=%v", legacy, problems)
	}
}

func TestGroupedKindNamesCoverAllLiveKinds(t *testing.T) {
	if got := len(KnownKinds()); got != 23 {
		t.Fatalf("live kind count = %d, want 23", got)
	}
	for _, kind := range KnownKinds() {
		group := strings.ReplaceAll(string(kind), "-", "_")
		if kind == KindConfigOverlay {
			group = "overlay"
		}
		if got := groupKind(group); got != kind {
			t.Errorf("group %q maps to %q, want %q", group, got, kind)
		}
	}
}

func TestUnknownGroupedKindUsesVersionSkewReaderPolicy(t *testing.T) {
	data := []byte("[[future_effect]]\nname = \"future\"\n")
	if _, problems := DecodeTOML(data); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "unknown kind") {
		t.Fatalf("strict reader should refuse future group, got %v", problems)
	}
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){"use": DecodeTOMLForUse, "tolerant": DecodeTOMLTolerant} {
		m, problems, skipped := decode(data)
		if m == nil || len(problems) != 0 || len(skipped) != 1 || len(m.Contributions()) != 0 {
			t.Errorf("%s reader: manifest=%#v problems=%v skipped=%v", name, m, problems, skipped)
		}
	}
}

func TestUnknownGroupedProviderModelFieldKeepsReaderModeSkewPolicy(t *testing.T) {
	data := []byte("[providers.meta.models.spark]\nname = \"Spark\"\nnew_fact = true\n")
	if _, problems := DecodeTOML(data); len(problems) != 1 || !strings.Contains(problems[0], "unknown provider model field") || !strings.Contains(problems[0], "providers.meta.models.spark.new_fact") {
		t.Fatalf("strict reader should locate unknown model fact, got %v", problems)
	}
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){"use": DecodeTOMLForUse, "tolerant": DecodeTOMLTolerant} {
		m, problems, skipped := decode(data)
		if m == nil || len(problems) != 0 || len(skipped) != 1 || len(m.Providers()) != 0 {
			t.Errorf("%s reader: manifest=%#v problems=%v skipped=%v", name, m, problems, skipped)
		}
	}
}

func TestGroupedTOMLRetainsPackWideMetadata(t *testing.T) {
	data := []byte(`name = "metadata"
needs = [{ pack = "wire-bridge" }]
supersedes = [{ capability = "old-job", because = "the job no longer exists" }]
session_homes = [{ dir = ".pi/config", env = "PI_CONFIG_DIR" }]
provider_keys = { pi = [{ surface = "pi/settings", pointer = "/env/PI_PROFILE" }] }
provider_env = { pi = { surface = "pi/settings", pointer = "/env" } }
model_list_check = { pi = { rules = [{ platform = "some-provider" }] } }

[[state]]
at = ".pi"
scope = "machine"
because = "shared configuration"

[[program]]
bin = "pi"
via = "npm"
package = "example/pi"
`)
	m, problems := DecodeTOML(data)
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	if len(m.DeclaredNeeds()) != 1 || len(m.Supersessions()) != 1 || len(m.SessionHomes()) != 1 {
		t.Fatalf("pack-wide declarations were lost: needs=%v supersedes=%v session_homes=%v", m.DeclaredNeeds(), m.Supersessions(), m.SessionHomes())
	}
	if got := m.ProviderKeys("pi"); len(got) != 1 || got[0].Pointer != "/env/PI_PROFILE" {
		t.Fatalf("provider_keys = %#v", got)
	}
	if got := m.ProviderEnv("pi"); len(got) != 1 || got[0].Name != "PI_PROFILE" {
		t.Fatalf("provider_env = %#v", got)
	}
	if m.ModelListChecks["pi"] == nil || len(m.ModelListChecks["pi"].Rules) != 1 {
		t.Fatalf("model_list_check = %#v", m.ModelListChecks)
	}
}

func TestDecodeJSONManifestRetainsLegacyOriginAndReplaceSemantics(t *testing.T) {
	m, problems := Decode([]byte(`{"name":"old","contributes":[{"kind":"program","bin":"pi"}]}`))
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if m.Origin.Format != ManifestFormatJSON || len(m.Contributions()) != 1 {
		t.Fatalf("legacy origin/contributions = %#v / %#v", m.Origin, m.Contributions())
	}
	c := m.Contributions()[0]
	if c.Origin.Format != ManifestFormatJSON || c.Replace {
		t.Fatalf("legacy contribution origin/replace = %#v / %v", c.Origin, c.Replace)
	}
}
