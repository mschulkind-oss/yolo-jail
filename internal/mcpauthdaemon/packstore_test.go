package mcpauthdaemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

func TestMCPAuthBrokerCanonicalRecordsArePerSelectedPack(t *testing.T) {
	store, err := mcpauth.NewCanonicalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b := broker{canonical: store}
	first := b.canonicalFor(mcpauth.Replica{Pack: "first"})
	second := b.canonicalFor(mcpauth.Replica{Pack: "second"})
	record := mcpauth.Record{Server: "linear", Identity: strings.Repeat("a", 64), URL: "https://mcp.example/linear", Generation: 1, State: "live", Entry: json.RawMessage(`{"access_token":"fake-only-first"}`)}
	if err := first.WithLock(func(f *mcpauth.CanonicalFile) error { f.Records = []mcpauth.Record{record}; return nil }); err != nil {
		t.Fatal(err)
	}
	copy, err := second.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(copy.Records) != 0 {
		t.Fatal("an agent pack saw another pack's canonical sign-in")
	}
	saved, err := first.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Records) != 1 {
		t.Fatal("first pack's canonical record was not retained")
	}
}
