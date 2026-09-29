package config

// providerplatform_test.go pins a user's own `providers.<name>.platform` (OQ-BR2,
// docs/design/providers-and-profiles-redesign.md): a provider a user defines may say what
// service it is, so it gets the behavior the shipped one does. Through ValidateConfig, the
// validator every launch runs, and through the workspace-scope rule, since the value decides
// which agents receive a pack's credential pointer.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestAUserProviderMayDeclareItsPlatform(t *testing.T) {
	errs, _ := ValidateConfig(decode(t, `{"providers": {"bedrock-eu":
	  {"platform": "aws-bedrock", "region": "eu-west-1"}}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.Contains(e, "platform") {
			t.Errorf("a user provider's platform must be accepted, got %q", e)
		}
	}
}

func TestAUserProviderPlatformIsShapeChecked(t *testing.T) {
	for body, want := range map[string]string{
		`["aws-bedrock"]`: "config.providers.p.platform: expected a string",
		`""`:              "config.providers.p.platform: an empty platform",
		`"aws bedrock"`:   `config.providers.p.platform: "aws bedrock" carries whitespace`,
	} {
		errs, _ := ValidateConfig(decode(t, `{"providers": {"p": {"platform": `+body+`}}}`), t.TempDir(), nil)
		if got := strings.Join(errs, "\n"); !strings.Contains(got, want) {
			t.Errorf("platform %s must be refused with %q, got:\n%s", body, want, got)
		}
	}
}

// `platform` decides which agents receive a pack's credential pointer (aws-auth's, keyed on
// "aws-bedrock"; OQ-BR8), so a workspace value is refused like every other credential-routing
// field (providerscope_test.go), while the same value in the user's config is an ordinary fact.
func TestWorkspaceProviderPlatformIsRefused(t *testing.T) {
	errs := providerScopeErrors(t, `{"providers": {"zai": {"platform": "aws-bedrock"}}}`, `{}`)
	found := ""
	for _, e := range errs {
		if strings.HasPrefix(e, "config.providers.zai.platform:") {
			found = e
		}
	}
	if found == "" {
		t.Fatalf("a workspace platform must be refused — it relabels a provider so its agents "+
			"receive a credential pointer; got %v", errs)
	}
	for _, want := range []string{"user-scope only", paths.UserConfigPath()} {
		if !strings.Contains(found, want) {
			t.Errorf("error %q missing %q", found, want)
		}
	}
}
