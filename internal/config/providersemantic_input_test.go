package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func selectedEmbeddedPacks(t *testing.T, names ...string) []*packload.Pack {
	t.Helper()
	wanted := make(map[string]bool, len(names))
	for _, name := range names {
		wanted[name] = true
	}
	found := make(map[string]*packload.Pack, len(names))
	for _, pack := range packload.Embedded() {
		if wanted[pack.Name] {
			found[pack.Name] = pack
		}
	}
	packs := make([]*packload.Pack, 0, len(names))
	for _, name := range names {
		pack := found[name]
		if pack == nil {
			t.Fatalf("leased embedded selected pack %q was not loaded", name)
		}
		packs = append(packs, pack)
	}
	return packs
}

func TestBuildReadableSemanticInputsAdaptsLoadedGitGitHubAndAWSDeclarations(t *testing.T) {
	packs := selectedEmbeddedPacks(t, "git", "github", "aws-auth")
	user := editReadableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["create_pr","create_issue"]},"git":{"always":["fetch"]}}}`)
	workspace := editReadableMap(t, `{"requests":{"github":{"create_pr":true,"create_issue":true}}}`)
	origins := map[string][]string{"allow.github.on_request": {"user.jsonc"}}
	catalog := map[string]map[string]packdecl.ProviderModel{}
	inputs, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		User: user, Workspace: workspace, UserOrigins: origins, Packs: packs, Catalog: catalog,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inputs.AllowanceInput.SelectedPacks, []string{"git", "github", "aws-auth"}) {
		t.Fatalf("selected packs = %#v", inputs.AllowanceInput.SelectedPacks)
	}
	if len(inputs.AllowanceInput.Declarations) != 2 {
		t.Fatalf("loaded broker declarations = %#v, want Git and GitHub", inputs.AllowanceInput.Declarations)
	}
	bySource := map[string]ReadableBrokerDeclaration{}
	for _, declaration := range inputs.AllowanceInput.Declarations {
		bySource[declaration.Brokered.Source] = declaration
	}
	git := bySource["git"]
	if git.ReadCapability != "fetch" || git.KindCapabilities["create-branch"] != "new_branches" || git.KindCapabilities["branch"] != "push" {
		t.Fatalf("Git capabilities = %#v", git)
	}
	github := bySource["github"]
	// create_pr and create_issue are delivered grant kinds (the broker covers creations under
	// them, docs/design/boundary-broker.md BB-D76), no longer pending ones.
	if github.ReadCapability != "read" || len(github.PendingCapabilities) != 0 {
		t.Fatalf("GitHub pending capabilities = %#v", github)
	}
	if !reflect.DeepEqual(github.Brokered.Grants, []string{"create-pr", "create-issue"}) {
		t.Fatalf("declared GitHub grant kinds = %#v", github.Brokered.Grants)
	}
	awsSettings := false
	for _, declaration := range inputs.AllowanceInput.PackSettings {
		if declaration.Pack == "aws-auth" {
			awsSettings = len(declaration.Settings) == 4
		}
	}
	if !awsSettings {
		t.Fatalf("AWS settings did not come from the loaded pack module: %#v", inputs.AllowanceInput.PackSettings)
	}
	if len(inputs.AllowancePlan.Requests) != 2 {
		t.Fatalf("GitHub requests = %#v, want two granted declarations", inputs.AllowancePlan.Requests)
	}
	for _, request := range inputs.AllowancePlan.Requests {
		if request.Status != ReadableRequestGranted {
			t.Errorf("request %s status %q, want granted", request.Path, request.Status)
		}
	}
	if inputs.AllowanceInput.UserOrigins["allow.github.on_request"][0] != "user.jsonc" {
		t.Fatal("origin was not copied to the typed allowance input")
	}
	origins["allow.github.on_request"][0] = "mutated.jsonc"
	catalog["external"] = map[string]packdecl.ProviderModel{"m": {Name: "mutated"}}
	if inputs.AllowanceInput.UserOrigins["allow.github.on_request"][0] != "user.jsonc" || inputs.ProviderInput.Catalog["external"] != nil {
		t.Fatal("typed inputs alias caller-owned origins or offline catalog")
	}
	github.Brokered.KindCapabilities["create-pr"] = "runtime-grant"
	if _, ok := bySource["github"].Brokered.KindCapabilities["create-pr"]; !ok {
		t.Fatal("test setup lost the detached GitHub declaration")
	}
	for _, pack := range packs {
		if pack.Name != "github" {
			continue
		}
		modules, refusals, warnings := pack.LoopholeModules()
		if len(refusals)+len(warnings) != 0 {
			t.Fatalf("re-read GitHub declarations: refusals=%v warnings=%v", refusals, warnings)
		}
		for _, module := range modules {
			if module.Name == "github-broker" && module.Decl.Brokered != nil &&
				!reflect.DeepEqual(module.Decl.Brokered.Grants, []string{"create-pr", "create-issue"}) {
				t.Fatalf("adapter changed source GitHub grants: %#v", module.Decl.Brokered.Grants)
			}
		}
	}
}

func TestBuildReadableSemanticInputsPreservesActiveProviderSelectionAndVia(t *testing.T) {
	packs := selectedEmbeddedPacks(t, "pi", "codex-auth", "zai", "bedrock")
	config := editReadableMap(t, `{"providers":{"bedrock":{"via":"wire-bridge"}},"active_provider":{"pi":["zai","bedrock"]}}`)
	inputs, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		Config: config, Packs: packs, Catalog: map[string]map[string]packdecl.ProviderModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(inputs.ProviderInput.Selection.Named["pi"], []string{"zai", "bedrock"}) {
		t.Fatalf("provider order = %#v", inputs.ProviderInput.Selection.Named)
	}
	if inputs.ProviderConsumer.Readable == nil || len(inputs.ProviderConsumer.Readable.Selection.ByReceiver["pi"]) != 2 {
		t.Fatalf("existing configured provider consumer was not used: %#v", inputs.ProviderConsumer)
	}
	var bedrockVia string
	for _, declaration := range inputs.ProviderInput.Declarations {
		if declaration.Name == "bedrock" {
			bedrockVia = declaration.Via
		}
	}
	if bedrockVia != "wire-bridge" {
		t.Fatalf("authored bedrock via = %q", bedrockVia)
	}

	native := editReadableMap(t, `{"active_provider":{"default":"native"}}`)
	nativeInputs, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		Config: native, Packs: packs, Catalog: map[string]map[string]packdecl.ProviderModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(nativeInputs.ProviderInput.Selection.Default, []string{"native"}) ||
		!nativeInputs.ProviderConsumer.Readable.Selection.NativeReceivers["pi"] {
		t.Fatalf("native selection was not preserved through existing semantics: %#v", nativeInputs.ProviderConsumer)
	}
}

func TestBuildReadableSemanticInputsDecodesAuthoredCanonicalProvidersWithoutSelection(t *testing.T) {
	config := editReadableMap(t, `{"providers":{"broken":{"models":{"child":{"twin_of":"missing"}}}}}`)
	_, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		Config: config, Packs: selectedEmbeddedPacks(t, "pi"),
		Catalog: map[string]map[string]packdecl.ProviderModel{},
	})
	if err == nil || !strings.Contains(err.Error(), "twin_of names a missing model row") {
		t.Fatalf("inactive authored canonical provider facts were not decoded and checked: %v", err)
	}
}

func TestBuildReadableSemanticInputsDecodesCanonicalProviderTablesAndKeepsOrder(t *testing.T) {
	config := editReadableMap(t, `{"providers":{"disabled":"off","local":{"model":"base","via":"wire-bridge","every_model":{"api":"openai-responses","context":100},"models":{"global.openai.zulu":{"context":200,"price":{"input":0,"output":0,"cache_read":0,"cache_write":0}},"global.openai.alpha":{"twin_of":"global.openai.zulu"}},"variants":{"zeta":{"via":"wire-bridge"},"retired":"off","alpha":{"model":"variant"}}}},"active_provider":{"default":"native","pi":["local/zeta","bedrock"]}}`)
	inputs, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		Config: config, Packs: selectedEmbeddedPacks(t, "pi", "claude", "bedrock", "wire-bridge"),
		Catalog: map[string]map[string]packdecl.ProviderModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var local *packdecl.ProviderContribution
	for i := range inputs.ProviderInput.Declarations {
		if inputs.ProviderInput.Declarations[i].Name == "local" {
			local = &inputs.ProviderInput.Declarations[i]
		}
		if inputs.ProviderInput.Declarations[i].Name == "disabled" {
			t.Fatal("providers.<name> = off became a declaration")
		}
	}
	if local == nil {
		t.Fatalf("canonical user provider missing from declarations: %#v", inputs.ProviderInput.Declarations)
	}
	if local.Via != "wire-bridge" || !reflect.DeepEqual(local.ModelOrder, []string{"global.openai.zulu", "global.openai.alpha"}) ||
		!reflect.DeepEqual(local.VariantOrder, []string{"zeta", "alpha"}) || local.Variants["zeta"].Via != "wire-bridge" {
		t.Fatalf("canonical provider facts/order/via = %#v", *local)
	}
	if !reflect.DeepEqual(inputs.ProviderInput.Selection.Named["pi"], []string{"local/zeta", "bedrock"}) {
		t.Fatalf("canonical selection order = %#v", inputs.ProviderInput.Selection.Named)
	}
	if inputs.ProviderConsumer.Readable == nil || !reflect.DeepEqual(inputs.ProviderConsumer.Readable.Selection.ByReceiver["pi"][0].Target, ProviderTarget{Provider: "local", Variant: "zeta"}) ||
		!inputs.ProviderConsumer.Readable.Selection.NativeReceivers["claude"] {
		t.Fatalf("canonical variant/native consumer result = %#v", inputs.ProviderConsumer)
	}
}

func TestBuildReadableSemanticInputsChecksInactiveAuthoredProviderFacts(t *testing.T) {
	loaded := selectedEmbeddedPacks(t, "pi")[0]
	manifest, problems := packdecl.DecodeTOML([]byte(`name = "inactive-invalid"
