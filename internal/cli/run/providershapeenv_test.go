package run

// providershapeenv_test.go pins the CALL SITE of the provider environment composition,
// not just the runner — the same discipline providersenv_test.go and
// agentprofileenv_test.go state, for the third thing a launch composes from a provider.
// internal/packload has its own unit tests for the env-derive runner; this proves the
// assembled podman argv actually carries the environment the agent's own derive
// composed, and that the credential half resolves through the channel the launch
// hydrates (in.userEnv, the same env_sources yolo-user-env.sh is written from) rather
// than through anything the launch would not have carried.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// zaiFactsTail is the zai provider's service facts: two protocol endpoints and the
// credential pointer. There is deliberately no delivery vocabulary here — WHICH variable
// each fact lands in is the agent pack's derive, not a provider fact (OQ-CS8).
const zaiFactsTail = `"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"},` +
	`"openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat-completions"}},` +
	`"api_key_env_name":"ZAI_API_KEY"`

// writePackManifest writes a one-file pack and loads it the way the launch path does, so
// every fixture here is a real staged-shape pack rather than a hand-built one.
func writePackManifest(t *testing.T, name, manifest string) *packload.Pack {
	t.Helper()
	return writePackManifestWithDerive(t, name, manifest, "")
}

// writePackManifestWithDerive is writePackManifest that also ships a derive.lua — the
// agent pack's own producer, without which a pack that installs a CLI composes no
// provider environment at all.
func writePackManifestWithDerive(t *testing.T, name, manifest, deriveLua string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if deriveLua != "" {
		if err := os.WriteFile(filepath.Join(root, "derive.lua"), []byte(deriveLua), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, problems := packload.LoadDir(root, name, false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

// shippedEnvDerive is the REAL packs/claude env producer, read from the embedded pack
// tree rather than restated here: these tests pin the delivery a launch actually makes,
// and a hand-written producer would pin whatever the fixture said instead of what ships.
func shippedEnvDerive(t *testing.T) string {
	t.Helper()
	return packload.DeriveScript(claudePackFixture(t)[0])
}

// zaiProfilePack ships the zai provider, a variant requiring it, the named CLI and the
// shipped producer — the one-pack shape. The variant is named "glm", NOT "zai": a
// same-named variant would let the profile-name fallback produce the right answer for
// the wrong reason, and the test could not tell resolution from luck.
func zaiProfilePack(t *testing.T, name, bin string) *packload.Pack {
	t.Helper()
	return writePackManifestWithDerive(t, name, `{"name":"`+name+`","contributes":[`+
		`{"kind":"program","bin":"`+bin+`","via":"npm","package":"example.com/`+bin+`"},`+
		`{"kind":"provider","name":"zai",`+zaiFactsTail+`},`+
		`{"kind":"profile","name":"glm","requires_provider":"zai"}]}`,
		shippedEnvDerive(t))
}

// zaiProviderOnlyPack ships the provider and the variant and installs NO cli — the
// ordinary provider pack, which is why the producer lookup cannot be keyed on the
// provider's shipper at all: the binding lives with the agent, the facts with the
// provider, and neither needs the other.
func zaiProviderOnlyPack(t *testing.T) *packload.Pack {
	t.Helper()
	return writePackManifest(t, "zai-facts", `{"name":"zai-facts","contributes":[`+
		`{"kind":"provider","name":"zai",`+zaiFactsTail+`},`+
		`{"kind":"profile","name":"glm","requires_provider":"zai"}]}`)
}

// assembleWithProviderEnv is assembleWithPacksAndConfig with the hydrated env_sources the
// run pipeline threads in, so the producer's credential has the channel it really
// resolves through.
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
		"use_profiles", profiles,
	)
}

// TestAssembleComposesProviderEnvForTheSelectedProfile: `-p glm` on a launch whose
// selected pack ships the zai provider composes claude's anthropic pair onto the argv —
// the endpoint of the protocol claude speaks and the key relayed from the hydrated
// variable, both named by the agent pack's own derive. Both halves are asserted so a
// half-composed delivery cannot pass.
func TestAssembleComposesProviderEnvForTheSelectedProfile(t *testing.T) {
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
// be declared by a pack that installs NO cli, with the bin (and the producer) owned by
// another pack — profile names are global (§3.3), and a provider pack installs no CLI at
// all. Keying the producer on the provider's shipper would have made this whole shape
// unreachable while every runner test stayed green. claudePackFixture is the EMBEDDED
// pack, so this also proves a materialized embedded pack's Root is a derive the runner
// can read — the same path the host notch takes.
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
