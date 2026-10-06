package run

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
)

// bedrockmodelsfetch_test.go pins DefaultFetchModelList's order (bedrockmodels.go): the running
// service's answer first, else the same fetch run by the launch. A stand-in `aws` answers every
// invocation; nothing reaches AWS.

// standInAws is a stand-in `aws` for the list fetch: each subcommand's canned answer.
func standInAws(calls *atomic.Int32) awsauth.Runner {
	return func(_ context.Context, argv []string) awsauth.Output {
		calls.Add(1)
		switch argv[2] {
		case "get-caller-identity":
			return awsauth.Output{Stdout: `{"Account":"stand-in-account"}`, Spawned: true}
		case "list-foundation-models":
			return awsauth.Output{Stdout: `{"modelSummaries":[{"modelId":"openai.gpt-test-1:0","modelName":"gpt test",` +
				`"providerName":"OpenAI","outputModalities":["TEXT"],"inferenceTypesSupported":["ON_DEMAND"]}]}`, Spawned: true}
		}
		return awsauth.Output{Stdout: `{"inferenceProfileSummaries":[]}`, Spawned: true}
	}
}

// TestTheLaunchAsksTheRunningServiceFirst: with the service up, its answer on the host socket is
// the launch's, and the launch runs no `aws` of its own.
func TestTheLaunchAsksTheRunningServiceFirst(t *testing.T) {
	saved := hostservice.Logger
	hostservice.Logger = log.New(io.Discard, "", 0)
	t.Cleanup(func() { hostservice.Logger = saved })
	dir, err := os.MkdirTemp("/tmp", "yj-ml-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	var daemonCalls, launchCalls atomic.Int32
	socket := filepath.Join(dir, "d.sock.host")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeUnix(awsauthdaemon.BuildHandler(awsauthdaemon.HandlerConfig{
			ModelLists: awsauthdaemon.ModelListSource{CachePath: filepath.Join(dir, "daemon", awsauth.ModelCacheFileName),
				Profile: "stand-in-profile", Lister: awsauth.ModelLister{Run: standInAws(&daemonCalls)},
				Tracker: awsauthdaemon.NewModelListTracker()}}), socket, stop)
	}()
	t.Cleanup(func() { close(stop); <-done })
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	ans := fetchModelListAt(socket, filepath.Join(dir, "launch", awsauth.ModelCacheFileName), standInAws(&launchCalls),
		ModelListRequest{Service: "aws-auth", Enabled: true, Profile: "stand-in-profile", Region: "us-east-1"})
	if ans.Source != "fetched" || !ans.Has() || daemonCalls.Load() == 0 || launchCalls.Load() != 0 {
		t.Errorf("answer %+v; the service ran %d and the launch %d invocations, want the service's alone",
			ans, daemonCalls.Load(), launchCalls.Load())
	}
}

// TestWithNoServiceTheLaunchFetchesTheListItself: the first Bedrock launch on a machine composes
// before it starts the service, so it runs the service's own fetch, as the configured profile,
// writing the same cache, and says so. A disabled service fetches nothing and says why.
func TestWithNoServiceTheLaunchFetchesTheListItself(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	cache := filepath.Join(dir, "aws-auth", awsauth.ModelCacheFileName)
	ans := fetchModelListAt(filepath.Join(dir, "absent.sock"), cache, standInAws(&calls),
		ModelListRequest{Service: "aws-auth", Enabled: true, Profile: "stand-in-profile", Region: "us-east-1"})
	if !ans.Has() || calls.Load() != 3 || !strings.Contains(ans.Source, "fetched by this launch") {
		t.Fatalf("answer %+v after %d invocations, want the launch's own fetch", ans, calls.Load())
	}
	c, err := awsauth.LoadModelCache(cache)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup("stand-in-profile", "us-east-1"); !ok {
		t.Error("the launch's fetch was not cached for the next launch")
	}
	if got := DefaultFetchModelList(ModelListRequest{Service: "aws-auth", Region: "us-east-1"}); got.Has() ||
		!strings.Contains(got.Note, "not enabled") {
		t.Errorf("a disabled service: %+v, want why", got)
	}
}
