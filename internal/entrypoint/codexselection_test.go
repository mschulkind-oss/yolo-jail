package entrypoint

// codexselection_test.go pins the codex half of OQ-CS1: a profile active at codex's CLI
// name writes codex's OWN selection keys — `model_provider` and `model` in config.toml,
// verified from the codex CLI binary 2026-08-20 (docs/research/local-model-endpoints.md
// §"Codex CLI"; provider-catalog-and-selection.md §3 codex row) — and the two cases that
// must write NOTHING: no active profile (OQ-CS2) and a selected provider codex cannot
// reach (the catalog's own gate, so a selection can never name a row the catalog dropped).
//
// The derive runs through ConfigurePackSurfaces — the entry the BOOT loop uses — over the
// REAL embedded codex and zai packs, so the pin covers the whole chain and not a copy of
// it: surfaceSelectionFor's resolution (surfacederiveselection_test.go owns that pin; this
// builds on it, because a selection key no derive consumes is dead either way), the shipped
// derive.lua, and the stateful TOML render. A test that ran the derive directly would stay
// green if the boot stopped handing codex's derive its selection, which is exactly the
// "test pins the callee while the call site is unpinned" shape AGENTS.md warns about.
//
// Nothing here asserts a file codex did not read: the render is decoded back as TOML, the
// way codex reads it.

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// renderCodexConfig drives the boot render of the real codex pack over the given provider
// table and selection table, and returns the decoded config.toml.
//
// The zai pack rides along because it is the pack that DECLARES the `zai` variant — the
// shipped shape where the profile lives on a pack that installs no CLI, so the resolution
// is cross-pack by construction. Without it, `{"codex": "zai"}` would resolve to the bare
// name and this harness would be testing a different rule than the one that ships.
func renderCodexConfig(t *testing.T, providersJSON, profilesJSON string) map[string]any {
	t.Helper()
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatalf("embedded codex: %v", err)
	}
	zai, err := embeddedPack("zai")
	if err != nil {
		t.Fatalf("embedded zai: %v", err)
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{
		"YOLO_PROVIDERS":    providersJSON,
		"YOLO_USE_PROFILES": profilesJSON,
	}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "codex")

	ConfigurePackSurfaces(e, []*packload.Pack{codex, zai})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	return decodeCodexTOML(t, filepath.Join(e.Home, ".codex", "config.toml"))
}

// zaiProviderJSON is packs/zai's provider fact beside a speakable neighbour, spelled the
// way the composed table carries it. zai declares an anthropic endpoint for claude and an
// openai one whose wire_api declares the protocol z.ai actually speaks — the first is why
// zai is a claude provider, the second is why it is not a codex one. The neighbour is what
// makes the case that SELECTS zai say something about zai rather than about an empty
// table: with it, the same render proves the table arrived and that the neighbour is
// cataloged while the selected provider is not.
const zaiProviderJSON = `{"zai":{
  "api_key_env_name":"ZAI_API_KEY",
  "models":{"default":"glm-4.7","fast":"glm-4.7-air"},
  "endpoints":{
    "anthropic":{"base_url":"https://api.z.ai/api/anthropic"},
    "openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat-completions"}
  }
},"llamacpp":{"base_url":"http://127.0.0.1:8080/v1","models":{"default":"llama"}}}`

// reachableProviderJSON is a provider codex CAN speak: the base_url shorthand with no
// wire_api, which is codex's own default (responses) — the shape a user's local llama.cpp
// entry takes (local-model-endpoints.md §"Codex CLI" recipe).
const reachableProviderJSON = `{"llamacpp":{
  "base_url":"http://127.0.0.1:8080/v1",
  "models":{"default":"llama"}
}}`

