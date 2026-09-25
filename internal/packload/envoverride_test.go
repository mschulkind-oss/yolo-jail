package packload

// envoverride_test.go pins the EVALUATION of `overridden_by`: when a declaration is
// tripped, when it is not, and what the refusal says. The schema is packdecl's and is
// pinned there; the three launch arms and the `yolo check` prediction that call this are
// pinned at their call sites.
//
// Most fixtures below declare MADE-UP variables in a made-up pack, and that is the point of
// them: OQ-SSO8 ruled that core names no AWS variable, so the rule has to fire for a
// declaration nothing in core has heard of. The shipped aws-auth declaration gets its own
// test at the end, because it is the configuration the ruling is about.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// widgetPack declares an agent (so a profile can be active for a bin), and one env
// contribution setting WIDGET_POINTER, gated on `gate` when gate is non-empty, with the
// given overrides.
func widgetPack(t *testing.T, gate, overrides string) *Pack {
	t.Helper()
	profile := ""
	if gate != "" {
		profile = `"profile": "` + gate + `",`
	}
	return &Pack{Name: "widget", Decl: declFrom(t, `{"contributes": [
	  {"kind": "program", "bin": "widgetcli", "via": "npm", "package": "widgetcli"},
	  {"kind": "env", `+profile+`
	   "vars": {"WIDGET_POINTER": "http://127.0.0.1:1/creds"},
	   "overridden_by": `+overrides+`}
	]}`)}
}

// delivered is an OriginLookup over a fixed var->origin map; absent keys are undelivered.
func delivered(m map[string]string) OriginLookup {
	return func(name string) (string, bool) {
		where, ok := m[name]
		return where, ok
	}
}

const tokenOverride = `[{"vars": ["WIDGET_TOKEN"], "because": "the widget client reads WIDGET_TOKEN first"}]`

// TestEnvOverrideRefusesAMadeUpVariable is the ruling's core test: a variable no line of
// core names, declared by a pack no line of core names, refuses the launch — and the
// refusal carries both origins, the pack's own reason, and the remedy.
func TestEnvOverrideRefusesAMadeUpVariable(t *testing.T) {
	packs := []*Pack{widgetPack(t, "", tokenOverride)}
	lines := EnvOverrideRefusal(packs, nil, delivered(map[string]string{
		"WIDGET_TOKEN": FromEnvSources,
	}), nil)
	if len(lines) == 0 {
		t.Fatal("a delivered override beside an unconditional contribution was not refused")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"Refusing to launch",
		"pack widget",    // whose contribution
		"WIDGET_POINTER", // what it sets
		"WIDGET_TOKEN is delivered by " + FromEnvSources, // what overrides it, and from where
		"the widget client reads WIDGET_TOKEN first",     // the pack's own words
		"Drop one.",
		"Remove WIDGET_TOKEN from " + FromEnvSources,
		"deselect pack widget",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusal does not say %q:\n%s", want, joined)
		}
	}
}

// TestEnvOverrideIsSilentWhenTheContributionIsNotDelivered: a gated contribution whose
// profile is not active delivers nothing, so there is nothing to override — refusing here
// is the false positive OQ-SSO8 forbids. The same fixture refuses once the profile is
// active for a bin the launch installs.
func TestEnvOverrideIsSilentWhenTheContributionIsNotDelivered(t *testing.T) {
	packs := []*Pack{widgetPack(t, "gate", tokenOverride)}
	look := delivered(map[string]string{"WIDGET_TOKEN": FromLaunchEnv})

	if lines := EnvOverrideRefusal(packs, nil, look, nil); lines != nil {
		t.Errorf("refused over a contribution no profile delivers:\n%s", strings.Join(lines, "\n"))
	}
	if lines := EnvOverrideRefusal(packs, map[string]string{"widgetcli": "other"}, look, nil); lines != nil {
		t.Errorf("refused under a different active profile:\n%s", strings.Join(lines, "\n"))
	}
	lines := EnvOverrideRefusal(packs, map[string]string{"widgetcli": "gate"}, look, nil)
	if len(lines) == 0 {
		t.Fatal("the gating profile is active and the override delivered, and nothing refused")
	}
	if joined := strings.Join(lines, "\n"); !strings.Contains(joined, "`gate` profile") {
		t.Errorf("the remedy must name the profile that delivers the contribution:\n%s", joined)
	}
}

