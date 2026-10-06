package awsauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// modellist_test.go drives the Bedrock list fetch through the Runner seam, with stand-ins for
// what the three `aws` invocations print. No test runs `aws` or reaches AWS. The stand-in
// account, model ids and backing-model references are invented, and the references are
// deliberately not ARNs: the join reads the `foundation-model/` resource, which a stand-in
// spells as well as AWS does.

const (
	standInIdentity = `{"UserId":"stand-in","Account":"stand-in-account","Arn":"stand-in"}`
	standInModels   = `{"modelSummaries":[
	  {"modelArn":"stand-in:foundation-model/anthropic.claude-test-v1","modelId":"anthropic.claude-test-v1",
	   "modelName":"Claude Test","providerName":"Anthropic","outputModalities":["TEXT"],
	   "inferenceTypesSupported":["INFERENCE_PROFILE"]},
	  {"modelArn":"stand-in:foundation-model/openai.gpt-test-1:0","modelId":"openai.gpt-test-1:0",
	   "modelName":"gpt test","providerName":"OpenAI","outputModalities":["TEXT"],
	   "inferenceTypesSupported":["ON_DEMAND"]},
	  {"modelArn":"stand-in:foundation-model/amazon.image-test-v1","modelId":"amazon.image-test-v1",
	   "modelName":"Image Test","providerName":"Amazon","outputModalities":["IMAGE"],
	   "inferenceTypesSupported":["ON_DEMAND"]},
	  {"modelArn":"stand-in:foundation-model/mistral.test-v1","modelId":"mistral.test-v1",
	   "modelName":"Mistral Test","providerName":"Mistral AI","outputModalities":["TEXT"],
	   "inferenceTypesSupported":["ON_DEMAND","INFERENCE_PROFILE"]}
	]}`
	standInProfiles = `{"inferenceProfileSummaries":[
	  {"inferenceProfileId":"us.anthropic.claude-test-v1","inferenceProfileName":"US Claude Test",
	   "status":"ACTIVE","type":"SYSTEM_DEFINED",
	   "models":[{"modelArn":"stand-in-elsewhere:foundation-model/anthropic.claude-test-v1"}]},
	  {"inferenceProfileId":"us.amazon.image-test-v1","inferenceProfileName":"US Image Test",
	   "status":"ACTIVE","type":"SYSTEM_DEFINED",
	   "models":[{"modelArn":"stand-in:foundation-model/amazon.image-test-v1"}]},
	  {"inferenceProfileId":"eu.anthropic.claude-test-v1","inferenceProfileName":"EU Claude Test",
	   "status":"INACTIVE","type":"SYSTEM_DEFINED",
	   "models":[{"modelArn":"stand-in:foundation-model/anthropic.claude-test-v1"}]}
	]}`
)

// listRunner answers each of the three invocations by its subcommand, recording every argv.
type listRunner struct {
	mu   sync.Mutex
	seen [][]string
	outs map[string]Output
}

func (r *listRunner) run(_ context.Context, argv []string) Output {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, argv)
	return r.outs[argv[2]]
}

func standInRunner() *listRunner {
	return &listRunner{outs: map[string]Output{
		"get-caller-identity":     okOut(standInIdentity),
		"list-foundation-models":  okOut(standInModels),
		"list-inference-profiles": okOut(standInProfiles),
	}}
}

func testLister(r *listRunner) ModelLister {
	return ModelLister{Run: r.run, Binary: "aws", Now: func() time.Time { return time.Unix(1_700_000_000, 0) }}
}

