package stores

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/miseuse"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// TestTheMiseRowNamesItsReclaimerAndWhatItWouldDo: the shared tool store was listed as reclaimed
// by nothing (minimal-disk-footprint.md §2.6). Since OQ-DF4 it has a reclaimer, and the row says
// what that reclaimer would remove right now — judged the way `yolo prune` judges — or why it
// cannot judge yet. In a jail it promises nothing: the store there is the host's.
func TestTheMiseRowNamesItsReclaimerAndWhatItWouldDo(t *testing.T) {
	o, state := testOptions(t)
	now := time.Now().Add(2 * miseuse.Window) // the fixture's directories are made now
	o.Now = func() time.Time { return now }
	o.IsMacOS = func() bool { return false } // a Linux host: the state dir's mise/ is the store
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		return prune.ProbeResult{Ran: true} // the runtime answers: nothing runs
	}
	store := filepath.Join(state, "mise")
	writeFile(t, filepath.Join(store, "installs", "node", "20.1.0", "bin", "node"), 1000)
	writeFile(t, filepath.Join(store, "installs", "node", "22.5.0", "bin", "node"), 1000)
	if err := miseuse.Write(store, miseuse.NewName(), miseuse.Record{
		Workspace: "/home/u/code/a", Recorded: now.Add(-time.Hour), Installs: []string{"node/22.5.0"},
	}); err != nil {
		t.Fatal(err)
	}

	// No host launch has started the record's clock yet: nothing is judged, and the row says so.
	row := storeByKey(t, Inventory(o), "state.mise")
	if row.Reclaimer.Func != "PruneUnusedMiseVersions" || row.Verdict != VerdictYolo {
		t.Fatalf("the mise row reports reclaimer %q / verdict %q", row.Reclaimer.Func, row.Verdict)
	}
	if !strings.Contains(row.Note, "nothing judged yet") {
		t.Errorf("a store whose record is an hour old should say it is not judged yet: %q", row.Note)
	}

	since := now.Add(-miseuse.Window - 24*time.Hour).UTC().Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(store, miseuse.DirName, miseuse.SinceName), []byte(since), 0o644); err != nil {
		t.Fatal(err)
	}
	row = storeByKey(t, Inventory(o), "state.mise")
	if !strings.Contains(row.Note, "1000 B is in 1 tool version(s) no jail has used for 30 days") {
		t.Errorf("the row does not say what its reclaimer would remove: %q", row.Note)
	}
	if _, err := os.Stat(filepath.Join(store, "installs", "node", "20.1.0")); err != nil {
		t.Fatal("yolo stores removed a version; it must never mutate a store")
	}

	o.IsMacOS = func() bool { return true }
	row = storeByKey(t, Inventory(o), "state.mise")
	if row.Reclaimer.Func != "" || !strings.Contains(row.Note, "container VM") {
		t.Errorf("on a Mac the row claims reclaimer %q (note %q); the jails there keep the store in the VM", row.Reclaimer.Func, row.Note)
	}

	o.IsMacOS = func() bool { return false }
	o.InJail = func() bool { return true }
	row = storeByKey(t, Inventory(o), "state.mise")
	if row.Reclaimer.Func != "" || !strings.Contains(row.Note, "/mise") {
		t.Errorf("in a jail the row claims reclaimer %q (note %q); the store there is the host's", row.Reclaimer.Func, row.Note)
	}
}
