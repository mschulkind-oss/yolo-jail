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
