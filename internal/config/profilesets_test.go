package config

// profilesets_test.go pins the config half of OQ-AP1 to OQ-AP3
// (docs/design/active-provider-sets.md, ruled 2026-09-29) on the `profile` key (PP-D10): a value
// may be a list, the agent's ACTIVE SET in order (a term that doc coins); a list NAMED at an
// agent whose pack declares no provider_sets is refused; a list naming no agent (the top-level
// list, or one under "*") is not, since the launch narrows it for such an agent and says so; and
// a profile name may not contain the comma the -p grammar separates a list with. Driven through
// ValidateConfig and LoadProfiles, the callers `yolo check` and every launch use, so unwiring
// either check from validation fails here.

import (
	"strings"
	"testing"
)

func TestTheProfileKeyTakesAListForASetCapableAgent(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t,
		`{"profile": {"pi": ["zai", "openrouter"], "claude": "bedrock", "codex": ["codex"]}}`),
		t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("a list for pi (set-capable) and a list of one anywhere must validate clean: %v", errs)
	}
}

// A BARE list names no agent (OQ-AP3), so it is never refused for an agent that cannot hold it:
// the launch gives that agent the first entry and says so. The top-level list and "*" are that
// list, as `-p zai,openrouter` is.
func TestTheProfileKeysBareListIsNeverRefusedForASingleProviderAgent(t *testing.T) {
	for _, body := range []string{`["zai", "openrouter"]`, `{"*": ["zai", "openrouter"], "claude": "bedrock"}`} {
		useProfileKeysHome(t)
		if errs, _ := ValidateConfig(decode(t, `{"profile": `+body+`}`), t.TempDir(), nil); len(errs) != 0 {
			t.Errorf("profile %s must validate clean: %v", body, errs)
		}
	}
}

func TestTheProfileKeyRefusesWhatAListCannotMean(t *testing.T) {
	cases := []struct {
		name, body, says string
	}{
		{"a list at a single-provider agent (OQ-AP2)", `{"claude": ["zai", "openrouter"]}`,
			"config.profile.claude: profiles zai, openrouter are selected for claude, whose pack does not declare provider_sets"},
		{"an empty list", `{"pi": []}`, "an empty list selects nothing, and nothing has one spelling: null"},
		{"an empty bare list", `[]`, "an empty list selects nothing, and nothing has one spelling: null"},
		{"a name listed twice", `{"pi": ["zai", "zai"]}`, `profile "zai" is listed twice`},
		{"a name listed twice in a bare list", `{"*": ["zai", "zai"]}`, `profile "zai" is listed twice`},
		{"an entry that is not a name", `{"pi": ["zai", 3]}`, "entry 2 of the list is not a profile name"},
		{"neither a name nor a list", `{"pi": {"zai": true}}`, "expected a profile name, a list of them"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useProfileKeysHome(t)
			errs, _ := ValidateConfig(decode(t, `{"profile": `+tc.body+`}`), t.TempDir(), nil)
			if len(errs) != 1 || !strings.Contains(errs[0], tc.says) {
				t.Fatalf("errs = %v, want one saying %q", errs, tc.says)
			}
		})
	}
}

// The single-provider refusal names the spellings that work, so the fix is in the message.
func TestTheSingleProviderListRefusalNamesTheFix(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t, `{"profile": {"codex": ["codex", "zai"]}}`), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want one", errs)
	}
	for _, want := range []string{"`-p codex=codex`", `"profile": {"codex": "codex"}`, "drop zai"} {
		if !strings.Contains(errs[0], want) {
			t.Errorf("the refusal must name %q:\n%s", want, errs[0])
		}
	}
}

// oh-omp holds a set since packs/omp declares provider_sets (AP-D18), so a list named at it
// validates clean, through the declaration config validation reads off the shipped packs
// (SetCapableCLINames). Until then its refusal named the missing declaration rather than saying it
// runs one provider per session, which its format contradicts; dropping the declaration refuses it
// again.
func TestTheProfileKeyTakesAListForOmp(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t, `{"profile": {"oh-omp": ["zai", "openrouter"]}}`),
		t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("a list for oh-omp, which declares provider_sets, must validate clean: %v", errs)
	}
}

// opencode holds a set since packs/opencode declares provider_sets (§8 step 3, AP-D15), so a list
// named at it validates clean, through the declaration config validation reads off the shipped
// packs (SetCapableCLINames). Dropping the declaration refuses it again.
func TestTheProfileKeyTakesAListForOpencode(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t, `{"profile": {"opencode": ["zai", "openrouter"]}}`),
		t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("a list for opencode, which declares provider_sets, must validate clean: %v", errs)
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

// A list written in the retired key is respelled under the new one, list and all, so the user
// who wrote `use_profiles` for the first cut of this build is told the exact new spelling.
func TestTheRetiredKeysRefusalRespellsAList(t *testing.T) {
	useProfileKeysHome(t)
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{"use_profiles": {"pi": ["zai", "openrouter"]}}`), t.TempDir(), nil)
	if len(errs) != 1 || !strings.Contains(errs[0], `Your entries, respelled: "profile": {"pi": ["zai", "openrouter"]}`) {
		t.Fatalf("errs = %v, want the rename respelling pi's list", errs)
	}
}