// TestEnvOverrideReachesACLILessPack: the env fold's WIDE pass — a pack installing no CLI
// has its gated env delivered when any selected pack's bin has the profile active. That is
// packs/aws-auth's own situation (it installs nothing; claude's `bedrock` profile delivers
// its pointer), so the evaluator must use the fold's rule and not a narrower one.
func TestEnvOverrideReachesACLILessPack(t *testing.T) {
	cliLess := &Pack{Name: "pointer", Decl: declFrom(t, `{"contributes": [
	  {"kind": "env", "profile": "gate", "vars": {"WIDGET_POINTER": "x"},
	   "overridden_by": `+tokenOverride+`}]}`)}
	agent := &Pack{Name: "agent", Decl: declFrom(t, `{"contributes": [
	  {"kind": "program", "bin": "widgetcli", "via": "npm", "package": "widgetcli"}]}`)}
	lines := EnvOverrideRefusal([]*Pack{agent, cliLess}, map[string]string{"widgetcli": "gate"},
		delivered(map[string]string{"WIDGET_TOKEN": FromLaunchEnv}), nil)
	if len(lines) == 0 {
		t.Fatal("a CLI-less pack's gated contribution is delivered by the wide pass, and its " +
			"declared override was not evaluated")
	}
}

// pairOverride is the conjunction-with-an-exception shape, in made-up names.
const pairOverride = `[{"vars": ["WIDGET_ID", "WIDGET_SECRET"], "unless": ["WIDGET_PROFILE"],
  "because": "the static pair answers before the pointer"}]`

// TestEnvOverridePairNeedsBothHalvesAndNoException: the three conditions OQ-SSO8 names for
// the static pair — a lone half launches, the pair refuses, the pair plus the exception
// launches — on made-up names, so the conjunction and `unless` are core's generic rules.
func TestEnvOverridePairNeedsBothHalvesAndNoException(t *testing.T) {
	packs := []*Pack{widgetPack(t, "", pairOverride)}
	cases := []struct {
		name   string
		env    map[string]string
		refuse bool
	}{
		{"one half alone", map[string]string{"WIDGET_ID": FromEnvSources}, false},
		{"the other half alone", map[string]string{"WIDGET_SECRET": FromEnvSources}, false},
		{"both halves", map[string]string{
			"WIDGET_ID": FromEnvSources, "WIDGET_SECRET": FromLaunchEnv}, true},
		{"both halves plus the exception", map[string]string{
			"WIDGET_ID": FromEnvSources, "WIDGET_SECRET": FromEnvSources,
			"WIDGET_PROFILE": FromLaunchEnv}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := EnvOverrideRefusal(packs, nil, delivered(tc.env), nil)
			if (len(lines) > 0) != tc.refuse {
				t.Errorf("refused=%v want %v:\n%s", len(lines) > 0, tc.refuse, strings.Join(lines, "\n"))
			}
		})
	}

	lines := EnvOverrideRefusal(packs, nil, delivered(map[string]string{
		"WIDGET_ID": FromEnvSources, "WIDGET_SECRET": FromLaunchEnv}), nil)
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"WIDGET_ID is delivered by " + FromEnvSources,
		"WIDGET_SECRET is delivered by " + FromLaunchEnv,
		// Either half breaks the pair, so the remedy must not imply both have to go.
		"Remove any one of: WIDGET_ID from " + FromEnvSources,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the pair refusal does not say %q:\n%s", want, joined)
		}
	}
}

// TestEnvOverrideHostFile: a rendered host_files destination at or under the declared
// path refuses, naming the destination; a sibling sharing its letters does not.
func TestEnvOverrideHostFile(t *testing.T) {
	packs := []*Pack{widgetPack(t, "", `[{"host_file": ".widget",
	  "because": "the widget config dir answers before the pointer"}]`)}
	look := delivered(nil)

	if lines := EnvOverrideRefusal(packs, nil, look, []string{".widgetfoo", ".config/widget"}); lines != nil {
		t.Errorf("a sibling destination was refused as ~/.widget:\n%s", strings.Join(lines, "\n"))
	}
	lines := EnvOverrideRefusal(packs, nil, look, []string{".widget/config", ".ok"})
	if len(lines) == 0 {
		t.Fatal("a rendered ~/.widget/config beside the contribution was not refused")
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{
		"A host_files entry renders ~/.widget/config into the jail.",
		"the widget config dir answers before the pointer",
		"Remove the host_files entry for ~/.widget/config",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the host_file refusal does not say %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "~/.ok") {
		t.Errorf("an unrelated destination was named:\n%s", joined)
	}
}

// TestEnvOverrideDescribesAnUnknownOrigin: a caller that knows a variable is delivered but
// not from where still produces a readable sentence rather than "delivered by .".
func TestEnvOverrideDescribesAnUnknownOrigin(t *testing.T) {
	lines := EnvOverrideRefusal([]*Pack{widgetPack(t, "", tokenOverride)}, nil,
		delivered(map[string]string{"WIDGET_TOKEN": "  "}), nil)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "delivered by this launch's environment") {
		t.Errorf("an empty origin was not given a readable phrase:\n%s", joined)
	}
}

// TestEnvOverrideTakesNilInputs: every caller hands this whatever it has; nil must mean
// "nothing delivered" and never panic.
func TestEnvOverrideTakesNilInputs(t *testing.T) {
	if lines := EnvOverrideRefusal([]*Pack{nil, {Name: "empty"}, widgetPack(t, "", tokenOverride)},
		nil, nil, nil); lines != nil {
		t.Errorf("nil inputs produced a refusal: %v", lines)
	}
}

