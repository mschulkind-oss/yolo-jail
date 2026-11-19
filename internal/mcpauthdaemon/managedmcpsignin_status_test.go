package mcpauthdaemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStatusHandlerCreatesNoOnceState(t *testing.T) {
	f := newOnceDaemonFixture(t)
	target := filepath.Join(f.b.canonical.Dir, "once")
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("fixture already has state: %v", err)
	}
	reply := askBroker(t, f.b, map[string]any{"op": "status"})
	if reply.rc != 0 {
		t.Fatalf("readable missing state: %+v", reply)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("status created state: %v", err)
	}
}

func TestStatusHandlerIncludesHeaderOnlyServers(t *testing.T) {
	f := newOnceDaemonFixture(t)
	f.b.share.Sources = nil
	reply := askBroker(t, f.b, map[string]any{"op": "status"})
	if reply.rc != 0 {
		t.Fatalf("status: %+v", reply)
	}
	var body struct {
		Once []map[string]any `json:"once"`
	}
	if err := json.Unmarshal([]byte(reply.stdout), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Once) != 1 || body.Once[0]["name"] != "slack" {
		t.Fatalf("header-only invisible: %s", reply.stdout)
	}
}
