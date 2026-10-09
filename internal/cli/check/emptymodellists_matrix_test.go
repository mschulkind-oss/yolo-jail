package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func runSyntheticListSection(t *testing.T, providerFields, extraContributions string, override any, overrideSet bool,
	modelListCheck string, selected bool) (string, *reporter) {
	t.Helper()
	dir := t.TempDir()
	provider := `{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}}`
	if providerFields != "" {
		provider += "," + providerFields
	}
	provider += "}"
	contributions := `{"kind":"program","bin":"fixture-agent","via":"npm","package":"example/fixture-agent","protocols":["openai"]},` +
		provider + `,{"kind":"profile","name":"selected","provider":"target"}`
	if extraContributions != "" {
		contributions += "," + extraContributions
	}
	manifest := `{"model_list_check":{"fixture-agent":` + modelListCheck + `},"contributes":[` + contributions + `]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs":["file://`+dir+`"]}`)
	merged := jsonx.NewOrderedMap()
	if selected {
		merged = useProfiles("fixture-agent", "selected")
	}
	if overrideSet {
		providerOverride := jsonx.NewOrderedMap()
		providerOverride.Set("models", override)
		providers := jsonx.NewOrderedMap()
		providers.Set("target", providerOverride)
		merged.Set("providers", providers)
	}
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, merged)
	return buf.String(), r
}

func emptyListCount(out string) int {
	return strings.Count(out, "contributes no callable model")
}

func TestSectionPacksDistinguishesAbsentExplicitEmptyAndUserNull(t *testing.T) {
	for _, tc := range []struct {
		name, providerFields string
		override             any
		overrideSet          bool
		want                 int
	}{
		{"absent list", "", nil, false, 0},
		{"explicit empty provider map", `"models":{}`, nil, false, 1},
		{"explicit empty user map", "", jsonx.NewOrderedMap(), true, 1},
		{"explicit null removes provider list", `"models":{"model":"model"}`, nil, true, 0},
		{"last user-deleted alias remains supplied", `"models":{"model":"model"}`, func() *jsonx.OrderedMap {
			m := jsonx.NewOrderedMap()
			m.Set("model", nil)
			return m
		}(), true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, r := runSyntheticListSection(t, tc.providerFields, "", tc.override, tc.overrideSet, `{}`, true)
			if got := emptyListCount(out); got != tc.want {
				t.Errorf("empty-list diagnostic count = %d, want %d:\n%s", got, tc.want, out)
			}
			if tc.want > 0 && (!strings.Contains(out, `provider "target"`) || !strings.Contains(out, `Profile "selected"`) ||
				!strings.Contains(out, `pack "001"`) || !strings.Contains(out, "providers.target.models")) {
				t.Errorf("warning must identify provider, governing profile, selected program pack and remedy:\n%s", out)
			}
			if r.failed != 0 {
				t.Errorf("the diagnostic is a warning, never a refusal:\n%s", out)
			}
		})
	}
	// A selected program with no configured profile is not diagnosed.
	out, r := runSyntheticListSection(t, `"models":{}`, "", nil, false, `{}`, false)
	if emptyListCount(out) != 0 || r.failed != 0 {
		t.Errorf("no-profile selection must stay silent for this diagnostic:\n%s", out)
	}
}

func TestSectionPacksReportsAnEmptyOnlyIntersectionAndUserAliasRestoration(t *testing.T) {
	contributions := `{"kind":"models","provider":"target","add":[{"id":"one","vendor":"allowed"},{"id":"two","vendor":"allowed"}]},` +
		`{"kind":"models","provider":"target","only":["one"]},{"kind":"models","provider":"target","only":["two"]}`
	out, r := runSyntheticListSection(t, "", contributions, nil, false, `{}`, true)
	if got := emptyListCount(out); got != 1 || r.failed != 0 {
		t.Fatalf("intersecting onlys must produce one warning, not a failure (count=%d):\n%s", got, out)
	}
	userModels := jsonx.NewOrderedMap()
	userModels.Set("restored", "vendorless-restored-model")
	out, r = runSyntheticListSection(t, "", contributions, userModels, true, `{}`, true)
	if got := emptyListCount(out); got != 0 || r.failed != 0 {
		t.Fatalf("the final vendorless user alias must restore callability (count=%d):\n%s", got, out)
	}
}

