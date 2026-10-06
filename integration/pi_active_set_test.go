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
	// The keys ride env_sources, the channel the credential gate delivers into the agent's own
	// env file: the shell yolo is launched from reaches no jail's agent (bedrock-plumbing.md BR-D2),
	// and the pre-flight refuses a key left only there for an agent nothing relays it to. The
	// shell's own copies are blanked, so the launch cannot lean on them.
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["pi", "zai", "openrouter"], "env_sources": [`+
		`{"ZAI_API_KEY": "integration-probe-not-a-real-key", "OPENROUTER_API_KEY": "integration-probe-not-a-real-key"}]}`)
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
	// The scoped list is the union, the primary's first (§4.4). Both are pi's own providers, so
	// each contributes every model pi has for it, and none of yolo's list
	// (docs/design/pi-codex-provider-shadowing.md OQ-3).
	enabled, _ := settings.raw["enabledModels"].([]any)
	if len(enabled) == 0 || enabled[0] != "zai/*" {
		t.Fatalf("pi's enabledModels = %v, want zai's run, pi's own list, first", enabled)
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
	// Neither gets a models.json row: both are pi's own (OQ-3).
	models := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
	requireNotCataloged(t, models.raw, "providers", "zai", "pi models.json")
	requireNotCataloged(t, models.raw, "providers", "openrouter", "pi models.json")
}

// A BEDROCK ENTRY AFTER THE FIRST (AP-D12's "anywhere in pi's set"): `-p pi=zai,bedrock` with a
// region on the provider starts pi on zai, catalogs the Bedrock list under pi's own
// amazon-bedrock provider, and hands pi the region in its own environment, through the staged
// packs, the needs closure that joins bedrock's aws-auth, the channel and the boot render. No
// agent runs, and nothing reaches AWS.
func TestPiRunsOnABedrockEntryAfterItsFirst(t *testing.T) {
	requireJail(t)
	t.Setenv("ZAI_API_KEY", "") // delivered through env_sources, as above
	const region, opus = "eu-west-1", "global.anthropic.claude-opus-5-5"

	dir := writeProject(t, `{}`)
	// A list the user supplies: packs/bedrock ships none (docs/design/model-lists-and-pickers.md MM-D32).
	packHome(t, `{"packs": ["pi", "zai"], "providers": {"bedrock": {"region": "`+region+`", `+
		`"models": {"global.anthropic.claude-opus-5-5": {"id": "global.anthropic.claude-opus-5-5", "vendor": "anthropic"}, "us.openai.gpt-6.1-sol": {"id": "us.openai.gpt-6.1-sol", "vendor": "openai"}}}}, `+
		`"env_sources": [{"ZAI_API_KEY": "integration-probe-not-a-real-key"}]}`)
	// pi's own env file, sourced as pi's launcher sources it; absent when nothing is scoped to pi,
	// which then reads as no region rather than as a failed launch.
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "pi=zai,bedrock", "--", "bash", "-lc",
		`f=~/.config/yolo-agent-env/pi.sh; if [ -r "$f" ]; then . "$f"; fi; printf 'AWS_REGION=%s\n' "${AWS_REGION-}"`))
	if r.rc != 0 {
		t.Fatalf("the set launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "AWS_REGION="+region+"\n") {
		t.Errorf("pi's own environment must carry the Bedrock entry's region:\n%s", r.combined())
	}
	settings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
	if settings.provider != "zai" {
		t.Errorf("a fresh session starts on the primary: defaultProvider = %q", settings.provider)
	}
	enabled, _ := settings.raw["enabledModels"].([]any)
	found := false
	for _, e := range enabled {
		found = found || e == "amazon-bedrock/"+opus
	}
	if !found {
		t.Errorf("pi's enabledModels = %v, want the Bedrock entry's models under amazon-bedrock", enabled)
	}
	// zai is pi's own and gets no row (OQ-3); the Bedrock entry's list is pi's native row.
	models := readPioencodeSurface(t, dir, "pi", "agent", "models.json")
	requireNotCataloged(t, models.raw, "providers", "zai", "pi models.json")
	requireCataloged(t, models.raw, "providers", "amazon-bedrock", "pi models.json")
}

// THE PROFILE KEY'S LIST FORM (docs/design/active-provider-sets.md AP-D13, on the key PP-D10
// renamed): `"profile": ["zai", "openrouter"]` in the user config names no agent, so a real
// launch gives pi the whole list and claude its first entry, and says which agent ignores the
// rest. Through config resolution, the one fold, the boot render and the launch lines.
func TestTheProfileKeysListReachesPiWholeAndClaudeFirst(t *testing.T) {
	requireJail(t)
	t.Setenv("ZAI_API_KEY", "") // delivered through env_sources, as above
	t.Setenv("OPENROUTER_API_KEY", "")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "pi", "zai", "openrouter"], "profile": ["zai", "openrouter"], `+
		`"env_sources": [{"ZAI_API_KEY": "integration-probe-not-a-real-key", "OPENROUTER_API_KEY": "integration-probe-not-a-real-key"}]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "--", "true"))
	if r.rc != 0 {
		t.Fatalf("the key's list launch failed: rc %d\n%s", r.rc, r.combined())
	}
	for _, want := range []string{"Active set for pi: zai, openrouter",
		"Profile list zai, openrouter (the profile key's list, naming no agent)",
		"claude takes one profile", "ignores openrouter"} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the launch must say %q:\n%s", want, r.combined())
		}
	}
	settings := readPioencodeSurface(t, dir, "pi", "agent", "settings.json")
	if settings.provider != "zai" {
		t.Errorf("pi's start provider = %s, want the list's first entry, zai", settings.provider)
	}
	enabled, _ := settings.raw["enabledModels"].([]any)
	if len(enabled) == 0 || enabled[len(enabled)-1] != "openrouter/*" {
		t.Errorf("pi's enabledModels = %v, want the key's whole list, openrouter's run last", enabled)
	}
}
