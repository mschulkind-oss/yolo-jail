package cli

// hostcredentialgrant_test.go pins THE HOST'S GRANT for an ad-hoc command
// (docs/design/credential-sources-separation.md §5, ES-D1 to ES-D5): `yolo host -p <profile> --
// <cmd>` makes any command, not only an agent a pack installs, a recipient of that profile's
// claimed env_sources values. Every cell runs `yolo host` through hostMain to the exec, as the
// TestHostGate* cells in hostcredentialgate_test.go do, with `bash` as the command: no selected
// pack installs it, so no pack code runs for it and the grant is the only thing that can hand it
// a claimed value.

import (
	"strings"
	"testing"
)

// esGrantConfig is the design's measured setup (§3.1): packs claude, pi and zai, and
// env_sources carrying zai's key beside an unclaimed PORT.
const esGrantConfig = `{"packs": ["claude", "pi", "zai"], "env_sources": [` +
	`{"ZAI_API_KEY": "tok-es", "PORT": "8080"}]}`

// ES-D1: `yolo host -p zai -- bash` hands bash zai's claimed key, keeps the unclaimed value, and
// says the key went to bash alone. It fails if ScopeCredentials' agent loop starts asking
// whether a pack installs the name, or if effectiveHostProfiles stops keying the launched
// basename: both are what make a non-agent a recipient.
func TestHostGrantTypedProfileHandsAnAdHocCommandItsClaimedValues(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig, nil, []string{"-p", "zai"}, "bash")
	if env["ZAI_API_KEY"] != "tok-es" {
		t.Errorf("yolo host -p zai -- bash: ZAI_API_KEY = %q, want the env_sources value — "+
			"a typed -p is the host's grant for any command\n%s", env["ZAI_API_KEY"], errs)
	}
	if env["PORT"] != "8080" {
		t.Errorf("an unclaimed env_sources value reaches every process, bash on zai included: "+
			"PORT = %q\n%s", env["PORT"], errs)
	}
	if !strings.Contains(errs, "ZAI_API_KEY (provider zai): bash only") {
		t.Errorf("the grant is disclosed, naming its one recipient:\n%s", errs)
	}
}

// The same command with no -p is not a recipient: zai's key is withheld from it and the
// unclaimed value still arrives. The contrast is what makes the cell above a grant rather
// than a leak.
func TestHostGrantAbsentWithholdsTheClaimedValueAndKeepsTheRest(t *testing.T) {
	env, errs := hostGateLaunchWith(t, esGrantConfig, nil, nil, "bash")
	if env["ZAI_API_KEY"] != "" {
		t.Errorf("yolo host -- bash selected nothing and was handed ZAI_API_KEY=%q", env["ZAI_API_KEY"])
	}
	if env["PORT"] != "8080" {
		t.Errorf("PORT = %q, want the unclaimed env_sources value\n%s", env["PORT"], errs)
	}
	if !strings.Contains(errs, "ZAI_API_KEY (provider zai): withheld") {
		t.Errorf("the withheld key is disclosed:\n%s", errs)
	}
}
