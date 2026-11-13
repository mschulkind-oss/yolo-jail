package run

// providertable_equivalence_test.go is the byte-identity proof for carrying a model said once
// (`every_model`, `twin_of`, `compact_at`) and catalog facts (C7) into a launch
// (docs/reference/config/rulings.md#RC-D29): every config that launched before resolves to the
// SAME provider table after. The table is what crosses into the jail as YOLO_PROVIDERS and the
// one input every derive reads its providers from, so an identical table is an identical
// rendered provider/model config for every agent.
//
// It runs the launch's own composition (composedProviders, the function a launch calls) over
// every shipped pack that declares a provider, alone and all together, at both served sets a
// container launch and a host-shaped launch compose with, for representative user configs in
// both formats: legacy JSONC, decoded as the launch decodes it, and TOML, loaded through the
// launch's own config loader. Each config must pass the launch's validation first, so every
// case is one that launched before. testdata/provider-table-equivalence.golden holds one
// sha256 per case, written by the code before the change; YOLO_UPDATE_PROVIDER_GOLDEN=1
// rewrites it, which a change claiming byte identity must never need.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const providerGoldenPath = "testdata/provider-table-equivalence.golden"

// equivalenceJSONCConfigs are legacy JSONC user configs, each exercising one existing provider
// surface: string and object-form aliases over a shipped provider, a deleted alias, a fact
// tombstone, shared `model_facts` through `extends`, OpenRouter routing, a provider of the
// user's own, a region override and a dropped provider.
var equivalenceJSONCConfigs = map[string]string{
	"jsonc-empty": `{}`,
	"jsonc-zai-aliases": `{"providers": {"zai": {
    "models": {"fast": "glm-5.3-flash", "glm-4.6": null,
      "big": {"id": "glm-5.3", "name": "GLM big", "context_window": 200000, "max_tokens": 64000,
        "reasoning": true, "input": ["text", "image"], "vendor": "zhipu",
        "cost": {"input": 1, "output": 2.5, "cache_read": 0.1, "cache_write": 0}}},
    "options": {"context_window": "200000"}}}}`,
	"jsonc-shared-facts": `{"model_facts": {"opus": {"max_tokens": 32000, "reasoning": true, "context_window": 1000000}},
  "providers": {"openrouter": {"models": {
    "opus": {"id": "anthropic/claude-opus-5", "extends": "opus", "vendor": "anthropic",
      "openrouter_routing": {"order": ["anthropic"], "allow_fallbacks": false}},
    "opus-small": {"id": "anthropic/claude-opus-5", "extends": "opus", "wire_id": "anthropic/claude-opus-5", "context_window": 200000}}}}}`,
	"jsonc-own-provider": `{"providers": {"acme": {"api_key_env_name": ["ACME_KEY", "ACME_TOKEN"],
    "endpoints": {"openai": {"base_url": "https://acme.example/v1", "wire_api": "openai-chat-completions"}},
    "models": {"default": "acme-1", "big": {"id": "acme-2", "max_tokens": 8000, "description": "Big"}},
    "options": {"model": "default", "context_window": "128000"},
    "capabilities": ["web_search"]}}}`,
	"jsonc-region-and-drop": `{"providers": {"bedrock": {"region": "us-west-2"}, "llamacpp": null, "cerebras": null}}`,
	"jsonc-tombstone": `{"providers": {"openai-codex": {"models": {
    "gpt-6-luna": {"id": "gpt-6-luna", "context_window": null, "name": "Luna"}}}}}`,
}

// equivalenceTOMLConfigs are readable user configs, loaded through config.LoadConfigWithSources
// exactly as a launch loads ~/.config/yolo-jail/config.toml. None writes a model row: a readable
// row is keyed by its id with no `id` field, which the launch refused before RC-D29, so no TOML
// config with one ever launched.
var equivalenceTOMLConfigs = map[string]string{
	"toml-zai-aliases": `[providers.zai]
api_key_env_name = 'ZAI_KEY'
[providers.zai.options]
context_window = '200000'
`,
	"toml-own-provider": `[providers.acme]
api_key_env_name = 'ACME_KEY'
endpoints = { openai = { base_url = 'https://acme.example/v1' } }
capabilities = ['web_search']
[providers.acme.options]
model = 'acme-1'
`,
	"toml-region": `[providers.bedrock]
region = 'eu-central-1'
`,
}

