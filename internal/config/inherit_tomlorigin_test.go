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
// legacy include writes
// a null TOML cannot carry yet (RC-D50's open gaps): neither format is honest, so delivery fails
// naming both sides, the legacy line and the next step, rather than handing readable values to
// the legacy reader.
func TestInheritedDeliveryFailsRatherThanFallBackWithTOMLWrittenValues(t *testing.T) {
	src, filtered := loadConvertedHostConfig(t, "include_if_found = ['extra.jsonc']\n"+convertedTOMLUserConfig,
		map[string]string{"extra.jsonc": `{"active_provider": {"claude": null}}`})
	values := filtered[InheritPreflight]
	if _, present := values.Get("active_provider"); !present {
		t.Fatalf("fixture: the legacy include's null did not reach the preflight scope, so this proves nothing: %v", values)
	}
	body, format, fallback, err := RenderFilteredForDelivery(values, src, InheritPreflight, "")
	if err == nil {
		t.Fatalf("delivery fell back (format=%v, reason %q) with TOML-written values:\n%s", format, fallback, body)
	}
	for _, want := range []string{paths.UserConfigTOMLName, "config.active_provider.claude", "extra.jsonc:1:", "yolo config migrate"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the delivery failure does not say %q: %v", want, err)
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
