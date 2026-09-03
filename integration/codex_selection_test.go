package integration

// codex_selection_test.go is the integration tier of OQ-CS1 for codex: the selection keys
// a profile writes land in the rendered ~/.codex/config.toml, and the cases that must
// write nothing write nothing THERE — after config resolution, pack staging, provider
// composition and the jail's boot render have all had a chance to drop or mutate them.
// The unit pin (internal/entrypoint/codexselection_test.go) drives the same derive through
// the boot loop; only a launch proves the table the launch actually composed reaches it.
//
// Launches are per-workspace on purpose: OQ-CS2 is a statement about a LAUNCH with no
// active profile, and a second launch reusing a workspace would read yolo's capture of
// in-jail edits rather than the rule (docs/reference/providers.md — Selection: write on activation, never on absence).

import (
	"regexp"
	"testing"
)

var (
	// codexModelProviderAssign is a top-level `model_provider = "..."` in config.toml.
	// Anchored at line start so it cannot match the model_providers table's own keys.
	codexModelProviderAssign = regexp.MustCompile(`(?m)^model_provider\s*=\s*"([^"]*)"`)

	// codexModelAssign is a top-level `model = "..."`. `model_provider` does not match:
	// after `model` it has `_`, not the whitespace this expression requires.
	codexModelAssign = regexp.MustCompile(`(?m)^model\s*=\s*"([^"]*)"`)

	// codexProviderRow is one `[model_providers.<id>]` table header.
	codexProviderRow = regexp.MustCompile(`(?m)^\[model_providers\.([^\]]+)\]`)
)

// codexProbeProject is the provider table every launch here carries: zai's pack-shipped
// facts (selected by the pack set) beside a user-declared provider codex CAN speak — the
// base_url shorthand with no wire_api, which is codex's own default (responses), the shape
// a local llama.cpp entry takes (local-model-endpoints.md §"Codex CLI"). The neighbour is
// not decoration: without one codex-speakable provider, "no selection keys" would be
// indistinguishable from "the provider table never reached the derive".
const codexProbeProject = `{"providers":{"llamacpp":{
  "base_url":"http://127.0.0.1:8080/v1",
  "models":{"default":"llama"}
}}}`