func TestSectionPacksUsesPackOwnedMetadataForSyntheticPrograms(t *testing.T) {
	out, r := runSyntheticListSection(t,
		`"models":{"model":"model"},"model_options":{"model":{"vendor":"blocked"}}`, "", nil, false,
		`{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`, true)
	if emptyListCount(out) != 1 || r.failed != 0 {
		t.Fatalf("the synthetic pack-owned filter must drive exactly one warning, never a failure:\n%s", out)
	}
	for _, want := range []string{
		"fixture-agent", `provider "target"`, `Profile "selected"`, `pack "001"`,
		"Add a callable alias under `providers.target.models`",
		"This list-only warning does not say the program lacks its own default",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("warning missing %q:\n%s", want, out)
		}
	}
}

func TestSectionPacksRestrictedAndUnrestrictedProgramsShareOneProvider(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"model_list_check":{"restricted":{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]},"unrestricted":{}},"contributes":[
		{"kind":"program","bin":"restricted","via":"npm","package":"example/restricted","protocols":["openai"]},
		{"kind":"program","bin":"unrestricted","via":"npm","package":"example/unrestricted","protocols":["openai"]},
		{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}},"models":{"model":"model"},"model_options":{"model":{"vendor":"blocked"}}},
		{"kind":"profile","name":"selected","provider":"target"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs":["file://`+dir+`"]}`)
	active := jsonx.NewOrderedMap()
	active.Set("restricted", "selected")
	active.Set("unrestricted", "selected")
	merged := jsonx.NewOrderedMap()
	merged.Set("profile", active)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, merged)
	out := buf.String()
	if emptyListCount(out) != 1 || !strings.Contains(out, "restricted's supplied list") || strings.Contains(out, "unrestricted's supplied list") || r.failed != 0 {
		t.Fatalf("only the restricted program should warn on a shared list (count=%d failed=%d):\n%s", emptyListCount(out), r.failed, out)
	}
}

func TestSectionPacksUsesActiveSetsForLaterAndDuplicateProfiles(t *testing.T) {
	t.Run("callable primary with empty later provider", func(t *testing.T) {
		dir := t.TempDir()
		manifest := `{"model_list_check":{"fixture-agent":{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}},"contributes":[
			{"kind":"program","bin":"fixture-agent","via":"npm","package":"example/fixture-agent","protocols":["openai"],"provider_sets":true},
			{"kind":"provider","name":"primary","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://primary.invalid/v1"}},"models":{"ready":"ready"},"model_options":{"ready":{"vendor":"allowed"}}},
			{"kind":"provider","name":"later","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://later.invalid/v1"}},"models":{}},
			{"kind":"profile","name":"primary","provider":"primary"},
			{"kind":"profile","name":"later","provider":"later"}]}`
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		packsFixture(t, `{"packs":["file://`+dir+`"]}`)
		var buf bytes.Buffer
		r := &reporter{w: &buf}
		(&Options{}).sectionPacks(r, setSelection("fixture-agent", "primary", "later"))
		out := buf.String()
		if emptyListCount(out) != 1 || r.failed != 0 {
			t.Fatalf("the empty later set entry should warn without failing (count=%d failed=%d):\n%s", emptyListCount(out), r.failed, out)
		}
		for _, want := range []string{
			`fixture-agent's supplied list for provider "later"`, `Profile "later" governs fixture-agent here`,
			`pack "001"`, `Add a callable alias under ` + "`providers.later.models`",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("later-entry warning missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("duplicate provider profiles warn once at the first entry", func(t *testing.T) {
		render := func(models string) (string, *reporter) {
			t.Helper()
			dir := t.TempDir()
			manifest := `{"model_list_check":{"fixture-agent":{}},"contributes":[
				{"kind":"program","bin":"fixture-agent","via":"npm","package":"example/fixture-agent","protocols":["openai"],"provider_sets":true},
				{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://target.invalid/v1"}}` + models + `},
				{"kind":"profile","name":"first","provider":"target"},
				{"kind":"profile","name":"duplicate","provider":"target"}]}`
			if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			packsFixture(t, `{"packs":["file://`+dir+`"]}`)
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r, setSelection("fixture-agent", "first", "duplicate"))
			return buf.String(), r
		}
		baseline, baselineReport := render("")
		out, r := render(`,"models":{}`)
		if emptyListCount(out) != 1 || strings.Count(out, `Profile "first" governs fixture-agent here`) != 1 ||
			strings.Contains(out, `Profile "duplicate" governs fixture-agent here`) || r.failed != baselineReport.failed {
			t.Fatalf("duplicate provider profiles should emit one first-profile warning without changing active-set failures (count=%d failures=%d baseline=%d):\n%s\nBaseline:\n%s",
				emptyListCount(out), r.failed, baselineReport.failed, out, baseline)
		}
	})
}

func TestSectionPacksViaProfileObservesTheNotchAwarePresenceCallback(t *testing.T) {
	company := emptyModelCheckPack(t,
		`{"kind":"models","provider":"bedrock","only":["not-added"]}`)
	packsFixture(t, `{"packs":["claude","bedrock","file://`+company+`"]}`)
	for _, tc := range []struct {
		name    string
		runtime string
		macOS   bool
	}{
		{"container via", "", false},
		{"launch-owned macos-user via recomposition", "macos-user", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			merged := useProfiles("claude", "bedrock-bridge")
			if tc.runtime != "" {
				merged.Set("runtime", tc.runtime)
			}
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{IsMacOS: tc.macOS}).sectionPacks(r, merged)
			out := buf.String()
			if emptyListCount(out) != 1 || r.failed != 0 {
				t.Fatalf("the live via profile's explicit empty list must reach the report through ComposeProvidersAt: count=%d failed=%d\n%s",
					emptyListCount(out), r.failed, out)
			}
			if !strings.Contains(out, `Profile "bedrock-bridge"`) || !strings.Contains(out, `pack "claude"`) {
				t.Errorf("via warning must name its governing profile and declaring program pack:\n%s", out)
			}
		})
	}
}

