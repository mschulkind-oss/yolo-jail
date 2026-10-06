package integration

// pi_opencode_selection_test.go is the integration tier of OQ-CS1 for pi and opencode: the
// selection keys a profile writes land in the files those agents read — pi's
// defaultProvider/defaultModel pair in ~/.pi/agent/settings.json (pi 0.84.4
// settings-manager.d.ts:71-72) and opencode's top-level `model = "<provider>/<model>"`
// (v1.18.18 config.ts:74-76) — and the cases that must write nothing write nothing THERE,
// after config resolution, pack staging, provider composition and the jail's boot render
// have all had a chance to drop or mutate them. The unit pin
// (internal/entrypoint/pioencodeselection_test.go) drives the same derives through the boot
// loop; only a launch proves the table the launch actually composed reaches them.
//
// zai is the provider under test because it is the shipped pairing both agents serve (it is the
// one codex cannot speak — codex_selection_test.go), and since
// docs/design/pi-codex-provider-shadowing.md OQ-3 (ruled 2026-10-05) both serve it through their
// OWN provider: yolo writes no model entry over a provider an agent has built in. pi's own zai
// already calls the coding plan; opencode's own `zai` is the metered API, so yolo's zai is its
// own zai-coding-plan, which reads ZHIPU_API_KEY. This is that ruling's launch tier: pi's
// models.json has no zai row, and opencode's config selects zai-coding-plan.

import (
	"encoding/json"
	"strings"
	"testing"
)

// piAndOpencodePacks is the pack set every launch here carries: the two agent packs that
// own the surfaces under test, plus zai — the pack that DECLARES the `zai` variant and
// ships the provider fact, installing no CLI of its own — and llamacpp, a provider neither
// agent has built in, whose row proves the composed table reached both derives. Selecting a
// pack renders its surfaces and installs no CLI, so no vendor install happens in this test
// (providers_test.go TestProvidersRenderInTheAgentsOwnVocabulary is the same trick). zai's key
// rides env_sources, the one channel that reaches pi and opencode in a jail.
const piAndOpencodePacks = `{"packs": ["pi", "opencode", "zai", "llamacpp"], "env_sources": [{"ZAI_API_KEY": "integration-probe-not-a-real-key"}]}`

// pioencodeSurface is one rendered agent file decoded as the JSON object the agent reads,
// with the keys a selection must and must not add. The `selection` key is the reserved
// namespace as the FILE would spell it: it must never appear, because the namespace is an
// implementation detail of the computed layer, never of the file
// (docs/reference/providers.md — Selection: write on activation, never on absence).
type pioencodeSurface struct {
	raw       map[string]any
	provider  string
	model     string
	slashJoin string
}

func readPioencodeSurface(t *testing.T, dir string, rel ...string) pioencodeSurface {
	t.Helper()
	var s pioencodeSurface
	if err := json.Unmarshal(renderedSurface(t, dir, rel...), &s.raw); err != nil {
		t.Fatalf("parsing the rendered surface %v: %v", rel, err)
	}
	str := func(k string) string {
		v, _ := s.raw[k].(string)
		return v
	}
	s.provider = str("defaultProvider")
	s.model = str("defaultModel")
	s.slashJoin = str("model")
	if _, present := s.raw["selection"]; present {
		t.Errorf("%v carries a literal `selection` table — the reserved namespace reached "+
			"the agent's file, which is an implementation detail of the layer, never of "+
			"the file: %v", rel, s.raw)
	}
	return s
}

// requireCataloged asserts the catalog row a selection names is present — the two halves
// answer one gate, so a selection whose provider the catalog dropped is the half-selection
// the shared gate exists to make unrepresentable. It is also the vacuity guard: without it,
// "no selection keys" would be indistinguishable from "the provider table never reached the
// derive".
func requireCataloged(t *testing.T, raw map[string]any, tableKey, id string, what string) {
	t.Helper()
	provs, ok := raw[tableKey].(map[string]any)
	if !ok {
		t.Fatalf("%s has no %s table at all — the composed provider table never reached the "+
			"derive, so the selection assertions say nothing: %v", what, tableKey, raw)
	}
	if _, present := provs[id]; !present {
		t.Errorf("%s has no %s.%s row, and the selection yolo wrote names it — the catalog "+
			"and the selection must answer one gate: %v", what, tableKey, id, provs)
	}
}

