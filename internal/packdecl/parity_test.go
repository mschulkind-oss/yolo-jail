package packdecl

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestGroupedSkipsDoNotStealRetainedContributionProvenance(t *testing.T) {
	fixtures := map[string]struct {
		data []byte
		key  string
	}{
		"different payload": {data: []byte(`[[env]]
vars = { FUTURE = "new" }
future_schema_key = true
replace = false

[[env]]
vars = { CURRENT = "kept" }
replace = true
`), key: "CURRENT"},
		"same known payload": {data: []byte(`[[env]]
vars = { CURRENT = "kept" }
future_schema_key = true
replace = false

[[env]]
vars = { CURRENT = "kept" }
replace = true
`), key: "CURRENT"},
	}
	for fixtureName, fixture := range fixtures {
		for readerName, decode := range map[string]func([]byte) (*Manifest, []string, []string){
			"use":      DecodeTOMLForUse,
			"tolerant": DecodeTOMLTolerant,
		} {
			t.Run(fixtureName+"/"+readerName, func(t *testing.T) {
				manifest, problems, skipped := decode(fixture.data)
				if len(problems) != 0 || len(skipped) != 1 {
					t.Fatalf("read problems=%v skipped=%v", problems, skipped)
				}
				contributions := manifest.Contributions()
				if len(contributions) != 1 || contributions[0].Vars[fixture.key] != "kept" {
					t.Fatalf("retained contributions = %#v", contributions)
				}
				if !contributions[0].Replace || contributions[0].Location.Line != 6 || contributions[0].Location.Key != "env[1]" {
					t.Errorf("retained origin/replace = %#v, want second declaration line 6 env[1] and replace=true", contributions[0])
				}
			})
		}
	}
}

func TestGroupedPatchedForkKeepsLegacyInstallAndPatchFields(t *testing.T) {
	legacy, problems := Decode([]byte(`{"contributes":[{"kind":"program","bin":"fixture-tool","via":"source","fork_of":"base","source":"git+https://example.com/fixture-fork?ref=main","build":"make install","produces":[".local/bin/fixture-tool"],"patches":"patches","follow":"head"}]}`))
	if len(problems) != 0 {
		t.Fatalf("valid legacy fork fixture rejected: %v", problems)
	}
	const groupedText = `[[program]]
bin = "fixture-tool"
via = "source"
fork_of = "base"
source = "git+https://example.com/fixture-fork?ref=main"
build = "make install"
produces = [".local/bin/fixture-tool"]
patches = "patches"
follow = "head"
`
	for readerName, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"use":      DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		grouped, problems, skipped := decode([]byte(groupedText))
		if len(problems) != 0 || len(skipped) != 0 {
			t.Fatalf("%s grouped fork fixture rejected: problems=%v skipped=%v", readerName, problems, skipped)
		}
		want, got := legacy.Contributions(), grouped.Contributions()
		if len(want) != 1 || len(got) != 1 {
			t.Fatalf("%s fork contribution counts=%d/%d", readerName, len(want), len(got))
		}
		want[0].Origin, want[0].Location, want[0].Replace = ManifestOrigin{}, ManifestLocation{}, false
		got[0].Origin, got[0].Location, got[0].Replace = ManifestOrigin{}, ManifestLocation{}, false
		if !reflect.DeepEqual(want, got) {
			t.Errorf("%s fork contribution differs: legacy=%#v grouped=%#v", readerName, want, got)
		}
		if !reflect.DeepEqual(legacy.DepRequirements(), grouped.DepRequirements()) || !reflect.DeepEqual(legacy.InstallContributions(), grouped.InstallContributions()) {
			t.Errorf("%s fork install/dependency projections differ", readerName)
		}
		contributions := grouped.Contributions()
		if len(contributions) != 1 || !contributions[0].IsFork() || !contributions[0].IsPatchedFork() || len(grouped.InstallContributions()) != 0 {
			t.Errorf("%s fork flags/install projection = %#v / %#v", readerName, contributions, grouped.InstallContributions())
		}
	}
}

func TestGroupedInstallProjectionKeepsItsDeepCopyBoundary(t *testing.T) {
	manifest, problems := DecodeTOML([]byte(`launch_computed = { fixture-tool = [{ key = "policy", argv = ["--policy", "{value}"] }] }
[[program]]
bin = "fixture-tool"
via = "npm"
package = "fixture-tool"
launch_selection = { surface = ".fixture/config.json", flags = [{ key = "defaultModel", argv = ["--model", "{value}"] }] }
`))
	if len(problems) != 0 {
		t.Fatalf("valid grouped computed install fixture rejected: %v", problems)
	}
	first := manifest.InstallContributions()
	if len(first) != 1 || first[0].LaunchSelection == nil || len(first[0].LaunchSelection.Computed) != 1 {
		t.Fatalf("grouped install projection lacks launch fields: %#v", first)
	}
	first[0].LaunchSelection.Flags[0].Argv[0] = "changed"
	first[0].LaunchSelection.Computed[0].Argv[0] = "changed"
	second := manifest.InstallContributions()
	if second[0].LaunchSelection.Flags[0].Argv[0] != "--model" || second[0].LaunchSelection.Computed[0].Argv[0] != "--policy" {
		t.Errorf("mutating the grouped reader's Install projection changed its source: %#v", second[0].LaunchSelection)
	}
}

