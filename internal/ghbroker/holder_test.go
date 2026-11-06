package ghbroker

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestAnotherLaunchsEndpointFilesAndSpendsNothing: the holder comes from this broker's own
// launch, gated by the crossing identity, so a connection through an endpoint another launch
// published can neither file a request for this launch, read its status, nor spend its grant.
func TestAnotherLaunchsEndpointFilesAndSpendsNothing(t *testing.T) {
	for form, foreignDir := range productionEndpointDirs("another-container") {
		t.Run(form, func(t *testing.T) {
			b, n := windowFixture(t)
			own, _ := frontedEndpoint(t, b, filepath.Base(paths.HostServicesDir(authorityContainer, false)))
			foreign, _ := frontedEndpoint(t, b, foreignDir)
			if got := Forward(writeReq().Argv, foreign); got == 0 || got == ExitTempFail {
				t.Fatalf("a write through another launch's endpoint: exit %d, want a refusal", got)
			}
			if rs, _, _ := b.store.List(); len(rs) != 0 {
				t.Fatalf("another launch's connection filed %+v", rs)
			}
			if got := Forward(writeReq().Argv, own); got != ExitTempFail {
				t.Fatalf("own write: exit %d", got)
			}
			rs, _, _ := b.store.List()
			if len(rs) != 1 {
				t.Fatal(rs)
			}
			if got := Forward([]string{"status", rs[0].ID}, foreign); got == 0 || got == ExitTempFail {
				t.Fatalf("another launch read this launch's request status: exit %d", got)
			}
			if _, err := b.store.Decide(rs[0].ID, "pending", brokerstore.Decision{}); err != nil {
				t.Fatal(err)
			}
			if got := Forward(writeReq().Argv, foreign); got == 0 || n.Load() != 0 {
				t.Fatalf("another launch spent this launch's grant: exit %d, ran %d", got, n.Load())
			}
			if got := Forward(writeReq().Argv, own); got != 0 || n.Load() != 1 {
				t.Fatalf("own approved write: exit %d, ran %d", got, n.Load())
			}
		})
	}
}

func TestLaunchHolderMapsOnlyThisLaunchsEndpointDirs(t *testing.T) {
	const c = authorityContainer
	hash := paths.JailShortHash(c)
	other := paths.JailShortHash("another-container")
	for crossing, want := range map[string]string{
		filepath.Base(paths.HostServicesDir(c, false)): hash,
		paths.HostServicesSessionPrefix(c) + "q7Zx01":  hash,
		// Not this launch's, or not an endpoint dir at all: unchanged, so never this holder.
		paths.HostServicesDirName(other):                  paths.HostServicesDirName(other),
		paths.HostServicesSessionPrefix("another") + "a1": paths.HostServicesSessionPrefix("another") + "a1",
		paths.HostServicesSessionPrefix(c):                paths.HostServicesSessionPrefix(c),
		paths.HostServicesDirName(hash) + "x":             paths.HostServicesDirName(hash) + "x",
		"unknown":                                         "unknown",
	} {
		if got := launchHolder(c, crossing); got != want {
			t.Errorf("launchHolder(%q) = %q, want %q", crossing, got, want)
		}
	}
	if got := launchHolder("", paths.HostServicesDirName(hash)); got != paths.HostServicesDirName(hash) {
		t.Errorf("a launch with no container mapped its crossing to %q", got)
	}
}

func TestEvaluateThroughFrontedEndpointRejectsForeignHolderBeforeFacts(t *testing.T) {
	b, _ := windowFixture(t)
	foreignName := filepath.Base(paths.HostServicesDir("another-container", false))
	foreign, _ := frontedEndpoint(t, b, foreignName)
	entry, _, err := b.store.AddWorking(brokerstore.WorkingEntry{Workspace: b.workspace,
		Repo: "github.com/o/r", Kind: brokerstore.WorkingPR, Resource: "#1", By: GrantCreatePR})
	if err != nil {
		t.Fatal(err)
	}
	factReads := 0
	b.execute = func(_ context.Context, argv []string, outData []byte, out, _ func([]byte)) RunResult {
		factReads++
		if len(argv) == 2 && argv[0] == "api" && argv[1] == "repos/o/r/issues/1" {
			out([]byte(`{"number":1,"pull_request":{"merged_at":null}}`))
		}
		return RunResult{Exit: 0}
	}
	body := `{"op":"evaluate","version":1,"argv":["pr","comment","1","--body","hello"],"repo":"o/r"}`
	foreignOut, foreignCode := evaluationThroughEndpoint(t, foreign, body)
	if foreignCode != 0 || !strings.Contains(string(foreignOut), `"verdict":"refused"`) || strings.Contains(string(foreignOut), entry.ID) || factReads != 0 {
		t.Fatalf("foreign endpoint leaked/inspected standing: exit=%d output=%s fact reads=%d", foreignCode, foreignOut, factReads)
	}
}

