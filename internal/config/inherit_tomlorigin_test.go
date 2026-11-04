package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// convertedTOMLUserConfig is a user config as `yolo config migrate` leaves it, with the
// readable-only shapes the legacy reader misjudges: a bare named pack source, a handle qualified
// by it, a bare git pack table and a provider's own `model`.
const convertedTOMLUserConfig = `packs = ['claude', 'mine/tools', {git = 'https://example.invalid/acme/craft', ref = 'main', bare = true}]

[pack_sources.mine]
path = '~/mine-packs'
bare = true

[providers.bedrock]
region = 'us-east-1'
model = 'global.example.model-1'
`

// loadConvertedHostConfig loads a host whose user config.toml is body, plus the files in extra
// beside it, and returns the provenance and each inherited scope's filtered values.
func loadConvertedHostConfig(t *testing.T, body string, extra map[string]string) (*Sources, map[InheritScope]*jsonx.OrderedMap) {
	t.Helper()
	host := t.TempDir()
	t.Setenv("HOME", host)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	write(t, filepath.Join(host, "mine-packs", "tools", "pack.toml"), "description = 'tools'\n")
	write(t, filepath.Join(host, ".config", "yolo-jail", paths.UserConfigTOMLName), body)
	for name, content := range extra {
		write(t, filepath.Join(host, ".config", "yolo-jail", name), content)
	}
	effective, src, err := LoadConfigWithoutWorkspaceFileWithSources(t.TempDir(), true, func(string) {})
	if err != nil {
		t.Fatalf("host effective config: %v", err)
	}
	filtered := map[InheritScope]*jsonx.OrderedMap{}
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		values, _, err := FilterInheritErr(effective, scope)
		if err != nil {
			t.Fatal(err)
		}
		filtered[scope] = values
	}
	return src, filtered
}

// A JSONC copy of values a TOML file wrote is read in the jail by the legacy reader, which
// reported the converted config's qualified handle, `bare` and the provider's `model` as broken
// although the host accepted every one. So no JSONC copy of them is ever made: the render that
// would make one refuses, naming the TOML file. Red if the refusal is removed.
func TestInheritedJSONCRenderRefusesValuesATOMLFileWrote(t *testing.T) {
	src, filtered := loadConvertedHostConfig(t, convertedTOMLUserConfig, nil)
	for scope, values := range filtered {
		body, err := RenderFilteredWithOrigins(values, src, scope, "")
		if err == nil {
			t.Fatalf("%s: TOML-written values were rendered as legacy JSONC:\n%s", scope, body)
		}
		if !strings.Contains(err.Error(), paths.UserConfigTOMLName) {
			t.Errorf("%s: the refusal does not name the TOML file: %v", scope, err)
		}
		// The TOML route is still open, and it is the one delivery takes.
		_, format, fallback, err := RenderFilteredForDelivery(values, src, scope, "")
		if err != nil || format != configdoc.TOML || fallback != "" {
			t.Fatalf("%s: the converted config did not deliver as TOML: format=%v fallback=%q err=%v", scope, format, fallback, err)
		}
	}
}