// TestShippedAWSAuthDeclaresTheThreeOverrides pins packs/aws-auth's declaration — the
// configuration OQ-SSO8 is about — through the evaluator rather than by reading the JSON,
// so the test fails if the pack or the rule stops refusing. The pack's pointer is gated on
// the `bedrock` profile, which claude's bin carries here; aws-auth installs no CLI, so this
// also exercises the wide pass.
func TestShippedAWSAuthDeclaresTheThreeOverrides(t *testing.T) {
	var aws *Pack
	for _, p := range Embedded() {
		if p.Name == "aws-auth" {
			aws = p
		}
	}
	if aws == nil {
		t.Fatal("packs/aws-auth is not embedded")
	}
	agent := &Pack{Name: "agent", Decl: declFrom(t, `{"contributes": [
	  {"kind": "program", "bin": "claude", "via": "npm", "package": "x"}]}`)}
	packs := []*Pack{agent, aws}
	on := map[string]string{"claude": "bedrock"}

	cases := []struct {
		name   string
		env    map[string]string
		grants []string
		refuse bool
	}{
		{"the pointer alone — the pack's own shape", nil, nil, false},
		{"a bearer", map[string]string{"AWS_BEARER_TOKEN_BEDROCK": FromEnvSources}, nil, true},
		{"a lone access key id", map[string]string{"AWS_ACCESS_KEY_ID": FromEnvSources}, nil, false},
		{"a lone secret", map[string]string{"AWS_SECRET_ACCESS_KEY": FromEnvSources}, nil, false},
		{"the static pair", map[string]string{
			"AWS_ACCESS_KEY_ID": FromEnvSources, "AWS_SECRET_ACCESS_KEY": FromEnvSources}, nil, true},
		{"the static pair plus AWS_PROFILE", map[string]string{
			"AWS_ACCESS_KEY_ID": FromEnvSources, "AWS_SECRET_ACCESS_KEY": FromEnvSources,
			"AWS_PROFILE": FromLaunchEnv}, nil, false},
		{"a session token alone — optional to the environment provider", map[string]string{
			"AWS_SESSION_TOKEN": FromEnvSources}, nil, false},
		{"a ~/.aws grant", nil, []string{".aws"}, true},
		{"a ~/.aws/config grant", nil, []string{".aws/config"}, true},
		{"a ~/.awsfoo grant", nil, []string{".awsfoo"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := EnvOverrideRefusal(packs, on, delivered(tc.env), tc.grants)
			if (len(lines) > 0) != tc.refuse {
				t.Errorf("refused=%v want %v:\n%s", len(lines) > 0, tc.refuse, strings.Join(lines, "\n"))
			}
		})
	}

	// Without the `bedrock` profile the pointer is not delivered, so NOTHING refuses —
	// selecting the pack alone must change nothing observable (design §12 step 4).
	if lines := EnvOverrideRefusal(packs, nil, delivered(map[string]string{
		"AWS_BEARER_TOKEN_BEDROCK": FromEnvSources}), []string{".aws"}); lines != nil {
		t.Errorf("the pack refused a launch that does not deliver its pointer:\n%s",
			strings.Join(lines, "\n"))
	}
}

// TestFootprintReportsEachOverride: `yolo pack lint` and `yolo pack footprint` print one
// line per `overridden_by` entry, so an author sees what the pack will REFUSE a launch
// over before any launch does — a refusal with no hatch is the last thing a user should
// first meet at launch time. Each line names the condition and the contribution's gate.
func TestFootprintReportsEachOverride(t *testing.T) {
	fp := FootprintOf(widgetPack(t, "gate", `[
	  {"vars": ["WIDGET_ID", "WIDGET_SECRET"], "unless": ["WIDGET_PROFILE"], "because": "x"},
	  {"host_file": ".widget", "because": "y"}
	]`))
	var got []string
	for _, c := range fp.Claims {
		if c.Kind != OverriddenByClaimKind {
			continue
		}
		if c.Target != "WIDGET_POINTER" {
			t.Errorf("override claim target = %q, want the variable the contribution sets", c.Target)
		}
		if c.ReviewWorthy {
			t.Errorf("an override claim widens nothing and must not reach the launch banner: %+v", c)
		}
		got = append(got, c.Detail)
	}
	want := []string{
		`launch refused beside WIDGET_ID + WIDGET_SECRET unless WIDGET_PROFILE is also delivered (when profile "gate" is active)`,
		`launch refused beside a host_files grant at ~/.widget (when profile "gate" is active)`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("override claims:\n got %q\nwant %q", got, want)
	}
	if packdecl.KnownKind(OverriddenByClaimKind) {
		t.Error("OverriddenByClaimKind is a registered contribution kind — it must stay a " +
			"display label, or Collisions and the per-kind census tests start treating it as one")
	}
}
