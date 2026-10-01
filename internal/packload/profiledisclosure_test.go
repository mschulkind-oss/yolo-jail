package packload

import (
	"strings"
	"testing"
)

// profiledisclosure_test.go pins what the profile disclosure answers per agent from the launch's
// declarations alone (docs/reference/providers.md#what-the-launch-checks-and-prints), over the
// shipped packs: the route each agent's selection reaches its provider by, the one binding rule
// for a provider named by its platform, and the credential half for a provider no endpoint names.

// disclose runs the disclosure over the shipped packs named, with table as the profile table and
// reaches as the per-agent lookup, and returns the one entry for profile.
func disclose(t *testing.T, names []string, table map[string]string,
	reaches func(agent, name string) bool) ProfileDisclosure {
	t.Helper()
	packs := embeddedNamed(t, names...)
	providers, resolved, _ := launchSelection(t, packs, nil, nil, table)
	got := ProfileDisclosures(ProfileDisclosureInput{Table: table, Packs: packs,
		Resolved: resolved, Providers: providers, Reaches: reaches})
	if len(got) != 1 {
		t.Fatalf("want one disclosure for %v, got %+v", table, got)
	}
	return got[0]
}

func reachOf(t *testing.T, d ProfileDisclosure, agent string) ProfileReach {
	t.Helper()
	for _, a := range d.Agents {
		if a.Agent == agent {
			return a
		}
	}
	t.Fatalf("no answer for %s in %+v", agent, d)
	return ProfileReach{}
}

// A BEDROCK PROVIDER IS REACHED BY A CLIENT OF THE AGENT'S OWN, which its pack declares it binds
// (bindsPlatform): every shipped agent pack whose derive binds Bedrock needs packs/bedrock, which
// ships a provider of that platform (awsauthneed_test.go pins that need to the derives). copilot's
// needs no such pack, so it is reached only through the wire bridge, the carrier of an agent with
// no client of the platform (carrier.go; bedrock-plumbing.md OQ-BR1), and the line says which.
// With no bridge in the launch nothing carries copilot, so the selection reaches nothing for it
// and the line says why, with the fix.
func TestTheProfileDisclosureReadsEachAgentsPlatformBinding(t *testing.T) {
	table := map[string]string{"claude": "bedrock", "pi": "bedrock", "codex": "bedrock",
		"opencode": "bedrock", "copilot": "bedrock"}
	d := disclose(t, []string{"claude", "pi", "codex", "opencode", "copilot", "bedrock", "aws-auth",
		"openai-auth", "wire-bridge"}, table, nil)
	if strings.Join(d.Declared, ",") != "bedrock" {
		t.Errorf("declared by %v, want the bedrock pack alone", d.Declared)
	}
	for _, agent := range []string{"claude", "pi", "codex", "opencode"} {
		r := reachOf(t, d, agent)
		if r.Provider != "bedrock" || r.Route != `through `+agent+`'s own "aws-bedrock" client` || len(r.Warnings) != 0 {
			t.Errorf("%s: %+v, want provider bedrock through its own client and no warning", agent, r)
		}
	}
	carried := reachOf(t, d, "copilot")
	if carried.Route != `through pack "wire-bridge", which carries an agent with no "aws-bedrock" client of its own` ||
		len(carried.Warnings) != 0 {
		t.Errorf("copilot beside the bridge: %+v, want it carried by the bridge and no warning", carried)
	}
	// No bridge: the closure is the packs named, so nothing joins one.
	alone := disclose(t, []string{"pi", "copilot", "bedrock", "aws-auth"},
		map[string]string{"pi": "bedrock", "copilot": "bedrock"}, nil)
	copilot := reachOf(t, alone, "copilot")
	if copilot.Route != "" || len(copilot.Warnings) != 1 ||
		!strings.Contains(copilot.Warnings[0], `reaches nothing for copilot`) ||
		!strings.Contains(copilot.Warnings[0], "`-p copilot=<name>`") {
		t.Errorf("copilot with no bridge: %+v, want one warning that the selection reaches nothing for it", copilot)
	}
	if !strings.Contains(alone.Line(), `copilot → provider "bedrock", which it cannot use here (below)`) {
		t.Errorf("the line must say copilot cannot use it: %s", alone.Line())
	}
}

// A CARRIED AGENT NEEDS THE CREDENTIAL TOO: the bridge sends copilot's requests on with the AWS
// credential that reaches copilot, so when none of the provider's claimed variables does, the
// line says the carrier has none to send them with, though the address the carrier composed is
// an endpoint of the entry copilot sees. One that reaches copilot silences it.
func TestACarriedAgentIsToldWhenNoCredentialReachesIt(t *testing.T) {
	names := []string{"claude", "copilot", "bedrock", "aws-auth", "wire-bridge"}
	table := map[string]string{"claude": "bedrock", "copilot": "bedrock"}
	none := disclose(t, names, table, func(string, string) bool { return false })
	copilot := reachOf(t, none, "copilot")
	if len(copilot.Warnings) != 1 || !strings.Contains(copilot.Warnings[0],
		`delivers copilot no credential for provider "bedrock"`) || !strings.Contains(copilot.Warnings[0],
		`so pack "wire-bridge", which sends copilot's requests on with the credential that reaches copilot, has none`) {
		t.Errorf("copilot carried with no credential: %+v, want the carrier's credential warning", copilot)
	}
	some := disclose(t, names, table, func(agent, name string) bool { return name == "AWS_ACCESS_KEY_ID" })
	if w := reachOf(t, some, "copilot").Warnings; len(w) != 0 {
		t.Errorf("copilot carried with a key that reaches it is warned: %v", w)
	}
}

