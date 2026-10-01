package run

// ompactiveset_test.go pins the jail notch's half of oh-omp holding an ACTIVE SET
// (docs/design/active-provider-sets.md AP-D18; the active set, a term that doc coins, is the
// ordered list of profiles one agent runs on for one launch) for a BARE list: since packs/omp
// declares provider_sets, a list naming no agent reaches oh-omp whole (OQ-AP3), so the rule that a
// carried entry sits only first (AP-D9, AP-D18) now refuses a bare list too, where oh-omp used to
// take the list's first entry. Through the launch's own composition over the SHIPPED packs.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func ompBedrockSetPacks(t *testing.T) []*packload.Pack {
	return []*packload.Pack{officialPack(t, "claude"), officialPack(t, "omp"), officialPack(t, "zai"),
		officialPack(t, "bedrock"), officialPack(t, "aws-auth"), officialPack(t, "wire-bridge")}
}

// `-p zai,bedrock` beside claude and oh-omp: claude takes zai, oh-omp the whole list, and plain
// bedrock second in oh-omp's set rides no route (oh-omp has no Bedrock client, and the wire
// bridge's one route for it is the first entry's), so the launch refuses, naming the reorder that
// works; `-p bedrock,zai` composes.
func TestABareListWithACarriedSecondEntryRefusesForOmp(t *testing.T) {
	home := packHome(t)
	emptyLoopholeDirs(t)
	cfg := bareConfig()
	withBedrockRegion(cfg)
	keys := userEnvWith(map[string]string{"ZAI_API_KEY": "tok-zai",
		"AWS_ACCESS_KEY_ID": "AKIDTEST", "AWS_SECRET_ACCESS_KEY": "secret"})
	packs := ompBedrockSetPacks(t)

	o := goldenOptions(t.TempDir(), home)
	o.ProfileName = "zai,bedrock"
	if got := wireOf(o.effectiveUseProfiles(cfg, packs)); !strings.Contains(got, `"oh-omp":["zai","bedrock"]`) ||
		!strings.Contains(got, `"claude":"zai"`) {
		t.Fatalf("effective table = %s, want oh-omp's whole list and claude's first entry", got)
	}
	_, err := o.composePackChannel(cfg, packs, keys)
	if err == nil {
		t.Fatal("a bare list putting a carried entry second in oh-omp's set must refuse the launch")
	}
	for _, want := range []string{`profile "bedrock" (entry 2 of oh-omp's profiles: zai, bedrock)`,
		"-p oh-omp=bedrock,zai"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q:\n%v", want, err)
		}
	}

	reordered := goldenOptions(t.TempDir(), home)
	reordered.ProfileName = "bedrock,zai"
	if _, err := reordered.composePackChannel(cfg, packs, keys); err != nil {
		t.Errorf("with the carried entry first the bare list must compose: %v", err)
	}
}
