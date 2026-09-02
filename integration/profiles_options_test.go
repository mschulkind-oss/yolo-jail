package integration

// profiles_options_test.go is the container tier of the profiles/options step
// (provider-catalog-and-selection.md §5.2, OQ-CS4/5/6/7). Four things only a running
// container can say:
//
//  1. the launch is ACCEPTED with a user-declared profile over a shipped provider pack —
//     packs/zai declares `options: {model: default}`, so the census DOES run here, and
//     `model` is a declared option rather than a free string;
//  2. YOLO_PROFILES actually CROSSED. The variable is written by the launcher onto the
//     container argv and parsed by the entrypoint, and the two halves deploy on different
//     cadences — a unit tier can pin each side, only a launch can pin them together;
//  3. an undeclared profile NAME in use_profiles refuses the LAUNCH, naming it — the
//     OQ-CS6 reversal, whose unit-tier twin composes a channel directly and would stay
//     green if the check were wired nowhere at all;
//  4. the option an active profile states becomes the AGENT'S OWN selection key — pi's
//     defaultModel carries the id under the alias the profile's `model` names, read out
//     of the file pi reads. This is the end-to-end half of what
//     internal/entrypoint/pioencodeselection_test.go pins at the boot loop, and codex's
//     half of the same launch asserts the negative: a profile whose provider codex cannot
//     speak writes nothing selection-shaped there, even with the option resolved
//     (codex_selection_test.go owns the codex-only cases; provider-table-fidelity.md
//     §3.3 is the fact about the world that makes it so).

import (
	"regexp"
	"strings"
	"testing"
)

