package run

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// bedrockmodels_test.go pins the launch's half of the fetched list
// (docs/design/model-lists-and-pickers.md OQ-MM6) through composePackChannel, the composition every
// backend and an attach run: what it asks the platform's credential service, where the answer
// lands, and the refusal when an agent is left with nothing to start on. The service is the
// FetchModelList seam, a stand-in: no test here reaches AWS.

// bareBedrockPack is a Bedrock provider with no model list, as packs/bedrock ships under MM-D32,
// with its own region so the test needs no ~/.aws/config.
const bareBedrockPack = `{"name":"bare-bedrock","contributes":[
 {"kind":"provider","name":"bb","platform":"aws-bedrock","region":"us-east-1",
  "api_key_env_name":["AWS_CONTAINER_CREDENTIALS_FULL_URI"],"region_env_name":["AWS_REGION"]},
 {"kind":"profile","name":"bbp","provider":"bb"}]}`

func bareBedrockLaunch(t *testing.T) (*Options, *jsonx.OrderedMap, []*packload.Pack, *bytes.Buffer) {
	t.Helper()
	emptyLoopholeDirs(t)
	packs := []*packload.Pack{officialPack(t, "copilot"), officialPack(t, "aws-auth"), officialPack(t, "wire-bridge"),
		{Name: "bare-bedrock", Root: t.TempDir(), Decl: envDisclosureDecl(t, bareBedrockPack)}}
	o := goldenOptions(t.TempDir(), packHome(t))
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.ProfileName = "bbp"
	cfg := awsAuthServedConfig(t, packs)
	lp, _ := cfg.Get("loopholes")
	aa, _ := lp.(*jsonx.OrderedMap).Get("aws-auth")
	aa.(*jsonx.OrderedMap).Set("settings", newConfig("profile", "stand-in-profile"))
	return o, cfg, packs, &stderr
}

func shapeOf(c *packChannel, agent, key string) string {
	for _, v := range c.scope.Agent(agent).Shape {
		if v.Key == key && !v.Unset {
			return v.Value
		}
	}
	return ""
}

// TestTheLaunchHandsTheFetchedListToTheJail: a provider with no list gets the region's from the
// platform's credential service, asked as the configured profile in the provider's region; the
// list lands in YOLO_PROVIDERS under its own key, and copilot starts on its Claude model. It fails
// with composeFetchedLists' call removed from the composition.
func TestTheLaunchHandsTheFetchedListToTheJail(t *testing.T) {
	o, cfg, packs, stderr := bareBedrockLaunch(t)
	var asked []ModelListRequest
	o.FetchModelList = func(req ModelListRequest) awsauthdaemon.ModelListAnswer {
		asked = append(asked, req)
		return awsauthdaemon.ModelListAnswer{Source: "fetched", List: awsauth.ModelList{Region: req.Region,
			Models: []awsauth.BedrockModel{{ID: "openai.gpt-test-1:0", Vendor: "openai"},
				{ID: "us.anthropic.claude-test-v1", Vendor: "anthropic", Name: "US Claude Test"}}}}
	}
	c, err := o.composePackChannel(cfg, packs, jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != (ModelListRequest{Service: "aws-auth", Enabled: true,
		Profile: "stand-in-profile", Region: "us-east-1"}) {
		t.Fatalf("asked %+v, want aws-auth once, as the configured profile in us-east-1", asked)
	}
	v, _ := c.providers.Get("bb")
	got := packload.FetchedModelsOf(v.(*jsonx.OrderedMap))
	if len(got) != 2 || got[1].ID != "us.anthropic.claude-test-v1" || got[1].Vendor != "anthropic" {
		t.Errorf("the composed bb entry's fetched list = %+v", got)
	}
	if m := shapeOf(c, "copilot", "COPILOT_MODEL"); m != "us.anthropic.claude-test-v1" {
		t.Errorf("copilot's model = %q, want the fetched Claude model (the gate composed again)", m)
	}
	if !strings.Contains(stderr.String(), `Model list for provider "bb" (aws-bedrock, us-east-1): 2 models`) {
		t.Errorf("the launch did not say where the list came from:\n%s", stderr)
	}
}

// TestALaunchLeftWithNothingToStartOnSaysWhyAndWhatToAdd: no pack list, copilot's pack says it
// has no Bedrock catalog, the profile names no model, and the fetch failed: the launch stops,
// naming why and the three ways out (the happy-path principle).
func TestALaunchLeftWithNothingToStartOnSaysWhyAndWhatToAdd(t *testing.T) {
	o, cfg, packs, _ := bareBedrockLaunch(t)
	o.FetchModelList = func(ModelListRequest) awsauthdaemon.ModelListAnswer {
		return awsauthdaemon.ModelListAnswer{Note: "the fetch failed: run aws sso login --profile stand-in-profile"}
	}
	_, err := o.composePackChannel(cfg, packs, jsonx.NewOrderedMap())
	if err == nil {
		t.Fatal("the launch composed with nothing for copilot to start on")
	}
	for _, want := range []string{`copilot has no model to start on for profile "bbp"`,
		"aws sso login --profile stand-in-profile", `"model" on profile "bbp"`, `"providers.bb.models"`,
		"`models` contribution"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%s", want, err)
		}
	}
}

// TestNoSeamFetchesNothing: a launch with no FetchModelList, every hand-built Options in a test,
// asks nothing and refuses nothing, so no test of the run pipeline can reach AWS; the front door
// installs the production one (TestTheFrontDoorInstallsTheModelListFetch, internal/cli).
func TestNoSeamFetchesNothing(t *testing.T) {
	o, cfg, packs, _ := bareBedrockLaunch(t)
	c, err := o.composePackChannel(cfg, packs, jsonx.NewOrderedMap())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := c.providers.Get("bb")
	if got := packload.FetchedModelsOf(v.(*jsonx.OrderedMap)); got != nil {
		t.Errorf("a launch with no fetcher composed a fetched list: %+v", got)
	}
}
