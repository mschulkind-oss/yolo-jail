package mcpauthdaemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
)

func TestHeaderDoesNotRefreshAnUnexpiredToken(t *testing.T) {
	f := newOnceDaemonFixture(t)
	once, err := f.b.newOnceBroker()
	if err != nil {
		t.Fatal(err)
	}
	if err := f.b.passAll(context.Background(), once, f.b.onceSources()); err != nil {
		t.Fatal(err)
	}
	<-f.refreshs
	store, err := mcpauth.NewOnceStore(filepath.Join(f.b.canonical.Dir, "once"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WithLock(func(file *mcpauth.CanonicalFile) error {
		file.Records[0].ExpiresAtMS = time.Now().Add(time.Minute).UnixMilli()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	reply := askBroker(t, f.b, map[string]any{"op": "header", "server": "slack"})
	if reply.rc != 0 || reply.stdout != "Bearer at-2\n" {
		t.Fatalf("header: %+v", reply)
	}
	select {
	case token := <-f.refreshs:
		t.Fatalf("unexpired issuance redeemed %q", token)
	default:
	}
}