// TestTheFetchJoinsTextModelsWithTheirProfilesAndAWSsMaker is the join: a text model callable on
// demand under its own id, one callable only through a cross-region profile listed under the
// profile's id, an image model and an inactive profile left out, and each maker AWS's
// providerName lowercased, never read off the id.
func TestTheFetchJoinsTextModelsWithTheirProfilesAndAWSsMaker(t *testing.T) {
	r := standInRunner()
	list, err := testLister(r).Fetch(context.Background(), "stand-in-profile", "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []BedrockModel{
		{ID: "us.anthropic.claude-test-v1", Vendor: "anthropic", Name: "US Claude Test"},
		{ID: "mistral.test-v1", Vendor: "mistral ai", Name: "Mistral Test"},
		{ID: "openai.gpt-test-1:0", Vendor: "openai", Name: "gpt test"},
	}
	if !reflect.DeepEqual(list.Models, want) {
		t.Errorf("models = %+v\nwant     %+v", list.Models, want)
	}
	if list.Account != "stand-in-account" || list.Region != "us-east-1" || list.FetchedAtMS != 1_700_000_000_000 {
		t.Errorf("list = %+v, want the caller's account, the region and the fetch time", list)
	}
	if len(r.seen) != 3 {
		t.Fatalf("ran %d invocations, want 3: %v", len(r.seen), r.seen)
	}
	for _, argv := range r.seen {
		joined := strings.Join(argv, " ")
		if !strings.Contains(joined, "--profile stand-in-profile --region us-east-1 --output json") {
			t.Errorf("%q does not run as the configured profile in the region", joined)
		}
	}
}

// TestTheFetchSaysWhatToFixWhenAWSRefuses: a lapsed session names the login, as the mint's
// failure does, and a role that may not list is AWS's own refusal naming the call.
func TestTheFetchSaysWhatToFixWhenAWSRefuses(t *testing.T) {
	r := standInRunner()
	r.outs["list-foundation-models"] = failOut("Error loading SSO Token: Token for x does not exist")
	_, err := testLister(r).Fetch(context.Background(), "stand-in-profile", "us-east-1")
	var me *MintError
	if !errors.As(err, &me) || me.Kind != FailureLoginRequired || !strings.Contains(me.Message, "aws sso login --profile stand-in-profile") {
		t.Errorf("lapsed session: %v, want the login named", err)
	}
	r = standInRunner()
	r.outs["list-inference-profiles"] = failOut("An error occurred (AccessDeniedException) when calling the ListInferenceProfiles operation: not authorized")
	_, err = testLister(r).Fetch(context.Background(), "stand-in-profile", "us-east-1")
	if !errors.As(err, &me) || !strings.Contains(me.Message, "bedrock list-inference-profiles") ||
		!strings.Contains(me.Message, "AccessDeniedException") {
		t.Errorf("refused list: %v, want AWS's words naming the call", err)
	}
}

// TestTheFetchRefusesARegionThatIsNotOne: nothing a caller sends becomes a second CLI flag.
func TestTheFetchRefusesARegionThatIsNotOne(t *testing.T) {
	r := standInRunner()
	for _, region := range []string{"", "--debug", "us-east-1 --endpoint-url x", "US-EAST-1"} {
		if _, err := testLister(r).Fetch(context.Background(), "p", region); err == nil {
			t.Errorf("region %q was fetched", region)
		}
	}
	if len(r.seen) != 0 {
		t.Errorf("ran %v for a region that is not one", r.seen)
	}
}

