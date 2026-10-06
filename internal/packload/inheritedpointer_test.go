package packload

// inheritedpointer_test.go pins ServedDaemons.WithInherited (docs/design/sso-backed-bedrock.md
// SSO-D2): a daemon whose pointer a nested launch takes from the launching jail is served by
// inheritance, its pointer delivered with the launching jail's values in place of the declared
// ones (no listen address, no caller token of this launch's), and a pointer variable the
// inheritance does not carry is withheld and named, as an unserved one is.

import (
	"strings"
	"testing"
)

func TestAnInheritedPointerIsDeliveredWithTheLaunchingJailsValues(t *testing.T) {
	packs := embeddedNamed(t, "codex", "openai-auth", "aws-auth", "bedrock", "claude")
	profiles := map[string]string{"codex": "bedrock"}
	providers, resolved, _ := launchSelection(t, packs, nil, nil, profiles)
	const awsURI, awsToken = "AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_CONTAINER_AUTHORIZATION_TOKEN"
	compose := func(inherited map[string]string) *CredentialScope {
		t.Helper()
		// The payload holds no aws-auth daemon and this launch minted it no token: the
		// inheritance alone serves its pointer.
		served := ServedInJail([]string{"openai-auth-broker"}).WithListen(declaredListen).
			WithInherited(map[string]map[string]string{"aws-auth": inherited})
		s, err := ScopeCredentials(ScopeInput{Packs: packs, Profiles: profiles, NoDerives: true, Served: &served,
			Providers: providers, Resolved: resolved})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	value := func(s *CredentialScope, key string) (string, bool) {
		for _, e := range s.FoldFor("codex") {
			if e.Key == key {
				return e.Value, true
			}
		}
		return "", false
	}

	s := compose(map[string]string{awsURI: "http://127.0.0.1:52001/credentials", awsToken: "parent-token"})
	if v, _ := value(s, awsURI); v != "http://127.0.0.1:52001/credentials" {
		t.Errorf("%s = %q, want the launching jail's", awsURI, v)
	}
	if v, _ := value(s, awsToken); v != "parent-token" {
		t.Errorf("%s = %q, want the launching jail's token", awsToken, v)
	}
	if lines := UnservedLines(s, nil, nil); lines != nil {
		t.Errorf("an inherited pointer was reported unserved: %v", lines)
	}

	// Half a pointer is no pointer: the variable the inheritance lacks is withheld and named.
	s = compose(map[string]string{awsURI: "http://127.0.0.1:52001/credentials"})
	if _, ok := value(s, awsToken); ok {
		t.Errorf("%s was delivered with no inherited value", awsToken)
	}
	if lines := strings.Join(UnservedLines(s, nil, nil), "\n"); !strings.Contains(lines, awsToken) {
		t.Errorf("the withheld variable is not named:\n%s", lines)
	}
}

func TestPlusKeepsAnInheritedDaemon(t *testing.T) {
	a := ServedInJail([]string{"x"}).WithInherited(map[string]map[string]string{"aws-auth": {"V": "v"}})
	got := a.Plus(ServedByLaunch([]string{"y"}))
	if !got.Serves("aws-auth") || !got.Inherits("aws-auth") {
		t.Error("Plus dropped the inherited daemon")
	}
	if v, ok := got.inheritedValue("aws-auth", "V"); !ok || v != "v" {
		t.Errorf("inherited value = %q, %v", v, ok)
	}
	if ServedInJail(nil).WithInherited(nil).Inherits("aws-auth") {
		t.Error("an empty inheritance inherits")
	}
}
