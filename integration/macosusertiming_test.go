package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMacosUserTimingRecordsTheBackendsSteps is the macos-user timing surface on the hardware
// (docs/reference/perf-logging.md): a launch with YOLO_TIMING=1 records, in the workspace's host
// perf log, the backend's own steps — the nix build, the bootstrap, the provisioning stage, the
// session — beside the host-side spans before the dispatch, and prints the one quiet line naming
// the file. It is the first breakdown of a real macos-user launch's time; the table's numbers are
// the measurement, so the test asserts only that each step was recorded.
func TestMacosUserTimingRecordsTheBackendsSteps(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": []}`)
	ws := macosUserWorkspace(t, `{"mise_tools": {"jq": "latest"}}`)
	r := runMacosUser(t, ws, "echo TIMED", withEnv("YOLO_TIMING=1"))
	if r.rc != 0 || !strings.Contains(r.stdout, "TIMED") {
		t.Fatalf("the timed launch failed (rc %d):\n%s", r.rc, r.combined())
	}
	logPath := filepath.Join(ws, ".yolo", "host-perf.log")
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("no host perf log at %s: %v\n%s", logPath, err, r.combined())
	}
	body := string(b)
	for _, step := range []string{"macos_user.materialize", "macos_user.bootstrap", "macos_user.provision",
		"macos_user.agent", "launch.macos_user"} {
		if !strings.Contains(body, step) {
			t.Errorf("the host perf log records no %s:\n%s", step, body)
		}
	}
	if !strings.Contains(r.stderr, "timings recorded in") {
		t.Errorf("a YOLO_TIMING launch did not print the quiet line naming the file:\n%s", r.stderr)
	}
	t.Logf("the host perf log of one macos-user launch:\n%s", body)
}
