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
// zai is the provider under test because it is the shipped pairing that WORKS for these two
// (it is the one codex cannot speak — codex_selection_test.go): pi translates z.ai's
// openai-chat-completions wire_api into its own openai-completions, opencode consumes no
// wire_api at all, and packs/zai declares `models: {default: glm-5.3[1m], ...}`, which is the
// model half of both selections.

import (
	"encoding/json"
	"testing"
)

// piAndOpencodePacks is the pack set every launch here carries: the two agent packs that
// own the surfaces under test, plus zai — the pack that DECLARES the `zai` variant and
// ships the provider fact, installing no CLI of its own. Selecting a pack renders its
// surfaces and installs no CLI, so no vendor install happens in this test
// (providers_test.go TestProvidersRenderInTheAgentsOwnVocabulary is the same trick).
const piAndOpencodePacks = `{"packs": ["pi", "opencode", "zai"]}`

// pioencodeSurface is one rendered agent file decoded as the JSON object the agent reads,
// with the keys a selection must and must not add. The `selection` key is the reserved
// namespace as the FILE would spell it: it must never appear, because the namespace is an
// implementation detail of the computed layer, never of the file
// (provider-catalog-and-selection.md §5.1).
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
	// ProviderCredentialGaps). Set before any launch.
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")

	t.Run("a profile at both CLIs writes both selections, and no profile clears nothing", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		packHome(t, piAndOpencodePacks)

		// runCommand rather than runYolo: the flag goes BEFORE the `--` that starts the
		// container command, which runYolo's shape does not allow. Both agents' profiles in
		// one flag, the spelling a user types.
		r := runCommand(t, dir, append(jailRunArgs(),
			"--pack-profile", "pi=zai,opencode=zai", "--", "true"))
		if r.rc != 0 {
			t.Fatalf("profiled three-pack launch failed: rc %d\n%s", r.rc, r.combined())
		}

		piSettings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
		if piSettings.provider != "zai" {
			t.Errorf("pi settings.json defaultProvider = %q, want the provider the variant "+
				"delivers — OQ-CS1: activating a profile works for all", piSettings.provider)
		}
		if piSettings.model != "glm-5.3[1m]" {
			t.Errorf("pi settings.json defaultModel = %q, want packs/zai's declared `default` "+
				"alias glm-5.3[1m] — the model id must match pi's provider list exactly "+
				"(OQ-CS3: the fallback is the derive's business)", piSettings.model)
		}
		// The catalog and the selection are DIFFERENT FILES for pi: yolo's computed
		// models.json holds the providers table, settings.json holds the pair pi reads
		// (packs/pi declares the two surfaces separately), so the guard reads the file the
		// catalog actually lands in. Asserting it against settings.json could only ever
		// fail — and as shipped, it failed every launch here, including the ones whose
		// selection had landed.
		piModels := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
		requireCataloged(t, piModels.raw, "providers", "zai", "pi models.json")

		ocConfig := readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
		if ocConfig.slashJoin != "zai/glm-5.3[1m]" {
			t.Errorf("opencode.json model = %q, want %q — \"<provider>/<model>\", split on "+
				"the first slash (v1.18.18 config.ts:74-76, model.ts:33-39)",
				ocConfig.slashJoin, "zai/glm-5.3[1m]")
		}
		// ~/.config is ONE shared overlay, so this file's host-side path runs through
		// "config" (providers_test.go); its catalog row is read from the same file.
		requireCataloged(t, ocConfig.raw, "provider", "zai", "opencode.json")

		// The second launch on the SAME workspace, with no profile: OQ-CS2 is a statement
		// about a launch, and the never-clear half of it is only observable on a home a
		// selecting launch already wrote. yolo can turn a selection on and cannot turn it
		// off — a second launch that reverted these keys would be re-asserting a computed
		// key, which is exactly what the reserved namespace exists to stop.
		r = runYolo(t, dir, "true")
		if r.rc != 0 {
			t.Fatalf("unprofiled relaunch failed: rc %d\n%s", r.rc, r.combined())
		}

		piSettings = readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
		if piSettings.provider != "zai" || piSettings.model != "glm-5.3[1m]" {
			t.Errorf("after an unprofiled relaunch pi's pair = %q/%q, want the selection the "+
				"first launch wrote left standing — deactivation clears nothing "+
				"(provider-catalog-and-selection.md §5.1 OQ-CS2)",
				piSettings.provider, piSettings.model)
		}
		ocConfig = readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
		if ocConfig.slashJoin != "zai/glm-5.3[1m]" {
			t.Errorf("after an unprofiled relaunch opencode's model = %q, want the selection "+
				"the first launch wrote left standing (OQ-CS2)", ocConfig.slashJoin)
		}
	})

	t.Run("a launch with no profile on a fresh workspace writes nothing selection-shaped", func(t *testing.T) {
		dir := writeProject(t, `{}`)
		packHome(t, piAndOpencodePacks)
		r := runYolo(t, dir, "true")
		if r.rc != 0 {
			t.Fatalf("unprofiled three-pack launch failed: rc %d\n%s", r.rc, r.combined())
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
		// so the provider must still be a row both agents can pick interactively — the
		// catalogue disappearing with the selection is option B, rejected. Read from
		// models.json, where pi's catalog lives (see the guard above).
		piModels := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
		requireCataloged(t, piModels.raw, "providers", "zai", "pi models.json")
		requireCataloged(t, ocConfig.raw, "provider", "zai", "opencode.json")
	})
}
