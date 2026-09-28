package entrypoint

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/openaiauthhost"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// The jail's launcher and `yolo host` read ONE declarative OpenAI prelaunch
// (docs/plans/notch-convergence.md item 15): the variables a pack's `env` declares for a
// launcher binary. The launcher spells them in shell; the host spells them through
// openaiauthhost.PrelaunchVar. This pins the two spellings to one prefix, one field set and one
// suffix rule, so a rename on either side fails here instead of leaving one notch reading a
// variable no pack sets.
func TestTheJailLauncherReadsTheHostsPrelaunchVariables(t *testing.T) {
	for _, field := range []string{"FLAG", "PATH", "LOGIN"} {
		want := `="` + openaiauthhost.PrelaunchPrefix + `${auth_suffix}_` + field + `"`
		if !strings.Contains(agentAuthPrelaunchShellFn, want) {
			t.Errorf("the launcher's prelaunch does not read %s%s: want %q in agentAuthPrelaunchShellFn",
				openaiauthhost.PrelaunchPrefix, field, want)
		}
	}
	// The suffix rule PrelaunchVar implements: uppercase, then every byte outside A-Z0-9_ to _.
	if !strings.Contains(agentAuthPrelaunchShellFn,
		`auth_suffix=$(printf '%s' "$BIN" | tr '[:lower:]' '[:upper:]' | tr -c 'A-Z0-9_' '_')`) {
		t.Error("the launcher's suffix rule moved; openaiauthhost.PrelaunchVar must move with it")
	}
}

// Every view flag a shipped pack declares is one the host serves, so no shipped prelaunch works
// in a jail and refuses at `yolo host`.
func TestEveryShippedPrelaunchViewIsOneTheHostServes(t *testing.T) {
	served := map[string]bool{openaiauthhost.CodexViewFlag: true, openaiauthhost.PiViewFlag: true}
	seen := 0
	for _, p := range packload.Embedded() {
		vars := map[string]string{}
		for k, v := range p.Decl.EnvContributions() {
			vars[k] = v
		}
		for _, gated := range p.Decl.ProfiledEnvContributions() {
			for k, v := range gated.Vars {
				vars[k] = v
			}
		}
		for k, v := range vars {
			if !strings.HasPrefix(k, openaiauthhost.PrelaunchPrefix) || !strings.HasSuffix(k, "_FLAG") {
				continue
			}
			seen++
			if !served[v] {
				t.Errorf("pack %s declares %s=%q, a view `yolo host` does not serve", p.Name, k, v)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no shipped pack declares a prelaunch view; the fixture stopped exercising this")
	}
}
