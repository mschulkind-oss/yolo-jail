package cli

// providerdocs_test.go is the DRIFT GATE between a provider's two schemas and `yolo config-ref`
// (config_ref.txt): every field of a pack's provider contribution appears in the `provider` kind
// entry, and every key a user's `providers.<name>` entry accepts has its own row under
// `providers`. A review of the OQ-BR6 build found `region_env_name` documented only in a doc
// comment and a reference doc, and "no coverage test catches the gap, so the suite stays green";
// this is that test. It reads the lists the code accepts, so a field added tomorrow fails here
// until it is documented.

import (
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// providerKindEntry is config-ref's `provider` kind entry: its row and every continuation line
// (indented to the description column) under it.
func providerKindEntry(t *testing.T) string {
	t.Helper()
	lines := strings.Split(configRefContent, "\n")
	for i, l := range lines {
		if !strings.HasPrefix(l, "      provider ") {
			continue
		}
		entry := []string{l}
		for _, next := range lines[i+1:] {
			if !strings.HasPrefix(next, strings.Repeat(" ", 20)) {
				break
			}
			entry = append(entry, next)
		}
		return strings.Join(entry, "\n")
	}
	t.Fatal("config_ref.txt has no `provider` kind entry")
	return ""
}

// legacyProviderDocFields are the fields consumed by packload's legacy provider projection and
// therefore part of config-ref's pack.json grammar. Grouped TOML's alias/model/catalog fields are
// intentionally absent: config-ref describes the active JSONC user keys and legacy pack.json.
var legacyProviderDocFields = map[string]string{
	"Name": "name", "Endpoints": "endpoints", "APIKeyEnvName": "api_key_env_name",
	"Platform": "platform", "Region": "region", "RegionEnvName": "region_env_name",
	"RegionFile": "region_file", "Models": "models", "ModelOptions": "model_options",
	"Options": "options", "Capabilities": "capabilities", "PinnedEndpoint": "pinned_endpoint",
}

// groupedProviderFields are normalized fields populated by packdecl's grouped-TOML codec and
// retained with json:"-" on Contribution. Their provenance is also exercised through DecodeTOML
// and Providers() below; they are not legacy pack.json keys.
var groupedProviderFields = map[string]string{
	"Aliases": "ProviderAliases", "SelectName": "ProviderSelectName", "Model": "ProviderModel", "Variants": "ProviderVariants",
	"VariantOrder": "ProviderVariantOrder", "EveryModel": "EveryProviderModel",
	"ModelRows": "ProviderModels", "ModelOrder": "ProviderModelOrder",
}

// Via shares Contribution's legacy program/profile field, but the legacy provider composer does
// not consume it. Grouped TOML does populate the normalized accessor, so it is kept as a distinct
// classification rather than advertised as a provider pack.json field.
const sharedProviderViaField = "Via"

// providerContributionDocProblems enforces a closed, format-specific classification. A new
// projection field must be deliberately assigned to the legacy grammar, grouped TOML, or the
// shared-but-not-provider Via case; json:"-" alone is never a reason to skip a field.
func providerContributionDocProblems(entry string, projection, contribution reflect.Type) []string {
	var problems []string
	checkBacking := func(name, backing, wantTag string) {
		f, ok := contribution.FieldByName(backing)
		if !ok {
			problems = append(problems, name+" has no Contribution."+backing+" backing field")
			return
		}
		got := strings.Split(f.Tag.Get("json"), ",")[0]
		if got != wantTag {
			problems = append(problems, name+" is backed by Contribution."+backing+
				" with JSON tag "+strconv.Quote(got)+", want "+strconv.Quote(wantTag))
		}
	}
	for i := 0; i < projection.NumField(); i++ {
		name := projection.Field(i).Name
		if tag, ok := legacyProviderDocFields[name]; ok {
			checkBacking(name, name, tag)
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(tag) + `\b`).MatchString(entry) {
				problems = append(problems, "legacy provider field "+strconv.Quote(tag)+" is not named in config-ref's `provider` entry")
			}
			continue
		}
		if backing, ok := groupedProviderFields[name]; ok {
			checkBacking(name, backing, "-")
			continue
		}
		if name == sharedProviderViaField {
			checkBacking(name, name, "via")
			continue
		}
		problems = append(problems, "ProviderContribution."+name+" has no provider documentation format classification")
	}
	for name := range legacyProviderDocFields {
		if _, ok := projection.FieldByName(name); !ok {
			problems = append(problems, "legacy provider doc classification names missing ProviderContribution."+name)
		}
	}
	for name := range groupedProviderFields {
		if _, ok := projection.FieldByName(name); !ok {
			problems = append(problems, "grouped provider classification names missing ProviderContribution."+name)
		}
	}
	// The kind's declaration is the legacy pack.json shape; keep the shared program/profile
	// `via` spelling out of that provider grammar unless the provider runtime consumes it.
	declaration := strings.SplitN(entry, " —", 2)[0]
	if regexp.MustCompile(`\bvia\b`).MatchString(declaration) {
		problems = append(problems, "legacy provider declaration advertises shared `via` without a provider reader")
	}
	return problems
}