func TestEvaluateThroughFrontedEndpointUsesNormalizedHolderQuota(t *testing.T) {
	b, _ := windowFixture(t)
	ownName := filepath.Base(paths.HostServicesDir(authorityContainer, false))
	own, _ := frontedEndpoint(t, b, ownName)
	for i := 0; i < 3; i++ {
		qArgv := []string{"pr", "comment", "20", "--body", "other"}
		qDecision, qPolicy, err := b.authority.Classify(qArgv, "o/r")
		if err != nil {
			t.Fatal(err)
		}
		q := brokerstore.Request{Jail: paths.JailShortHash(authorityContainer), Start: b.startID, Workspace: b.workspace,
			LaunchID: b.authority.scope.LaunchID, Argv: qDecision.Argv, Repo: "o/r", Set: qDecision.Set, Policy: qPolicy,
			StdinDigest: "different" + string(rune('a'+i))}
		q.Digest = brokerstore.CommandDigest(q)
		if _, err := b.store.File(q); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"op":"evaluate","version":1,"argv":["pr","comment","2","--body","hello"],"repo":"o/r"}`
	out, code := evaluationThroughEndpoint(t, own, body)
	if code != 0 || !strings.Contains(string(out), `"verdict":"refused"`) || !strings.Contains(string(out), "pending request limit") {
		t.Fatalf("own normalized holder missed its pending quota: exit=%d output=%s", code, out)
	}
}

func TestEvaluateFrameRejectsMixedStatusOperation(t *testing.T) {
	b, _ := windowFixture(t)
	own, _ := frontedEndpoint(t, b, filepath.Base(paths.HostServicesDir(authorityContainer, false)))
	out, code := evaluationThroughEndpoint(t, own, `{"op":"evaluate","version":1,"operation":"status","request":"r-invalid"}`)
	if code != ExitUsage || len(out) != 0 {
		t.Fatalf("evaluate+status entered the legacy status path: exit=%d output=%s", code, out)
	}
}

func evaluationThroughEndpoint(t *testing.T, env ForwardEnv, body string) ([]byte, int) {
	t.Helper()
	endpoint := env.Getenv(EndpointEnv)
	conn, err := svcendpoint.Dial(endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := frameproto.WriteRequest(conn, []byte(body)); err != nil {
		t.Fatal(err)
	}
	var output []byte
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			output = append(output, frame.Payload...)
		case frameproto.StreamExit:
			return output, int(binary.BigEndian.Uint32(frame.Payload))
		}
	}
}

// TestStandingBranchAcceptsTheProductionHolder: window's standing branch compares the same
// holder, so a production crossing identity passes it and another launch's does not.
func TestStandingBranchAcceptsTheProductionHolder(t *testing.T) {
	read := Request{Argv: []string{"pr", "view", "1"}, Repo: "o/r"}
	for _, tc := range []struct {
		crossing string
		ok       bool
	}{
		{filepath.Base(paths.HostServicesDir(authorityContainer, false)), true},
		{paths.HostServicesSessionPrefix(authorityContainer) + "q7Zx01", true},
		{paths.HostServicesDirName(paths.JailShortHash("another-container")), false},
	} {
		b, n := windowFixture(t)
		d, _, err := b.authority.Classify(read.Argv, read.Repo)
		if err != nil || d.Outcome != OutcomeStanding {
			t.Fatalf("fixture read is not standing: %+v %v", d, err)
		}
		var ev brokeraudit.Event
		code, msg := b.window(context.Background(), read, b.holder(tc.crossing), func([]byte) {}, func([]byte) {}, &ev)
		if ran := n.Load() == 1; ran != tc.ok || (code == 0) != tc.ok {
			t.Fatalf("%s: exit %d %q, ran %v, want ran %v", tc.crossing, code, msg, ran, tc.ok)
		}
		if !tc.ok && !strings.Contains(msg, "standing holder mismatch") {
			t.Fatalf("%s: %q", tc.crossing, msg)
		}
	}
}
