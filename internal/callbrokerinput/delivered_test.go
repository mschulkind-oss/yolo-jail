package callbrokerinput

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

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
