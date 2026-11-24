package callbrokerinput

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestPublishDeliveredRefusesStartedLeaseWithoutAnyPublication(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := brokerscope.File{Source: "tasks", LaunchID: "1234567890abcdef", Workspace: t.TempDir(), PID: os.Getpid(), Container: "private", Form: brokerscope.FormCallRoot, Repos: []string{"alpha"}}
	f.CallInput = filepath.Join(t.TempDir(), "input.json")
	path, err := brokerscope.Write(f)
	if err != nil {
		t.Fatal(err)
	}
	f, err = brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bound, lease, err := brokercore.BindLease(f.Source, path, f, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	defer brokercore.CloseLease(f.Source, lease)
	input := Launch{Version: "yolo.call-broker-launch/2", Source: f.Source, ScopeFile: path, DeclarationHash: strings.Repeat("b", 64)}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	inputPath, grantPath := f.CallInput, filepath.Join(t.TempDir(), "grant.json")
	if err := os.WriteFile(inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grantPath, []byte(`{"read":"granted"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	asks := brokerscope.DeliveredAsks{Requests: json.RawMessage(`{}`), Scopes: json.RawMessage(`{}`), PackRead: json.RawMessage(`null`), Model: "legacy/1"}
	if err := brokerlock.WithTransaction(filepath.Dir(filepath.Dir(path)), func() error {
		_, err := brokerscope.PublishDeliveredLocked(bound, brokerscope.DeliveredBaseline{
			Binding: brokerscope.PolicyBinding{Source: bound.Source, Workspace: bound.Workspace, LaunchID: bound.LaunchID, DeclarationHash: input.DeclarationHash, InputDigest: InputDigest(data)},
			Start:   bound.BrokerStart, Asks: asks,
		})
		return err
	}); err == nil {
		t.Error("low-level publisher accepted complete asks for an already-started matching lease")
	}
	if err := PublishDelivered(path, data, asks); err == nil {
		t.Error("started lease accepted retrospective delivered ask provenance")
	}
	if _, err := os.Lstat(brokerscope.DeliveredBaselinePath(bound)); !os.IsNotExist(err) {
		t.Error("retrospective publication left delivered baseline")
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(before, after) {
		t.Fatal("refused publisher changed live source/locator")
	}
	if after, err := os.ReadFile(inputPath); err != nil || !bytes.Equal(data, after) {
		t.Fatal("refused publisher changed original input")
	}
	if after, err := os.ReadFile(grantPath); err != nil || string(after) != `{"read":"granted"}` {
		t.Fatal("refused publisher changed ancillary grant")
	}
}

func TestCaptureDeliveredPreservesCompleteRawPresenceAndSourceScopedAsks(t *testing.T) {
	var requests, scopes []byte
	for _, raw := range []string{
		`{}`,
		`{"requests":{"tasks":null}}`,
		`{"requests":{"tasks":{"all":{"update":false}}}}`,
		`{"requests":{"tasks":{"all":{"update":true}}}}`,
		`{"requests":{"tasks":{"all":{"update":"ask"}}}}`,
	} {
		value, err := jsonx.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		asks, err := CaptureDelivered("tasks", "fixture", "legacy/1", value.(*jsonx.OrderedMap))
		if err != nil {
			t.Fatal(err)
		}
		if requests != nil && bytes.Equal(requests, asks.Requests) {
			t.Fatalf("raw source presence/value discarded: %s", raw)
		}
		requests = asks.Requests
	}
	for _, raw := range []string{
		`{"scopes":{"other":{"source":"not-tasks","requests":{"read":true}}}}`,
		`{"scopes":{"mine":{"source":"tasks","resource":"a","requests":{"read":false}}}}`,
		`{"scopes":{"mine":{"source":"tasks","resource":"a","requests":{"read":true}}}}`,
	} {
		value, err := jsonx.Decode([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		asks, err := CaptureDelivered("tasks", "fixture", "scoped/1", value.(*jsonx.OrderedMap))
		if err != nil {
			t.Fatal(err)
		}
		if scopes != nil && bytes.Equal(scopes, asks.Scopes) {
			t.Fatalf("false/true scoped ask discarded: %s", raw)
		}
		scopes = asks.Scopes
	}
}
