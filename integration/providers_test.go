package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// piBuiltinApis is pi's OWN api registry, copied from the package the launcher installs —
// pi 0.84.4, `@earendil-works/pi-ai/dist/compat.js:108-119` (`BUILTIN_APIS`; npm-extracted
// to a scratch prefix, the CLI never run). It is the set of `api` values a pi provider
// entry can name and still reach a wire protocol; the schema itself is a free string
// (`pi-coding-agent/dist/core/model-config.js`, `ProviderConfigSchema.api`), so an unknown
// value loads cleanly and dies at first request.
//
// This list is the point of the test it backs: D1 shipped because the value was checked
// against a list YOLO owns. Checking it here against a list PI owns is the tier that
// catches that. When pi ships a new api id, this copy goes stale — the failure mode is a
// false RED naming the value pi added, not a silent green, and the comment above says
// where to re-read it.
var piBuiltinApis = map[string]bool{
	"anthropic-messages":      true,
	"openai-completions":      true,
	"openai-responses":        true,
	"openai-codex-responses":  true,
	"azure-openai-responses":  true,
	"google-generative-ai":    true,
	"google-vertex":           true,
	"mistral-conversations":   true,
	"bedrock-converse-stream": true,
	"pi-messages":             true,
}

// wireAPIAssign is one `wire_api = "..."` in codex's config.toml.
var wireAPIAssign = regexp.MustCompile(`wire_api\s*=\s*"([^"]*)"`)

// renderedSurface reads a rendered agent surface back from the HOST-side copy of the
// jail's per-workspace home overlay — under `<workspace>/.yolo/home`, the same tree the
// jail bind-mounts at /home/agent (jail-home.md §2), addressed exactly as it lands on
// disk: a top-level dotdir maps to its own subdir (`~/.codex/config.toml` →
// .yolo/home/codex/config.toml) and anything under `~/.config` runs through the shared
// config/ overlay (`~/.config/opencode/opencode.json` →
// .yolo/home/config/opencode/opencode.json). No exec into the container:
// mcp_test.go's hasChromeDevtools is the pattern, and reading the host copy is what lets
// one launch feed assertions on three agents' files.
func renderedSurface(t *testing.T, dir string, rel ...string) []byte {
	t.Helper()
	p := filepath.Join(append([]string{dir, ".yolo", "home"}, rel...)...)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading the rendered surface %s: %v", p, err)
	}
	return data
}

