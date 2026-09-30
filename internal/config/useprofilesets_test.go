package config

// useprofilesets_test.go pins the config half of OQ-AP1 and OQ-AP2
// (docs/design/active-provider-sets.md, ruled 2026-09-29): a `use_profiles` value may be a list,
// the agent's ACTIVE SET in order (a term that doc coins), a list at an agent whose pack declares
// no provider_sets is refused, and a profile name may not contain the comma the -p grammar
// separates a list with. Driven through ValidateConfig and LoadProfiles, the callers `yolo check`
// and every launch use, so unwiring either check from validation fails here.

import (
	"strings"
	"testing"
)

func TestUseProfilesTakesAListForASetCapableAgent(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t,
		`{"use_profiles": {"pi": ["zai", "openrouter"], "claude": "bedrock", "codex": ["codex"]}}`),
		t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("a list for pi (set-capable) and a list of one anywhere must validate clean: %v", errs)
	}
}

// An empty string selected nothing before lists existed, and still does: only the list shapes
// are new, so `"pi": ""` validates clean rather than being refused as a missing name.
func TestAnEmptyUseProfilesValueStillSelectsNothing(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t, `{"use_profiles": {"pi": ""}}`), t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("an empty use_profiles value must validate clean, as it did before lists: %v", errs)
	}
}

func TestUseProfilesRefusesWhatAListCannotMean(t *testing.T) {
	cases := []struct {
		name, body, says string
	}{
		{"a list at a single-provider agent (OQ-AP2)", `{"claude": ["zai", "openrouter"]}`,
			"config.use_profiles.claude: profiles zai, openrouter are selected for claude, which runs one provider per session"},
		{"an empty list", `{"pi": []}`, "an empty list selects nothing, and nothing has one spelling: null"},
		{"a name listed twice", `{"pi": ["zai", "zai"]}`, `profile "zai" is listed twice`},
		{"an entry that is not a name", `{"pi": ["zai", 3]}`, "entry 2 of the list is not a profile name"},
		{"neither a name nor a list", `{"pi": {"zai": true}}`, "expected a profile name, or a list of them"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useProfileKeysHome(t)
			errs, _ := ValidateConfig(decode(t, `{"use_profiles": `+tc.body+`}`), t.TempDir(), nil)
			if len(errs) != 1 || !strings.Contains(errs[0], tc.says) {
				t.Fatalf("errs = %v, want one saying %q", errs, tc.says)
			}
		})
	}
}

// The single-provider refusal names the spellings that work, so the fix is in the message.
func TestTheSingleProviderListRefusalNamesTheFix(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t, `{"use_profiles": {"codex": ["codex", "zai"]}}`), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want one", errs)
	}
	for _, want := range []string{"`-p codex=codex`", `"use_profiles": {"codex": "codex"}`, "drop zai"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("the refusal must name %q:\n%s", want, errs[0])
		}
	}
}

// A comma leaves the profile-name alphabet (§4.1), in the user's `profiles` as in a pack's.
// Driven through checkProfiles, the `profiles` key's one reader.
func TestAProfileNameMayNotContainAComma(t *testing.T) {
	got, problems := checkProfiles(decode(t, `{"zai,fast": {"provider": "zai"}, "fast": {"provider": "zai"}}`))
	if len(problems) != 1 || !strings.Contains(problems[0], `a profile name must not contain ","`) {
		t.Errorf("problems = %v, want the comma refusal for \"zai,fast\" alone", problems)
	}
	if _, kept := got["zai,fast"]; kept {
		t.Error("a refused name must not be declared")
	}
	if _, kept := got["fast"]; !kept {
		t.Error("the valid neighbour must still be declared")
	}
}
