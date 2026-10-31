package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// The provider-level keys config-user.md §5 rules (a provider's starting `model`, its `alias`,
// `via` and `variants`) are what `yolo config migrate` writes for an old profile (RC-D32), and
// `yolo config set` accepts them. The launch's validator refused them as unknown keys, so a
// migrated user config was refused at the very next launch.
func TestLaunchValidatorAcceptsTheReadableProviderKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), `packs = ["bedrock"]

[providers.bedrock]
region = "us-east-1"
model = "global.openai.gpt-6.1-sol"

[providers.bedrock.variants.opus]
model = "global.anthropic.claude-opus-5-5"
`)
	workspace := t.TempDir()
	cfg, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, workspace, nil)
	for _, e := range sources.Annotate(errs) {
		if strings.Contains(e, "providers.bedrock") {
			t.Errorf("the launch refused a ruled provider key: %s", e)
		}
	}
}

// `pack_sources` names the tree a `<source>/<pack>` handle reads (OQ-RC15), and `yolo config
// migrate` writes it for an old path entry in `packs` (RC-D43). The launch's top-level key check
// called it unknown.
func TestLaunchValidatorAcceptsPackSources(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(filepath.Join(home, "packs", "mine"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(userDir, paths.UserConfigTOMLName), "packs = [\"src/mine\"]\n\n[pack_sources]\nsrc = \"~/packs\"\n")
	workspace := t.TempDir()
	cfg, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, workspace, nil)
	for _, e := range sources.Annotate(errs) {
		if strings.Contains(e, "pack_sources") || strings.Contains(e, "packs") {
			t.Errorf("the launch refused a readable pack source: %s", e)
		}
	}
}

// A converted profile lands on its provider as `model`, or as a variant (RC-D32), and the config
// may select nothing (the selection lives in another machine's file, or on `-p`). The provider's
// own `model` and variants must still be what `-p bedrock` and `-p bedrock/opus` start on, as the
// old `profiles.bedrock` was; they were read only beside an `active_provider` key.
func TestAProvidersOwnModelAndVariantsAreSelectableWithoutActiveProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	packs := activeProviderPacks(t)
	cfg := decode(t, `{"providers": {"bedrock": {"model": "sonnet", "variants": {"opus": {"model": "opus"}}}}}`)
	profiles, err := SelectableProfiles(map[string]packload.UserProfile{}, cfg, packs)
	if err != nil {
		t.Fatal(err)
	}
	if got := profiles["bedrock"].Options["model"]; got != "sonnet" {
		t.Errorf("-p bedrock starts on %q, want the provider's own model sonnet", got)
	}
	if got := profiles["bedrock/opus"].Options["model"]; got != "opus" {
		t.Errorf("-p bedrock/opus starts on %q, want opus", got)
	}
	// A config still selecting through the legacy `profile` key keeps its legacy providers shape.
	legacy := decode(t, `{"profile": {"*": "zai"}, "providers": {"bedrock": {"model": "sonnet"}}}`)
	if profiles, err := SelectableProfiles(map[string]packload.UserProfile{}, legacy, packs); err != nil {
		t.Fatal(err)
	} else if got := profiles["bedrock"].Options["model"]; got != "" {
		t.Errorf("a legacy-selecting config's provider joined the readable namespace: model %q", got)
	}
}

// A --user-layer is never converted (the owner's ruling, config-migration.md: "not included. not
// touched. we only convert included stuff."), so a JSONC layer must keep loading on top of a TOML
// user config: the one-format-per-scope rule is about siblings, not about a layer.
func TestAJSONCUserLayerLoadsOverATOMLUserConfig(t *testing.T) {
	home := layerHome(t, "")
	t.Setenv("YOLO_VERSION", "")
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName), "[install]\nnix = ['user']\n")
	layer := filepath.Join(t.TempDir(), "work.jsonc")
	write(t, layer, `{"packages": ["layer"], "env_sources": [{"FROM_LAYER": "1"}]}`+"\n")
	t.Setenv(UserLayerEnv, layer)
	ws := t.TempDir()

	cfg, src, err := LoadConfigWithSources(ws, true, func(string) {})
	if err != nil {
		t.Fatalf("a JSONC --user-layer over a TOML user config is refused: %v", err)
	}
	errs, _ := ValidateConfig(cfg, ws, nil)
	if errs = src.Annotate(errs); len(errs) != 0 {
		t.Fatalf("the launch refuses a JSONC --user-layer over a TOML user config: %v", errs)
	}
	install, _ := cfg.Get("install")
	got := fmt.Sprint(install)
	if !strings.Contains(got, "layer") || !strings.Contains(got, "user") {
		t.Errorf("the layer and the user config do not both reach the launch: install = %s", got)
	}
}

