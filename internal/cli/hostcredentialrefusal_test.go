package cli

// hostcredentialrefusal_test.go pins docs/plans/notch-convergence.md item 14 (row C7) at the
// host notch: one credential refusal renderer (packload.ProviderCredentialRefusal) and one
// disclosure renderer (CredentialScope.DisclosureWith) serve both notches, so a refused
// `yolo host -- <cmd>` prints the jail's body behind "yolo host: ", byte for byte.

import (
	"strings"
	"testing"
)

// hostCredentialRefusalBody is run.credentialRefusalBody, the jail's refusal of the same
// declaration (a selected zai provider whose ZAI_API_KEY nothing delivers), with this notch's
// name in front. The two literals are the cross-notch contract: change one and the other
// test says the notches disagree.
const hostCredentialRefusalBody = `yolo host: Refusing to launch: a selected pack needs a provider this launch cannot deliver.
  • pack zai requires provider "zai", whose credential variable ZAI_API_KEY is not set in this launch's environment
  consulted for credentials: the environment yolo was launched from
  Put the variable in one of the consulted channels, or launch anyway with YOLO_ALLOW_MISSING_PROVIDERS=1.
`

// The host's refusal is the jail's body, through the real launch: it used to open on the
// first fact, with no sentence saying what was refused, and to name the inherited environment
// in words of its own.
func TestHostCredentialRefusalIsTheJailsBody(t *testing.T) {
	valueFlagHome(t, `{"packs":["zai"]}`)
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("YOLO_ALLOW_MISSING_PROVIDERS", "")
	rc, reached, errw := hostExecRun(t, "bash", "-p", "zai")
	if rc != 1 || reached {
		t.Fatalf("a selected provider with no key must refuse the host launch: rc=%d reached=%v\n%s", rc, reached, errw)
	}
	if !strings.Contains(errw, hostCredentialRefusalBody) {
		t.Errorf("the host refusal is not the jail's body:\n%s\nwant it to contain:\n%s", errw, hostCredentialRefusalBody)
	}

	// Held, it is the shared override notice over the same facts, and the launch proceeds.
	t.Setenv("YOLO_ALLOW_MISSING_PROVIDERS", "1")
	rc, reached, errw = hostExecRun(t, "bash", "-p", "zai")
	if rc != 0 || !reached {
		t.Fatalf("the hatch must let the launch proceed: rc=%d reached=%v\n%s", rc, reached, errw)
	}
	facts := strings.Join(strings.Split(hostCredentialRefusalBody, "\n")[1:3], "\n")
	if !strings.Contains(errw, "yolo host: Warning: YOLO_ALLOW_MISSING_PROVIDERS is set — CONTINUING") ||
		!strings.Contains(errw, facts) || strings.Contains(errw, "launch anyway with") {
		t.Errorf("the held notice is not the shared one:\n%s", errw)
	}
}
