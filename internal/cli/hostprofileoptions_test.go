package cli

// hostprofileoptions_test.go pins the HOST notch's half of the profiles/options step: the
// OQ-CS6 refusal a bare `yolo host -- claude -p <name>` must produce, and the resolved
// option map the host's env derive composes from.
//
// The call-site rule this suite keeps restating decides the shape: hostprovidershape_test.go
// already found that a runner test cannot see the host side never calling it, and the same
// is true here — if composeHostVars stopped resolving profiles (or stopped refusing an
// undeclared name), the agent would launch with whatever the shell carried and nothing in
// packload would notice. So both tests go through composeHostEnv, the function
// `yolo host --` actually execs through.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOptionsLocalPack is writeZaiLocalPack with the one addition this step is about: the
// provider DECLARES an option, and the derive reads it out of ctx.profile rather than out
// of the providers table — so the var that arrives proves the RESOLUTION crossed, not just
// the selection.
func writeOptionsLocalPack(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail", "local")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"zai","contributes":[` +
		`{"kind":"program","bin":"claude","via":"npm","package":"@anthropic-ai/claude-code"},` +
		`{"kind":"provider","name":"zai","options":{"model":"default"},` +
		`"endpoints":{"anthropic":{"base_url":"https://api.z.ai/api/anthropic"}},` +
		`"api_key_env_name":"ZAI_API_KEY"},` +
		`{"kind":"profile","name":"fast","requires_provider":"zai"}]}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "derive.lua"), []byte(
		`yolo.env("claude", function(ctx)
  local n = 0
  for _ in pairs(ctx.profile) do n = n + 1 end
  return { PROFILE_MODEL = ctx.profile.model or "", PROFILE_KEYS = tostring(n) }
end)`), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A host launch that names a profile nothing declares refuses, with the same message the
// jail notch refuses with — one ruling, one wording, both notches. The declared set here
// is the local pack's `fast`; the user's own `profiles` entries count too, which is why
// this fixture needs no user entry to be a real list.
func TestHostRefusesAProfileNameNothingDeclares(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeOptionsLocalPack(t, home)
	userCfg(t, home, `{}`)

	_, _, err := composeHostEnv("claude", "nope", func(string) {})
	if err == nil {
		t.Fatal("a host launch naming an undeclared profile must refuse, not silently run unprofiled")
	}
	for _, want := range []string{`profile "nope" selected for claude`, `declared: fast`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %s, got: %v", want, err)
		}
	}
}

// A DECLARED profile reaches the host's derive resolved: the provider's default under the
// user's own value, so the env var carries the option the derive was written to read.
// Composing nothing here is the failure mode that looks like a working launch — the agent
// starts, its model setting never arrives.
func TestHostDeliversTheResolvedProfileOptionsToItsDerive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	writeOptionsLocalPack(t, home)
	userCfg(t, home, `{
	  "use_profiles": {"claude": "fast"},
	  "profiles": {"fast": {"provider": "zai", "model": "fast"}},
	  "env_sources": [{"ZAI_API_KEY": "tok-9"}]
	}`)

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
	if seen["PROFILE_MODEL"] != "fast" {
		t.Errorf("PROFILE_MODEL = %q, want the profile's own value fast — the resolved option "+
			"map must reach the host derive", seen["PROFILE_MODEL"])
	}
	if seen["PROFILE_KEYS"] != "1" {
		t.Errorf("PROFILE_KEYS = %q, want 1 (model only: the provider declares no other default)",
			seen["PROFILE_KEYS"])
	}
}