// THE OTHER TWO BINDING DECLARATIONS, each alone in a pack that needs nothing: a program's own
// region variables for the platform (opencode's `platform_regions` shape), and its own switch for
// it (claude's `platform_switches` shape). The same agent with neither reaches nothing.
func TestAProgramsOwnPlatformDeclarationBindsIt(t *testing.T) {
	bedrock := embeddedNamed(t, "bedrock")
	for _, tc := range []struct{ name, program string }{
		{"region", `{"kind": "program", "bin": "acme", "via": "npm", "package": "@acme/acme", ` +
			`"platform_regions": [{"platform": "aws-bedrock", "region_env_name": ["AWS_REGION"]}]}`},
		{"switch", `{"kind": "program", "bin": "acme", "via": "npm", "package": "@acme/acme", ` +
			`"platform_switches": [{"platform": "aws-bedrock", "surface": "acme/settings", ` +
			`"pointer": "/env/ACME_USE_BEDROCK"}]}`},
		{"neither", `{"kind": "program", "bin": "acme", "via": "npm", "package": "@acme/acme"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acme := writePack(t, "acme", "acme", `{"name": "acme", "contributes": [`+tc.program+`]}`)
			packs := append([]*Pack{acme}, bedrock...)
			table := map[string]string{"acme": "bedrock"}
			providers, resolved, _ := launchSelection(t, packs, nil, nil, table)
			d := ProfileDisclosures(ProfileDisclosureInput{Table: table, Packs: packs,
				Resolved: resolved, Providers: providers})
			r := reachOf(t, d[0], "acme")
			if binds := r.Route == `through acme's own "aws-bedrock" client`; binds != (tc.name != "neither") {
				t.Errorf("%s: %+v", tc.name, r)
			}
		})
	}
}

// A PROVIDER WITH ENDPOINTS is reached by the pairing protocol resolution settles, and the
// credential half stays the credential pre-flight's, which refuses a missing key itself.
func TestTheProfileDisclosureNamesTheResolvedEndpoint(t *testing.T) {
	d := disclose(t, []string{"claude", "zai"}, map[string]string{"claude": "zai"},
		func(string, string) bool { return false })
	r := reachOf(t, d, "claude")
	if r.Provider != "zai" || r.Route != `on its "anthropic" endpoint` || len(r.Warnings) != 0 {
		t.Errorf("claude on zai: %+v, want its anthropic endpoint and no warning", r)
	}
}

// THE CREDENTIAL HALF: a Bedrock agent none of whose provider's claimed variables reaches it is
// told so, naming them; one reached by any of them, the pointer included, is not; and a notch
// that asks no credential question (Reaches nil) says nothing either way.
func TestTheProfileDisclosureSaysWhenNoCredentialReachesTheAgent(t *testing.T) {
	names := []string{"pi", "bedrock", "aws-auth", "openai-auth"}
	table := map[string]string{"pi": "bedrock"}
	none := reachOf(t, disclose(t, names, table, func(string, string) bool { return false }), "pi")
	if len(none.Warnings) != 1 || !strings.Contains(none.Warnings[0],
		`profile "bedrock" delivers pi no credential for provider "bedrock" at this notch`) ||
		!strings.Contains(none.Warnings[0], "AWS_CONTAINER_CREDENTIALS_FULL_URI") ||
		!strings.Contains(none.Warnings[0], "AWS_PROFILE") {
		t.Errorf("pi with no credential: %+v", none)
	}
	pointer := reachOf(t, disclose(t, names, table, func(_, name string) bool {
		return name == "AWS_CONTAINER_CREDENTIALS_FULL_URI"
	}), "pi")
	if len(pointer.Warnings) != 0 {
		t.Errorf("pi reached by the pointer is still warned: %+v", pointer)
	}
	if unasked := reachOf(t, disclose(t, names, table, nil), "pi"); len(unasked.Warnings) != 0 {
		t.Errorf("a disclosure asked no credential question warned: %+v", unasked)
	}
}

// THE ACTIVE-SET LINE NAMES EACH SET OF MORE THAN ONE, in order, and says where a fresh session
// starts in words true of every set-capable agent (docs/design/active-provider-sets.md AP-D15):
// pi keeps a pick of its own inside the set, and opencode keeps none past its session while yolo
// writes its `model`, so "when yolo has to pick", which the line said before, was false for
// opencode. A set of one prints nothing.
func TestTheActiveSetLineSaysWhereAFreshSessionStarts(t *testing.T) {
	got := ActiveSetLines(map[string][]string{
		"pi": {"zai", "openrouter"}, "opencode": {"zai", "bedrock"}, "claude": {"zai"},
	})
	want := []string{
		"Active set for opencode: zai, bedrock — every entry's provider is live in one session, " +
			"and a fresh session starts on zai unless the agent keeps a pick of its own inside the set",
		"Active set for pi: zai, openrouter — every entry's provider is live in one session, " +
			"and a fresh session starts on zai unless the agent keeps a pick of its own inside the set",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ActiveSetLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