// The drift gate is intentionally exact; do not turn json:"-" into a blanket exemption.
func TestEveryProviderContributionFieldIsDocumented(t *testing.T) {
	entry := providerKindEntry(t)
	projection := reflect.TypeOf(packdecl.ProviderContribution{})
	contribution := reflect.TypeOf(packdecl.Contribution{})
	if problems := providerContributionDocProblems(entry, projection, contribution); len(problems) != 0 {
		for _, problem := range problems {
			t.Error(problem)
		}
	}
}

// The drift gate must fail if a legacy field disappears from config-ref, if the projection gains
// an unclassified field, or if a grouped-only backing field is turned into a legacy JSON field.
func TestProviderContributionDocGateRejectsFormatDrift(t *testing.T) {
	entry := providerKindEntry(t)
	projection := reflect.TypeOf(packdecl.ProviderContribution{})
	contribution := reflect.TypeOf(packdecl.Contribution{})

	withoutLegacyField := strings.ReplaceAll(entry, "api_key_env_name", "api-key-env-name")
	if problems := providerContributionDocProblems(withoutLegacyField, projection, contribution); len(problems) == 0 {
		t.Fatal("removing api_key_env_name from the provider entry passed the drift gate")
	}
	withLegacyVia := strings.Replace(entry, "{name, platform", "{name, via, platform", 1)
	if problems := providerContributionDocProblems(withLegacyVia, projection, contribution); !containsProblem(problems, "advertises shared `via`") {
		t.Fatalf("shared program/profile via was accepted as a legacy provider field: %v", problems)
	}

	fields := make([]reflect.StructField, projection.NumField()+1)
	for i := 0; i < projection.NumField(); i++ {
		fields[i] = projection.Field(i)
	}
	fields[len(fields)-1] = reflect.StructField{Name: "Unexpected", Type: reflect.TypeOf("")}
	withUnknownProjection := reflect.StructOf(fields)
	if problems := providerContributionDocProblems(entry, withUnknownProjection, contribution); !containsProblem(problems, "Unexpected has no provider documentation format classification") {
		t.Fatalf("unknown projection field passed the closed classification: %v", problems)
	}

	contributionFields := make([]reflect.StructField, contribution.NumField())
	for i := 0; i < contribution.NumField(); i++ {
		contributionFields[i] = contribution.Field(i)
		if contributionFields[i].Name == "ProviderAliases" {
			contributionFields[i].Tag = `json:"alias,omitempty"`
		}
	}
	withGroupedJSONTag := reflect.StructOf(contributionFields)
	if problems := providerContributionDocProblems(entry, projection, withGroupedJSONTag); !containsProblem(problems, `Aliases is backed by Contribution.ProviderAliases with JSON tag "alias", want "-"`) {
		t.Fatalf("grouped-only field accepted a legacy JSON tag: %v", problems)
	}
}

func containsProblem(problems []string, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}

