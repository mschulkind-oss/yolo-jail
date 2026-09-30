package integration

// pi_active_set_test.go is the integration tier of docs/design/active-provider-sets.md (OQ-AP1,
// ruled 2026-09-29; the ACTIVE SET, a term that doc coins, is the ordered list of profiles one
// agent runs on for one launch): the maintainer's own spelling, `-p pi=zai,openrouter`, reaches
// pi's files through a real launch — config resolution, the -p grammar, pack staging, the
// credential gate and the jail's boot render. Until the build it started pi on zai alone and
// dropped openrouter in silence. The unit pins are internal/entrypoint/piactiveset_test.go (the
// render) and internal/cli/run/activeset_test.go (the channel); only a launch proves the list
// crosses into the jail and the boot renders it.

import (
	"strings"
	"testing"
)

func TestPiRunsOnEveryProviderOfItsSet(t *testing.T) {
	requireJail(t)
	// Both providers' keys, since the credential pre-flight demands every entry's (AP-D3).
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")
	t.Setenv("OPENROUTER_API_KEY", "integration-probe-not-a-real-key")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["pi", "zai", "openrouter"]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "pi=zai,openrouter", "--", "true"))
	if r.rc != 0 {
		t.Fatalf("the set launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Active set for pi: zai, openrouter") {
		t.Errorf("the launch must name pi's set in order:\n%s", r.combined())
	}

	settings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
	// A fresh session starts on the PRIMARY's default (AP-D1).
	if settings.provider != "zai" || settings.model != "glm-5.3" {
		t.Errorf("pi's start pair = %s/%s, want the first entry's zai/glm-5.3", settings.provider, settings.model)
	}
	// The scoped list is the union, the primary's default first; openrouter ships no model list,
	// so it contributes every model of the provider (§4.4).
	enabled, _ := settings.raw["enabledModels"].([]any)
	if len(enabled) == 0 || enabled[0] != "zai/glm-5.3" {
		t.Fatalf("pi's enabledModels = %v, want zai's default first", enabled)
	}
	if enabled[len(enabled)-1] != "openrouter/*" {
		t.Errorf("pi's enabledModels = %v, want openrouter's run after zai's", enabled)
	}
	// Child agents stay inside the set (§4.6).
	sub, _ := settings.raw["subagents"].(map[string]any)
	scope, _ := sub["modelScope"].(map[string]any)
	allow, _ := scope["allow"].([]any)
	var zai, router bool
	for _, a := range allow {
		s, _ := a.(string)
		zai = zai || strings.HasPrefix(s, "zai/")
		router = router || s == "openrouter/*"
	}
	if !zai || !router {
		t.Errorf("pi-subagents' scope %v must span both entries of the set", allow)
	}
	models := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
	requireCataloged(t, models.raw, "providers", "zai", "pi models.json")
	requireCataloged(t, models.raw, "providers", "openrouter", "pi models.json")
}