func TestProviderPublicReadersPreserveLegacyFlatAndGroupedRowsSeparately(t *testing.T) {
	legacy, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"meta","models":{"default":"wire-main","fast":"wire-fast"},"model_options":{"default":{"name":"Main","context_window":"64000","legacy_tombstone":null},"fast":{"name":"Fast","order":"2"}},"options":{"pin_model":null,"model":"default"}}]}`))
	if len(problems) != 0 {
		t.Fatalf("valid legacy flat provider fixture rejected: %v", problems)
	}
	legacyProviders := legacy.Providers()
	if len(legacyProviders) != 1 {
		t.Fatalf("legacy Providers() = %#v", legacyProviders)
	}
	old := legacyProviders[0]
	if !reflect.DeepEqual(old.Models, map[string]string{"default": "wire-main", "fast": "wire-fast"}) {
		t.Errorf("legacy provider alias map = %#v", old.Models)
	}
	if old.ModelOptions["default"]["context_window"] == nil || *old.ModelOptions["default"]["context_window"] != "64000" || old.ModelOptions["default"]["legacy_tombstone"] != nil || old.ModelOptions["fast"]["order"] == nil || *old.ModelOptions["fast"]["order"] != "2" {
		t.Errorf("legacy flat model facts/tombstone = %#v", old.ModelOptions)
	}
	if option := old.Options["pin_model"]; option.Defaulted || option.Value != "" {
		t.Errorf("legacy null OptionDefault = %#v, want declared with no default", option)
	}
	if option := old.Options["model"]; !option.Defaulted || option.Value != "default" {
		t.Errorf("legacy string OptionDefault = %#v", option)
	}

	grouped, problems := DecodeTOML([]byte(`[providers.meta]
alias = "muse"
model = "wire-main"
[providers.meta.models."wire-main"]
name = "Main"
wire_id = "wire-main"
context = 64000
[providers.meta.models."wire-fast"]
name = "Fast"
wire_id = "wire-fast"
context = 32000
`))
	if len(problems) != 0 {
		t.Fatalf("valid grouped model-row fixture rejected: %v", problems)
	}
	newProviders := grouped.Providers()
	if len(newProviders) != 1 {
		t.Fatalf("grouped Providers() = %#v", newProviders)
	}
	new := newProviders[0]
	if !reflect.DeepEqual(new.Aliases, []string{"muse"}) || new.Model != "wire-main" || len(new.ModelOrder) != 2 || new.ModelOrder[0] != "wire-main" || new.ModelOrder[1] != "wire-fast" {
		t.Errorf("grouped provider identity/order = %#v", new)
	}
	if main := new.ModelRows["wire-main"]; main.WireID != "wire-main" || main.Context != 64000 || main.Name != "Main" {
		t.Errorf("grouped integer model-row facts = %#v", main)
	}
	if fast := new.ModelRows["wire-fast"]; fast.WireID != "wire-fast" || fast.Context != 32000 || fast.Name != "Fast" {
		t.Errorf("grouped second model-row facts = %#v", fast)
	}
}

func TestGroupedProgramDefaultsIncludePackTreeHostHintsAndRunBin(t *testing.T) {
	const grouped = `[[program]]
bin = "fixture-tool"
via = "pack"
from = "tools/fixture-tool"
build = "make fixture-tool"
`
	for readerName, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"strict": func(data []byte) (*Manifest, []string, []string) {
			manifest, problems := DecodeTOML(data)
			return manifest, problems, nil
		},
		"use":      DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		manifest, problems, skipped := decode([]byte(grouped))
		if len(problems) != 0 || len(skipped) != 0 {
			t.Fatalf("%s valid pack-tree defaults rejected: problems=%v skipped=%v", readerName, problems, skipped)
		}
		contributions := manifest.Contributions()
		if len(contributions) != 1 || contributions[0].Run != "fixture-tool" {
			t.Errorf("%s run default = %#v, want bin", readerName, contributions)
		}
		wantHints := map[string]string{"brew": "fixture-tool", "apt": "fixture-tool", "dnf": "fixture-tool", "pacman": "fixture-tool", "nix": "fixture-tool"}
		install := manifest.InstallContributions()
		if len(install) != 1 || !reflect.DeepEqual(install[0].InstallHints, wantHints) {
			t.Errorf("%s install hints = %#v, want accepted manager defaults including via=pack", readerName, install)
		}
	}
	legacy, problems := Decode([]byte(`{"contributes":[{"kind":"program","bin":"fixture-tool","via":"pack","from":"tools/fixture-tool","run":"fixture-tool","build":"make fixture-tool"}]}`))
	if len(problems) != 0 {
		t.Fatalf("legacy public sparse pack-tree control rejected: %v", problems)
	}
	legacyInstall := legacy.InstallContributions()
	if len(legacyInstall) != 1 || len(legacyInstall[0].InstallHints) != 0 {
		t.Fatalf("legacy sparse install hints changed: %#v", legacyInstall)
	}
}

func TestLegacySparseInstallHintsRemainDistinctFromGroupedManagerDefaults(t *testing.T) {
	legacy, problems := Decode([]byte(`{"contributes":[{"kind":"program","bin":"fixture-tool","via":"npm","package":"fixture-tool","install_hints":{"custom":"vendor-tool","brew":"brew-tool"}}]}`))
	if len(problems) != 0 {
		t.Fatalf("legacy public install_hints control rejected: %v", problems)
	}
	legacyInstall := legacy.InstallContributions()
	if len(legacyInstall) != 1 || !reflect.DeepEqual(legacyInstall[0].InstallHints, map[string]string{"custom": "vendor-tool", "brew": "brew-tool"}) {
		t.Fatalf("legacy sparse/arbitrary public install hints = %#v", legacyInstall)
	}
	grouped, problems := DecodeTOML([]byte(`[[program]]
