package launchservice

// logcap_test.go pins a launch-owned service's log (launch-service-<service>.log, appended by
// every launch that starts the service) to internal/logcap's bound, as the host-service and socat
// logs are: Start's own open trims a log already past the cap, so putting a bare O_APPEND open
// back fails this.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/logcap"
)

func TestStartCapsTheServiceLog(t *testing.T) {
	selfAsHostHalf(t)
	t.Setenv(helperEnv, "serve")
	plan, addr := testPlan(t)
	log := LogPath(plan.Service)
	if err := os.MkdirAll(filepath.Dir(log), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("an old line from an earlier launch\n")
	if err := os.WriteFile(log, bytes.Repeat(old, logcap.MaxBytes/len(old)+2), 0o600); err != nil {
		t.Fatal(err)
	}

	r, err := Start(plan, map[string]string{"ADDR": addr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Stop)

	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > logcap.MaxBytes || bytes.Contains(got, []byte("an old line")) {
		t.Errorf("the log is %d bytes and still holds the earlier launches' lines — Start's open "+
			"no longer bounds it", len(got))
	}
	if !strings.Contains(string(got), "starting "+strings.Join(r.Argv, " ")) {
		t.Errorf("this launch's start line did not reach the log after the trim: %q", got)
	}
	archive, err := os.ReadFile(log + logcap.ArchiveSuffix)
	if err != nil {
		t.Fatalf("no archived generation beside the log: %v", err)
	}
	if !bytes.Contains(archive, []byte("an old line")) {
		t.Error("the archive does not hold the earlier lines")
	}
	if st, err := os.Stat(log); err == nil && st.Mode().Perm() != 0o600 {
		t.Errorf("the log is %v, want 0600: it holds a service's own output", st.Mode().Perm())
	}
}
