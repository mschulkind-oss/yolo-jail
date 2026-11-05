package mcpauth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestJSONAdapterKeepsUnsharedValuesAndUpdatesEntriesAtPath(t *testing.T) {
	adapter := Adapter{EntriesAt: []string{"mcpOAuth"}, EmptyStore: "delete"}
	input := []byte(`{"unrelated":{"z":1,"a":2},"mcpOAuth":{"shared":{"access_token":"old"},"other": [ 1, 2 ]}}`)
	store, parsed, err := adapter.Read(input)
	if err != nil || !parsed {
		t.Fatalf("Read parsed=%v err=%v", parsed, err)
	}
	store.Entries["shared"] = json.RawMessage(`{"access_token":"new"}`)
	encoded, err := adapter.Encode(store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"other":[ 1, 2 ]`) || !strings.Contains(string(encoded), `"unrelated":{"z":1,"a":2}`) {
		t.Fatalf("adapter lost non-entry JSON: %s", encoded)
	}
	decoded, parsed, err := adapter.Read(encoded)
	if err != nil || !parsed || string(decoded.Entries["shared"]) != `{"access_token":"new"}` {
		t.Fatalf("round trip = %#v parsed=%v err=%v", decoded, parsed, err)
	}
}

func TestJSONAdapterRejectsBadExpiryAndUnsupportedLocks(t *testing.T) {
	adapter := Adapter{Fields: packdecl.MCPAuthFields{AccessToken: "access_token", ExpiresAt: packdecl.MCPAuthExpiryField{Path: "expires_at", Unit: "s"}}}
	if _, _, _, _, err := adapter.ExtractFields(json.RawMessage(`{"access_token":"a","expires_at":9223372036854776}`)); err == nil {
		t.Fatal("overflowing seconds expiry accepted")
	}
	_, err := AdapterFor(&packdecl.MCPAuth{Store: ".x", Format: "json", EmptyStore: "delete", Lock: &packdecl.MCPAuthLock{Protocol: "proper-lockfile", Path: ".x.lock", StaleSeconds: 15}})
	if err == nil || !strings.Contains(err.Error(), `lock protocol "proper-lockfile" arrives with slice 2`) {
		t.Fatalf("unsupported protocol error = %v", err)
	}
}

func TestAdapterForRejectsNonJSONBeforeFileIO(t *testing.T) {
	_, err := AdapterFor(&packdecl.MCPAuth{Format: "dir", Lock: &packdecl.MCPAuthLock{Protocol: "flock", Path: "locks/file.lock"}})
	if err == nil || !strings.Contains(err.Error(), "format") {
		t.Fatalf("non-JSON adapter constructed: %v", err)
	}
}