[providers.invalid.models.left]
twin_of = "right"
[providers.invalid.models.right]
twin_of = "left"
`))
	if len(problems) != 0 {
		t.Fatalf("fixture TOML decode: %v", problems)
	}
	inactive := *loaded
	inactive.Name = "inactive-invalid"
	inactive.Decl = manifest
	_, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		Packs: []*packload.Pack{&inactive}, Catalog: map[string]map[string]packdecl.ProviderModel{},
	})
	if err == nil || !strings.Contains(err.Error(), "twin cycle") {
		t.Fatalf("inactive authored provider facts were skipped: %v", err)
	}
}

func TestBuildReadableSemanticInputsRequiresExplicitCatalogAndKeepsOffTombstones(t *testing.T) {
	packs := selectedEmbeddedPacks(t, "github")
	if _, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{Packs: packs}); err == nil || !strings.Contains(err.Error(), "explicit offline provider catalog") {
		t.Fatalf("missing catalog error = %v", err)
	}
	user, err := json5.Decode([]byte(`{"allow":{"github":"off"}}`))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := json5.Decode([]byte(`{"requests":{"github":"off"}}`))
	if err != nil {
		t.Fatal(err)
	}
	inputs, err := BuildReadableSemanticInputs(ReadableSemanticInputSource{
		User: user.(*jsonx.OrderedMap), Workspace: workspace.(*jsonx.OrderedMap), Packs: packs,
		Catalog: map[string]map[string]packdecl.ProviderModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.AllowancePlan.TableTombstones) != 2 {
		t.Fatalf("table off was not preserved as independent tombstones: %#v", inputs.AllowancePlan.TableTombstones)
	}
}