func assertEmptyListWarning(t *testing.T, out string, r *reporter, program, provider, profile, pack string) {
	t.Helper()
	if got := emptyListCount(out); got != 1 || r.failed != 0 {
		t.Fatalf("empty-list warning count=%d, failures=%d; want one warning and no refusal:\n%s", got, r.failed, out)
	}
	for _, want := range []string{
		program + `'s supplied list for provider "` + provider + `" contributes no callable model`,
		`Profile "` + profile + `" governs ` + program + ` here; the program is declared by pack "` + pack + `"`,
		"Add a callable alias under `providers." + provider + ".models`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("empty-list warning missing %q:\n%s", want, out)
		}
	}
}

func assertNoEmptyListWarning(t *testing.T, out string, r *reporter) {
	t.Helper()
	if got := emptyListCount(out); got != 0 || r.failed != 0 {
		t.Fatalf("empty-list warning count=%d, failures=%d; want no warning and no refusal:\n%s", got, r.failed, out)
	}
}

func TestSectionPacksUsesObjectAliasFacts(t *testing.T) {
	check := `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`
	for _, tc := range []struct {
		name, vendor string
		wantWarning  bool
	}{
		{"blocked object-form maker", "blocked", true},
		{"allowed object-form maker", "allowed", false},
		{"vendorless object-form alias", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facts := jsonx.NewOrderedMap()
			facts.Set("id", "object-model")
			if tc.vendor != "" {
				facts.Set("vendor", tc.vendor)
			}
			models := jsonx.NewOrderedMap()
			models.Set("object-alias", facts)
			out, r := runSyntheticListSection(t, "", "", models, true, check, true)
			if tc.wantWarning {
				assertEmptyListWarning(t, out, r, "fixture-agent", "target", "selected", "001")
			} else {
				assertNoEmptyListWarning(t, out, r)
			}
		})
	}
}