// A user `profiles` entry naming a shipped provider's pack launches, and the resolved
// table reaches the jail whole: the user's own entry with its option verbatim, beside the
// pack's own shipped profile. The table is the whole DECLARED set — not just the names
// this launch activates — because the activation decision belongs to the surface that
// reads it (YOLO_USE_PROFILES), not to the table.
func TestUserProfilesEntryLaunchesAndTheTableCrosses(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "zai"], `+
		`"profiles": {"zai-fast": {"provider": "zai", "model": "fast"}}}`)
	// zai ships api_key_env_name = ZAI_API_KEY, and the selected-pack credential pre-flight
	// refuses a launch whose environment cannot deliver it — the same provision
	// TestProvidersRenderInTheAgentsOwnVocabulary makes. That refusal is correct behaviour;
	// it is just not the thing this test is about.
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")

	r := runYolo(t, dir, `env | grep -E '^YOLO_PROFILES='`)
	if r.rc != 0 {
		t.Fatalf("a user-declared profile over the shipped zai pack must launch: rc %d\n%s",
			r.rc, r.combined())
	}
	for _, want := range []string{
		// The brief's own entry, resolved: `model` is one of the options zai's `options`
		// declares, the census passes, and the profile's own value stays on top of the
		// declared default ("default") rather than being re-spelled by it.
		`"zai-fast": {"provider": "zai", "model": "fast"}`,
		// The pack's own shipped profile, resolving to its provider and the declared
		// default of every option zai carries.
		`"zai": {"provider": "zai", "model": "default"}`,
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("YOLO_PROFILES should carry %s, got:\n%s", want, r.stdout)
		}
	}
}

// A use_profiles name nothing declares refuses the launch, and the refusal names the name
// and what IS declared — the OQ-CS6 reversal end to end. A typo'd profile used to select
// nothing and print a transparency line; now the launch stops, because a silently inert
// selector is indistinguishable from a working one.
func TestUndeclaredProfileNameRefusesTheLaunch(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	// claude alone keeps the declared set to one name, so the message's list is the fix a
	// reader types; no provider pack is selected, so no credential pre-flight can produce
	// a second, unrelated refusal.
	packHome(t, `{"packs": ["claude"], "use_profiles": {"claude": "zai-fst"}}`)

	r := runYolo(t, dir, "true")
	if r.rc == 0 {
		t.Fatalf("a use_profiles name nothing declares must refuse the launch, not select nothing:\n%s",
			r.combined())
	}
	for _, want := range []string{`zai-fst`, `declared: bedrock`} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal should name %q:\n%s", want, r.combined())
		}
	}
}

// The option half, end to end: a profile that states `model: "fast"` puts the id under
// zai's `fast` alias into pi's defaultModel — read out of the file pi itself reads, which
// is the only place the whole chain (user config → ResolveProfiles → YOLO_PROFILES → the
// entrypoint's ctx.profile → packs/pi/derive.lua) can be seen agreeing. The unit tiers pin
// each link; only a launch proves the links are joined.
//
// The same launch selects the same profile at codex's CLI name, where the answer is the
// negative one: z.ai speaks chat completions and codex speaks responses, so no catalog row
// exists for the selection to name and the derive writes nothing selection-shaped — an
// option resolving cleanly on one agent does not revive a provider another cannot reach.
func TestProfileOptionSelectsTheAliasInTheAgentsOwnFile(t *testing.T) {
	requireJail(t)

	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")

	// The codex-speakable neighbour is the vacuity guard (codex_selection_test.go): with
	// llamacpp cataloged in the same render, "codex wrote nothing" cannot be mistaken for
	// "the provider table never reached codex's derive".
	dir := writeProject(t, codexProbeProject)
	packHome(t, `{"packs": ["pi", "codex", "zai"], `+
		`"profiles": {"zai-fast": {"provider": "zai", "model": "fast"}}}`)

	// runCommand rather than runYolo: the flag goes BEFORE the `--` that starts the
	// container command, which runYolo's shape does not allow. Both CLIs in one flag, the
	// spelling a user types.
	r := runCommand(t, dir, append(jailRunArgs(),
		"--pack-profile", "pi=zai-fast,codex=zai-fast", "--", "true"))
	if r.rc != 0 {
		t.Fatalf("profiled three-pack launch failed: rc %d\n%s", r.rc, r.combined())
	}

	piSettings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
	if piSettings.provider != "zai" {
		t.Errorf("pi settings.json defaultProvider = %q, want the provider the profile "+
			"selects", piSettings.provider)
	}
	if piSettings.model != "glm-4.7-air" {
		t.Errorf("pi settings.json defaultModel = %q, want glm-4.7-air — the id under the "+
			"alias the profile's `model` option names (packs/zai declares models: "+
			"{default: glm-4.7, fast: glm-4.7-air}), not the declared default glm-4.7",
			piSettings.model)
	}
	// The vacuity guard reads the file the catalog lands in: pi's providers table is
	// yolo's computed models.json, not settings.json, which holds only the selection pair
	// (packs/pi declares the two surfaces separately).
	piModels := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
	requireCataloged(t, piModels.raw, "providers", "zai", "pi models.json")

	config := string(renderedSurface(t, dir, "codex", "config.toml"))
	if m := codexModelProviderAssign.FindStringSubmatch(config); m != nil {
		t.Errorf("codex config.toml carries model_provider = %q for a provider codex "+
			"cannot reach, though the same profile resolved cleanly for pi — the option "+
			"half does not lift the catalog's gate", m[1])
	}
	if m := codexModelAssign.FindStringSubmatch(config); m != nil {
		t.Errorf("codex config.toml carries model = %q beside a model_provider that must "+
			"not exist", m[1])
	}
	for _, row := range codexProviderRow.FindAllStringSubmatch(config, -1) {
		if row[1] == "zai" {
			t.Errorf("codex config.toml carries a model_providers.zai row, which the " +
				"catalog half drops for an unspeakable protocol")
		}
	}
	if !regexp.MustCompile(`(?m)^\[model_providers\.llamacpp\]`).MatchString(config) {
		t.Fatalf("codex config.toml has no model_providers.llamacpp row — the composed "+
			"table never reached codex's derive, so the absences above prove nothing:\n%s",
			config)
	}
}
