package run

// providersenv_test.go pins the CALL SITE of the provider composition, not just the
// composer: packload.ComposeProviders has its own tests, but the composed table reaches a
// jail only through the `-e YOLO_PROVIDERS=` pair assembled here, and before this file
// existed that was the one line whose deletion would have left the kind a schema no derive
// ever saw — with every composer test still green.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// providerEnvPack writes a local pack shipping one provider and loads it the way the
// launch path does, so the fixture is a real staged-shape pack rather than a hand-built one.
func providerEnvPack(t *testing.T, name string) *packload.Pack {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","contributes":[` +
		`{"kind":"provider","name":"zai",` +
		`"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"},` +
		`"openai":{"base_url":"https://api.z.ai/api/paas/v4","wire_api":"openai-chat"}},` +
		`"api_key_env_name":"ZAI_API_KEY",` +
		`"models":{"default":"glm-4.7","fast":"glm-4.7-air"}}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	p, problems := packload.LoadDir(root, name, false)
	if len(problems) != 0 {
		t.Fatalf("loading fixture pack: %v", problems)
	}
	return p
}

func assembleWithPacksAndConfig(t *testing.T, packs []*packload.Pack, cfg *jsonx.OrderedMap) []string {
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
	}
	return o.assembleRunCmd(in)
}

// TestAssembleEmitsComposedProvidersTable: a pack-shipped provider and a user override
// arrive in the jail's provider table TOGETHER — the pack's endpoints (which the user never
// restated) and the user's alias (which beats the pack's). Asserting both halves is what
// makes this the composition's call-site anchor rather than a re-test of the merge.
func TestAssembleEmitsComposedProvidersTable(t *testing.T) {
	models := jsonx.NewOrderedMap()
	models.Set("fast", "glm-5")
	providers := jsonx.NewOrderedMap()
	providers.Set("zai", func() *jsonx.OrderedMap {
		m := jsonx.NewOrderedMap()
		m.Set("models", models)
		return m
	}())
	argv := assembleWithPacksAndConfig(t,
		[]*packload.Pack{providerEnvPack(t, "zai-pack")},
		newConfig("providers", providers))

	got := envArgValues(argv, "YOLO_PROVIDERS")
	if len(got) != 1 {
		t.Fatalf("want exactly one YOLO_PROVIDERS env pair, got %q", got)
	}
	env := got[0]
	for _, want := range []string{
		"YOLO_PROVIDERS=", // present at all
		`"zai": {`,        // keyed by the provider name
		`"anthropic": {"base_url": "https://api.z.ai/api/anthropic"}`, // pack fact survives
		`"wire_api": "openai-chat"`,                                   // pack fact survives
		`"fast": "glm-5"`,                                             // user override wins
		`"default": "glm-4.7"`,                                        // pack alias the user did not mention survives
		`"api_key_env": "ZAI_API_KEY"`,                                // the credential POINTER, never a key
	} {
		if !strings.Contains(env, want) {
			t.Errorf("composed provider table missing %s\ngot: %s", want, env)
		}
	}
}

// A launch with no provider from either side still emits `{}` — the golden argv's
// byte-for-byte shape must not change because the kind exists. The pack here is one that
// ships no provider on purpose: packs/claude now always ships one (bedrock's shape), so
// a claude launch can never be the empty case again.
func TestAssembleEmitsEmptyProvidersTableWithoutProviders(t *testing.T) {
	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	argv := assembleWithPacksAndConfig(t, packsFixture(t, "pi"),
		newConfig("agents", []any{"pi"}, "security", sec))
	got := envArgValues(argv, "YOLO_PROVIDERS")
	if len(got) != 1 || got[0] != "YOLO_PROVIDERS={}" {
		t.Errorf("an unprofiled, provider-less launch must carry YOLO_PROVIDERS={}; got %q", got)
	}
}