// The grouped classification is grounded in the real decoder/accessor path, while a legacy
// provider's shared `via` value is proven not to enter the runtime provider table.
func TestProviderContributionDocumentationTracksManifestFormats(t *testing.T) {
	grouped, problems := packdecl.DecodeTOML([]byte(`[providers.fixture]
alias = "fast"
model = "fast-model"
via = "wire-bridge"
[providers.fixture.every_model]
api = "openai-responses"
[providers.fixture.models.fast-model]
name = "Fast"
[providers.fixture.variants.quick]
model = "fast-model"
`))
	if len(problems) != 0 {
		t.Fatalf("DecodeTOML: %v", problems)
	}
	providers := grouped.Providers()
	if len(providers) != 1 {
		t.Fatalf("grouped providers = %#v, want one", providers)
	}
	provider := providers[0]
	if !reflect.DeepEqual(provider.Aliases, []string{"fast"}) || provider.Model != "fast-model" || provider.Via != "wire-bridge" ||
		provider.EveryModel == nil || provider.EveryModel.API != "openai-responses" ||
		provider.ModelRows["fast-model"].Name != "Fast" || provider.Variants["quick"].Model != "fast-model" ||
		!reflect.DeepEqual(provider.ModelOrder, []string{"fast-model"}) ||
		!reflect.DeepEqual(provider.VariantOrder, []string{"quick"}) {
		t.Fatalf("grouped provider projection lost decoder-derived fields: %+v", provider)
	}

	legacy, legacyProblems := packdecl.Decode([]byte(`{"name":"fixture","contributes":[{"kind":"provider","name":"legacy","via":"wire-bridge","endpoints":{"openai":{"base_url":"https://example.invalid/v1"}}}]}`))
	if len(legacyProblems) != 0 {
		t.Fatalf("Decode legacy pack.json provider: %v", legacyProblems)
	}
	table, err := packload.ComposeProviders(nil, []*packload.Pack{{Name: "fixture", Decl: legacy}})
	if err != nil {
		t.Fatalf("ComposeProviders: %v", err)
	}
	value, ok := table.Get("legacy")
	if !ok {
		t.Fatal("legacy provider missing from composed table")
	}
	entry, ok := value.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("legacy provider entry = %T, want ordered object", value)
	}
	if _, ok := entry.Get("via"); ok {
		t.Fatal("legacy provider via unexpectedly reached the runtime provider table")
	}
}

// Every key a user's provider entry accepts has its own row in the `providers` section.
func TestEveryUserProviderKeyIsDocumented(t *testing.T) {
	start := strings.Index(configRefContent, "[bold]providers[/bold] (table)")
	if start < 0 {
		t.Fatal("config_ref.txt has no `providers` section")
	}
	section := configRefContent[start:]
	if end := strings.Index(section, "\n  [bold]"); end > 0 {
		section = section[:end]
	}
	row := regexp.MustCompile(`(?m)^      ([a-z_]+)\s{2,}`)
	documented := map[string]bool{}
	for _, m := range row.FindAllStringSubmatch(section, -1) {
		documented[m[1]] = true
	}
	for _, key := range config.KnownProviderKeys() {
		if !documented[key] {
			t.Errorf("`providers.<name>.%s` is accepted and has no row under config-ref's `providers`", key)
		}
	}
}

// THE REGION FILE COUNTS, in config-ref as in the launch (docs/design/bedrock-plumbing.md
// BR-DIR1, which revised BR-D5's "yolo does not read it"): the refusal offers the region file
// as a third way to set a region, so neither the `provider` kind entry nor the user's
// `providers.<name>.region` row may still say ~/.aws/config is not counted or name only two
// ways. A review found the region row saying so after the launch had stopped.
func TestConfigRefSaysTheRegionFileIsCounted(t *testing.T) {
	flat := strings.Join(strings.Fields(configRefContent), " ")
	for _, stale := range []string{
		"is not counted, since yolo does not read it",
		"yolo does not read it",
		"naming both ways to set one",
	} {
		if strings.Contains(flat, stale) {
			t.Errorf("config-ref still says %q, which BR-DIR1 made false", stale)
		}
	}
	start := strings.Index(configRefContent, "[bold]providers[/bold] (table)")
	if start < 0 {
		t.Fatal("config_ref.txt has no `providers` section")
	}
	row := regexp.MustCompile(`(?s)\n      region {2,}(.*?)\n      [a-z_]+ {2,}`).FindStringSubmatch(configRefContent[start:])
	if row == nil {
		t.Fatal("config-ref's `providers` section has no `region` row")
	}
	if region := strings.Join(strings.Fields(row[1]), " "); !strings.Contains(region,
		"is given the region its AWS profile has in ~/.aws/config") || !strings.Contains(region, "three ways") {
		t.Errorf("the `region` row must say the credential profile's region in ~/.aws/config is "+
			"delivered, and that the refusal names three ways to set one:\n%s", region)
	}
}
