package run

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// renameRecordToLegacy makes f's workspace look as a yolo from before slice 6a-iii left it: the
// delivery record, and the staged overlay it is named after, carry the legacy jail name (the
// project key alone), and no record names this launch's setup-qualified jail.
func renameRecordToLegacy(t *testing.T, f *inheritedRelaunchFixture) (legacyRecord, newRecord string) {
	t.Helper()
	newRecord = entrypoint.InheritedDeliveryRecordPath(f.sealed)
	legacyOverlay := filepath.Join(filepath.Dir(f.sealed), yoloruntime.FromWorkspace(f.ws))
	legacyRecord = entrypoint.InheritedDeliveryRecordPath(legacyOverlay)
	if legacyRecord == newRecord {
		t.Fatal("fixture: the legacy and setup-qualified jail names are the same")
	}
	if err := os.Rename(newRecord, legacyRecord); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.sealed, legacyOverlay); err != nil {
		t.Fatal(err)
	}
	return legacyRecord, newRecord
}

// THE SEATBELT RELAUNCH OF A WORKSPACE FROM BEFORE 6a-iii (the Mac run of 2026-11-09, §1;
// named-setups-plan.md SUP-D51). The only delivery record is the one named by the legacy jail
// name, and the file at the setup's home is the copy that record lists: the launch admits it,
// dispatches, and writes the record under its own jail name, leaving the legacy record in place.
// Red without the legacy fallback in inheritedStaging.readRecord: the launch refused with
// "cannot prove it is a file it delivered before".
func TestMacosUserSelfRelaunchAdmitsADeliveryTheLegacyNamedRecordLists(t *testing.T) {
	treeLaunchHome(t, true)
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil || f.dispatched != 1 {
		t.Fatalf("first Run() = %d, dispatched %d, installer error %v\nstderr:\n%s", rc, f.dispatched, err, stderr)
	}
	before := overlayFileInfo(t, f.delivered)
	legacyRecord, newRecord := renameRecordToLegacy(t, f)
	rc, stderr, installErr := f.launch()
	if rc != 0 || installErr != nil || f.dispatched != 2 {
		t.Fatalf("relaunch with only a legacy-named record = %d, dispatched %d, installer error %v\nstderr:\n%s",
			rc, f.dispatched, installErr, stderr)
	}
	if after := overlayFileInfo(t, f.delivered); !os.SameFile(before, after) {
		t.Error("the unchanged delivery was replaced; the relaunch should keep it")
	}
	if fi, err := os.Lstat(newRecord); err != nil || fi.Mode().Perm() != 0o444 {
		t.Errorf("no sealed record under this launch's jail name %s after the relaunch: %v", newRecord, err)
	}
	if _, err := os.Lstat(legacyRecord); err != nil {
		t.Errorf("the legacy-named record %s was not left in place: %v", legacyRecord, err)
	}
}

// THE FALLBACK NEVER WEAKENS THE PROOF. A file the legacy-named record does not list (here the
// owner's edit of the delivery) still refuses, before dispatch, and is left as it was.
func TestMacosUserSelfRelaunchRefusesAFileTheLegacyNamedRecordDoesNotList(t *testing.T) {
	treeLaunchHome(t, true)
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil || f.dispatched != 1 {
		t.Fatalf("first Run() = %d, dispatched %d, installer error %v\nstderr:\n%s", rc, f.dispatched, err, stderr)
	}
	body, err := os.ReadFile(f.delivered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.delivered); err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte("# owner edit\n")...)
	if err := os.WriteFile(f.delivered, body, 0o444); err != nil {
		t.Fatal(err)
	}
	owner := overlayFileInfo(t, f.delivered)
	_, newRecord := renameRecordToLegacy(t, f)
	rc, stderr, _ := f.launch()
	if rc == 0 || f.dispatched != 1 {
		t.Fatalf("relaunch over an unlisted file = %d, dispatched %d; want a host refusal\nstderr:\n%s", rc, f.dispatched, stderr)
	}
	if !strings.Contains(stderr, "cannot prove it is a file it delivered before") {
		t.Errorf("the refusal is not the owner-file one:\n%s", stderr)
	}
	got, err := os.ReadFile(f.delivered)
	if after := overlayFileInfo(t, f.delivered); err != nil || !bytes.Equal(got, body) || !os.SameFile(owner, after) {
		t.Fatalf("the refusal changed the owner's file: read error %v", err)
	}
	if _, err := os.Lstat(newRecord); !os.IsNotExist(err) {
		t.Errorf("a refused launch wrote a record under its jail name %s: %v", newRecord, err)
	}
}