func TestSectionPacksUsesRepointedAndCanonicalAliasFacts(t *testing.T) {
	check := `{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}`
	t.Run("repointed user alias drops the old maker", func(t *testing.T) {
		replacement := jsonx.NewOrderedMap()
		replacement.Set("alias", "replacement-id")
		out, r := runSyntheticListSection(t,
			`"models":{"alias":"original-id"},"model_options":{"alias":{"vendor":"blocked"}}`,
			"", replacement, true, check, true)
		assertNoEmptyListWarning(t, out, r)
	})
	t.Run("canonical id fact wins over a blocked sorted alias", func(t *testing.T) {
		out, r := runSyntheticListSection(t,
			`"models":{"model":"model","z-alias":"model"},"model_options":{"model":{"vendor":"blocked"},"z-alias":{"vendor":"allowed"}}`,
			"", nil, false, check, true)
		assertEmptyListWarning(t, out, r, "fixture-agent", "target", "selected", "001")
	})
	t.Run("allowed canonical id fact wins over a blocked sorted alias", func(t *testing.T) {
		out, r := runSyntheticListSection(t,
			`"models":{"model":"model","z-alias":"model"},"model_options":{"model":{"vendor":"allowed"},"z-alias":{"vendor":"blocked"}}`,
			"", nil, false, check, true)
		assertNoEmptyListWarning(t, out, r)
	})
}

func TestSectionPacksReportsFilteredListWithLiteralModelAndEnforcementOff(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"model_list_check":{"fixture-agent":{"rules":[{"platform":"fixture-platform","makers":["allowed"]}]}},"contributes":[
		{"kind":"program","bin":"fixture-agent","via":"npm","package":"example/fixture-agent","protocols":["openai"]},
		{"kind":"provider","name":"target","platform":"fixture-platform","endpoints":{"openai":{"base_url":"https://fixture.invalid/v1"}},"models":{"model":"model"},"model_options":{"model":{"vendor":"blocked"}}},
		{"kind":"profile","name":"selected","provider":"target"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs":["file://`+dir+`"],"profiles":{"selected":{"provider":"target","model":"literal-outside-the-list","enforce_models":false}}}`)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("fixture-agent", "selected"))
	out := buf.String()
	assertEmptyListWarning(t, out, r, "fixture-agent", "target", "selected", "001")
	if !strings.Contains(out, "literal profile model cannot run") {
		t.Fatalf("the list-only warning must preserve the literal-model caveat:\n%s", out)
	}
}

func runCodexBedrockListSection(t *testing.T, profile, additions string) (string, *reporter) {
	t.Helper()
	company := emptyModelCheckPack(t, `{"kind":"models","provider":"bedrock","add":[`+additions+`]}`)
	packs := `{"packs":["codex","bedrock","file://` + company + `"]}`
	if profile == "bedrock-bridge" {
		packs = `{"packs":["codex","bedrock","wire-bridge","file://` + company + `"]}`
	}
	packsFixture(t, packs)
	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r, useProfiles("codex", profile))
	return buf.String(), r
}

// Codex's aws-bedrock rule omits a via selector, so the native and bedrock-bridge paths both
// filter to OpenAI, as the unchanged derives do. Claude's distinct via:false metadata is not a
// reason to expect Codex's bridge path to be unrestricted.
func TestSectionPacksChecksCodexBedrockNativeAndViaMakerRules(t *testing.T) {
	for _, profile := range []string{"bedrock", "bedrock-bridge"} {
		t.Run(profile+" rejects a non-OpenAI-only list", func(t *testing.T) {
			out, r := runCodexBedrockListSection(t, profile,
				`{"id":"other-maker-model","vendor":"anthropic"}`)
			assertEmptyListWarning(t, out, r, "codex", "bedrock", profile, "codex")
		})
		t.Run(profile+" accepts a mixed list with an OpenAI row", func(t *testing.T) {
			out, r := runCodexBedrockListSection(t, profile,
				`{"id":"other-maker-model","vendor":"anthropic"},{"id":"openai-model","vendor":"openai"}`)
			assertNoEmptyListWarning(t, out, r)
		})
	}
}

func TestSectionPacksTreatsEmptyAliasAsVendorlessCallableRow(t *testing.T) {
	models := jsonx.NewOrderedMap()
	models.Set("", "callable-model")
	out, r := runSyntheticListSection(t, "", "", models, true, `{}`, true)
	assertNoEmptyListWarning(t, out, r)
}
