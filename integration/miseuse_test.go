package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"
)

// TestAJailRecordsTheToolVersionsItUses is the end-to-end half of OQ-DF4's use record
// (docs/design/minimal-disk-footprint.md; internal/miseuse): a real jail's main process writes,
// into the shared tool store, a record naming its workspace and what that workspace uses, without
// any session asking it to. The host's sweep of unused tool versions judges nothing but these
// records, so a jail that stopped writing one would leave the sweep waiting forever — or, once a
// window had passed, reading every version that jail uses as unused. The unit tests pin each half;
// only a launch shows the main process actually starts the recorder in a container.
func TestAJailRecordsTheToolVersionsItUses(t *testing.T) {
	requireJail(t)
	dir := tempProject(t)
	// The record is written beside the hold, not by the session, so the session waits for it.
	r := runYolo(t, dir, strings.Join([]string{
		`for i in $(seq 1 90); do ls /mise/.yolo-use/*.json >/dev/null 2>&1 && break; sleep 1; done`,
		`echo "=== SINCE ==="; cat /mise/.yolo-use/since 2>/dev/null; true`,
		`echo "=== RECORDS ==="; for f in /mise/.yolo-use/*.json; do tr -d '\n' < "$f"; echo; done`,
	}, "\n"))
	if r.rc != 0 {
		t.Fatalf("jail exited %d:\n%s", r.rc, r.combined())
	}
	// THE CLOCK IS A HOST LAUNCH'S TO START (internal/miseuse.SinceName): a suite run inside a jail
	// launches in-jail jails, which bind the host's store and must leave its clock alone. And a
	// LINUX host's alone: a Mac's jails bind a volume inside the container VM, which the host
	// never sweeps, so no Mac launch starts a clock there (markMiseUseRecording's IsMacOS). This
	// asserted a marker on the Intel nightly too, and failed there by that design (run 37931358062).
	since := strings.TrimSpace(section(r.stdout, "=== SINCE ===", "=== RECORDS ==="))
	switch {
	case os.Getenv("YOLO_VERSION") != "":
	case goruntime.GOOS == "darwin":
		if since != "" {
			t.Errorf("a Mac launch started a clock in the VM's tool store (since marker %q), which "+
				"no host sweep reads", since)
		}
	default:
		if _, err := time.Parse(time.RFC3339, since); err != nil {
			t.Errorf("a host launch did not start the store's clock: the since marker is %q (%v)", since, err)
		}
	}
	wanted := map[string]bool{dir: true}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		wanted[resolved] = true
	}
	var mine []map[string]any
	for _, line := range strings.Split(section(r.stdout, "=== RECORDS ===", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a record in the store is not JSON (%v): %s", err, line)
		}
		if ws, _ := rec["workspace"].(string); wanted[ws] {
			mine = append(mine, rec)
		}
	}
	if len(mine) == 0 {
		t.Fatalf("no record names this jail's workspace %s within 90 s of its start — the main "+
			"process no longer records the tool versions it uses:\n%s", dir, r.combined())
	}
	for _, rec := range mine {
		if u, _ := rec["unknown"].(string); u != "" {
			t.Errorf("the jail recorded that it could not tell what it uses (%s); a host would then "+
				"decline every sweep while the record is in force", u)
		}
		when, err := time.Parse(time.RFC3339Nano, rec["recorded"].(string))
		if err != nil || time.Since(when) > time.Hour || time.Until(when) > time.Minute {
			t.Errorf("record time %v (%v) is not this launch's", rec["recorded"], err)
		}
	}
}
