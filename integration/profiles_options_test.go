package integration

// profiles_options_test.go is the container tier of the profiles/options step
// (provider-catalog-and-selection.md §5.2, OQ-CS4/5/6/7). Three things only a running
// container can say:
//
//  1. the launch is ACCEPTED with a user-declared profile over a shipped provider pack —
//     the census must not fire where the design says it may not, and packs/zai declares
//     no `options` block today, so `model` is a free string;
//  2. YOLO_PROFILES actually CROSSED. The variable is written by the launcher onto the
//     container argv and parsed by the entrypoint, and the two halves deploy on different
//     cadences — a unit tier can pin each side, only a launch can pin them together;
//  3. an undeclared profile NAME in use_profiles refuses the LAUNCH, naming it — the
//     OQ-CS6 reversal, whose unit-tier twin composes a channel directly and would stay
//     green if the check were wired nowhere at all.
//
// The derive consumption of ctx.profile (which option becomes which agent's model key) is
// deliberately absent: that is the later serial step in the build order, and this file
// asserts only what THIS step delivers.

import (
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
		// The brief's own entry, resolved verbatim: zai declares no options, so there is
		// no schema to compose a default from and no census to fail `model` against.
		`"zai-fast": {"provider": "zai", "model": "fast"}`,
		// The pack's own shipped profile, with nothing to resolve but its provider.
		`"zai": {"provider": "zai"}`,
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