func TestCodexDeriveWritesTheSelectionKeys(t *testing.T) {
	cases := []struct {
		name         string
		providers    string
		profiles     string
		wantProvider string // "" asserts the key is ABSENT
		wantModel    string // "" asserts the key is ABSENT
		// cataloged states whether model_providers carries an entry for wantProvider;
		// ignored when wantProvider is "". It is asserted BESIDE the selection keys, not
		// instead of them: a model_provider naming a row the catalog dropped is a config
		// codex refuses at startup, so the two halves must always agree.
		cataloged bool
		// guard names a provider that MUST reach the catalog even though it is not
		// selected — a case whose provider table holds nothing codex can speak would make
		// the absence of a selection key vacuous, so those cases carry a speakable
		// neighbour and name it here. "" for the cases that select the one provider
		// they carry.
		guard string
	}{
		{
			name:         "a codex-reachable provider is selected with its default alias",
			providers:    reachableProviderJSON,
			profiles:     `{"codex":"llamacpp"}`,
			wantProvider: "llamacpp",
			wantModel:    "llama",
			cataloged:    true,
		},
		{
			// OQ-CS2: the no-profile case is the agent's own. The catalog half is NOT
			// gated on the selection (OQ-CS1 option D) — the provider stays a row codex
			// can pick interactively — so this case asserts absence AND presence, which
			// is what keeps the absence from being vacuous.
			name:      "no active profile writes nothing selection-shaped",
			providers: reachableProviderJSON,
			profiles:  ``,
			guard:     "llamacpp",
		},
		{
			name: "an anthropic-only provider is never selected",
			providers: `{"claude_only":{"api_key_env_name":"ANTHROPIC_API_KEY","endpoints":` +
				`{"anthropic":{"base_url":"https://api.anthropic.com"}}},` +
				`"llamacpp":{"base_url":"http://127.0.0.1:8080/v1"}}`,
			profiles: `{"codex":"claude_only"}`,
			guard:    "llamacpp",
		},
		{
			// The shipped pairing that cannot work: z.ai speaks chat completions, codex
			// speaks responses, no wire_api value bridges them (provider-table-fidelity.md
			// §3.3 — a fact about the world, recorded). Selecting zai for codex therefore
			// writes nothing, rather than a model_provider whose provider row the catalog
			// drops — which codex refuses at startup. The speakable neighbour in the same
			// table is what makes this a statement about zai and not about an empty table.
			name:      "an openai endpoint codex cannot speak is never selected",
			providers: zaiProviderJSON,
			profiles:  `{"codex":"zai"}`,
			guard:     "llamacpp",
		},
		{
			// A variant whose requires_provider the composed table does not hold delivers
			// nothing to any agent (packload.requiredProviders says the same from the
			// preflight side), so it selects nothing either.
			name:      "a selected name the table does not hold selects nothing",
			providers: reachableProviderJSON,
			profiles:  `{"codex":"mystery"}`,
			guard:     "llamacpp",
		},
		{
			// The model fallback is the derive's business (OQ-CS3) and today it is the
			// provider's declared `default` alias. No declared default means no model key
			// — codex supplies its own, and /model rewrites it. model_provider alone is
			// therefore not a half-selection; the killing kind is a model_provider with no
			// provider row, which the shared gate prevents.
			name:         "a provider with no declared default writes model_provider alone",
			providers:    `{"bare":{"base_url":"http://127.0.0.1:8080/v1"}}`,
			profiles:     `{"codex":"bare"}`,
			wantProvider: "bare",
			cataloged:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderCodexConfig(t, tc.providers, tc.profiles)

			// An absent TOML key decodes as nil, which is what absence reads as here.
			if absentOr(got["model_provider"]) != tc.wantProvider {
				t.Errorf("model_provider = %v, want %q — a selection codex cannot honour "+
					"must be absent, never half-written (provider-catalog-and-selection.md "+
					"§5.1 OQ-CS2)", got["model_provider"], tc.wantProvider)
			}
			if absentOr(got["model"]) != tc.wantModel {
				t.Errorf("model = %v, want %q (provider-catalog-and-selection.md §9 OQ-CS3: "+
					"the fallback is the derive's business)", got["model"], tc.wantModel)
			}

			provs, haveCatalog := got["model_providers"].(map[string]any)
			if tc.wantProvider != "" {
				if _, present := provs[tc.wantProvider]; present != tc.cataloged {
					t.Errorf("model_providers.%s present = %v, want %v — the selection and the "+
						"catalog must answer one gate", tc.wantProvider, present, tc.cataloged)
				}
			}
			// A case whose provider table holds a speakable provider that is NOT selected
			// proves the table reached the derive at all — and, with it, that the catalog
			// is not gated on the selection (OQ-CS1 option D).
			if tc.guard != "" {
				if !haveCatalog {
					t.Fatalf("model_providers missing entirely — the composed table did not "+
						"reach the derive: %#v", got)
				}
				if _, present := provs[tc.guard]; !present {
					t.Errorf("model_providers has no %s entry, and a provider nobody selected "+
						"still belongs in the catalog (OQ-CS1 option D): %#v", tc.guard, provs)
				}
			}
		})
	}
}

// absentOr is the type assertion every absent-key check above shares: TOML absent reads
// as nil, a present scalar reads as string, and the comparison wants both as string.
// (receipts_test.go's str is the variant that FAILS on absence; these keys are the ones
// whose absence is the assertion.)
func absentOr(v any) string {
	s, _ := v.(string)
	return s
}