func TestPiAndOpencodeSelectionFollowTheActiveProfile(t *testing.T) {
	requireJail(t)

	// zai ships api_key_env_name = ZAI_API_KEY, and the selected-pack credential preflight
	// refuses a launch whose environment cannot deliver it (internal/packload
	// ProviderCredentialGaps), so piAndOpencodePacks delivers it.
	// The key rides env_sources, the channel the credential gate delivers into the agent's own
	// env file: the shell yolo is launched from reaches no jail's agent (bedrock-plumbing.md BR-D2),
	// and the pre-flight refuses a key left only there for an agent nothing relays it to. The
	// shell's own copy is blanked, so the launch cannot lean on it.
	t.Setenv("ZAI_API_KEY", "")

	t.Run("a profile at both CLIs writes both selections, and no profile clears what yolo wrote", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		packHome(t, piAndOpencodePacks)

		// runCommand rather than runYolo: the flag goes BEFORE the `--` that starts the
		// container command, which runYolo's shape does not allow. Both agents' profiles in
		// one flag, the spelling a user types. The command reads opencode's own env file as
		// its launcher sources it, for the key opencode's own zai-coding-plan reads.
		r := runCommand(t, dir, append(jailRunArgs(),
			"-p", "pi=zai,opencode=zai", "--", "bash", "-lc",
			`. ~/.config/yolo-agent-env/opencode.sh && printf 'ZHIPU=%s\n' "${ZHIPU_API_KEY-unset}"`))
		if r.rc != 0 {
			t.Fatalf("profiled launch failed: rc %d\n%s", r.rc, r.combined())
		}

		piSettings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
		if piSettings.provider != "zai" {
			t.Errorf("pi settings.json defaultProvider = %q, want pi's own zai, the provider "+
				"the variant delivers — OQ-CS1: activating a profile works for all", piSettings.provider)
		}
		if piSettings.model != "glm-5.3" {
			t.Errorf("pi settings.json defaultModel = %q, want the profile's model glm-5.3 as "+
				"pi's own id: zai is one of pi's own providers, so pi runs its own list and "+
				"the profile's model is the one id yolo still names (OQ-3)", piSettings.model)
		}
		if got, _ := piSettings.raw["enabledModels"].([]any); len(got) != 1 || got[0] != "zai/*" {
			t.Errorf("pi settings.json enabledModels = %v, want [zai/*]: pi's own zai list, "+
				"whole, and none of yolo's (glm-4.6 is yolo's and not pi's)", piSettings.raw["enabledModels"])
		}
		// The catalog and the selection are DIFFERENT FILES for pi: yolo's computed
		// models.json holds the providers table, settings.json holds the pair pi reads
		// (packs/pi declares the two surfaces separately), so the guard reads the file the
		// catalog actually lands in. zai is pi's own, so its catalog has NO zai row
		// (docs/design/pi-codex-provider-shadowing.md OQ-3); llamacpp's row is the proof the
		// composed table reached the derive.
		piModels := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
		requireNotCataloged(t, piModels.raw, "providers", "zai", "pi models.json")
		requireCataloged(t, piModels.raw, "providers", "llamacpp", "pi models.json")
		requireZaiSubagents(t, piSettings.raw)

		// opencode's own `zai` is z.ai's metered API, so yolo's zai (the coding plan) is its
		// own zai-coding-plan: that is the provider the selection and the menu name, with no
		// row for either, and zai's key reaches it as ZHIPU_API_KEY, the name it reads.
		ocConfig := readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
		if ocConfig.slashJoin != "zai-coding-plan/glm-5.3" {
			t.Errorf("opencode.json model = %q, want %q — \"<provider>/<model>\" on opencode's "+
				"own provider for the plan (v1.18.18 config.ts:74-76, model.ts:33-39)",
				ocConfig.slashJoin, "zai-coding-plan/glm-5.3")
		}
		if got, _ := ocConfig.raw["enabled_providers"].([]any); len(got) != 1 || got[0] != "zai-coding-plan" {
			t.Errorf("opencode.json enabled_providers = %v, want [zai-coding-plan]", ocConfig.raw["enabled_providers"])
		}
		// ~/.config is ONE shared overlay, so this file's host-side path runs through
		// "config" (providers_test.go); its catalog rows are read from the same file.
		requireNotCataloged(t, ocConfig.raw, "provider", "zai", "opencode.json")
		requireNotCataloged(t, ocConfig.raw, "provider", "zai-coding-plan", "opencode.json")
		requireCataloged(t, ocConfig.raw, "provider", "llamacpp", "opencode.json")
		if !strings.Contains(r.stdout, "ZHIPU=integration-probe-not-a-real-key\n") {
			t.Errorf("opencode's env file does not carry zai's key as ZHIPU_API_KEY, the name its "+
				"own zai-coding-plan reads:\n%s", r.combined())
		}

		// The second launch on the SAME workspace, with no profile. OQ-PSW2
		// (docs/reference/providers.md#oq-psw2, ruled 2026-09-25) narrowed OQ-CS2's
		// never-clear: a deselect clears the keys yolo wrote, so each agent falls back to
		// its native default or the host layer, and only an interactive user edit
		// survives. Nothing here edited the files, so both pairs are yolo's and both clear.
		// Observable only on a home a selecting launch already wrote.
		r = runYolo(t, dir, "true")
		if r.rc != 0 {
			t.Fatalf("unprofiled relaunch failed: rc %d\n%s", r.rc, r.combined())
		}

		piSettings = readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
		if sub, present := piSettings.raw["subagents"]; present {
			t.Errorf("after an unprofiled relaunch pi's subagents block = %v, want it gone "+
				"with the profile that wrote it", sub)
		}
		if piSettings.provider != "" || piSettings.model != "" {
			t.Errorf("after an unprofiled relaunch pi's pair = %q/%q, want both cleared — "+
				"yolo wrote them and nobody edited them, so a deselect clears them "+
				"(OQ-PSW2; docs/reference/providers.md, Selection)",
				piSettings.provider, piSettings.model)
		}
		ocConfig = readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
		if ocConfig.slashJoin != "" {
			t.Errorf("after an unprofiled relaunch opencode's model = %q, want it cleared "+
				"(OQ-PSW2)", ocConfig.slashJoin)
		}
	})

	t.Run("a launch with no profile on a fresh workspace writes nothing selection-shaped", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		packHome(t, piAndOpencodePacks)
		r := runYolo(t, dir, "true")
		if r.rc != 0 {
			t.Fatalf("unprofiled launch failed: rc %d\n%s", r.rc, r.combined())
		}

		// OQ-CS2: not a default, not a clear — the no-profile case is the agent's own, and
		// with `model` unset opencode falls back to its own persisted interactive choice
		// (~/.local/state/opencode/model.json), which is exactly what a default written
		// here would silently revert on the next launch.
		piSettings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
		if piSettings.provider != "" || piSettings.model != "" {
			t.Errorf("a launch with no active profile wrote pi's defaultProvider/defaultModel "+
				"= %q/%q; yolo must never touch the selection keys in that case",
				piSettings.provider, piSettings.model)
		}
		ocConfig := readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
		if ocConfig.slashJoin != "" {
			t.Errorf("a launch with no active profile wrote opencode's model = %q (OQ-CS2)",
				ocConfig.slashJoin)
		}

		// Vacuity guard: the catalog half is NOT gated on the selection (OQ-CS1 option D),
		// so a provider neither agent has built in is still a row both can pick
		// interactively — the catalogue disappearing with the selection is option B,
		// rejected — while zai, which both have built in, is a row in neither (OQ-3). Read
		// from models.json, where pi's catalog lives (see the guard above).
		piModels := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
		requireCataloged(t, piModels.raw, "providers", "llamacpp", "pi models.json")
		requireCataloged(t, ocConfig.raw, "provider", "llamacpp", "opencode.json")
		requireNotCataloged(t, piModels.raw, "providers", "zai", "pi models.json")
		requireNotCataloged(t, ocConfig.raw, "provider", "zai", "opencode.json")
	})
}