func TestProviderTableIsByteIdenticalForEveryExistingConfigAndShippedPack(t *testing.T) {
	type userCase struct {
		name string
		cfg  *jsonx.OrderedMap
	}
	var users []userCase
	for name, doc := range equivalenceJSONCConfigs {
		t.Setenv("HOME", t.TempDir())
		decoded, err := jsonx.Decode([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		cfg := decoded.(*jsonx.OrderedMap)
		if errs, _ := config.ValidateConfig(cfg, t.TempDir(), nil); len(errs) != 0 {
			t.Fatalf("%s is not a config that launched before: %v", name, errs)
		}
		users = append(users, userCase{name, cfg})
	}
	for name, doc := range equivalenceTOMLConfigs {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		file := filepath.Join(home, ".config", "yolo-jail", paths.UserConfigTOMLName)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
			t.Fatal(err)
		}
		workspace := t.TempDir()
		cfg, _, err := config.LoadConfigWithSources(workspace, true, func(string) {})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if errs, _ := config.ValidateConfig(cfg, workspace, nil); len(errs) != 0 {
			t.Fatalf("%s is not a config that launched before: %v", name, errs)
		}
		users = append(users, userCase{name, cfg})
	}
	t.Setenv("HOME", t.TempDir())

	type packSet struct {
		name  string
		packs []*packload.Pack
	}
	sets := []packSet{{name: "no-packs"}}
	all := packload.Embedded()
	for _, p := range all {
		if len(p.Decl.Providers()) > 0 {
			sets = append(sets, packSet{name: "pack-" + p.Name, packs: []*packload.Pack{p}})
		}
	}
	sets = append(sets, packSet{name: "all-shipped", packs: all})
	if len(sets) < 10 {
		t.Fatalf("only %d pack sets; the shipped provider packs did not load", len(sets))
	}
	served := map[string]packload.ServedDaemons{
		"nothing-served":     packload.NothingServed(),
		"wire-bridge-served": packload.ServedInJail([]string{"wire-bridge"}),
	}

	got := map[string]string{}
	bodies := map[string]string{}
	for _, user := range users {
		for _, set := range sets {
			for servedName, s := range served {
				key := user.name + " / " + set.name + " / " + servedName
				cfg := jsonx.DeepCopy(user.cfg).(*jsonx.OrderedMap)
				table, _, err := composedProviders(cfg, set.packs, s)
				body := ""
				if err != nil {
					body = "error: " + err.Error()
				} else if table == nil {
					body = "null"
				} else if body, err = jsonx.DumpsIndent(table, 1); err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256([]byte(body))
				got[key] = hex.EncodeToString(sum[:])
				bodies[key] = body
			}
		}
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rendered strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&rendered, "%s\t%s\n", got[k], k)
	}
	// YOLO_PROVIDER_GOLDEN_DUMP=<dir> writes every composed table, for reading a difference whole.
	if dir := os.Getenv("YOLO_PROVIDER_GOLDEN_DUMP"); dir != "" {
		for _, k := range keys {
			name := strings.NewReplacer(" / ", "__", "/", "_").Replace(k) + ".json"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(bodies[k]), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if os.Getenv("YOLO_UPDATE_PROVIDER_GOLDEN") == "1" {
		if err := os.WriteFile(providerGoldenPath, []byte(rendered.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(providerGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v", providerGoldenPath, err)
	}
	want := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		sum, key, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("malformed golden line %q", line)
		}
		want[key] = sum
	}
	if len(want) != len(got) {
		t.Errorf("golden holds %d cases, the run composed %d", len(want), len(got))
	}
	for _, k := range keys {
		if want[k] != got[k] {
			t.Errorf("%s: provider table changed (sha256 %s, golden %s):\n%s", k, got[k], want[k], bodies[k])
		}
	}
}
