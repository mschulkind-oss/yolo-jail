package run

// launchshellcredential_test.go pins what the credential pre-flight counts when a provider's key
// is set ONLY in the shell yolo was launched from (docs/reference/providers.md#the-credential-preflight).
// No jail backend hands that environment to the jail's processes, so the key reaches an agent
// there only when the agent's own env derive relays its value into the agent's environment, as
// claude's does into ANTHROPIC_AUTH_TOKEN. opencode and pi read the variable itself, which nothing
// delivers, so a launch that counted the shell for them started them with no key: the agent's
// first request failed, the failure this pre-flight exists to name before anything starts.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// shellKeys stubs the environment yolo was launched from with the named variables set.
func shellKeys(o *Options, names ...string) {
	o.Getenv = func(name string) string {
		for _, n := range names {
			if name == n {
				return "sk-shell-" + strings.ToLower(n)
			}
		}
		return ""
	}
}

func TestAKeyOnlyInTheLaunchShellCountsOnlyForAnAgentWhoseDeriveRelaysIt(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "claude"), officialPack(t, "opencode"),
		officialPack(t, "pi"), officialPack(t, "zai"), officialPack(t, "openrouter")}
	for _, tc := range []struct {
		name     string
		profiles map[string]string
		keys     []string
		refused  []string // agents the refusal names; nil for no refusal
		spared   []string // agents it must not name
	}{
		{"claude relays the shell's key into its own environment",
			map[string]string{"claude": "zai"}, []string{"ZAI_API_KEY"}, nil, nil},
		{"opencode reads the variable itself, which nothing delivers",
			map[string]string{"opencode": "openrouter"}, []string{"OPENROUTER_API_KEY"}, []string{"opencode"}, nil},
		// opencode serves zai's plan as its own zai-coding-plan, which reads ZHIPU_API_KEY, so the
		// launch relays zai's key to it under that name (docs/design/pi-codex-provider-shadowing.md
		// OQ-3; packload.BuiltInKeyVars): a relay, which a key in the launching shell satisfies.
		{"opencode's zai plan is relayed under the name its own provider reads",
			map[string]string{"opencode": "zai"}, []string{"ZAI_API_KEY"}, nil, nil},
		{"every entry of pi's set is asked",
			map[string]string{"pi": "zai,openrouter"}, []string{"ZAI_API_KEY", "OPENROUTER_API_KEY"},
			[]string{"pi"}, nil},
		{"claude's relay does not vouch for opencode on the same provider",
			map[string]string{"claude": "openrouter", "opencode": "openrouter"}, []string{"OPENROUTER_API_KEY"},
			[]string{"opencode"}, []string{"claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			writeUserPacks(t, home, `[]`)
			o := goldenOptions(t.TempDir(), home)
			o.UseProfiles = tc.profiles
			shellKeys(o, tc.keys...)
			lines, refuse := o.checkProviderCredentials(bareConfig(), packs,
				channelFor(t, o, bareConfig(), packs, emptyEnv()), nil)
			got := strings.Join(lines, "\n")
			if tc.refused == nil {
				if refuse || len(lines) != 0 {
					t.Fatalf("the relayed key must satisfy the pre-flight:\n%s", got)
				}
				return
			}
			if !refuse {
				t.Fatalf("a key only in the launching shell must refuse for %v:\n%s", tc.refused, got)
			}
			for _, key := range tc.keys {
				if !strings.Contains(got, key+" is set only in the environment yolo was launched from") {
					t.Errorf("the refusal must say %s is left in the launching shell:\n%s", key, got)
				}
			}
			for _, agent := range tc.refused {
				if !strings.Contains(got, "nothing relays it to "+agent) {
					t.Errorf("the refusal must name %s as the agent the key does not reach:\n%s", agent, got)
				}
			}
			for _, agent := range tc.spared {
				if strings.Contains(got, "relays it to "+agent) {
					t.Errorf("%s receives the key through its relay and must not be named:\n%s", agent, got)
				}
			}
			if !strings.Contains(got, "env_sources") {
				t.Errorf("the refusal must offer env_sources, which reaches the agent:\n%s", got)
			}
		})
	}

	// The control: the same opencode launch with the key in env_sources is silent, so the
	// refusal above is the shell, not opencode being unsatisfiable.
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	o := goldenOptions(t.TempDir(), home)
	o.UseProfiles = map[string]string{"opencode": "openrouter"}
	key := jsonx.NewOrderedMap()
	key.Set("OPENROUTER_API_KEY", "tok-9")
	if lines, refuse := o.checkProviderCredentials(bareConfig(), packs,
		channelFor(t, o, bareConfig(), packs, key), nil); refuse || len(lines) != 0 {
		t.Errorf("opencode's key in env_sources must satisfy the pre-flight:\n%s", strings.Join(lines, "\n"))
	}
}
