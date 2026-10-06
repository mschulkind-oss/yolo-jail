package integration

// opencode_active_set_test.go is the integration tier of opencode holding an ACTIVE SET
// (docs/design/active-provider-sets.md §8 step 3, AP-D15; the active set, a term that doc coins,
// is the ordered list of profiles one agent runs on for one launch): `-p opencode=zai,openrouter`
// reaches opencode's own file through a real launch — config resolution, the -p grammar, pack
// staging, the credential gate and the jail's boot render. Until opencode's pack declared
// provider_sets, the same launch was refused. The unit pins are
// internal/entrypoint/opencodeactiveset_test.go (the render) and
// internal/cli/run/opencodeactiveset_test.go (the channel); only a launch proves the list crosses
// into the jail and the boot renders it. No agent runs: selecting a pack renders its files and
// installs nothing, and the jailed command is `true` or a shell.

import (
	"reflect"
	"strings"
	"testing"
)

func TestOpencodeRunsOnEveryProviderOfItsSet(t *testing.T) {
	requireJail(t)
	// Both providers' keys, since the credential pre-flight demands every entry's (AP-D3). They
	// are hydrated through env_sources, the channel the credential gate delivers into an agent's
	// own env file: the shell yolo is launched from reaches no jail's agent (BR-D2), and opencode's
	// rows name the variable (`{env:ZAI_API_KEY}`) rather than carry a value a derive relays. The
	// shell's own copies are blanked, so the bare-shell assertion reads what yolo delivered.
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["opencode", "zai", "openrouter"], "env_sources": [`+
		`{"ZAI_API_KEY": "integration-probe-not-a-real-key", "OPENROUTER_API_KEY": "integration-probe-not-a-real-key"}]}`)
	// opencode's own env file, sourced as opencode's launcher sources it, beside what a bare
	// shell holds: the set's keys are opencode's alone.
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "opencode=zai,openrouter", "--", "bash", "-lc",
		`printf 'shell zai=%s router=%s\n' "${ZAI_API_KEY:+set}" "${OPENROUTER_API_KEY:+set}"; `+
			`f=~/.config/yolo-agent-env/opencode.sh; if [ -r "$f" ]; then . "$f"; fi; `+
			`printf 'opencode zai=%s router=%s\n' "${ZAI_API_KEY:+set}" "${OPENROUTER_API_KEY:+set}"`))
	if r.rc != 0 {
		t.Fatalf("the set launch failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.combined(), "Active set for opencode: zai, openrouter") {
		t.Errorf("the launch must name opencode's set in order:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "opencode zai=set router=set\n") {
		t.Errorf("opencode's own environment must carry both entries' keys:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "shell zai= router=\n") {
		t.Errorf("a bare shell must carry neither of the set's keys:\n%s", r.combined())
	}

	cfg := readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
	// A fresh session starts on the PRIMARY's model (AP-D1).
	if cfg.slashJoin != "zai/glm-5.3" {
		t.Errorf("opencode.json model = %q, want the first entry's zai/glm-5.3", cfg.slashJoin)
	}
	// opencode's own provider filter names every entry, the primary first (§4.4).
	if got, _ := cfg.raw["enabled_providers"].([]any); !reflect.DeepEqual(got, []any{"zai", "openrouter"}) {
		t.Errorf("opencode.json enabled_providers = %v, want [zai openrouter]", got)
	}
	requireCataloged(t, cfg.raw, "provider", "zai", "opencode.json")
	requireCataloged(t, cfg.raw, "provider", "openrouter", "opencode.json")
}

// A BEDROCK ENTRY AFTER THE FIRST (AP-D12's "anywhere in the set"): `-p opencode=zai,bedrock` with
// a region on the provider starts opencode on zai, binds the Bedrock entry to opencode's own
// amazon-bedrock client with that region as options.region, and enables both, through the staged
// packs, the needs closure that joins bedrock's aws-auth, the channel and the boot render. No
// agent runs, and nothing reaches AWS.
func TestOpencodeRunsOnABedrockEntryAfterItsFirst(t *testing.T) {
	requireJail(t)
	t.Setenv("ZAI_API_KEY", "") // delivered through env_sources, as above
	const region, opus = "eu-west-1", "global.anthropic.claude-opus-5-5"

	dir := writeProject(t, `{}`)
	// A list the user supplies: packs/bedrock ships none (docs/design/model-lists-and-pickers.md MM-D32).
	packHome(t, `{"packs": ["opencode", "zai"], "providers": {"bedrock": {"region": "`+region+`", `+
		`"models": {"global.anthropic.claude-opus-5-5": {"id": "global.anthropic.claude-opus-5-5", "vendor": "anthropic"}, "us.openai.gpt-6.1-sol": {"id": "us.openai.gpt-6.1-sol", "vendor": "openai"}}}}, `+
		`"env_sources": [{"ZAI_API_KEY": "integration-probe-not-a-real-key"}]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "opencode=zai,bedrock", "--", "true"))
	if r.rc != 0 {
		t.Fatalf("the set launch failed: rc %d\n%s", r.rc, r.combined())
	}
	cfg := readPioencodeSurface(t, dir, "config", "opencode", "opencode.json")
	if !strings.HasPrefix(cfg.slashJoin, "zai/") {
		t.Errorf("a fresh session starts on the primary: model = %q", cfg.slashJoin)
	}
	if got, _ := cfg.raw["enabled_providers"].([]any); !reflect.DeepEqual(got, []any{"zai", "amazon-bedrock"}) {
		t.Errorf("opencode.json enabled_providers = %v, want [zai amazon-bedrock]", got)
	}
	requireCataloged(t, cfg.raw, "provider", "zai", "opencode.json")
	requireCataloged(t, cfg.raw, "provider", "amazon-bedrock", "opencode.json")
	rows, _ := cfg.raw["provider"].(map[string]any)
	native, _ := rows["amazon-bedrock"].(map[string]any)
	if opts, _ := native["options"].(map[string]any); opts["region"] != region {
		t.Errorf("the native row's options = %v, want region %s", native["options"], region)
	}
	if models, _ := native["models"].(map[string]any); models[opus] == nil {
		t.Errorf("the native row lacks the Bedrock list's %s: %v", opus, native["models"])
	}
}
