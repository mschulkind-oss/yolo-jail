package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The record file is machine-wide, so it holds other launches' records. A launch prints a
// sign-in line only for servers its own share names, never one per retained foreign record.
func TestManagedMCPLaunchDoesNotAnnounceOtherLaunchesRetainedRecords(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	dir := filepath.Join(f.home, ".local/share/yolo-jail/state/mcp-auth-broker/once")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"records":[{"server":"other-workspace-server","identity":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","url":"https://other.invalid/mcp","generation":1,"state":"live","entry":{}}]}`
	if err := os.WriteFile(filepath.Join(dir, "records.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "records.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	run := f.Start("orphan-announce", false, false, false)
	if run.rc != 0 {
		t.Fatalf("launch failed: %d %s", run.rc, run.errs.String())
	}
	if strings.Contains(run.errs.String(), "retained canonical state") {
		t.Fatalf("launch announced another launch's retained record: %s", run.errs.String())
	}
}