// TestProvidersRenderInTheAgentsOwnVocabulary is the integration tier D1 skipped
// (docs/reference/providers.md): the provider table is delivered in each agent's own
// vocabulary, and only a rendered file can say so. Every unit test in the delivery chain
// validates a value against a set YOLO owns; the defect is that the consumer owns a
// different set, so the whole chain went green while writing values the agents refuse.
//
// This test LANDED RED, ON PURPOSE (commit cee9c1fc, per provider-table-fidelity-plan.md
// build order step 1): it is the regression test for three defects that shipped, and the
// step that fixed each turned its subtest green — A0 (7fa624ba) turned D10/D11 green, A3
// (0f04632d) turned D1 green, and all four subtests are green as of A3.
//
//   - D1 (§3): codex rendered `wire_api = "openai-chat"` — codex accepts `responses` only
//     (`chat` was removed from the product; local-model-endpoints.md §"Codex CLI",
//     source-verified 2026-08-20) — and pi rendered `api = "openai-chat"`, which is in no
//     pi registry (piBuiltinApis above). → the first two subtests. A3 retired the
//     four-value enum for the three canonical protocol names and gave each derive a
//     dialect map, so a value reaches an agent only in that agent's own spelling — or the
//     agent gets no entry at all.
//   - D11 (§3.5): the credential was named on `apiKeyEnv`, a field pi's
//     ProviderConfigSchema does not have (model-config.js — name, baseUrl, apiKey, api,
//     oauth, headers, compat, authHeader, models, modelOverrides), so the provider had no
//     configured credential at all. pi's indirection is the config-value syntax on
//     `apiKey`. → the third subtest
//   - D10 (§3.5): opencode reads `baseURL`/`apiKey` only inside a provider's `options`
//     object (`provider.ts:82-94` of upstream v1.18.18 — the tag the shipped binary
//     reports), and the derive wrote them top-level. → the fourth subtest
//
// One launch, six packs. Selecting a pack RENDERS its surfaces and installs no CLI, so
// the four agent packs plus two provider packs cost one boot rather than four vendor installs
// (agents_test.go TestPackRendersConfigAndLauncher is the same trick, one pack at a
// time). The composed table needs no user `providers` entry: pack-shipped `kind:
// "provider"` facts compose in on their own (internal/packload ComposeProviders), and
// packs/claude's bedrock reaches no derive because it names no facts at all — its
// declaration is a name, and the compose leaves the entry empty.
func TestProvidersRenderInTheAgentsOwnVocabulary(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "zai", "cerebras", "codex", "pi", "opencode"]}`)
	// zai and cerebras ship api_key_env_name entries, and the selected-pack credential
	// preflight refuses a launch whose environment cannot deliver them
	// (internal/packload ProviderCredentialGaps). Set before the launch: the refusal is
	// correct behaviour and is D4's territory to change, not this test's.
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")
	t.Setenv("CEREBRAS_API_KEY", "integration-probe-not-a-real-key")

	r := runYolo(t, dir, "true")
	if r.rc != 0 {
		t.Fatalf("six-pack launch failed: rc %d\n%s", r.rc, r.combined())
	}

	codexTOML := renderedSurface(t, dir, "codex", "config.toml")

	var piModels struct {
		Providers map[string]struct {
			BaseURL   string `json:"baseUrl"`
			API       string `json:"api"`
			APIKey    string `json:"apiKey"`
			APIKeyEnv string `json:"apiKeyEnv"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(renderedSurface(t, dir, "pi", "agent", "models.json"), &piModels); err != nil {
		t.Fatalf("parsing pi's rendered models.json: %v", err)
	}

	var ocConfig struct {
		Provider map[string]struct {
			NPM     string `json:"npm"`
			BaseURL string `json:"baseURL"`
			APIKey  string `json:"apiKey"`
			Options *struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	// opencode's surface path is ~/.config/opencode/opencode.json (packs/opencode/
	// pack.json), and ~/.config is ONE shared overlay — `ws/config` → /home/agent/.config
	// (assemble_parts.go podmanBaseMounts) — so the host-side path runs through "config",
	// not through a per-agent dir the way codex (`~/.codex`) and pi (`~/.pi/agent`) get.
	if err := json.Unmarshal(renderedSurface(t, dir, "config", "opencode", "opencode.json"), &ocConfig); err != nil {
		t.Fatalf("parsing opencode's rendered opencode.json: %v", err)
	}

	t.Run("codex renders only wire_api values codex accepts", func(t *testing.T) {
		// `responses` is the only wire_api codex has: `chat` was deprecated 2025-12-09,
		// non-functional by v0.92.0 and removed early Feb 2026, and the binary says so
		// itself ("`wire_api = "chat"` is no longer supported"). The red this subtest
		// landed with (cee9c1fc) was `wire_api = "openai-chat"`, rendered verbatim from
		// yolo's then-four-value enum — D1, closed by A3's dialect map.
		for _, m := range wireAPIAssign.FindAllStringSubmatch(string(codexTOML), -1) {
			if got := m[1]; got != "responses" {
				t.Errorf("codex config.toml carries wire_api = %q, and codex accepts "+
					"only %q (chat was removed from the product) — a value yolo validated "+
					"against its own enum and handed to codex verbatim (D1)", got, "responses")
			}
		}
		// No vacuity guard here ON PURPOSE: A3's fix for an unspeakable protocol is to
		// emit NOTHING (design §3.4), so codex's zai entry legitimately disappears and a
		// "zai must be present" assertion would be wrong. That the composed table reached
		// this launch at all is what the two pi subtests and the opencode subtest below
		// prove — same table, same launch.
	})

	t.Run("pi api values are in pi's own registry", func(t *testing.T) {
		zai, ok := piModels.Providers["zai"]
		if !ok {
			t.Fatalf("pi's models.json has no zai entry — the composed provider table did "+
				"not reach pi's derive at all; entries: %v", keysOf(piModels.Providers))
		}
		if !piBuiltinApis[zai.API] {
			t.Errorf("pi's zai entry carries api = %q, which is not one of pi's registered "+
				"api ids (pi 0.84.4 BUILTIN_APIS); the schema accepts it as a free string and "+
				"pi fails at first request with \"No API provider registered for api\" (D1). "+
				"pi's spelling of chat completions is openai-completions", zai.API)
		}
	})

	t.Run("pi zai credential is on apiKey in pi config-value syntax", func(t *testing.T) {
		zai, ok := piModels.Providers["zai"]
		if !ok {
			t.Fatalf("pi's models.json has no zai entry — the composed provider table did "+
				"not reach pi's derive at all; entries: %v", keysOf(piModels.Providers))
		}
		if zai.APIKey == "" {
			t.Errorf("pi's zai entry carries no apiKey, so the provider has no configured " +
				"credential: pi filters its models from the available list and a forced stream " +
				"throws \"No API key for provider\" (D11)")
		} else if !strings.HasPrefix(zai.APIKey, "$") || !strings.Contains(zai.APIKey, "ZAI_API_KEY") {
			t.Errorf("pi's zai entry carries apiKey = %q; pi's env indirection is the "+
				"config-value syntax on apiKey — %q or %q — not a separate field (D11)",
				zai.APIKey, "${ZAI_API_KEY}", "$ZAI_API_KEY")
		}
		// The negative half of D11: `apiKeyEnv` is not a field in pi's
		// ProviderConfigSchema, so the schema tolerates it and nothing reads it — dead
		// configuration that reads as the thing delivering the credential.
		if zai.APIKeyEnv != "" {
			t.Errorf("pi's zai entry still carries apiKeyEnv = %q; pi has no such field, so "+
				"the value is dead configuration whatever else the entry says (D11)", zai.APIKeyEnv)
		}
	})

	t.Run("opencode zai baseURL and apiKey are under options", func(t *testing.T) {
		zai, ok := ocConfig.Provider["zai"]
		if !ok {
			t.Fatalf("opencode's config has no zai provider entry — the composed provider " +
				"table did not reach opencode's derive at all")
		}
		if zai.NPM == "" {
			t.Errorf("opencode's zai entry carries no npm (the SDK package), so it is not a " +
				"complete catalog entry")
		}
		if zai.Options == nil {
			t.Fatalf("opencode's zai entry carries no options object, and opencode reads " +
				"baseURL/apiKey only from provider.options (provider.ts of upstream v1.18.18) — " +
				"the top-level spelling produces \"undefined/chat/completions cannot be parsed " +
				"as a URL\" and zero requests (D10)")
		}
		if zai.Options.BaseURL == "" {
			t.Errorf("opencode's zai entry carries no options.baseURL; the URL opencode's SDK " +
				"actually dials lives there (D10)")
		}
		if zai.Options.APIKey == "" || !strings.Contains(zai.Options.APIKey, "ZAI_API_KEY") {
			t.Errorf("opencode's zai entry carries options.apiKey = %q; the credential must be "+
				"under options too, and name ZAI_API_KEY (D10)", zai.Options.APIKey)
		}
		// The negative half of D10: the top-level spelling is the part opencode ignores, and
		// it is what the derive emitted before A0 (7fa624ba) moved both halves under
		// `options`. Both halves must move together, or the fix reads green while the URL
		// still never reaches the SDK — so this asserts the top-level keys are GONE, not
		// merely that `options` exists.
		if zai.BaseURL != "" {
			t.Errorf("opencode's zai entry still carries a TOP-LEVEL baseURL = %q; opencode "+
				"reads it only under options, so a value here is dead configuration (D10)", zai.BaseURL)
		}
	})

	// The second provider pack, same launch: cerebras is chat-completions-only, so the
	// pairing claims that differ from zai's are the interesting ones — pi speaks it (the
	// same dialect row), opencode speaks it (URL only), and no derive invents an
	// anthropic route for it. docs/design/cerebras-pack-and-copilot-delivery.md §audit.
	t.Run("pi cerebras entry is the same chat-completions dialect", func(t *testing.T) {
		cerebras, ok := piModels.Providers["cerebras"]
		if !ok {
			t.Fatalf("pi's models.json has no cerebras entry — the composed provider table "+
				"did not reach pi's derive; entries: %v", keysOf(piModels.Providers))
		}
		if !piBuiltinApis[cerebras.API] {
			t.Errorf("pi's cerebras entry carries api = %q, which is not one of pi's "+
				"registered api ids — the dialect map must translate canonical "+
				"openai-chat-completions to pi's openai-completions", cerebras.API)
		}
		if !strings.Contains(cerebras.APIKey, "CEREBRAS_API_KEY") {
			t.Errorf("pi's cerebras entry carries apiKey = %q; the credential reference must "+
				"name CEREBRAS_API_KEY in pi's config-value syntax", cerebras.APIKey)
		}
	})

	t.Run("opencode cerebras baseURL and apiKey are under options", func(t *testing.T) {
		cerebras, ok := ocConfig.Provider["cerebras"]
		if !ok {
			t.Fatalf("opencode's config has no cerebras provider entry — the composed " +
				"provider table did not reach opencode's derive at all")
		}
		if cerebras.Options == nil || cerebras.Options.BaseURL != "https://api.cerebras.ai/v1" {
			t.Errorf("opencode's cerebras entry options = %+v; the URL opencode dials must "+
				"be the measured base URL, under options (D10)", cerebras.Options)
		}
		if cerebras.Options == nil || !strings.Contains(cerebras.Options.APIKey, "CEREBRAS_API_KEY") {
			t.Errorf("opencode's cerebras entry options.apiKey = %+v; the credential must be "+
				"under options too, naming CEREBRAS_API_KEY (D10)", cerebras.Options)
		}
	})

	// The claude half of the same delivery, in the same real jail — and the acceptance bar
	// for the env-derive flip (OQ-CS8): pi's and opencode's halves above read the composed
	// table out of a FILE their derives rendered, but claude's is ENV, composed by
	// packs/claude's own yolo.env producer host-side onto the container argv. Unit tests
	// pin both call sites (internal/cli/run providershapeenv_test.go, internal/cli
	// hostprovidershape_test.go); only a launch proves the pair survives config
	// resolution, staging, the credential lookup and argv assembly together.
	t.Run("claude env carries the selected provider's pair", func(t *testing.T) {
		// The invoking environment may already carry these keys (the very session running
		// this suite exports all five ANTHROPIC_* vars) — pin them to a sentinel so the
		// assertion reads what yolo COMPOSED, not what it inherited (the OQ-Z4 scrub
		// discipline; an inherited BASE_URL makes a composed one indistinguishable).
		t.Setenv("ANTHROPIC_BASE_URL", "inherited")
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited")
		// The persistent spelling, in the user config where selection keys live (OQ-CS5).
		// Last subtest on purpose: this packHome redirect wins for the rest of the test.
		packHome(t, `{"packs": ["claude", "zai"], "use_profiles": {"claude": "zai"}}`)
		r := runYolo(t, dir, `env | grep -E '^ANTHROPIC_(BASE_URL|AUTH_TOKEN)=' | sort`)
		if r.rc != 0 {
			t.Fatalf("profiled launch failed: rc %d\n%s", r.rc, r.combined())
		}
		want := "ANTHROPIC_AUTH_TOKEN=integration-probe-not-a-real-key\n" +
			"ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic\n"
		if got := r.stdout; got != want {
			t.Errorf("claude's provider env =\n%s\nwant exactly the composed pair:\n%s", got, want)
		}
	})

	// The fifth delivery, same acceptance bar: copilot's BYOK is env-var-only, so the
	// whole delivery is one yolo.env producer — and only a launch proves it survives
	// to the container argv (copilotbyok_test.go pins the same composition unit-tier).
	// The grep names the five composed vars exactly, the claude subtest's discipline:
	// the copilot pack itself contributes COPILOT_ALLOW_ALL=true statically, and a
	// bare ^COPILOT_ would fold it into the assertion.
	t.Run("copilot env carries the selected provider's BYOK block", func(t *testing.T) {
		packHome(t, `{"packs": ["copilot", "cerebras"], "use_profiles": {"copilot": "cerebras"}}`)
		r := runYolo(t, dir,
			`env | grep -E '^COPILOT_(MODEL|PROVIDER_API_KEY|PROVIDER_BASE_URL|PROVIDER_TYPE|PROVIDER_WIRE_API)=' | sort`)
		if r.rc != 0 {
			t.Fatalf("profiled copilot launch failed: rc %d\n%s", r.rc, r.combined())
		}
		want := "COPILOT_MODEL=qwen-3.8-27b\n" +
			"COPILOT_PROVIDER_API_KEY=integration-probe-not-a-real-key\n" +
			"COPILOT_PROVIDER_BASE_URL=https://api.cerebras.ai/v1\n" +
			"COPILOT_PROVIDER_TYPE=openai\n" +
			"COPILOT_PROVIDER_WIRE_API=completions\n"
		if got := r.stdout; got != want {
			t.Errorf("copilot's BYOK env =\n%s\nwant exactly the composed block:\n%s", got, want)
		}
	})
}

// keysOf is the diagnostic half of a missing-entry failure: naming what IS there turns
// "did not compose" from a guess into a reading.
func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
