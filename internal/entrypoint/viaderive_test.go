package entrypoint

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// viaderive_test.go pins the jail half of `via` (docs/design/wire-bridge-gateway.md
// OQ-WG6/WG7 (d)): the via pair crosses in YOLO_PROFILES under reserved keys, only the
// agent whose active profile is a via profile gets a ctx.via_url, and each OpenAI-speaking
// derive writes it as the selected provider's base URL — and nothing else changes.

const viaBase = "http://127.0.0.1:8216"

func TestLoadProfilesDecodesTheViaPair(t *testing.T) {
	e := &Env{Vars: map[string]string{"YOLO_PROFILES": `{"pz":{"provider":"zai","model":"glm",` +
		`"` + packload.WireViaKey + `":"wire-bridge","` + packload.WireViaBaseKey + `":"` + viaBase + `"}}`}}
	got := e.LoadProfiles()["pz"]
	if got.Via != "wire-bridge" || got.ViaBase != viaBase {
		t.Errorf("via pair = %q/%q, want wire-bridge/%s", got.Via, got.ViaBase, viaBase)
	}
	if _, leaked := got.Options[packload.WireViaKey]; leaked || got.Options["model"] != "glm" {
		t.Errorf("options = %v, want model only (the via pair is not an option)", got.Options)
	}
}

func TestSurfaceSelectionGivesViaURLOnlyToTheViaAgent(t *testing.T) {
	resolved := map[string]packload.ResolvedProfile{
		"pz":    {Provider: "zai", Via: "wire-bridge", ViaBase: viaBase},
		"plain": {Provider: "zai"},
	}
	profiles := map[string]string{"pi": "pz", "opencode": "plain"}
	if got := surfaceSelectionFor(nil, resolved, profiles, manifest.Surface{Agent: "pi", Name: "models"}).ViaURL; got != viaBase+"/agent/pi" {
		t.Errorf("pi ViaURL = %q, want %s/agent/pi", got, viaBase)
	}
	for _, agent := range []string{"opencode", "codex"} {
		if got := surfaceSelectionFor(nil, resolved, profiles, manifest.Surface{Agent: agent}).ViaURL; got != "" {
			t.Errorf("%s ViaURL = %q, want none (its profile is not a via profile)", agent, got)
		}
	}
}

func viaProvidersTable() map[string]any {
	return map[string]any{
		"zai": map[string]any{
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://api.z.ai/api/paas/v4", "wire_api": "openai-chat-completions"}},
			"models": map[string]any{"default": "glm-5.3"},
		},
		"other": map[string]any{
			"api_key_env_name": "OTHER_KEY",
			"endpoints": map[string]any{"openai": map[string]any{
				"base_url": "https://other.example/v1", "wire_api": "openai-chat-completions"}},
		},
	}
}

func TestPiDeriveWritesTheViaURLForTheSelectedProviderOnly(t *testing.T) {
	e := &Env{Vars: map[string]string{}}
	via := viaBase + "/agent/pi"
	provs := piModelsFor(t, surfaceSelection{Profile: "pz", Provider: "zai", ViaURL: via}, e, viaProvidersTable())
	zai := provs["zai"].(map[string]any)
	if zai["baseUrl"] != via || zai["api"] != "openai-completions" {
		t.Errorf("zai row = %v, want the via URL speaking openai-completions", zai)
	}
	if zai["apiKey"] != "local" {
		t.Errorf("a keyless via row keeps a key string (pi drops keyless models; the via address is loopback): %v", zai["apiKey"])
	}
	if other := provs["other"].(map[string]any); other["baseUrl"] != "https://other.example/v1" {
		t.Errorf("a provider the profile does not select must keep its own URL: %v", other)
	}
	native := piModelsFor(t, surfaceSelection{Profile: "pz", Provider: "zai"}, e, viaProvidersTable())
	if native["zai"].(map[string]any)["baseUrl"] != "https://api.z.ai/api/paas/v4" {
		t.Errorf("with no via the selected provider must keep its own URL: %v", native["zai"])
	}
}

func TestOmpDeriveWritesTheViaURLForTheSelectedProvider(t *testing.T) {
	script, s := deriveSurface(t, "omp", "oh-omp/models")
	via := viaBase + "/agent/omp"
	got, _, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script,
		surfaceSelection{Profile: "pz", Provider: "zai", ViaURL: via},
		map[string]map[string]any{manifest.SourceProviders: viaProvidersTable()})
	if err != nil {
		t.Fatal(err)
	}
	provs := got["providers"].(map[string]any)
	if zai := provs["zai"].(map[string]any); zai["baseUrl"] != via || zai["api"] != "openai-completions" {
		t.Errorf("zai row = %v, want the via URL", zai)
	}
	if other := provs["other"].(map[string]any); other["baseUrl"] != "https://other.example/v1" {
		t.Errorf("other row must keep its URL: %v", other)
	}
}

func TestOpencodeDeriveWritesTheViaURLForTheSelectedProvider(t *testing.T) {
	script, s := deriveSurface(t, "opencode", "opencode/config")
	via := viaBase + "/agent/opencode"
	got, _, err := deriveComputedLayer(&Env{Vars: map[string]string{}}, s, script,
		surfaceSelection{Profile: "pz", Provider: "zai", ViaURL: via},
		map[string]map[string]any{manifest.SourceProviders: viaProvidersTable()})
	if err != nil {
		t.Fatal(err)
	}
	provs := got["provider"].(map[string]any)
	opts := provs["zai"].(map[string]any)["options"].(map[string]any)
	if opts["baseURL"] != via || opts["apiKey"] != "local" {
		t.Errorf("zai options = %v, want the via URL and the loopback key string", opts)
	}
	if o := provs["other"].(map[string]any)["options"].(map[string]any); o["baseURL"] != "https://other.example/v1" {
		t.Errorf("other must keep its URL: %v", o)
	}
}

// TestAViaProfileReachesPiThroughTheWire is the chain end to end on the jail side: the
// host's own writer (packload.ProfilesWireTable) produces YOLO_PROFILES, the boot path's
// reader decodes it, the per-surface selection keys it by agent, and the real pi derive
// writes the per-agent URL. Each link is one a revert could cut silently.
func TestAViaProfileReachesPiThroughTheWire(t *testing.T) {
	wire, err := jsonx.DumpsCompact(packload.ProfilesWireTable(map[string]packload.ResolvedProfile{
		"pz": {Provider: "zai", Via: "wire-bridge", ViaBase: viaBase},
	}))
	if err != nil {
		t.Fatal(err)
	}
	e := &Env{Vars: map[string]string{"YOLO_PROFILES": wire}}
	sel := surfaceSelectionFor(nil, e.LoadProfiles(), map[string]string{"pi": "pz"},
		manifest.Surface{Agent: "pi", Name: "models"})
	provs := piModelsFor(t, sel, e, viaProvidersTable())
	if got := provs["zai"].(map[string]any)["baseUrl"]; got != viaBase+"/agent/pi" {
		t.Errorf("pi's zai baseUrl = %v, want %s/agent/pi", got, viaBase)
	}
}