func TestCodexSelectionFollowsTheActiveProfile(t *testing.T) {
	requireJail(t)

	// zai ships api_key_env_name = ZAI_API_KEY and its provider is cataloged (it carries
	// endpoints), so the credential preflight refuses a launch that cannot deliver it —
	// selected or not (internal/packload requiredProviders). Set before any launch.
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")

	t.Run("a selected provider codex cannot speak is never selected", func(t *testing.T) {
		dir := writeProject(t, codexProbeProject)
		// The five shipped packs that compose a provider table at all, and the zai
		// profile activated at codex's CLI name — the flag spelling, scoped to this
		// launch exactly as a user would type it.
		packHome(t, `{"packs": ["claude", "zai", "codex", "pi", "opencode"]}`)
		// runCommand rather than runYolo/runYoloDirect: the flag goes BEFORE the `--`
		// that starts the container command, which neither wrapper's shape allows.
		r := runCommand(t, dir, append(jailRunArgs(), "-p", "codex=zai", "--", "true"))
		if r.rc != 0 {
			t.Fatalf("profiled five-pack launch failed: rc %d\n%s", r.rc, r.combined())
		}
		config := string(renderedSurface(t, dir, "codex", "config.toml"))

		// z.ai speaks chat completions only and codex speaks responses only — no wire_api
		// value bridges them (docs/reference/providers.md — the codex/z.ai note, a fact about the world).
		// The selection therefore writes NOTHING, not a model_provider naming a provider
		// whose catalog row the same gate dropped: codex refuses that config at startup,
		// which is a half-selection that kills the launch rather than a request.
		if m := codexModelProviderAssign.FindStringSubmatch(config); m != nil {
			t.Errorf("codex config.toml carries model_provider = %q for a provider codex "+
				"cannot reach (z.ai speaks chat completions, codex responses only) — the "+
				"selection must follow the catalog's gate and write nothing", m[1])
		}
		if m := codexModelAssign.FindStringSubmatch(config); m != nil {
			t.Errorf("codex config.toml carries model = %q beside a model_provider that must "+
				"not exist (OQ-CS2/CS3)", m[1])
		}
		for _, row := range codexProviderRow.FindAllStringSubmatch(config, -1) {
			if row[1] == "zai" {
				t.Errorf("codex config.toml carries a model_providers.zai row, which the " +
					"catalog half drops for an unspeakable protocol — the selection and the " +
					"catalog answer one gate")
			}
		}
		// The vacuity guard: the same render must show the codex-speakable neighbour
		// cataloged, proving the composed table reached this derive at all — and that the
		// catalog is not gated on the selection (OQ-CS1 option D).
		if !codexProviderRow.MatchString(config) {
			t.Fatalf("codex config.toml has no model_providers table at all — the composed "+
				"table never reached the derive, so the absence above proves nothing:\n%s", config)
		}
		if !regexp.MustCompile(`(?m)^\[model_providers\.llamacpp\]`).MatchString(config) {
			t.Errorf("codex config.toml has no model_providers.llamacpp row; a provider "+
				"nobody selected still belongs in the catalog (OQ-CS1 option D):\n%s", config)
		}
	})

	t.Run("a profile naming a provider codex can speak writes the pair", func(t *testing.T) {
		dir := writeProject(t, codexProbeProject)
		// Only the codex pack: the provider is the user's, so the launch needs the one
		// pack that owns the surface. The profile is DECLARED beside the provider
		// (OQ-CS6 — a name nothing declares refuses the launch, as the undeclared
		// spelling of this very fixture proved), and it names the provider it selects,
		// which is itself. The persistent spelling for the selection (use_profiles,
		// OQ-CS5 — the same merge the flag above feeds), so both spellings of a
		// selection are covered by one file.
		packHome(t, `{"packs": ["codex"], `+
			`"profiles": {"llamacpp": {"provider": "llamacpp"}}, `+
			`"use_profiles": {"codex": "llamacpp"}}`)
		r := runYolo(t, dir, "true")
		if r.rc != 0 {
			t.Fatalf("profiled codex launch failed: rc %d\n%s", r.rc, r.combined())
		}
		config := string(renderedSurface(t, dir, "codex", "config.toml"))

		if m := codexModelProviderAssign.FindStringSubmatch(config); m == nil {
			t.Errorf("codex config.toml carries no model_provider for an active profile "+
				"whose provider codex can reach (OQ-CS1: activating a profile works for "+
				"all):\n%s", config)
		} else if m[1] != "llamacpp" {
			t.Errorf("model_provider = %q, want the provider the profile delivers "+
				"(llamacpp — the provider the user's own declaration names, which is "+
				"itself)", m[1])
		}
		if m := codexModelAssign.FindStringSubmatch(config); m == nil {
			t.Errorf("codex config.toml carries no model; the provider declares a `default` "+
				"alias and the fallback is the derive's business (OQ-CS3):\n%s", config)
		} else if m[1] != "llama" {
			t.Errorf("model = %q, want the provider's default alias llama", m[1])
		}
		// The row the selection names must be there, or codex refuses the whole config.
		if !regexp.MustCompile(`(?m)^\[model_providers\.llamacpp\]`).MatchString(config) {
			t.Errorf("codex config.toml selects model_provider without a "+
				"model_providers.llamacpp row beneath it — codex refuses that config at "+
				"startup:\n%s", config)
		}
	})

	t.Run("a launch with no profile writes nothing selection-shaped", func(t *testing.T) {
		dir := writeProject(t, codexProbeProject)
		packHome(t, `{"packs": ["codex"]}`)
		r := runYolo(t, dir, "true")
		if r.rc != 0 {
			t.Fatalf("unprofiled codex launch failed: rc %d\n%s", r.rc, r.combined())
		}
		config := string(renderedSurface(t, dir, "codex", "config.toml"))

		// OQ-CS2: not a default, not a clear — the no-profile case is the agent's own.
		if m := codexModelProviderAssign.FindStringSubmatch(config); m != nil {
			t.Errorf("a launch with no active profile wrote model_provider = %q; yolo must "+
				"never touch the selection key in that case (it would revert a model the "+
				"user picked interactively)", m[1])
		}
		if m := codexModelAssign.FindStringSubmatch(config); m != nil {
			t.Errorf("a launch with no active profile wrote model = %q (OQ-CS2)", m[1])
		}
		// Vacuity guard again: the catalog half is NOT gated on the selection, so the
		// provider must still be a row codex can pick interactively.
		if !regexp.MustCompile(`(?m)^\[model_providers\.llamacpp\]`).MatchString(config) {
			t.Errorf("codex config.toml has no model_providers.llamacpp row — the catalog "+
				"disappeared with the selection, which is option B (rejected):\n%s", config)
		}
	})
}
