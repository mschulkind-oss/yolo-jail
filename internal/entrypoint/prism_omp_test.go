package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
)

// TestConfigureOMPPackProjectsProviders drives OMP's real embedded derive through
// the pack-surface boot path. It must produce OMP's native YAML models catalog,
// carry an environment-variable reference instead of a secret, and explicitly
// mark a provider without a credential as keyless.
func TestConfigureOMPPackProjectsProviders(t *testing.T) {
	e := &Env{
		Home:      t.TempDir(),
		Workspace: t.TempDir(),
		Vars: map[string]string{
			"YOLO_PROVIDERS": `{
              "proxy":{"base_url":"https://proxy.example/v1","wire_api":"openai-responses","api_key_env_name":"PROXY_API_KEY","models":{"fast":"proxy-fast","vision":"proxy-vision"}},
              "anonymous":{"base_url":"https://anonymous.example/v1","wire_api":"openai-responses","models":{"default":"anonymous"}}
            }`,
		},
	}

	if err := ConfigurePackByName(e, "omp"); err != nil {
		t.Fatalf("ConfigurePackByName(omp): %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(e.Home, ".oh-omp", "agent", "models.yml"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := (codec.YAML{}).Decode(raw)
	if err != nil {
		t.Fatalf("models.yml is not valid YAML: %v\n%s", err, raw)
	}
	providers, ok := decoded.(map[string]any)["providers"].(map[string]any)
	if !ok {
		t.Fatalf("models.yml providers = %#v, want an object", decoded)
	}
	proxy, ok := providers["proxy"].(map[string]any)
	if !ok {
		t.Fatalf("proxy missing from OMP catalog: %#v", providers)
	}
	if proxy["baseUrl"] != "https://proxy.example/v1" || proxy["api"] != "openai-responses" {
		t.Errorf("proxy route = %#v, want documented OMP Responses fields", proxy)
	}
	if proxy["apiKey"] != "PROXY_API_KEY" {
		t.Errorf("proxy.apiKey = %#v, want the environment variable name", proxy["apiKey"])
	}
	if proxy["authHeader"] != true {
		t.Errorf("proxy.authHeader = %#v, want true", proxy["authHeader"])
	}
	models, ok := proxy["models"].([]any)
	if !ok || len(models) != 2 {
		t.Errorf("proxy.models = %#v, want the two declared model aliases", proxy["models"])
	}
	anonymous, ok := providers["anonymous"].(map[string]any)
	if !ok || anonymous["auth"] != "none" {
		t.Errorf("keyless provider = %#v, want auth: none", providers["anonymous"])
	}
	if !strings.Contains(string(raw), "PROXY_API_KEY") {
		t.Errorf("models.yml omitted the provider environment reference:\n%s", raw)
	}
}
