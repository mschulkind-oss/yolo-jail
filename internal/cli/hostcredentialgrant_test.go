package cli

// hostcredentialgrant_test.go pins THE HOST'S GRANT for an ad-hoc command
// (docs/design/credential-sources-separation.md §5, ES-D1 to ES-D5): `yolo host -p <profile> --
// <cmd>` makes any command, not only an agent a pack installs, a recipient of that profile's
// claimed env_sources values. Every cell runs `yolo host` through hostMain to the exec, as the
// TestHostGate* cells in hostcredentialgate_test.go do, with `bash` as the command: no selected
// pack installs it, so no pack code runs for it and the grant is the only thing that can hand it
// a claimed value.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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

// ES-D2 at `yolo host --`: the withheld line names the one existing remedy, the typed -p that
// would deliver the key, spelled for the command this launch was given. The profile is a
// declared one resolving to the claiming provider — zai's own same-named profile here.
func TestHostGrantWithheldLineNamesTheTypedProfileRemedy(t *testing.T) {
	_, errs := hostGateLaunchWith(t, esGrantConfig, nil, nil, "bash")
	line := scopeLine(t, errs, "ZAI_API_KEY")
	if !strings.Contains(line, "withheld") {
		t.Errorf("the key is still disclosed as withheld: %q", line)
	}
	if !strings.Contains(line, "`yolo host -p zai -- bash`") {
		t.Errorf("the withheld line must name `yolo host -p zai -- bash`, the grant that would "+
			"deliver it (ES-D2): %q", line)
	}
	if strings.Contains(line, "yolo host env") {
		t.Errorf("an exec names the exec spelling for the command it was given, not the env "+
			"verb's: %q", line)
	}
}

// ES-D2 at `yolo host env`: the same remedy, spelled for the slice the verb composes — its own
// --agent spelling for the shell, and the exec spelling for one launch of that agent.
func TestHostEnvWithheldLineNamesTheTypedProfileRemedy(t *testing.T) {
	hostGateHome(t, esGrantConfig, nil)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"env"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("yolo host env: rc = %d\n%s", rc, errw.String())
	}
	line := scopeLine(t, errw.String(), "ZAI_API_KEY")
	for _, want := range []string{"`yolo host env --agent claude -p zai`", "`yolo host -p zai -- claude`"} {
		if !strings.Contains(line, want) {
			t.Errorf("yolo host env's withheld line must name %s (ES-D2): %q", want, line)
		}
	}
}

// ES-D2's other arm: a provider the user declared under `providers` has no profile until the
// user declares one, and -p takes a profile name, so the line says to declare one rather than
// naming a -p that would refuse. The §1 incident's deepseek line.
func TestHostGrantWithheldLineSaysToDeclareAProfileWhenNoneSelectsTheProvider(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude"], `+
		`"providers": {"deepseek": {"base_url": "https://api.deepseek.example", `+
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil, nil, "bash")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	for _, want := range []string{"No declared profile selects deepseek", "`profiles`",
		`"deepseek": {"provider": "deepseek"}`, "`yolo host -p deepseek -- bash`"} {
		if !strings.Contains(line, want) {
			t.Errorf("with no profile selecting deepseek the line must say to declare one (%q "+
				"missing): %q", want, line)
		}
	}
}

// With a user profile declared over that provider, the remedy names it: a declared profile
// that resolves to the claimant, whatever it is called.
func TestHostGrantWithheldLineNamesAUserProfileOverTheProvider(t *testing.T) {
	_, errs := hostGateLaunchWith(t, `{"packs": ["claude"], `+
		`"providers": {"deepseek": {"base_url": "https://api.deepseek.example", `+
		`"api_key_env_name": "DEEPSEEK_API_KEY"}}, `+
		`"profiles": {"ds": {"provider": "deepseek"}}, `+
		`"env_sources": [{"DEEPSEEK_API_KEY": "tok-ds"}]}`, nil, nil, "bash")
	line := scopeLine(t, errs, "DEEPSEEK_API_KEY")
	if !strings.Contains(line, "`yolo host -p ds -- bash`") {
		t.Errorf("the remedy must name the user's profile over deepseek: %q", line)
	}
}

// remedyProfile prefers the profile named after a claiming provider, and otherwise takes the
// first by name that resolves to any claimant.
func TestRemedyProfilePrefersTheProvidersOwnName(t *testing.T) {
	resolved := map[string]packload.ResolvedProfile{
		"a-zai": {Provider: "zai"}, "zai": {Provider: "zai"}, "b-ds": {Provider: "deepseek"},
		"a-ds": {Provider: "deepseek"}, "other": {Provider: "openrouter"},
	}
	for _, tc := range []struct {
		claimants []string
		want      string
	}{
		{[]string{"zai"}, "zai"},
		{[]string{"deepseek"}, "a-ds"},
		{[]string{"deepseek", "zai"}, "zai"},
		{[]string{"cerebras"}, ""},
	} {
		if got := remedyProfile(resolved, tc.claimants); got != tc.want {
			t.Errorf("remedyProfile(%v) = %q, want %q", tc.claimants, got, tc.want)
		}
	}
}

// scopeLine returns the credential-scope disclosure line whose name list holds name, failing
// when there is none.
func scopeLine(t *testing.T, errs, name string) string {
	t.Helper()
	for _, l := range strings.Split(errs, "\n") {
		head, _, ok := strings.Cut(l, " (provider ")
		if ok && strings.Contains(", "+strings.TrimSpace(head)+", ", ", "+name+", ") {
			return l
		}
	}
	t.Fatalf("no disclosure line names %s:\n%s", name, errs)
	return ""
}
