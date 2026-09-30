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

func TestAKeyLeftInTheLaunchShellRefusesOpencodesLaunch(t *testing.T) {
	requireJail(t)
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["opencode", "zai"]}`)
	r := runCommand(t, dir, append(jailRunArgs(), "-p", "opencode=zai", "--", "true"))
	if r.rc == 0 {
		t.Fatalf("opencode's key only in the launching shell must refuse the launch:\n%s", r.combined())
	}
	for _, want := range []string{
		"ZAI_API_KEY is set only in the environment yolo was launched from",
		"nothing relays it to opencode",
		"env_sources",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("the refusal must say %q:\n%s", want, r.combined())
		}
	}
}