bin = "fixture-tool"
via = "npm"
package = "fixture-tool"
install = { brew = "brew-tool" }
`))
	if len(problems) != 0 {
		t.Fatalf("valid grouped manager-default fixture rejected: %v", problems)
	}
	groupedInstall := grouped.InstallContributions()
	if len(groupedInstall) != 1 || reflect.DeepEqual(legacyInstall[0].InstallHints, groupedInstall[0].InstallHints) {
		t.Fatalf("fixture no longer exposes representability boundary: legacy=%#v grouped=%#v", legacyInstall, groupedInstall)
	}
	if _, ok := groupedInstall[0].InstallHints["apt"]; !ok {
		t.Errorf("grouped known-manager defaults no longer fill missing apt hint: %#v", groupedInstall[0].InstallHints)
	}
}

func TestGroupedBuiltInProviderOffPlanNullPreservesLegacyAccessor(t *testing.T) {
	legacy, problems := Decode([]byte(`{"contributes":[{"kind":"program","bin":"fixture-tool","via":"npm","package":"fixture-tool","built_in_providers":{"names":["zai"],"plans":{"specific":{"provider":"zai"},"no_provider":null}}}]}`))
	if len(problems) != 0 {
		t.Fatalf("valid legacy nullable-plan fixture rejected: %v", problems)
	}
	const offPlan = `[[program]]
bin = "fixture-tool"
via = "npm"
package = "fixture-tool"
built_in_providers = { names = ["zai"], plans = { specific = { provider = "zai" }, no_provider = "off" } }
`
	for readerName, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"use":      DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		grouped, problems, skipped := decode([]byte(offPlan))
		if len(problems) != 0 || len(skipped) != 0 {
			t.Fatalf("%s grouped nullable-plan fixture rejected: problems=%v skipped=%v", readerName, problems, skipped)
		}
		assertBuiltInPlanParity(t, readerName, legacy, grouped)
	}
	grouped, problems := DecodeTOML([]byte(offPlan))
	if len(problems) != 0 {
		t.Fatalf("strict grouped nullable-plan fixture rejected: %v", problems)
	}
	assertBuiltInPlanParity(t, "strict", legacy, grouped)
	badPlan := []byte(strings.Replace(offPlan, `no_provider = "off"`, `no_provider = "later"`, 1))
	for readerName, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"strict": func(data []byte) (*Manifest, []string, []string) {
			manifest, problems := DecodeTOML(data)
			return manifest, problems, nil
		},
		"use":      DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		bad, problems, _ := decode(badPlan)
		if bad != nil || len(problems) == 0 {
			t.Errorf("%s non-off string plan must be fatal, manifest=%#v problems=%v", readerName, bad, problems)
		}
	}
}

func assertBuiltInPlanParity(t *testing.T, reader string, legacy, grouped *Manifest) {
	t.Helper()
	legacyProviders := legacy.BuiltInProvidersFor("fixture-tool")
	groupedProviders := grouped.BuiltInProvidersFor("fixture-tool")
	if !reflect.DeepEqual(legacyProviders, groupedProviders) {
		t.Fatalf("%s BuiltInProvidersFor differs: legacy=%#v grouped=%#v", reader, legacyProviders, groupedProviders)
	}
	if groupedProviders == nil {
		t.Fatal("BuiltInProvidersFor lost the program declaration")
	}
	if plan, exists := groupedProviders.Plans["no_provider"]; !exists || plan != nil {
		t.Errorf("%s off plan = (%#v, %t), want present nil (no program provider for this plan)", reader, plan, exists)
	}
	if plan, exists := groupedProviders.Plans["specific"]; !exists || plan == nil || plan.Provider != "zai" {
		t.Errorf("%s specific plan = (%#v, %t), want its named program provider", reader, plan, exists)
	}
	if _, exists := groupedProviders.Plans["absent"]; exists {
		t.Errorf("%s absent plan became a declaration: %#v", reader, groupedProviders.Plans)
	}
}

func TestGroupedModelListCheckDoesNotAcceptOffAsNullablePlan(t *testing.T) {
	_, problems := DecodeTOML([]byte(`model_list_check = { fixture-tool = "off" }
[[program]]
bin = "fixture-tool"
via = "npm"
package = "fixture-tool"
`))
	if len(problems) == 0 {
		t.Fatal("model_list_check off must remain invalid; only built_in_providers.plans has this null spelling")
	}
}

func TestGroupedProviderImplicitTableKeepsGlobalContributionOrder(t *testing.T) {
	legacy, problems := Decode([]byte(`{"contributes":[
		{"kind":"provider","name":"meta"},
		{"kind":"skills"},
		{"kind":"program","bin":"fixture-tool","via":"npm","package":"fixture-tool"}
	]}`))
	if len(problems) != 0 {
		t.Fatalf("valid legacy order fixture rejected: %v", problems)
	}
	grouped, problems := DecodeTOML([]byte(`[providers.meta.models.zulu]
name = "Zulu"
wire_id = "vendor-zulu"

[[skills]]