// A legacy file included from a TOML one keeps the JSONC route only while the payload holds no
// TOML-written value a reader judges by format. Here the TOML file writes such shapes and the
// legacy include writes a key the readers keep in its legacy spelling (`model_facts`): neither
// format carries the scope as it is, so it crosses as TOML, always (LC-D4): as the lowering
// respells it, and where the lowering leaves a legacy value with no TOML spelling, without it,
// disclosed once naming the legacy line. It never refuses, and it never hands readable values to
// the legacy reader. Fails if the combined refusal comes back, or if an omission goes unnamed.
func TestInheritedDeliveryWithTOMLWrittenValuesCrossesAsTOMLLeavingOutWhatHasNoSpelling(t *testing.T) {
	src, filtered := loadConvertedHostConfig(t, "include_if_found = ['extra.jsonc']\n"+convertedTOMLUserConfig,
		map[string]string{"extra.jsonc": `{"model_facts": {"m1": {"context_window": 1000}}}`})
	values := filtered[InheritPreflight]
	if _, present := values.Get("model_facts"); !present {
		t.Fatalf("fixture: the legacy include's model_facts did not reach the preflight scope, so this proves nothing: %v", values)
	}
	var sawTOMLWritten, sawLegacy bool
	keep := func(v *jsonx.OrderedMap, tomlWritten func(string) bool) (*jsonx.OrderedMap, []InheritOmission) {
		sawTOMLWritten = tomlWritten("packs[1]") && tomlWritten("providers.bedrock.model")
		sawLegacy = !tomlWritten("model_facts.m1")
		return v, nil
	}
	for name, lower := range map[string]UserScopeLowering{"no lowering": nil, "a lowering that respells nothing": keep} {
		d, err := RenderInheritedDelivery(values, src, InheritPreflight, "", lower)
		if err != nil {
			t.Fatalf("%s: the crossing refused a scope the host accepted: %v", name, err)
		}
		if d.Format != configdoc.TOML || strings.Contains(d.Body, "model_facts") || !strings.Contains(d.Body, "mine/tools") {
			t.Fatalf("%s: want TOML holding the TOML handle and no model_facts, got format=%v:\n%s", name, d.Format, d.Body)
		}
		if len(d.Omissions) != 1 || !strings.HasPrefix(d.Omissions[0].Path, "config.model_facts") ||
			!strings.Contains(d.Omissions[0].Where, "extra.jsonc:1:") || !strings.Contains(d.Omissions[0].String(), "without it") {
			t.Errorf("%s: the left-out legacy value is not disclosed once, naming its line: %+v", name, d.Omissions)
		}
	}
	if !sawTOMLWritten || !sawLegacy {
		t.Errorf("the lowering is not told which values a TOML file wrote (LC-D5): toml=%v legacy=%v", sawTOMLWritten, sawLegacy)
	}

	// A lowering that converts it is what crosses, as TOML, with no host origins: the jail
	// judges a value by its origin's format, and the host file behind a respelled value is the
	// legacy one.
	respell := func(v *jsonx.OrderedMap, _ func(string) bool) (*jsonx.OrderedMap, []InheritOmission) {
		facts := jsonx.NewOrderedMap()
		row := jsonx.NewOrderedMap()
		row.Set("context", 1000)
		facts.Set("m1", row)
		v.Set("model_facts", facts)
		return v, nil
	}
	d, err := RenderInheritedDelivery(values, src, InheritPreflight, "", respell)
	if err != nil || d.Format != configdoc.TOML || d.FallbackReason != "" || len(d.Omissions) != 0 {
		t.Fatalf("the lowered scope did not cross as TOML: %+v err=%v", d, err)
	}
	if !strings.Contains(d.Body, "context = 1000") || strings.Contains(d.Body, originsTOMLLinePrefix) {
		t.Errorf("the lowered scope is not the lowering's, or names host origins:\n%s", d.Body)
	}
}

// LC-D8: in the inherited bottom layer a legacy null the readable schema has no removal spelling
// for means absence, the rule the crossing already applies to env: it neither refuses nor is
// disclosed. Fails if such a null blocks the TOML delivery again.
func TestInheritedLegacyNullWithNoRemovalSpellingCrossesAsAbsent(t *testing.T) {
	src, filtered := loadConvertedHostConfig(t, "include_if_found = ['extra.jsonc']\n"+convertedTOMLUserConfig,
		map[string]string{"extra.jsonc": `{"mise_tools": null, "resources": null}`})
	for scope, values := range filtered {
		d, err := RenderInheritedDelivery(values, src, scope, "", nil)
		if err != nil || d.Format != configdoc.TOML || len(d.Omissions) != 0 {
			t.Fatalf("%s: a legacy null did not cross as absence: %+v err=%v", scope, d, err)
		}
	}
}

// The guard is exactly the readers' own split. A provider's readable-only key a TOML file wrote
// is refused on its own, with no pack entry beside it; and a TOML file writing only keys both
// formats read alike keeps the JSONC route a legacy include may still need.
func TestInheritedJSONCRenderRefusesOnlyFormatJudgedTOMLValues(t *testing.T) {
	src, filtered := loadConvertedHostConfig(t, "[providers.bedrock]\nregion = 'us-east-1'\nmodel = 'global.example.model-1'\n", nil)
	if _, err := RenderFilteredWithOrigins(filtered[InheritPreflight], src, InheritPreflight, ""); err == nil ||
		!strings.Contains(err.Error(), "config.providers.bedrock.model") {
		t.Fatalf("a TOML-written provider model was not refused by name: %v", err)
	}

	src, filtered = loadConvertedHostConfig(t, "[install]\nnix = ['strace']\n", nil)
	if _, err := RenderFilteredWithOrigins(filtered[InheritPreflight], src, InheritPreflight, ""); err != nil {
		t.Fatalf("a TOML-written key both formats read alike lost its JSONC route: %v", err)
	}
}
