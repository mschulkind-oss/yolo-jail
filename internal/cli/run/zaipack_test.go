package run

// zaipack_test.go pins the FIRST REAL CONSUMER of the provider/profile pair — the shipped
// packs/zai (zai-plumbing.md §4 route B, §7's acceptance story) — against the pack the
// binary actually embeds rather than a fixture shaped like it. providershapeenv_test.go
// proves the composition MECHANISM with a hand-written manifest; what only this file can
// prove is that the manifest zai ships resolves to the facts the design measured: claude
// reaching the anthropic endpoint as ANTHROPIC_BASE_URL, the key relayed from the channel
// the launch hydrates, and the openai endpoint reaching the catalog the three derives read.
//
// The pre-flight half is here too, because the pack's whole contract is "select it, drop in
// a key": a launch that selects the pack without one refuses (OQ-13). That makes the shipped
// set no longer credential-silent, which TestShippedPacksRequireNoCredential records as the
// one deliberate exception.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// officialPack materializes the embedded official packs and returns one by name, loaded the
// way a launch loads it. A missing name is fatal rather than a skip: this file's whole point
// is the pack that ships, and a rename that drops it must be answered here.
func officialPack(t *testing.T, name string) *packload.Pack {
	t.Helper()
	loaded, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) != 0 {
		t.Fatalf("materializing official packs: %v", problems)
	}
	for _, p := range loaded {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no official pack named %q — packs/%s must exist and ship in the embed list", name, name)
	return nil
}

// zaiSelected is the acceptance story's selected set: the provider pack beside an agent
// pack. zai installs no CLI, which is the point — the name reaches the agent through the
// global -p, not through a bin the pack owns.
func zaiSelected(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "zai")}
}

// zaiLaunch is assembleWithProviderEnv with the caller able to set the flag the story
// launches with; every other seam is the same deterministic podman/linux fixture.
func zaiLaunch(t *testing.T, packs []*packload.Pack, cfg *jsonx.OrderedMap,
	userEnv *jsonx.OrderedMap, tune func(*Options)) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	if tune != nil {
		tune(o)
	}
	in := &assembleInput{
		cfg:          cfg,
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		packs:        packs,
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
		userEnv:      userEnv,
	}
	return o.assembleRunCmd(in)
}

// bareConfig is a launch config with nothing in it but the shape assembly expects.
func bareConfig() *jsonx.OrderedMap {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return newConfig("agents", []any{"claude"}, "security", sec)
}

// TestZaiPackFiresClaudeAtGLM: `-p zai` on a launch that selected the pack composes
// claude's anthropic pair onto the argv — the endpoint of the protocol claude speaks, from
// the manifest the pack ships, and the key relayed from the hydrated env_sources.
func TestZaiPackFiresClaudeAtGLM(t *testing.T) {
	argv := zaiLaunch(t, zaiSelected(t), bareConfig(), hydratedKey(),
		func(o *Options) { o.ProfileName = "zai" })

	got := envArgValues(argv, "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN")
	want := []string{
		"ANTHROPIC_AUTH_TOKEN=tok-9",
		"ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic",
	}
	if len(got) != len(want) {
		t.Fatalf("zai env args = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("zai env arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestZaiPackComposesNoEnvWithoutTheProfile: presence is not selection (zai-plumbing.md §7
// — "Without -p zai the catalogs still contain zai"). The pack selected and the key hydrated
// still puts neither var on the argv, because an unprofiled launch that quietly pointed
// claude at z.ai would reroute a subscription user without a line saying so.
func TestZaiPackComposesNoEnvWithoutTheProfile(t *testing.T) {
	argv := zaiLaunch(t, zaiSelected(t), bareConfig(), hydratedKey(), nil)
	if got := envArgValues(argv, "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"); len(got) != 0 {
		t.Errorf("unprofiled launch carried zai env: %q", got)
	}
}

// TestZaiPackShipsTheCatalogTheDerivesRead: the openai half crosses as YOLO_PROVIDERS — the
// table pi, codex and opencode consume — with the measured wire protocol (zai OQ-Z1: chat
// completions only) and the key NAME rather than a key. This is the whole of what the
// openai-speaking agents get from the pack; their selection is the derives' business, and
// the env shape deliberately declares no openai delivery for them to compose.
func TestZaiPackShipsTheCatalogTheDerivesRead(t *testing.T) {
	argv := zaiLaunch(t, zaiSelected(t), bareConfig(), hydratedKey(), nil)

	vals := envArgValues(argv, "YOLO_PROVIDERS")
	if len(vals) != 1 {
		t.Fatalf("argv carries %d YOLO_PROVIDERS args, want 1", len(vals))
	}
	v, err := jsonx.Decode([]byte(strings.TrimPrefix(vals[0], "YOLO_PROVIDERS=")))
	if err != nil {
		t.Fatalf("YOLO_PROVIDERS is not JSON: %v", err)
	}
	providers, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("YOLO_PROVIDERS is not an object: %T", v)
	}
	zai, ok := mapGet(providers, "zai").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("YOLO_PROVIDERS has no zai entry: %v", providers.Keys())
	}
	if got := mapStr(zai, "api_key_env_name"); got != "ZAI_API_KEY" {
		t.Errorf("zai api_key_env_name = %q, want the NAME the user hydrates", got)
	}
	endpoints, ok := mapGet(zai, "endpoints").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("zai ships no endpoints table: %v", zai.Keys())
	}
	anthropic, _ := mapGet(endpoints, "anthropic").(*jsonx.OrderedMap)
	openai, _ := mapGet(endpoints, "openai").(*jsonx.OrderedMap)
	if mapStr(anthropic, "base_url") != "https://api.z.ai/api/anthropic" {
		t.Errorf("anthropic endpoint = %v, want the measured base URL", anthropic)
	}
	if mapStr(openai, "base_url") != "https://api.z.ai/api/paas/v4" ||
		mapStr(openai, "wire_api") != "openai-chat-completions" {
		t.Errorf("openai endpoint = %v, want the canonical name for the chat-completions route "+
			"the probe measured (OQ-Z1: /v4/responses 404s there) — yolo's vocabulary, which the "+
			"derives translate, never codex's or pi's spelling", openai)
	}
}

// TestZaiPackRefusesALaunchWithNoKey and is quiet once it arrives: the pack's own README
// contract, and the reason a selected pack may ship a credential pointer at all. The check
// is keyed on the SELECTED pack, so the pack selected and the variable never hydrated is the
// refusal, not a warning.
func TestZaiPackRefusesALaunchWithNoKey(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "zai")}

	o := retireOptions(t, discardBuf())
	lines, refuse := o.checkProviderCredentials(bareConfig(), packs, channelFor(t, o, bareConfig(), packs, emptyEnv()), nil)
	if !refuse {
		t.Fatal("a selected zai pack with no key hydrated must refuse the launch")
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"ZAI_API_KEY", `provider "zai"`, "pack zai"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal must name %q:\n%s", want, got)
		}
	}

	o = retireOptions(t, discardBuf())
	if lines, refuse := o.checkProviderCredentials(bareConfig(), packs, channelFor(t, o, bareConfig(), packs, hydratedKey()), nil); len(lines) != 0 || refuse {
		t.Errorf("the key the README says to drop in must satisfy the check:\n%s",
			strings.Join(lines, "\n"))
	}
}
