package integration

// launchshellcredential_test.go is the integration tier of the credential pre-flight's rule for
// a key left only in the shell yolo was launched from (docs/reference/providers.md#the-credential-preflight):
// no jail backend hands that shell to the jail's processes, so such a key reaches an agent only
// through its env derive's relay, and opencode, which reads the variable itself, would start with
// no key. A real launch refuses before any container starts, naming the variable, the agent and
// env_sources. The unit pins are internal/cli/run/launchshellcredential_test.go (the call site)
// and internal/packload's TestTheJailsCredentialPreflightAsksEachAgent (the rule).

import (
	"strings"
	"testing"
)

// openrouter, which opencode runs on its own client reading OPENROUTER_API_KEY itself. zai is not
// the case any more: opencode serves zai's plan as its own zai-coding-plan, whose key the launch
// relays as ZHIPU_API_KEY (docs/design/pi-codex-provider-shadowing.md OQ-3), so a key in the
// launching shell reaches it.
func TestAKeyLeftInTheLaunchShellRefusesOpencodesLaunch(t *testing.T) {
	requireJail(t)
	t.Setenv("OPENROUTER_API_KEY", "integration-probe-not-a-real-key")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["opencode", "openrouter"]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "opencode=openrouter", "--", "true"))
	if r.rc == 0 {
		t.Fatalf("opencode's key only in the launching shell must refuse the launch:\n%s", r.combined())
	}
	for _, want := range []string{
		"OPENROUTER_API_KEY is set only in the environment yolo was launched from",
		"nothing relays it to opencode",
		"env_sources",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, r.combined())
		}
	}
}
