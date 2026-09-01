package cli

// hostprovidershape_test.go pins the HOST half of the provider env-shape composition —
// the same call-site discipline host_test.go applies to the pack-env block. The jail's
// argv and `yolo host -- <agent>` must compose the same environment from the same resolved
// profile (host-agent-environment.md §2.2's parity claim), and a composer test alone would
// not notice the host side simply never calling it.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZaiLocalPack writes the conventional local pack shipping the zai provider's facts
// and a variant requiring it. It installs no CLI, which is the ordinary provider pack —
// the shape that proves the lookup is not keyed on the bin's owner alone.
func writeZaiLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"zai","contributes":[` +
		`{"kind":"provider","name":"zai",` +
		`"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}},` +
		`"api_key_env_name":"ZAI_API_KEY",` +
		`"env_shape":{"anthropic":{"ANTHROPIC_BASE_URL":"{endpoint}","ANTHROPIC_AUTH_TOKEN":"{key}"}}},` +
		`{"kind":"profile","name":"zai","requires_provider":"zai"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
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

// TestHostExecComposesTheProviderEnvShape: `yolo host -- claude` with the zai variant
// active delivers the anthropic pair to the host process — the endpoint from the
// provider's declared facts, the token relayed from the hydrated env_sources value. This
// is the half of the payload split a config surface can never carry (§5), and the reason
// a host-side `.bashrc` wrapper for claude can be deleted at all.
func TestHostExecComposesTheProviderEnvShape(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	scrubAnthropicEnv(t)
	t.Chdir(t.TempDir())
	writeZaiLocalPack(t, home)
	userCfg(t, home, `{
	  "pack_profiles": {"claude": "zai"},
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
