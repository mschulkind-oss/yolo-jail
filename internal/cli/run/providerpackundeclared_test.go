package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// providerpackundeclared_test.go pins the launch's undeclared-profile refusal at the call site
// (composePackChannel → checkProfileDeclarations), for the three answers
// docs/design/provider-enablement.md gives: a profile only an UNSELECTED shipped pack declares
// names the pack to add (BI-D14); a former profile name names the new one (OQ-PE3, PE-D7); a name
// a selected agent has built in types out the declaration (OQ-PE4).

func profileFor(agent, name string) *jsonx.OrderedMap {
	cfg := bareConfig()
	prof := jsonx.NewOrderedMap()
	prof.Set(agent, name)
	cfg.Set("profile", prof)
	return cfg
}

func TestAnUndeclaredCopilotProfileNamesItsPack(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	cfg := profileFor("pi", "copilot")
	_, err := o.composePackChannel(cfg, []*packload.Pack{officialPack(t, "pi")}, zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), `no profile named "copilot" is declared`) ||
		!strings.Contains(err.Error(), "run `yolo pack add copilot-auth`") {
		t.Fatalf("composePackChannel = %v, want the refusal to name adding the copilot-auth pack", err)
	}
	// Selected beside pi, the pack declares it.
	if _, err := o.composePackChannel(cfg, []*packload.Pack{officialPack(t, "pi"),
		officialPack(t, "copilot-auth")}, zaiAndRouterKeys()); err != nil {
		t.Fatalf("pi with the copilot-auth pack: %v", err)
	}
}

func TestTheLaunchRefusesTheFormerCopilotProfileNamingTheNewOne(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	_, err := o.composePackChannel(profileFor("pi", "github-copilot"),
		[]*packload.Pack{officialPack(t, "pi"), officialPack(t, "copilot-auth")}, zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), `profile "github-copilot" selected for pi`) ||
		!strings.Contains(err.Error(), `renamed "copilot"`) || !strings.Contains(err.Error(), `select "copilot" instead`) {
		t.Fatalf("composePackChannel = %v, want the former name refused naming copilot", err)
	}
}

func TestTheLaunchTypesOutTheDeclarationForABuiltInName(t *testing.T) {
	home := packHome(t)
	o := goldenOptions(t.TempDir(), home)
	_, err := o.composePackChannel(profileFor("pi", "groq"), []*packload.Pack{officialPack(t, "pi")},
		zaiAndRouterKeys())
	if err == nil || !strings.Contains(err.Error(), "pi has groq built in") ||
		!strings.Contains(err.Error(), `"profiles": {"groq": {"provider": "groq"}}`) {
		t.Fatalf("composePackChannel = %v, want the two-line declaration typed out", err)
	}
}
