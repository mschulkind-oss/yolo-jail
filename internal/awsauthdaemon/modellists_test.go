package awsauthdaemon

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// modellists_test.go drives the `bedrock-models` action over the daemon's real transports with a
// stand-in for every `aws` invocation: no test runs `aws` or reaches AWS.

const (
	standInIdentity = `{"Account":"stand-in-account"}`
	standInModels   = `{"modelSummaries":[{"modelArn":"stand-in:foundation-model/anthropic.claude-test-v1",` +
		`"modelId":"anthropic.claude-test-v1","modelName":"Claude Test","providerName":"Anthropic",` +
		`"outputModalities":["TEXT"],"inferenceTypesSupported":["INFERENCE_PROFILE"]},` +
		`{"modelId":"openai.gpt-test-1:0","modelName":"gpt test","providerName":"OpenAI",` +
		`"outputModalities":["TEXT"],"inferenceTypesSupported":["ON_DEMAND"]}]}`
	standInProfiles = `{"inferenceProfileSummaries":[{"inferenceProfileId":"us.anthropic.claude-test-v1",` +
		`"inferenceProfileName":"US Claude Test","status":"ACTIVE","type":"SYSTEM_DEFINED",` +
		`"models":[{"modelArn":"stand-in:foundation-model/anthropic.claude-test-v1"}]}]}`
)

// listRunner is a stand-in `aws`: each subcommand's canned answer, a count of the fetches, and an
// optional gate a fetch waits on.
func listRunner(fetches *atomic.Int32, fail string, gate <-chan struct{}) awsauth.Runner {
	return func(ctx context.Context, argv []string) awsauth.Output {
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
			}
		}
		switch argv[2] {
		case "get-caller-identity":
			fetches.Add(1)
			return awsauth.Output{Stdout: standInIdentity, Spawned: true}
		case "list-foundation-models":
			if fail != "" {
				return awsauth.Output{Stderr: fail, Code: 254, Spawned: true}
			}
			return awsauth.Output{Stdout: standInModels, Spawned: true}
		case "list-inference-profiles":
			return awsauth.Output{Stdout: standInProfiles, Spawned: true}
		}
		return awsauth.Output{Stderr: "unexpected argv " + strings.Join(argv, " "), Code: 2, Spawned: true}
	}
}

func testListSource(t *testing.T, run awsauth.Runner) ModelListSource {
	t.Helper()
	return ModelListSource{
		CachePath: filepath.Join(t.TempDir(), "aws-auth", awsauth.ModelCacheFileName),
		Profile:   "stand-in-profile",
		Lister:    awsauth.ModelLister{Run: run, Binary: "aws"},
		Tracker:   NewModelListTracker(),
	}
}

func decodeAnswer(t *testing.T, r reply) ModelListAnswer {
	t.Helper()
	if r.rc != 0 {
		t.Fatalf("rc %d, stderr %q", r.rc, r.stderr)
	}
	var a ModelListAnswer
	if err := json.Unmarshal([]byte(r.stdout), &a); err != nil {
		t.Fatalf("answer is not JSON (%q): %v", r.stdout, err)
	}
	return a
}

// TestTheHostSocketAnswersTheRegionsListAndCachesIt: the first ask fetches and caches, the next is
// answered from the cache with no `aws` invocation at all, as the ruling's once a day asks. It
// fails with the action's case removed from BuildHandler.
func TestTheHostSocketAnswersTheRegionsListAndCachesIt(t *testing.T) {
	var fetches atomic.Int32
	src := testListSource(t, listRunner(&fetches, "", nil))
	ask := serveHandler(t, HandlerConfig{ModelLists: src})
	req := map[string]any{"action": ModelListAction, ModelListRegionKey: "us-east-1", "budget_ms": 2000}
	first := decodeAnswer(t, ask(req))
	if first.Source != "fetched" || len(first.List.Models) != 2 || first.List.Models[0].Vendor != "anthropic" ||
		first.List.Models[0].ID != "us.anthropic.claude-test-v1" {
		t.Fatalf("first answer = %+v, want the fetched list, Claude under its profile id", first)
	}
	second := decodeAnswer(t, ask(req))
	if second.Source != "cache" || fetches.Load() != 1 {
		t.Errorf("second answer = %+v after %d fetches, want the cache and one fetch", second, fetches.Load())
	}
	info, err := os.Stat(src.CachePath)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the cache is %v (%v), want a 0600 file", info, err)
	}
}

