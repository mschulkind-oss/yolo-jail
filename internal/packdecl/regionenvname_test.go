package packdecl

// regionenvname_test.go pins a provider's `region_env_name` on the manifest decoder a launch
// reads packs through: the field that makes a region a launch requirement (OQ-BR6,
// docs/design/bedrock-plumbing.md §8) and names the variables an agent reads its region from.

import (
	"strings"
	"testing"
)

// The list decodes whole and in order onto the provider a launch reads (Providers), which is
// the projection packload's region pre-flight walks: a field that decoded onto Contribution
// and never reached ProviderContribution would declare a requirement nothing enforces.
func TestAProviderMayDeclareItsRegionVariables(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[
	  {"kind":"provider","name":"regional","platform":"cloud","region_env_name":["AWS_REGION","AWS_DEFAULT_REGION"]},
	  {"kind":"provider","name":"plain"}]}`))
	if len(problems) != 0 {
		t.Fatalf("a region_env_name list is legal: %v", problems)
	}
	provs := m.Providers()
	if got := strings.Join(provs[0].RegionEnvName, ","); got != "AWS_REGION,AWS_DEFAULT_REGION" {
		t.Errorf("region_env_name must reach the provider projection whole and in order, got %q", got)
	}
	if provs[1].RegionEnvName != nil {
		t.Errorf("a provider that declares no region_env_name requires no region, got %v", provs[1].RegionEnvName)
	}
}

// Each malformed spelling is refused, naming the field it is in — not api_key_env_name, whose
// validator this one deliberately does not share.
func TestARegionVariableListIsValidated(t *testing.T) {
	for name, body := range map[string]string{
		"an empty list": `[]`,
		"a bad name":    `["NOT-A-NAME"]`,
		"a duplicate":   `["DUP","DUP"]`,
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p","platform":"cloud",` +
				`"region_env_name":` + body + `}]}`))
			if len(problems) == 0 {
				t.Fatalf("region_env_name: %s must be refused", body)
			}
			if got := strings.Join(problems, "\n"); !strings.Contains(got, "region_env_name") ||
				strings.Contains(got, "api_key_env_name") {
				t.Errorf("the refusal must name region_env_name, and only it:\n%s", got)
			}
		})
	}
}

// THE VARIABLES ARE A PLATFORM'S (OQ-BR2's marker keys the requirement): a list declared on a
// provider with no platform would attach to nothing, so it is refused, naming the field it
// needs — the shape BR-D1 first shipped, before the marker was ruled.
func TestRegionVariablesNeedAPlatform(t *testing.T) {
	_, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p",` +
		`"region_env_name":["AWS_REGION"]}]}`))
	if got := strings.Join(problems, "\n"); !strings.Contains(got, `"region_env_name"`) ||
		!strings.Contains(got, `needs the "platform"`) {
		t.Errorf("region_env_name with no platform must be refused, naming platform:\n%s", got)
	}
	if _, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p",` +
		`"platform":"cloud","region_env_name":["AWS_REGION"]}]}`)); len(problems) != 0 {
		t.Errorf("control: the same list beside a platform is legal: %v", problems)
	}
}

// A string is not the field's spelling: unlike api_key_env_name it never had a one-variable
// form to stay compatible with, so a bare string is the decoder's refusal.
func TestARegionVariableMustBeAList(t *testing.T) {
	if _, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p","platform":"cloud",` +
		`"region_env_name":"AWS_REGION"}]}`)); len(problems) == 0 {
		t.Error(`region_env_name: "AWS_REGION" (a string) must be refused`)
	}
}

// A PACK'S REGION IS ONE DNS LABEL, as a user's is (config.validateProviderEntries): an agent
// builds its service's host name from it, so a fetched pack shipping "attacker.example/#" as a
// region would send every request on the provider, and its credential, to that host.
func TestAProviderRegionIsOneDNSLabel(t *testing.T) {
	for _, bad := range []string{"attacker.example/#", "us-east-1.evil", "US-EAST-1"} {
		_, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p","region":"` + bad + `"}]}`))
		if got := strings.Join(problems, "\n"); !strings.Contains(got, `"region"`) || !strings.Contains(got, "one DNS label") {
			t.Errorf("region %q must be refused, naming the field:\n%s", bad, got)
		}
	}
	if _, problems := Decode([]byte(`{"contributes":[{"kind":"provider","name":"p","region":"eu-west-1"}]}`)); len(problems) != 0 {
		t.Errorf("control: a region is legal: %v", problems)
	}
}

// A PROGRAM MAY SAY WHICH OF A PLATFORM'S REGION VARIABLES IT READS (`platform_regions`): opencode
// 1.18.32 reads AWS_REGION and not AWS_DEFAULT_REGION, so the region pre-flight must not count the
// latter for it. The list decodes onto the program's projection a launch reads
// (RegionEnvNamesFor), per platform; a platform the program lists nothing for answers nil.
func TestAProgramMayNarrowAPlatformsRegionVariables(t *testing.T) {
	m, problems := Decode([]byte(`{"contributes":[{"kind":"program","bin":"agent","package":"agent",` +
		`"via":"npm","platform_regions":[{"platform":"aws-bedrock","region_env_name":["AWS_REGION"]}]}]}`))
	if len(problems) != 0 {
		t.Fatalf("a program's platform_regions is legal: %v", problems)
	}
	if got := strings.Join(m.RegionEnvNamesFor("agent", "aws-bedrock"), ","); got != "AWS_REGION" {
		t.Errorf("RegionEnvNamesFor(agent, aws-bedrock) = %q, want AWS_REGION", got)
	}
	if got := m.RegionEnvNamesFor("agent", "other"); got != nil {
		t.Errorf("a platform the program lists nothing for must answer nil, got %v", got)
	}
	if got := m.RegionEnvNamesFor("someone-else", "aws-bedrock"); got != nil {
		t.Errorf("a bin the manifest does not install must answer nil, got %v", got)
	}
}

func TestAProgramsRegionVariablesAreValidated(t *testing.T) {
	program := func(body string) string {
		return `{"contributes":[{"kind":"program","bin":"agent","package":"agent","via":"npm","platform_regions":` + body + `}]}`
	}
	for name, tc := range map[string]struct{ manifest, want string }{
		"no platform":      {program(`[{"region_env_name":["AWS_REGION"]}]`), `needs the "platform"`},
		"a spaced one":     {program(`[{"platform":"aws bedrock","region_env_name":["AWS_REGION"]}]`), "carries whitespace"},
		"no variables":     {program(`[{"platform":"aws-bedrock"}]`), "names no variable"},
		"a bad name":       {program(`[{"platform":"aws-bedrock","region_env_name":["NOT-A-NAME"]}]`), "invalid env var name"},
		"a platform twice": {program(`[{"platform":"p","region_env_name":["A"]},{"platform":"p","region_env_name":["B"]}]`), "listed twice"},
		"on another kind": {`{"contributes":[{"kind":"env","vars":{"A":"b"},` +
			`"platform_regions":[{"platform":"p","region_env_name":["A"]}]}]}`, `does not take "platform_regions"`},
		"region_env_name on a program": {`{"contributes":[{"kind":"program","bin":"agent","package":"agent","via":"npm",` +
			`"region_env_name":["AWS_REGION"]}]}`, `does not take "region_env_name"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, problems := Decode([]byte(tc.manifest))
			if got := strings.Join(problems, "\n"); !strings.Contains(got, tc.want) {
				t.Errorf("want a refusal saying %q, got:\n%s", tc.want, got)
			}
		})
	}
}
