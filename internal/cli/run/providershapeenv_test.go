package run

// providershapeenv_test.go pins the CALL SITE of the provider env-shape composition, not
// just the composer — the same discipline providersenv_test.go and agentprofileenv_test.go
// state, for the third thing a launch composes from a provider. internal/agentenv has its
// own unit tests for the placeholders; this proves the assembled podman argv actually
// carries the composed environment, and that the {key} half resolves through the channel
// the launch hydrates (in.userEnv, the same env_sources yolo-user-env.sh is written from)
// rather than through anything the launch would not have carried.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const zaiShapeManifestTail = `"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"},` +
	`"openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat"}},` +
	`"api_key_env_name":"ZAI_API_KEY",` +
	`"env_shape":{"anthropic":{"ANTHROPIC_BASE_URL":"{endpoint}","ANTHROPIC_AUTH_TOKEN":"{key}"}}}`

// writePackManifest writes a one-file pack and loads it the way the launch path does, so
// every fixture here is a real staged-shape pack rather than a hand-built one.
func writePackManifest(t *testing.T, name, manifest string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name, false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

// zaiProfilePack ships the zai provider, a variant requiring it, and the named CLI — the
// one-pack shape. The variant is named "glm", NOT "zai": a same-named variant would let
// the profile-name fallback produce the right answer for the wrong reason, and the test
// could not tell resolution from luck.
func zaiProfilePack(t *testing.T, name, bin string) *packload.Pack {
	t.Helper()
	return writePackManifest(t, name, `{"name":"`+name+`","contributes":[`+
		`{"kind":"program","bin":"`+bin+`","via":"npm","package":"example.com/`+bin+`"},`+
		`{"kind":"provider","name":"zai",`+zaiShapeManifestTail+`,`+
		`{"kind":"profile","name":"glm","requires_provider":"zai"}]}`)
}

// zaiProviderOnlyPack ships the provider and the variant and installs NO cli — the
// ordinary provider pack, which is why the bin-owner rule cannot be the whole lookup.
func zaiProviderOnlyPack(t *testing.T) *packload.Pack {
	t.Helper()
	return writePackManifest(t, "zai-facts", `{"name":"zai-facts","contributes":[`+
		`{"kind":"provider","name":"zai",`+zaiShapeManifestTail+`,`+
		`{"kind":"profile","name":"glm","requires_provider":"zai"}]}`)
}

// assembleWithProviderEnv is assembleWithPacksAndConfig with the hydrated env_sources the
// run pipeline threads in, so the {key} placeholder has the channel it really resolves
// through.
func assembleWithProviderEnv(t *testing.T, packs []*packload.Pack, cfg *jsonx.OrderedMap, userEnv *jsonx.OrderedMap) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
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

// hydratedKey is the env_sources channel: one hydrated credential variable.
func hydratedKey() *jsonx.OrderedMap {
	m := jsonx.NewOrderedMap()
	m.Set("ZAI_API_KEY", "tok-9")
	return m
}

func profiledConfig() *jsonx.OrderedMap {
	profiles := jsonx.NewOrderedMap()
	profiles.Set("claude", "glm")
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	return newConfig(
		"agents", []any{"claude"},
		"security", sec,
		"pack_profiles", profiles,
	)
}

// TestAssembleComposesProviderEnvShapeForTheSelectedProfile: `-p zai` on a launch whose
// selected pack ships the zai provider composes claude's anthropic pair onto the argv —
// the endpoint of the protocol claude speaks, and the key relayed from the hydrated
// variable. Both halves are asserted so a half-composed shape cannot pass.
func TestAssembleComposesProviderEnvShapeForTheSelectedProfile(t *testing.T) {
	argv := assembleWithProviderEnv(t,
		[]*packload.Pack{zaiProfilePack(t, "zai-pack", "claude")},
		profiledConfig(), hydratedKey())

	got := envArgValues(argv, "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN")
	want := []string{
		"ANTHROPIC_AUTH_TOKEN=tok-9",
		"ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic",
	}
	if len(got) != len(want) {
		t.Fatalf("provider env args = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("provider env arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAssembleComposesNoProviderEnvWithoutAProfile: presence is not selection. The same
// selected pack with no active variant must carry neither var — an unprofiled launch that
// quietly pointed claude at z.ai would reroute a subscription user without a single line
// saying so.
func TestAssembleComposesNoProviderEnvWithoutAProfile(t *testing.T) {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := assembleWithProviderEnv(t,
		[]*packload.Pack{zaiProfilePack(t, "zai-pack", "claude")},
		newConfig("agents", []any{"claude"}, "security", sec), hydratedKey())
	if got := envArgValues(argv, "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"); len(got) != 0 {
		t.Errorf("unprofiled launch carried provider env: %q", got)
	}
}

// TestAssembleComposesProviderEnvDeclaredByAnotherPack: the variant and the provider may
// be declared by a pack that installs NO cli, with the bin owned by another pack — profile
// names are global (§3.3), and a provider pack installs no CLI at all. Keying the lookup
// on the bin's owner alone would have made this whole shape unreachable while every
// composer test stayed green.
func TestAssembleComposesProviderEnvDeclaredByAnotherPack(t *testing.T) {
	argv := assembleWithProviderEnv(t,
		append(claudePackFixture(t), zaiProviderOnlyPack(t)),
		profiledConfig(), hydratedKey())

	got := envArgValues(argv, "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN")
	want := []string{
		"ANTHROPIC_AUTH_TOKEN=tok-9",
		"ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic",
	}
	if len(got) != len(want) {
		t.Fatalf("provider env args = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("provider env arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}