// requireNotCataloged asserts the agent's model file has no row under id: a provider the agent
// has built in gets none (docs/design/pi-codex-provider-shadowing.md OQ-3). A file with no table
// at all holds no row.
func requireNotCataloged(t *testing.T, raw map[string]any, tableKey, id string, what string) {
	t.Helper()
	provs, _ := raw[tableKey].(map[string]any)
	if row, present := provs[id]; present {
		t.Errorf("%s has a %s.%s row, a model entry over the agent's own provider: %v",
			what, tableKey, id, row)
	}
}

// requireZaiSubagents asserts pi-subagents' block for the shipped zai profile
// (docs/research/extension-model-defaults.md OQ-XM3): a child with no model of its own starts
// on the profile's default, and may name only zai's models: pi's own list, whole, since zai is
// one of pi's own providers (OQ-3).
func requireZaiSubagents(t *testing.T, settings map[string]any) {
	t.Helper()
	sub, _ := settings["subagents"].(map[string]any)
	if sub["defaultProvider"] != "zai" || sub["defaultModel"] != "zai/glm-5.3" {
		t.Errorf("pi subagents default = %v/%v, want zai and zai/glm-5.3 — a -p zai launch "+
			"starts a child agent on the profile's default (OQ-XM3)",
			sub["defaultProvider"], sub["defaultModel"])
	}
	scope, _ := sub["modelScope"].(map[string]any)
	allow, _ := scope["allow"].([]any)
	if scope["enforce"] != true || scope["strict"] != true || len(allow) != 1 || allow[0] != "zai/*" {
		t.Errorf("pi subagents.modelScope = %v, want enforced and strict over [zai/*] — only "+
			"zai's models, so a child can never cross providers", scope)
	}
}
