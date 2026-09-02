package cli

// hostprovidershape_test.go pins the HOST half of the provider environment composition —
// the same call-site discipline host_test.go applies to the pack-env block. The jail's
// argv and `yolo host -- <agent>` compose the same environment from the same resolved
// profile through the ONE env-derive runner (host-agent-environment.md §2.2's parity
// claim), and a runner test alone would not notice the host side simply never calling
// it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZaiLocalPack writes the conventional local pack for a zai-profiled claude: the
// zai provider's facts, the variant that requires them, and — the delivery itself — the
// agent pack's derive.lua, carrying the yolo.env producer that states which variable
// takes which fact (OQ-CS8). The producer is the SHIPPED packs/claude script, read from
// the embedded tree, so this fixture composes exactly what a jail launch composes.
func writeZaiLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"zai","contributes":[` +
		`{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},` +
		`{"kind":"provider","name":"zai",` +
		`"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}},` +
		`"api_key_env_name":"ZAI_API_KEY"},` +
		`{"kind":"profile","name":"zai","provider":"zai"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"),
		[]byte(shippedClaudeDeriveLua(t)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scrubAnthropicEnv pins the two vars this composition delivers to a sentinel, so a test
// asserts what yolo COMPOSED rather than what the invoking shell happened to already
// carry. The OQ-Z4 measurement ran behind the same scrub, for the same reason: an
// inherited ANTHROPIC_BASE_URL makes a composed one indistinguishable from a kept one.
func scrubAnthropicEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ANTHROPIC_BASE_URL", "inherited")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited")
	t.Setenv("ZAI_API_KEY", "") // the key comes from env_sources, not the shell
}

// TestHostExecComposesTheProviderEnv: `yolo host -- claude` with the zai variant active
// delivers the anthropic pair to the host process — the endpoint from the provider's
// declared facts, the token relayed from the hydrated env_sources value, both named by
// the agent pack's own derive. This is the half of the payload split a config surface can
// never carry (§5), and the reason a host-side `.bashrc` wrapper for claude can be
// deleted at all.
func TestHostExecComposesTheProviderEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	scrubAnthropicEnv(t)
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{
	  "use_profiles": {"claude": "zai"},
	  "env_sources": [{"ZAI_API_KEY": "tok-9"}]
	}`)

	env, agent, err := composeHostEnv("claude", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if agent != "claude" {
		t.Errorf("agent = %q", agent)
	}
	seen := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			seen[kv[:i]] = kv[i+1:]
		}
	}
	if seen["ANTHROPIC_BASE_URL"] != "https://api.z.ai/api/anthropic" {
		t.Errorf("ANTHROPIC_BASE_URL = %q, want the provider's anthropic endpoint", seen["ANTHROPIC_BASE_URL"])
	}
	if seen["ANTHROPIC_AUTH_TOKEN"] != "tok-9" {
		t.Errorf("ANTHROPIC_AUTH_TOKEN = %q, want the value hydrated from env_sources", seen["ANTHROPIC_AUTH_TOKEN"])
	}
}

// TestHostExecComposesNoProviderEnvWithoutTheProfile: the same host launch with no active
// variant must compose neither var — both keep whatever the invoking shell handed them.
func TestHostExecComposesNoProviderEnvWithoutTheProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	scrubAnthropicEnv(t)
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{"env_sources": [{"ZAI_API_KEY": "tok-9"}]}`)

	env, _, err := composeHostEnv("claude", "", func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i > 0 {
			seen[kv[:i]] = kv[i+1:]
		}
	}
	for _, k := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"} {
		if got := seen[k]; got != "inherited" {
			t.Errorf("%s = %q, want the inherited sentinel untouched", k, got)
		}
	}
}
