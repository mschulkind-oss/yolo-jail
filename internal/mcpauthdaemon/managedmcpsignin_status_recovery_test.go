package mcpauthdaemon

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func TestHeaderRefusalUsesDeliveredNativeRecovery(t *testing.T) {
	f := newOnceDaemonFixture(t)
	f.b.share.Sources = nil
	f.b.share.SignIn = &mcpsignin.Launch{Version: 1, Decisions: []mcpsignin.Decision{{Server: "slack", Agent: "fixture-agent", Capturable: true, Point: &mcpsignin.Point{NativeName: "plugin:renamed-original:real-key"}}}}
	reply := askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"})
	if reply.rc != 1 || reply.stdout != "" || !strings.Contains(reply.stderr, "authenticate plugin:renamed-original:real-key") || !strings.Contains(reply.stderr, "paste the full callback URL") || strings.Contains(reply.stderr, "authenticate slack") {
		t.Fatalf("header invented a native name: %+v", reply)
	}
}

func TestStatusDeadHeaderSourceRequiresRestartBeforeNativeRecovery(t *testing.T) {
	f := newOnceDaemonFixture(t)
	store := adoptHeaderFixture(t, f)
	if err := store.WithLock(func(file *mcpauth.CanonicalFile) error { file.Records[0].State = "dead"; return nil }); err != nil {
		t.Fatal(err)
	}
	f.b.share.Sources = nil
	f.b.share.SignIn = &mcpsignin.Launch{Version: 1, Decisions: []mcpsignin.Decision{{Server: "slack", Agent: "fixture-agent", Capturable: true, HeaderForm: "json", Point: &mcpsignin.Point{NativeName: "plugin:renamed-original:real-key"}}}}
	reply := askBroker(t, f.b, map[string]any{"op": "status"})
	if reply.rc != 0 || !strings.Contains(reply.stdout, "restart the agent") || !strings.Contains(reply.stdout, "plugin:renamed-original:real-key") || !strings.Contains(reply.stdout, `"diagnosis": "record dead"`) {
		t.Fatalf("dead helper did not restore native entry: %+v", reply)
	}
	token, err := f.b.header(context.Background(), "slack")
	if token != "" || err == nil || !strings.Contains(err.Error(), "restart the agent") {
		t.Fatal("header/status recovery diverged")
	}
}