// TestTheCacheFindsAnAccountsListThroughTheProfile: stored per account and region, found from
// the profile alone, fresh for a day, written 0600, and a second region kept beside the first.
func TestTheCacheFindsAnAccountsListThroughTheProfile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "aws-auth")
	path := filepath.Join(dir, ModelCacheFileName)
	now := time.Unix(1_700_000_000, 0)
	east := ModelList{Account: "a", Region: "us-east-1", FetchedAtMS: now.UnixMilli(),
		Models: []BedrockModel{{ID: "m", Vendor: "anthropic"}}}
	west := east
	west.Region = "us-west-2"
	if err := StoreModelList(path, "p", east, true); err != nil {
		t.Fatal(err)
	}
	if err := StoreModelList(path, "p", west, true); err != nil {
		t.Fatal(err)
	}
	c, err := LoadModelCache(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup("p", "us-east-1")
	if !ok || !reflect.DeepEqual(got, east) {
		t.Errorf("Lookup = %+v, %v; want %+v", got, ok, east)
	}
	if _, ok := c.Lookup("p", "us-west-2"); !ok {
		t.Error("the second region's store dropped the first's, or the reverse")
	}
	if _, ok := c.Lookup("other", "us-east-1"); ok {
		t.Error("a profile that never fetched found a list")
	}
	if !got.Fresh(now.Add(23*time.Hour)) || got.Fresh(now.Add(25*time.Hour)) {
		t.Error("a list is fresh for a day and no longer")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("cache mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
}

// TestTheCacheIsNotRecreatedWhereTheDaemonMayNot: the daemon's NoCreateDir rule holds for the
// cache, so a write landing after its state directory was retired does not bring it back.
func TestTheCacheIsNotRecreatedWhereTheDaemonMayNot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone", ModelCacheFileName)
	err := StoreModelList(path, "p", ModelList{Account: "a", Region: "us-east-1", FetchedAtMS: 1}, false)
	if err == nil {
		t.Fatal("a write with create off made the directory")
	}
	if _, statErr := os.Stat(filepath.Dir(path)); !os.IsNotExist(statErr) {
		t.Errorf("the state directory exists: %v", statErr)
	}
}

// TestTheJoinCarriesAWSsLifecycleAndProfileDates: the facts a start model is chosen by, each AWS's
// own and none parsed from an id. A model AWS marks LEGACY says so on its own entry and on every
// profile it backs, and a cross-region profile carries its creation time, normalized to UTC; an id
// callable on demand has no date. AWS-shaped input: an old Claude callable on demand beside a
// current one callable only through a profile, which the join's (maker, id) order puts second.
func TestTheJoinCarriesAWSsLifecycleAndProfileDates(t *testing.T) {
	const models = `{"modelSummaries":[
	  {"modelArn":"stand-in:foundation-model/anthropic.claude-3-haiku-20240307-v1:0",
	   "modelId":"anthropic.claude-3-haiku-20240307-v1:0","modelName":"Claude 3 Haiku","providerName":"Anthropic",
	   "outputModalities":["TEXT"],"inferenceTypesSupported":["ON_DEMAND","INFERENCE_PROFILE"],
	   "modelLifecycle":{"status":"LEGACY"}},
	  {"modelArn":"stand-in:foundation-model/anthropic.claude-opus-5-5","modelId":"anthropic.claude-opus-5-5",
	   "modelName":"Claude Opus 5.5","providerName":"Anthropic","outputModalities":["TEXT"],
	   "inferenceTypesSupported":["INFERENCE_PROFILE"],"modelLifecycle":{"status":"ACTIVE"}}
	]}`
	const profiles = `{"inferenceProfileSummaries":[
	  {"inferenceProfileId":"us.anthropic.claude-opus-5-5","inferenceProfileName":"US Claude Opus 5.5",
	   "status":"ACTIVE","type":"SYSTEM_DEFINED","createdAt":"2026-05-01T12:00:00.123000+02:00",
	   "models":[{"modelArn":"stand-in:foundation-model/anthropic.claude-opus-5-5"}]},
	  {"inferenceProfileId":"us.anthropic.claude-3-haiku-20240307-v1:0","inferenceProfileName":"US Claude 3 Haiku",
	   "status":"ACTIVE","type":"SYSTEM_DEFINED","createdAt":"2024-08-08T18:28:37.425000+00:00",
	   "models":[{"modelArn":"stand-in:foundation-model/anthropic.claude-3-haiku-20240307-v1:0"}]}
	]}`
	r := standInRunner()
	r.outs["list-foundation-models"] = okOut(models)
	r.outs["list-inference-profiles"] = okOut(profiles)
	list, err := testLister(r).Fetch(context.Background(), "stand-in-profile", "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []BedrockModel{
		{ID: "anthropic.claude-3-haiku-20240307-v1:0", Vendor: "anthropic", Name: "Claude 3 Haiku", Legacy: true},
		{ID: "us.anthropic.claude-3-haiku-20240307-v1:0", Vendor: "anthropic", Name: "US Claude 3 Haiku",
			Legacy: true, Created: "2024-08-08T18:28:37Z"},
		{ID: "us.anthropic.claude-opus-5-5", Vendor: "anthropic", Name: "US Claude Opus 5.5",
			Created: "2026-05-01T10:00:00Z"},
	}
	if !reflect.DeepEqual(list.Models, want) {
		t.Errorf("models = %+v\nwant     %+v", list.Models, want)
	}
}
