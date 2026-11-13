package run

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/awsauth"
	"github.com/mschulkind-oss/yolo-jail/internal/awsauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// bedrockmodelsfetch_test.go pins fetchModelListAt's order (bedrockmodels.go): the running
// service's answer first, else the same fetch run by the launch; and that DefaultFetchModelList
// asks no service at all (HD-D22). A stand-in `aws` answers every
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
	dir, err := os.MkdirTemp(testtmp.ShortDir(), "yj-ml-")
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

// TestTheLaunchAsksNoOlderYolosMachineWideCopyForAList pins DefaultFetchModelList's call site
// (HD-D22): since HD-R1 the one aws-auth daemon a launch could ask before it starts its own is a
// machine-wide copy an older yolo left running, which fetches as ITS configured profile. One is
// served at that copy's private host socket here, and the launch must run its own fetch as the
// configured profile instead, through a stand-in `aws` on PATH, and never ask the older copy.
func TestTheLaunchAsksNoOlderYolosMachineWideCopyForAList(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("AF_UNIX and a shell stand-in")
	}
	saved := hostservice.Logger
	hostservice.Logger = log.New(io.Discard, "", 0)
	t.Cleanup(func() { hostservice.Logger = saved })
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	argvLog := filepath.Join(bin, "argv.log")
	script := "#!/bin/sh\necho \"$*\" >> '" + argvLog + "'\n" +
		"echo 'An error occurred (AccessDeniedException): stand-in aws refuses every call' >&2\nexit 254\n"
	if err := os.WriteFile(filepath.Join(bin, "aws"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var olderCalls atomic.Int32
	socket := awsauthdaemon.HostSocketPath(paths.HostSingletonSocket(awsauthdaemon.LoopholeName))
	assertSockPathFits(t, socket)
	_ = os.Remove(socket)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = hostservice.ServeUnix(awsauthdaemon.BuildHandler(awsauthdaemon.HandlerConfig{
			ModelLists: awsauthdaemon.ModelListSource{CachePath: filepath.Join(t.TempDir(), awsauth.ModelCacheFileName),
				Profile: "the-older-copys-profile", Lister: awsauth.ModelLister{Run: standInAws(&olderCalls)},
				Tracker: awsauthdaemon.NewModelListTracker()}}), socket, stop)
	}()
	t.Cleanup(func() { close(stop); <-done; _ = os.Remove(socket) })
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	ans := DefaultFetchModelList(ModelListRequest{Service: awsauthdaemon.LoopholeName, Enabled: true,
		Profile: "stand-in-profile", Region: "us-east-1"})
	if olderCalls.Load() != 0 {
		t.Errorf("the launch asked the older yolo's machine-wide copy for its list (%d invocations "+
			"as that copy's profile): %+v", olderCalls.Load(), ans)
	}
	logged, _ := os.ReadFile(argvLog)
	if !strings.Contains(string(logged), "stand-in-profile") {
		t.Errorf("the launch did not run its own fetch as the configured profile; the stand-in "+
			"`aws` saw:\n%s", logged)
	}
}
