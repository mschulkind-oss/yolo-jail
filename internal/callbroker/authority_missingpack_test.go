package callbroker

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// The call-broker daemon turns the in-line fetch of a missing selected pack on, so the
// approval-authority reads it makes (authority.go) see a pack its first read skipped.
func TestTheCallBrokerDaemonEnablesTheMissingPackFetch(t *testing.T) {
	t.Cleanup(func() { config.EnableMissingPackFetch(nil) })
	if config.MissingPackFetchEnabled() {
		t.Fatal("fetching is enabled before the daemon ran")
	}
	// An unreadable launch input ends Main early, after the switch.
	if code := Main([]string{"--socket", "s", "--launch-input", filepath.Join(t.TempDir(), "absent")}); code == 0 {
		t.Fatalf("Main with no readable input returned %d", code)
	}
	if !config.MissingPackFetchEnabled() {
		t.Fatal("the daemon did not enable the fetch")
	}
}