[[program]]
bin = "fixture-tool"
via = "npm"
package = "fixture-tool"
`))
	if len(problems) != 0 {
		t.Fatalf("valid grouped implicit-provider fixture rejected: %v", problems)
	}
	want, got := legacy.Contributions(), grouped.Contributions()
	if len(want) != 3 || len(got) != 3 {
		t.Fatalf("legacy/grouped contribution counts = %d/%d", len(want), len(got))
	}
	for i := range want {
		if want[i].Kind != got[i].Kind {
			t.Errorf("Contributions()[%d].Kind = %q, want authored %q; grouped=%#v", i, got[i].Kind, want[i].Kind, got)
		}
	}
	provider := grouped.Providers()
	if len(provider) != 1 || len(provider[0].ModelOrder) != 1 || provider[0].ModelOrder[0] != "zulu" || provider[0].ModelRows["zulu"].WireID != "vendor-zulu" {
		t.Fatalf("grouped public provider row = %#v", provider)
	}
	if got[0].Location.Line < 1 || got[0].Location.Key != "providers.meta" {
		t.Errorf("provider public contribution location = %#v, want positioned providers.meta", got[0].Location)
	}
}

// TestGroupedTOMLMatchesLegacyPublicContributionReaders pins the public legacy and grouped
// readers to one valid, ordered fixture covering every current kind. The fixture is built
// from declarations (not decoder internals), and assertions compare accessor results rather
// than the codecs' syntax.
func assertAutonomyRowMetadata(t *testing.T, reader string, old, got *AutonomyContribution) {
	t.Helper()
	if old == nil || got == nil {
		if old != got {
			t.Errorf("%s autonomy presence differs: legacy=%#v grouped=%#v", reader, old, got)
		}
		return
	}
	for _, half := range []struct {
		name string
		old  *AutonomyPosture
		got  *AutonomyPosture
	}{{"autonomous", old.Autonomous, got.Autonomous}, {"guarded", old.Guarded, got.Guarded}} {
		if half.old == nil || half.got == nil {
			continue
		}
		if len(half.old.Launch) != len(half.got.Launch) {
			t.Errorf("%s %s launch metadata row count legacy=%d grouped=%d", reader, half.name, len(half.old.Launch), len(half.got.Launch))
			continue
		}
		for i := range half.old.Launch {
			legacy, grouped := half.old.Launch[i], half.got.Launch[i]
			if legacy.Origin.Format != ManifestFormatJSON || legacy.Origin.File != ManifestName || legacy.Location.File != ManifestName || legacy.Location.Key == "" || legacy.Location.Line != 0 || legacy.Replace {
				t.Errorf("%s legacy %s launch[%d] metadata=%#v", reader, half.name, i, legacy)
			}
			if grouped.Origin.Format != ManifestFormatTOML || grouped.Origin.File != ManifestTOMLName || grouped.Location.File != ManifestTOMLName || grouped.Location.Line == 0 || grouped.Location.Column == 0 || grouped.Replace {
				t.Errorf("%s grouped %s launch[%d] metadata=%#v", reader, half.name, i, grouped)
			}
		}
	}
}

func assertPostureRowMetadata(t *testing.T, reader string, autonomy bool, old, got *AutonomyPosture) {
	t.Helper()
	if old == nil || got == nil || len(old.Launch) != len(got.Launch) {
		return
	}
	for i := range old.Launch {
		legacy, grouped := old.Launch[i], got.Launch[i]
		if legacy.Origin.Format != ManifestFormatJSON || legacy.Location.File != ManifestName || legacy.Location.Key == "" || grouped.Origin.Format != ManifestFormatTOML || grouped.Location.File != ManifestTOMLName || grouped.Location.Line == 0 || grouped.Location.Column == 0 {
			t.Errorf("%s posture=%t launch[%d] row metadata legacy=%#v grouped=%#v", reader, autonomy, i, legacy, grouped)
		}
	}
}

func clearAutonomyRowMetadata(a *AutonomyContribution) {
	if a == nil {
		return
	}
	clearPostureRowMetadata(a.Autonomous)
	clearPostureRowMetadata(a.Guarded)
}

func clearPostureRowMetadata(p *AutonomyPosture) {
	if p == nil {
		return
	}
	for i := range p.Launch {
		p.Launch[i].Origin, p.Launch[i].Location, p.Launch[i].Replace = ManifestOrigin{}, ManifestLocation{}, false
	}
	p.configLocations, p.configReplaces = nil, nil
}

func TestGroupedTOMLMatchesLegacyPublicContributionReaders(t *testing.T) {
	legacy, grouped := contributionParityFixture(t)
	old, problems := Decode(legacy)
	if len(problems) != 0 {
		t.Fatalf("valid legacy contribution fixture rejected: %v\\n%s", problems, legacy)
	}
	for name, decode := range map[string]func([]byte) (*Manifest, []string, []string){
		"strict":   DecodeTOMLForUse,
		"tolerant": DecodeTOMLTolerant,
	} {
		got, problems, skipped := decode(grouped)
		if len(problems) != 0 || len(skipped) != 0 {
			t.Fatalf("valid grouped fixture rejected by %s reader: problems=%v skipped=%v\n%s", name, problems, skipped, grouped)
		}
		assertContributionParity(t, name, old, got)
	}
	strict, problems := DecodeTOML(grouped)
	if len(problems) != 0 {
		t.Fatalf("valid grouped fixture rejected by strict authoring reader: %v\n%s", problems, grouped)
	}
	assertContributionParity(t, "strict", old, strict)
}

func TestGroupedManifestParityPreservesNamespacedSkillsTier(t *testing.T) {
	legacy, problems := Decode([]byte(`{"name":"namespaced-fixture","description":"Namespaced fixture.","skills_tier":"namespaced"}`))
	if len(problems) != 0 {
		t.Fatalf("valid legacy metadata rejected: %v", problems)
	}
	grouped, problems := DecodeTOML([]byte("name = \"namespaced-fixture\"\ndescription = \"Namespaced fixture.\"\nskills_tier = \"namespaced\"\n"))
	if len(problems) != 0 {
		t.Fatalf("valid grouped metadata rejected: %v", problems)
	}
	if legacy.Name != grouped.Name || legacy.Description != grouped.Description || legacy.SkillsTier != grouped.SkillsTier {
		t.Fatalf("manifest header metadata differs: legacy=%#v grouped=%#v", legacy, grouped)
	}
	if grouped.SkillsTier != "namespaced" || !grouped.WantsNamespacedSkills() {
		t.Fatalf("positive namespaced tier was not preserved: SkillsTier=%q WantsNamespacedSkills=%t", grouped.SkillsTier, grouped.WantsNamespacedSkills())
	}
}

func assertContributionParity(t *testing.T, reader string, old, got *Manifest) {
	t.Helper()
	// The fixture-built row has no legacy install hints. Its grouped declaration omits
	// install, so this comparison expects the accepted grouped bin-name default rather
	// than pretending the legacy public reader synthesized that fact. The raw legacy
	// absence remains asserted by TestGroupedProgramDefaultsIncludePackTreeHostHintsAndRunBin.
	legacyWithGroupedDefaults := *old
	legacyWithGroupedDefaults.Contributes = append([]Contribution(nil), old.Contributes...)
	for i := range legacyWithGroupedDefaults.Contributes {
		c := &legacyWithGroupedDefaults.Contributes[i]
		if c.Kind == KindProgram && c.Bin == "fixture-built" {
			c.InstallHints = map[string]string{"brew": c.Bin, "apt": c.Bin, "dnf": c.Bin, "pacman": c.Bin, "nix": c.Bin}
		}
	}
	old = &legacyWithGroupedDefaults
	if old.Name != got.Name || old.Description != got.Description || old.SkillsTier != got.SkillsTier {
		t.Errorf("%s manifest header metadata differs: legacy name/description/skills_tier=%q/%q/%q grouped=%q/%q/%q", reader,
			old.Name, old.Description, old.SkillsTier, got.Name, got.Description, got.SkillsTier)
	}
	want := append([]Contribution(nil), old.Contributions()...)
	have := append([]Contribution(nil), got.Contributions()...)
	if len(want) != len(have) {
		t.Fatalf("%s Contributions count=%d, want %d", reader, len(have), len(want))
	}
	seen := map[Kind]bool{}
	for i := range want {
		seen[want[i].Kind] = true
		if want[i].Origin.Format != ManifestFormatJSON || have[i].Origin.Format != ManifestFormatTOML || have[i].Origin.File != ManifestTOMLName || have[i].Location.File != ManifestTOMLName || have[i].Location.Key == "" || have[i].Location.Line == 0 || have[i].Location.Column == 0 {
			t.Errorf("%s contribution[%d] format metadata legacy=%#v grouped=%#v", reader, i, want[i], have[i])
		}
		// Grouped provider model rows are a different accepted shape; their relation to
		// legacy alias/model_options composer semantics remains an F-owned obligation.
		if want[i].Kind == KindProvider {
			if want[i].Name != have[i].Name || !reflect.DeepEqual(want[i].Endpoints, have[i].Endpoints) ||
				!reflect.DeepEqual(want[i].Capabilities, have[i].Capabilities) ||
				!reflect.DeepEqual(want[i].APIKeyEnvName, have[i].APIKeyEnvName) {
				t.Errorf("%s provider common facts differ: legacy=%#v grouped=%#v", reader, want[i], have[i])
			}
			continue
		}
		// Format provenance and explicit TOML replace metadata are format-specific. Compare
		// those above, then scope semantic payload parity to independent row copies.
		want[i].Origin, want[i].Location, want[i].Replace = ManifestOrigin{}, ManifestLocation{}, false
		have[i].Origin, have[i].Location, have[i].Replace = ManifestOrigin{}, ManifestLocation{}, false
		// Contributions returns rows with pointer-valued nested postures. Copy those rows
		// before comparing normalized payloads so metadata checks and test edits cannot mask
		// later public accessors or rewrite either decoded manifest.
		if want[i].Kind == KindAutonomy {
			want[i].Autonomous = cloneAutonomyPosture(old.Contributes[i].Autonomous)
			want[i].Guarded = cloneAutonomyPosture(old.Contributes[i].Guarded)
			have[i].Autonomous = cloneAutonomyPosture(got.Contributes[i].Autonomous)
			have[i].Guarded = cloneAutonomyPosture(got.Contributes[i].Guarded)
			clearPostureRowMetadata(want[i].Autonomous)
			clearPostureRowMetadata(want[i].Guarded)
			clearPostureRowMetadata(have[i].Autonomous)
			clearPostureRowMetadata(have[i].Guarded)
		}
		if !reflect.DeepEqual(want[i], have[i]) {
			t.Errorf("%s Contributions()[%d] (%s) differs:\n legacy=%#v\n grouped=%#v", reader, i, want[i].Kind, want[i], have[i])
		}
	}
	if old.MCPAuth != nil && got.MCPAuth != nil {
		seen[KindMCPAuth] = true
		if !reflect.DeepEqual(old.MCPAuth, got.MCPAuth) {
			t.Errorf("%s MCPAuth declaration differs: legacy=%#v grouped=%#v", reader, old.MCPAuth, got.MCPAuth)
		}
	}
	if len(seen) != len(KnownKinds()) {
		t.Errorf("fixture covers %d kinds, current schema has %d: seen=%v", len(seen), len(KnownKinds()), seen)
	}
	if !reflect.DeepEqual(old.InstallContributions(), got.InstallContributions()) {
		t.Errorf("%s InstallContributions differ:\n legacy=%#v\n grouped=%#v", reader, old.InstallContributions(), got.InstallContributions())
	}
	if !reflect.DeepEqual(old.DepRequirements(), got.DepRequirements()) {
		t.Errorf("%s DepRequirements differ:\n legacy=%#v\n grouped=%#v", reader, old.DepRequirements(), got.DepRequirements())
	}
	if !reflect.DeepEqual(old.RequiredBins(), got.RequiredBins()) {
		t.Errorf("%s RequiredBins differ:\n legacy=%#v\n grouped=%#v", reader, old.RequiredBins(), got.RequiredBins())
	}
	wantAutonomy, haveAutonomy := old.AutonomyContributions(), got.AutonomyContributions()
	assertAutonomyRowMetadata(t, reader, wantAutonomy, haveAutonomy)
	clearAutonomyRowMetadata(wantAutonomy)
	clearAutonomyRowMetadata(haveAutonomy)
	if !reflect.DeepEqual(wantAutonomy, haveAutonomy) {
		t.Errorf("%s AutonomyContributions semantic payload differs", reader)
	}
	for _, enabled := range []bool{false, true} {
		wantPosture, havePosture := old.PostureFor(enabled), got.PostureFor(enabled)
		assertPostureRowMetadata(t, reader, enabled, wantPosture, havePosture)
		clearPostureRowMetadata(wantPosture)
		clearPostureRowMetadata(havePosture)
		if !reflect.DeepEqual(wantPosture, havePosture) {
			t.Errorf("%s PostureFor(%t) semantic payload differs", reader, enabled)
		}
	}
	if !reflect.DeepEqual(old.Profiles(), got.Profiles()) {
		t.Errorf("%s profile public projections differ: legacy=%#v grouped=%#v", reader, old.Profiles(), got.Profiles())
	}
	if !reflect.DeepEqual(old.Adapters(), got.Adapters()) || !reflect.DeepEqual(old.Services(), got.Services()) || !reflect.DeepEqual(old.ModelsContributions(), got.ModelsContributions()) {
		t.Errorf("%s adapter/service/models public projections differ", reader)
	}
	if !reflect.DeepEqual(old.HookContributions(), got.HookContributions()) || !reflect.DeepEqual(old.MCPContributions(), got.MCPContributions()) {
		t.Errorf("%s hook/MCP public projections differ", reader)
	}
	oldOverlays, groupedOverlays := old.ConfigOverlayContributions(), got.ConfigOverlayContributions()
	if len(oldOverlays) != len(groupedOverlays) {
		t.Errorf("%s config-overlay projection length legacy=%d grouped=%d", reader, len(oldOverlays), len(groupedOverlays))
	} else {
		for i := range oldOverlays {
			if oldOverlays[i].Origin.Format != ManifestFormatJSON || groupedOverlays[i].Origin.Format != ManifestFormatTOML || groupedOverlays[i].Location.File != ManifestTOMLName || groupedOverlays[i].Location.Line == 0 || groupedOverlays[i].Location.Column == 0 || groupedOverlays[i].Replace {
				t.Errorf("%s config-overlay[%d] format metadata legacy=%#v grouped=%#v", reader, i, oldOverlays[i], groupedOverlays[i])
			}
			oldOverlays[i].Origin, oldOverlays[i].Location, oldOverlays[i].Replace = ManifestOrigin{}, ManifestLocation{}, false
			groupedOverlays[i].Origin, groupedOverlays[i].Location, groupedOverlays[i].Replace = ManifestOrigin{}, ManifestLocation{}, false
		}
		if !reflect.DeepEqual(oldOverlays, groupedOverlays) {
			t.Errorf("%s config-overlay semantic payloads differ: legacy=%#v grouped=%#v", reader, oldOverlays, groupedOverlays)
		}
	}
	if !reflect.DeepEqual(old.HostFileContributions(), got.HostFileContributions()) || !reflect.DeepEqual(old.MountContributions(), got.MountContributions()) || !reflect.DeepEqual(old.ConfigListContributions(), got.ConfigListContributions()) {
		t.Errorf("%s host/mount/config-list public projections differ", reader)
	}
	if !reflect.DeepEqual(old.DeclaredNeeds(), got.DeclaredNeeds()) || !reflect.DeepEqual(old.Supersessions(), got.Supersessions()) || !reflect.DeepEqual(old.SessionHomes(), got.SessionHomes()) {
		t.Errorf("%s pack-wide metadata accessors differ", reader)
	}
	if !reflect.DeepEqual(old.ProviderKeys("fixture-tool"), got.ProviderKeys("fixture-tool")) || !reflect.DeepEqual(old.ProviderKeysFor("fixture-tool/settings"), got.ProviderKeysFor("fixture-tool/settings")) || !reflect.DeepEqual(old.ProviderEnv("fixture-tool"), got.ProviderEnv("fixture-tool")) {
		t.Errorf("%s provider key/env metadata accessors differ", reader)
	}
	oldCheck, oldCheckOK := old.ProgramModelListCheck("fixture-tool")
	gotCheck, gotCheckOK := got.ProgramModelListCheck("fixture-tool")
	if oldCheckOK != gotCheckOK || !reflect.DeepEqual(oldCheck, gotCheck) {
		t.Errorf("%s program model-list-check accessor differs: legacy=%#v/%t grouped=%#v/%t", reader, oldCheck, oldCheckOK, gotCheck, gotCheckOK)
	}
	if !reflect.DeepEqual(old.ProgramModelReferences("fixture-tool"), got.ProgramModelReferences("fixture-tool")) ||
		!reflect.DeepEqual(old.AgentFiles("fixture-tool"), got.AgentFiles("fixture-tool")) ||
		old.ReadsSharedModelsFor("fixture-tool") != got.ReadsSharedModelsFor("fixture-tool") ||
		old.NeedsModelList("fixture-tool", "fixture-provider") != got.NeedsModelList("fixture-tool", "fixture-provider") ||
		!reflect.DeepEqual(old.NativeCapabilities("fixture-tool"), got.NativeCapabilities("fixture-tool")) ||
		old.HoldsProviderSets("fixture-tool") != got.HoldsProviderSets("fixture-tool") ||
		!reflect.DeepEqual(old.SpokenProtocols("fixture-tool"), got.SpokenProtocols("fixture-tool")) ||
		!reflect.DeepEqual(old.PlatformSwitches("fixture-tool"), got.PlatformSwitches("fixture-tool")) ||
		!reflect.DeepEqual(old.RegionEnvNamesFor("fixture-tool", "fixture-provider"), got.RegionEnvNamesFor("fixture-tool", "fixture-provider")) {
		t.Errorf("%s model reference/program metadata accessors differ", reader)
	}
	if !reflect.DeepEqual(old.SkillsSources(), got.SkillsSources()) || !reflect.DeepEqual(old.ProjectSkillDirs(), got.ProjectSkillDirs()) ||
		old.WantsNamespacedSkills() != got.WantsNamespacedSkills() || !reflect.DeepEqual(old.EnvContributions(), got.EnvContributions()) ||
		!reflect.DeepEqual(old.GatedEnvContributions(), got.GatedEnvContributions()) || !reflect.DeepEqual(old.EnvServedBy(), got.EnvServedBy()) ||
		!reflect.DeepEqual(old.WritableDirContributions(), got.WritableDirContributions()) || !reflect.DeepEqual(old.SharedDirContributions(), got.SharedDirContributions()) ||
		!reflect.DeepEqual(old.LoopholeSources(), got.LoopholeSources()) || !reflect.DeepEqual(old.SurfaceContributions(), got.SurfaceContributions()) ||
		!reflect.DeepEqual(old.EnvOverrideContributions(), got.EnvOverrideContributions()) || !reflect.DeepEqual(old.HostMountContributions(), got.HostMountContributions()) {
		t.Errorf("%s skills/env/state/host/surface projections differ", reader)
	}
	if got.EnvServedBy()["FIXTURE_FLAG"] != "fixture-service" || got.EnvContributions()["FIXTURE_FLAG"] != "1" ||
		len(got.GatedEnvContributions()) != 1 || len(got.ProjectSkillDirs()) != 1 ||
		len(got.WritableDirContributions()) != 1 || len(got.SharedDirContributions()) != 1 ||
		len(got.LoopholeSources()) != 1 || len(got.SurfaceContributions()) == 0 {
		t.Errorf("%s fixture did not exercise non-empty skills/env/state/loophole/surface accessors", reader)
	}
	for i := range old.Contributions() {
		oldEntries, gotEntries := old.Contributions()[i].ModelEntries(), got.Contributions()[i].ModelEntries()
		if !reflect.DeepEqual(oldEntries, gotEntries) {
			t.Errorf("%s Contributions()[%d].ModelEntries differ", reader, i)
		}
	}
}

// contributionParityFixture creates a valid legacy document and its grouped TOML counterpart.
// Each contribution is an explicit current-schema declaration; grouping only moves the kind
// discriminator to its array-table name. The provider's new model-row grammar is tested
// independently because it is not a spelling alias for legacy flat models/model_options.
func contributionParityFixture(t *testing.T) ([]byte, []byte) {
	t.Helper()
	contribs := []map[string]any{
		{"kind": "program", "bin": "fixture-tool", "via": "npm", "package": "fixture-tool", "install_hints": map[string]string{"brew": "fixture-tool", "apt": "fixture-tool", "dnf": "fixture-tool", "pacman": "fixture-tool", "nix": "fixture-tool"}, "flags": []string{"--safe"}, "launch_selection": map[string]any{"surface": ".fixture/config.json", "flags": []any{map[string]any{"key": "defaultModel", "argv": []string{"--model", "{value}"}}}}, "agent_files": map[string]string{"FIXTURE_CONFIG": "config.json"}, "built_in_providers": map[string]any{"names": []string{"own"}, "plans": map[string]any{"fixture-provider": map[string]any{"provider": "own"}}}, "reads_shared_models": true, "needs_model_list": []string{"fixture-provider"}, "capabilities": []string{"web_search"}, "provider_sets": true, "protocols": []string{"openai"}, "platform_switches": []any{map[string]any{"platform": "fixture-provider", "surface": "fixture-tool/settings", "pointer": "/env/FIXTURE"}}, "platform_regions": []any{map[string]any{"platform": "fixture-provider", "region_env_name": []string{"FIXTURE_REGION"}}}},
		{"kind": "skills", "agent": "fixture-tool", "into": ".fixture/skills", "project_dirs": []string{".fixture/projects"}},
		{"kind": "briefing"},
		{"kind": "files", "from": "assets", "agents": []string{"fixture-tool"}},
		{"kind": "files", "into": ".pi/agent/yolo-patched/fixture-ext", "source": "git+https://example.com/fixture-ext?ref=main", "patches": "patches"},
		{"kind": "config", "config": map[string]any{"name": "fixture", "agent": "fixture-tool", "codec": "json", "path": "~/.fixture/config.json"}},
		{"kind": "config-overlay", "surface": "fixture-tool/settings", "config": map[string]any{"managed": map[string]any{"theme": "dark"}}},
		{"kind": "config-list", "surface": "fixture-tool/settings", "path": "/packages", "add": json.RawMessage(`["one"]`)},
		{"kind": "state", "at": ".fixture", "scope": "machine", "because": "shared provider state"},
		{"kind": "state", "at": ".fixture/work", "scope": "workspace", "because": "workspace state"},
		{"kind": "reads-host", "host": ".fixture/host.json"},
		{"kind": "mount", "host": ".fixture/cache", "into": "ctx/cache"},
		{"kind": "env", "vars": map[string]string{"FIXTURE_FLAG": "1"}, "served_by": "fixture-service"},
		{"kind": "env", "vars": map[string]string{"FIXTURE_PROFILE_FLAG": "2"}, "profile": "fixture-profile"},
		{"kind": "hook", "hook": "boot_check", "from": ".fixture/check", "flags": []string{"--boot-check"}},
		{"kind": "autonomy", "autonomous": map[string]any{"launch": []any{map[string]any{"bin": "fixture-tool", "flags": []string{"--autonomous"}}}}},
		{"kind": "profile", "name": "fixture-profile", "provider": "fixture-provider"},
		{"kind": "provider", "name": "fixture-provider", "endpoints": map[string]any{"openai": map[string]any{"base_url": "https://api.example.test/v1", "wire_api": "openai-chat-completions"}}, "models": map[string]string{"default": "wire-model"}, "model_options": map[string]any{"default": map[string]any{"name": "Fixture model", "context_window": "64000"}}},
		{"kind": "models", "provider": "fixture-provider", "add": json.RawMessage(`[{"id":"wire-model","vendor":"fixture"}]`)},
		{"kind": "loophole", "from": "loopholes/fixture"},
		{"kind": "service", "name": "fixture-service", "jail_daemon": map[string]any{"cmd": []string{"fixture-daemon"}}},
		{"kind": "adapter", "adapts": map[string]any{"from": "openai", "to": "anthropic"}, "address": "http://127.0.0.1:12345"},
		{"kind": "blocked-tool", "bin": "badtool", "message": "blocked", "suggestion": "use fixture-tool", "replacement": "fixture-tool"},
		{"kind": "intercept", "bin": "fixture-wrapper", "forward": []string{"yolo", "fixture-tool"}},
		{"kind": "mcp", "name": "fixture-mcp", "bin": "fixture-server", "config": map[string]any{"command": "fixture-server", "args": []string{"--stdio"}}},
	}
	// The twenty-second kind is a separate program tree with the decided prebuilt/run shape.
	contribs = append(contribs, map[string]any{"kind": "program", "bin": "fixture-built", "via": "pack", "from": "bin", "run": "fixture-built", "build": "make fixture-built", "prebuilt": map[string]any{"linux/amd64": map[string]any{"url": "https://example.test/fixture.tar.gz", "sha256": strings.Repeat("a", 64)}}})
	contribs = append(contribs, map[string]any{
		"kind": "mcp-auth", "store": ".fixture/auth.json", "format": "json", "entries_at": []any{},
		"companions": []any{}, "empty_store": "delete", "lock": map[string]any{"protocol": "none"},
	})
	root := map[string]any{
		"name": "parity-fixture", "description": "Contribution parity fixture.", "skills_tier": "flat",
		"needs":                 []any{map[string]any{"pack": "wire-bridge", "when_bins": []string{"fixture-tool"}}},
		"mcp_remote_transports": []string{"http", "sse"},
		"supersedes":            []any{map[string]any{"capability": "old-capability", "because": "the capability is retired"}},
		"session_homes":         []any{map[string]any{"dir": ".fixture/config", "env": "FIXTURE_HOME", "shared": []string{"auth.json"}}},
		"provider_keys":         map[string]any{"fixture-tool": []any{map[string]any{"surface": "fixture-tool/settings", "pointer": "/env/PROVIDER"}}},
		"provider_env":          map[string]any{"fixture-tool": map[string]any{"surface": "fixture-tool/settings", "pointer": "/env"}},
		"model_list_check": map[string]any{"fixture-tool": map[string]any{
			"rules": []any{map[string]any{"platform": "fixture-provider", "makers": []string{"fixture"}}},
			"references": map[string]any{
				"surface": "fixture-tool/settings", "models": []string{"/model"}, "patterns": []string{"/subagents/*"},
				"scope": "/subagents/*", "selection": map[string]any{"provider": "/provider", "model": "/model"},
				"registry":          []any{map[string]any{"surface": "fixture-tool/settings", "ids": "/models/*/id", "provider": "own"}},
				"profile_subagents": true,
			},
		}},
		"launch_computed": map[string]any{"fixture-tool": []any{map[string]any{"key": "policy", "argv": []string{"--policy", "{value}"}, "opt_in": "FIXTURE_POLICY"}}},
		"contributes":     contribs,
	}
	legacy, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	// The TOML codec emits root keys before array tables. Build table blocks in the actual
	// authored contribution order, and keep metadata at the root ahead of the first table.
	rootValues := jsonx.NewOrderedMap()
	for _, key := range []string{"name", "description", "skills_tier", "needs", "mcp_remote_transports", "supersedes", "session_homes", "provider_keys", "provider_env", "model_list_check", "launch_computed"} {
		rootValues.Set(key, root[key])
	}
	metadata, err := configdoc.Encode(rootValues, configdoc.TOML)
	if err != nil {
		t.Fatal(err)
	}
	var sectionsBuilder strings.Builder
	for _, raw := range contribs {
		kind := raw["kind"].(string)
		if kind == "provider" {
			fields := map[string]any{}
			for k, v := range raw {
				if k != "kind" && k != "name" && k != "models" && k != "model_options" {
					fields[k] = parityTomlValue(t, v)
				}
			}
			sectionsBuilder.WriteString("[providers.fixture-provider]\n")
			writeParityTOMLFields(t, &sectionsBuilder, fields)
			continue
		}
		fields := make(map[string]any, len(raw)-1)
		for k, v := range raw {
			if k != "kind" {
				fields[k] = parityTomlValue(t, v)
			}
		}
		if kind == "program" && fields["bin"] == "fixture-tool" {
			fields["install"] = fields["install_hints"]
			delete(fields, "install_hints")
		}
		if kind == "program" && fields["bin"] == "fixture-built" {
			delete(fields, "run") // Grouped run defaults to bin; the legacy fixture spells it.
		}
		if kind == "config-overlay" {
			config := fields["config"].(map[string]any)
			fields["set"] = config["managed"]
			delete(fields, "config")
		}
		group := strings.ReplaceAll(kind, "-", "_")
		if kind == "config-overlay" {
			group = "overlay"
		}
		sectionsBuilder.WriteString("[[" + group + "]]\n")
		writeParityTOMLFields(t, &sectionsBuilder, fields)
	}
	return legacy, append(metadata, []byte(sectionsBuilder.String())...)
}

func writeParityTOMLFields(t *testing.T, out *strings.Builder, fields map[string]any) {
	t.Helper()
	for _, key := range sortedKeys(fields) {
		value, err := configdoc.EncodeValue(fields[key], configdoc.TOML)
		if err != nil {
			t.Fatalf("encode %s TOML field: %v", key, err)
		}
		out.WriteString(key + " = " + string(value) + "\n")
	}
	out.WriteByte('\n')
}

func parityTomlValue(t *testing.T, value any) any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}
