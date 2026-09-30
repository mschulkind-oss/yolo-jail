package run

// hostdoorwaysets_test.go pins withoutClientlessPlatforms over an ACTIVE SET
// (docs/design/active-provider-sets.md; the ordered list of profiles one agent runs on, a term
// that doc coins): the doorways a `yolo host` launch plans (PlanHostDoorways) are asked over the
// agent's whole set, so an entry on a platform the agent has no client of is blanked, as a
// primary on one is (HS-D23), and the entries it does have a client of keep asking.

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestWithoutClientlessPlatformsBlanksAClientlessEntryOfASet(t *testing.T) {
	// pi binds aws-bedrock (its own client); copilot binds none.
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "copilot"), officialPack(t, "bedrock")}
	sel := packload.GateSelection{
		Profiles:     map[string]string{"pi": "zai", "copilot": "zai"},
		Sets:         map[string][]string{"pi": {"zai", "bedrock"}, "copilot": {"zai", "bedrock"}},
		SetPlatforms: map[string][]string{"pi": {"", "aws-bedrock"}, "copilot": {"", "aws-bedrock"}},
	}
	out, clientless := withoutClientlessPlatforms(packs, sel)
	if !reflect.DeepEqual(clientless, map[string]string{"copilot": "aws-bedrock"}) {
		t.Errorf("clientless = %v, want copilot's aws-bedrock alone", clientless)
	}
	if got := out.SetPlatforms["pi"]; !reflect.DeepEqual(got, []string{"", "aws-bedrock"}) {
		t.Errorf("pi's entries = %v, want its Bedrock entry kept: pi has a client of it", got)
	}
	if got := out.SetPlatforms["copilot"]; !reflect.DeepEqual(got, []string{"", ""}) {
		t.Errorf("copilot's entries = %v, want its Bedrock entry blanked", got)
	}
	if !reflect.DeepEqual(out.Sets, sel.Sets) {
		t.Errorf("the sets themselves must be kept: %v", out.Sets)
	}
}
