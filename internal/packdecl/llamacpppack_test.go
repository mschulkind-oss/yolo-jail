package packdecl

// llamacpppack_test.go pins the SHIPPED `llamacpp` manifest — local inference as a MODE
// (docs/research/local-model-endpoints.md, OQ-LM1: a provider and the profile over it,
// never a parallel `llm_endpoints` key).
//
// It reads packs/llamacpp/pack.json through the production decoder, the way
// agentupdateswiring_test.go reads the tree it is about, because every assertion below is
// a fact an AGENT acts on and none of them is spelled in Go:
//
//   - the anthropic endpoint is the whole of claude's delivery (claude's env derive reads
//     `endpoints.anthropic` or the shorthand, nothing else). Drop it and the flagship
//     agent silently gets no local model at all.
//   - the openai endpoint's `openai-chat-completions` is what makes codex emit NO entry —
//     the honest degradation, since codex speaks `responses` only. Re-spelling it
//     `openai-responses` would produce a config that boots green and fails at the first
//     turn against a stock llama-server.
//   - the ABSENT api_key_env_name is OQ-LM2's ruling made real: the credential pre-flight
//     follows catalog membership, so naming a variable here would REFUSE every launch that
//     selects this pack without one — for a server that skips key validation entirely.
//
// It is not a schema test. The schema has its own cells; this is about the values.

import (
	"os"
	"path/filepath"
	"testing"
)

func llamacppManifest(t *testing.T) *Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(packsRepoRoot(t), "packs", "llamacpp", "pack.json"))
	if err != nil {
		t.Fatal(err)
	}
	m, problems := Decode(data)
	if len(problems) > 0 {
		t.Fatalf("the shipped manifest does not decode clean: %v", problems)
	}
	return m
}

// The mode is a BUNDLE: one provider carrying both protocols, and one profile selecting
// it. Either half alone is a switch that can half-land, which is the failure OQ-LM1 cites.
func TestLlamacppShipsOneProviderAndTheProfileOverIt(t *testing.T) {
	m := llamacppManifest(t)
	providers := m.Providers()
	if len(providers) != 1 || providers[0].Name != "llamacpp" {
		t.Fatalf("providers = %+v, want exactly one named llamacpp", providers)
	}
	p := providers[0]

	for _, tc := range []struct{ protocol, baseURL, wireAPI string }{
		// The origin, NOT .../v1: Claude Code appends its own path segments, and
		// llama-server serves the Anthropic Messages API natively (llama.cpp PR #17570),
		// which is why this pack needs no wire-bridge `needs` entry.
		{"anthropic", "http://localhost:8080", ""},
		{"openai", "http://localhost:8080/v1", "openai-chat-completions"},
	} {
		ep, ok := p.Endpoints[tc.protocol]
		if !ok {
			t.Errorf("no %q endpoint — the agents reading it get no local model", tc.protocol)
			continue
		}
		if ep.BaseURL != tc.baseURL {
			t.Errorf("%s base_url = %q, want %q", tc.protocol, ep.BaseURL, tc.baseURL)
		}
		if ep.WireAPI != tc.wireAPI {
			t.Errorf("%s wire_api = %q, want %q", tc.protocol, ep.WireAPI, tc.wireAPI)
		}
	}
	if len(p.Endpoints) != 2 {
		t.Errorf("endpoints = %v, want exactly the two protocols an agent here speaks", p.Endpoints)
	}

	profiles := m.Profiles()
	if len(profiles) != 1 || profiles[0].Name != "llamacpp" || profiles[0].Provider != "llamacpp" {
		t.Fatalf("profiles = %+v, want one selection over the provider", profiles)
	}
}

// OQ-LM2, the half that is a REFUSAL rather than a value: a local endpoint names no
// credential variable, so the launch's credential pre-flight requires nothing of it. The
// hosted case is served by the user adding `api_key_env_name` in their own config.
func TestLlamacppNamesNoCredentialVariable(t *testing.T) {
	if got := llamacppManifest(t).Providers()[0].APIKeyEnvName; got != "" {
		t.Errorf("api_key_env_name = %q — a keyless llama-server would now refuse every "+
			"launch that could not deliver that variable", got)
	}
}

// The options are the profile surface, and a profile naming an option the provider does
// not declare is refused — so this list is exactly what a user may tune. `api_timeout_ms`
// is DECLARED WITH A NULL: settable, with no default, because the right ceiling for local
// inference is the user's hardware and guessing one here would be a fact nobody measured.
func TestLlamacppDeclaresTheOptionsAProfileMayTune(t *testing.T) {
	opts := llamacppManifest(t).Providers()[0].Options
	for name, want := range map[string]OptionDefault{
		"model":          {Defaulted: true, Value: "default"},
		"context_window": {Defaulted: true, Value: "32768"},
		"max_tokens":     {Defaulted: true, Value: "8192"},
		"api_timeout_ms": {}, // declared, no default
	} {
		got, ok := opts[name]
		if !ok {
			t.Errorf("option %q is not declared — a profile setting it would be refused", name)
			continue
		}
		if got != want {
			t.Errorf("option %q = %+v, want %+v", name, got, want)
		}
	}
	if len(opts) != 4 {
		t.Errorf("options = %v, want only the four a derive reads", opts)
	}
	// One alias, and `default` is the name every derive falls back to when a profile
	// states no `model`. The id is what `llama-server --alias` must report.
	if models := llamacppManifest(t).Providers()[0].Models; len(models) != 1 || models["default"] != "llama" {
		t.Errorf("models = %v, want one `default` alias naming the server's --alias", models)
	}
}

// The env half of the bundle (OQ-LM1: a mode is credential, env and model ids TOGETHER).
// CLAUDE_CODE_ATTRIBUTION_HEADER=0 is the prompt-cache fix for this pairing specifically:
// Claude Code prepends an attribution block to the system prompt, llama.cpp then fails
// prefix reuse and reprocesses the whole prompt every turn. Verified in the SHIPPED
// implementation rather than from the blog post that first reported it — claude 2.1.274
// reads `process.env.CLAUDE_CODE_ATTRIBUTION_HEADER` and returns the empty block when it
// is set falsey.
//
// GATED ON THE PROFILE, and the unconditional map must stay empty: a pack env fold is
// jail-global, so an ungated entry would strip the attribution block from every Claude
// Code in the jail whether or not anyone asked for a local model.
func TestLlamacppSetsTheAttributionHeaderOnlyUnderItsProfile(t *testing.T) {
	m := llamacppManifest(t)
	if got := m.EnvContributions(); len(got) != 0 {
		t.Errorf("ungated env = %v, want none — a pack env fold reaches every process in "+
			"the jail, selected profile or not", got)
	}
	gated := m.ProfiledEnvContributions()
	if len(gated) != 1 {
		t.Fatalf("profiled env contributions = %+v, want exactly one", gated)
	}
	if gated[0].Profile != "llamacpp" {
		t.Errorf("gate = %q, want the profile this pack ships", gated[0].Profile)
	}
	if got := gated[0].Vars["CLAUDE_CODE_ATTRIBUTION_HEADER"]; got != "0" {
		t.Errorf("CLAUDE_CODE_ATTRIBUTION_HEADER = %q, want \"0\" — without it llama.cpp "+
			"reprocesses the entire prompt on every turn", got)
	}
}

// packsRepoRoot walks up for the directory holding go.mod.
func packsRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}