// Only a provider's starting `model` and its `variants` declare something `-p` selects, so only
// they move a config that selects through neither key into the readable namespace. A table that
// spells only `every_model` (or `alias`, `via`) declares no model, and a legacy JSONC user file
// never switches, whatever its table spells.
func TestOnlyModelOrVariantsSwitchTheNamespaceAndOnlyBesideATOMLUserFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if selectsThroughActiveProvider(decode(t, `{"providers": {"bedrock": {"every_model": {"context": 1000}}}}`)) {
		t.Error("a provider spelling only every_model switched the selection namespace")
	}
	if selectsThroughActiveProvider(decode(t, `{"providers": {"bedrock": {"alias": "b", "via": "x"}}}`)) {
		t.Error("a provider spelling only alias and via switched the selection namespace")
	}
	withModel := decode(t, `{"providers": {"bedrock": {"model": "sonnet"}}}`)
	if !selectsThroughActiveProvider(withModel) {
		t.Fatal("a provider's own model did not switch the namespace beside no user file")
	}
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName), "{}\n")
	if selectsThroughActiveProvider(withModel) {
		t.Error("a legacy JSONC user file's providers switched the selection namespace")
	}
}

// The readable provider keys are accepted where a TOML file writes them; a legacy JSONC file
// cannot spell them, and the launch tells it so, as it did before the readable keys existed.
func TestTheLaunchRefusesReadableProviderKeysInALegacyFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	write(t, filepath.Join(home, ".config", "yolo-jail", paths.UserConfigLegacyJSONCName),
		`{"providers": {"bedrock": {"region": "us-east-1", "model": "m", "variants": {"o": {"model": "o"}}}}}`+"\n")
	workspace := t.TempDir()
	cfg, sources, err := LoadConfigWithSources(workspace, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := ValidateConfig(cfg, workspace, nil)
	joined := strings.Join(sources.Annotate(errs), "\n")
	for _, key := range []string{".model", ".variants"} {
		if !strings.Contains(joined, key) {
			t.Errorf("the launch accepted the readable key %q in a legacy JSONC file:\n%s", key, joined)
		}
	}
}

// RC-D44, as the review of 825892281 found it short: the migration's gate must run the launch's
// capability gate and its undeclared-profile refusal, which `yolo check` predicts beside
// ValidateConfig, and say which checks could not run because a selected pack is not fetched.
func TestTheMigrationGateRunsTheCapabilityAndProfileChecks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	write(t, filepath.Join(dir, paths.UserConfigTOMLName), `packs = ["claude"]
required_capabilities = ["x-nothing-provides-this"]

[active_provider]
default = "x-undeclared-provider"
`)
	g := RunMigrationGate(dir, t.TempDir(), nil)
	joined := strings.Join(g.Problems, "\n")
	for _, want := range []string{"x-nothing-provides-this", "x-undeclared-provider"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the migration gate missed %q:\n%s", want, joined)
		}
	}

	write(t, filepath.Join(dir, paths.UserConfigTOMLName), `packs = ["claude", { git = "https://example.invalid/o/r", ref = "main" }]
`)
	g = RunMigrationGate(dir, t.TempDir(), nil)
	if len(g.Unchecked) == 0 {
		t.Errorf("a selected pack this machine has not fetched left no word on what went unchecked: %+v", g)
	}
}