// TestAFailedRefreshServesTheCachesLastGoodList: a list older than a day is refreshed, and when the
// refresh fails the last good list is answered with why; with no list at all, the why alone.
func TestAFailedRefreshServesTheCachesLastGoodList(t *testing.T) {
	var fetches atomic.Int32
	src := testListSource(t, listRunner(&fetches, "Error loading SSO Token: expired", nil))
	stale := awsauth.ModelList{Account: "stand-in-account", Region: "us-east-1",
		FetchedAtMS: time.Now().Add(-48 * time.Hour).UnixMilli(),
		Models:      []awsauth.BedrockModel{{ID: "old.model-v1", Vendor: "anthropic"}}}
	if err := awsauth.StoreModelList(src.CachePath, src.Profile, stale, true); err != nil {
		t.Fatal(err)
	}
	a := ObtainModelList(src, "us-east-1", time.Second)
	if a.Source != "stale cache" || !a.Has() || a.List.Models[0].ID != "old.model-v1" ||
		!strings.Contains(a.Note, "aws sso login --profile stand-in-profile") {
		t.Errorf("answer = %+v, want the stale list and the login named", a)
	}
	none := ObtainModelList(src, "eu-west-1", time.Second)
	if none.Has() || !strings.Contains(none.Note, "aws sso login") {
		t.Errorf("no cache, failed fetch: %+v, want no list and why", none)
	}
}

// TestASlowFetchOutlivesTheLaunchsBudget: past the budget the launch is answered at once, and the
// fetch it started still lands in the cache for the next launch.
func TestASlowFetchOutlivesTheLaunchsBudget(t *testing.T) {
	var fetches atomic.Int32
	gate := make(chan struct{})
	src := testListSource(t, listRunner(&fetches, "", gate))
	a := ObtainModelList(src, "us-east-1", 20*time.Millisecond)
	if a.Has() || !strings.Contains(a.Note, "had not finished") {
		t.Fatalf("answer = %+v, want none yet and why", a)
	}
	close(gate)
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, _ := awsauth.LoadModelCache(src.CachePath)
		if _, ok := c.Lookup(src.Profile, "us-east-1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fetch the launch stopped waiting for never reached the cache")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAJailCannotAskForTheList: through a front, the action is refused and nothing is fetched,
// because the list is fetched with the credential the narrowing keeps out of jails.
func TestAJailCannotAskForTheList(t *testing.T) {
	var fetches atomic.Int32
	src := testListSource(t, listRunner(&fetches, "", nil))
	saved := hostservice.Logger
	hostservice.Logger = log.New(io.Discard, "", 0)
	socket := filepath.Join(shortTempDir(t), "f.sock")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeFrontedUnix(BuildHandler(HandlerConfig{ModelLists: src}), socket, stop)
	}()
	t.Cleanup(func() { close(stop); <-done; hostservice.Logger = saved })
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the fronted socket never bound")
		}
		time.Sleep(2 * time.Millisecond)
	}
	conn, err := dialUnix(socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	pre, _ := json.Marshal(map[string]any{"jail_id": "a-jail", "service": "aws-auth", "v": svcendpoint.PreambleVersion})
	frame := make([]byte, 4+len(pre))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(pre)))
	copy(frame[4:], pre)
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"action": ModelListAction, ModelListRegionKey: "us-east-1"})
	if err := frameproto.WriteRequest(conn, body); err != nil {
		t.Fatal(err)
	}
	rc, stderr := -1, ""
	for {
		f, err := frameproto.ReadFrame(conn)
		if err != nil {
			break
		}
		if f.StreamID == frameproto.StreamStderr {
			stderr += string(f.Payload)
		}
		if f.StreamID == frameproto.StreamExit {
			rc, _ = frameproto.ExitCode(f.Payload)
			break
		}
	}
	if rc == 0 || !strings.Contains(stderr, "host socket only") || fetches.Load() != 0 {
		t.Errorf("a fronted ask: rc %d, stderr %q, %d fetches; want a refusal and no fetch", rc, stderr, fetches.Load())
	}
}
