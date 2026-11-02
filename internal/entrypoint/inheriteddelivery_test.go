package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// inheriteddelivery_test.go pins the installer's half of issue #70: an inherited config delivered
// as a COPY (the hard link refused) is admitted on the next install by the delivery record the host
// keeps in the staging root, and nothing short of that record admits one.

// writeInheritedRecord seals a delivery record for overlay listing each body's digest at dest.
func writeInheritedRecord(t *testing.T, overlay, dest string, mode os.FileMode, bodies ...[]byte) string {
	t.Helper()
	path := InheritedDeliveryRecordPath(overlay)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	r := InheritedDeliveryRecord{Digests: map[string][]string{}}
	for _, b := range bodies {
		r.Digests[dest] = append(r.Digests[dest], ContentDigest(b))
	}
	data, err := r.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(path)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// refuseLinks makes the installer's hard links fail as the reporter's Seatbelt-confined one did.
func refuseLinks(t *testing.T) {
	t.Helper()
	t.Cleanup(RefuseOverlayLinksForTest(unix.EPERM))
}

// writeOwnerFile replaces dest with a sealed file of the given bytes, as an agent could.
func writeOwnerFile(t *testing.T, dest string, body []byte) os.FileInfo {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(dest)
	if err := os.WriteFile(dest, body, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o444); err != nil {
		t.Fatal(err)
	}
	return inheritedCoreOverlayEntryInfo(t, dest)
}

// requireUntouched fails unless path is still the file before, with body.
func requireUntouched(t *testing.T, path string, before os.FileInfo, body []byte) {
	t.Helper()
	after := inheritedCoreOverlayEntryInfo(t, path)
	got, err := os.ReadFile(path)
	if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !bytes.Equal(got, body) {
		t.Fatalf("a refusal changed %s: before %v, after %v, body %q, err %v", path, before, after, got, err)
	}
}

// THE REPORTED RELAUNCH. The link is refused, so the first install copies, and says so in the boot
// log; the second install finds a sealed copy whose bytes the record lists and keeps it. Before the
// record, the second install refused the copy as "not the unchanged sealed hardlink".
func TestInheritedCoreOverlayAdmitsARecordedCopiedDelivery(t *testing.T) {
	refuseLinks(t)
	f := newInheritedCoreOverlayFixture(t)
	writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o444, inheritedCoreOverlayTestBody)
	var log bytes.Buffer
	f.env.LogOnly = &log
	if err := f.install(); err != nil {
		t.Fatal(err)
	}
	delivered := inheritedCoreOverlayEntryInfo(t, f.dest)
	if os.SameFile(delivered, inheritedCoreOverlayEntryInfo(t, f.source)) || delivered.Mode().Perm() != 0o444 {
		t.Fatalf("the refused link was not delivered as a sealed copy: %v", delivered)
	}
	line := "~/" + inheritedCoreOverlayTestDest + " was delivered as a copy"
	if !strings.Contains(log.String(), line) || !strings.Contains(log.String(), unix.EPERM.Error()) {
		t.Fatalf("the boot log does not name the copied destination and the link error:\n%s", log.String())
	}
	if err := f.install(); err != nil {
		t.Fatalf("relaunch over the recorded copied delivery: %v", err)
	}
	requireUntouched(t, f.dest, delivered, inheritedCoreOverlayTestBody)
}

// A RE-STAGED OVERLAY REPLACES THE RECORDED PREVIOUS DELIVERY: the host lists the previous body's
// digest beside the new one, and the installer swaps the old copy for the new body.
func TestInheritedCoreOverlayReplacesARecordedPreviousDelivery(t *testing.T) {
	refuseLinks(t)
	f := newInheritedCoreOverlayFixture(t)
	old := []byte("previous inherited config\n")
	writeOwnerFile(t, f.dest, old)
	writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o444, inheritedCoreOverlayTestBody, old)
	if err := f.install(); err != nil {
		t.Fatalf("installing over the recorded previous delivery: %v", err)
	}
	got, err := os.ReadFile(f.dest)
	if err != nil || !bytes.Equal(got, inheritedCoreOverlayTestBody) {
		t.Fatalf("the recorded previous delivery was not replaced: %q, %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(f.dest), ".config.jsonc"+overlayAsideSuffix)); !os.IsNotExist(err) {
		t.Fatalf("the previous delivery was left aside: %v", err)
	}
}

// NOTHING SHORT OF THE SEALED RECORD ADMITS. A file whose header (here, whole first line) matches
// but whose content differs; matching bytes with no record; and matching bytes with a record the
// sealing does not protect, are each refused and left exactly as they were.
func TestInheritedCoreOverlayRefusesWhatTheRecordDoesNotAdmit(t *testing.T) {
	headerOnly := append(append([]byte(nil), inheritedCoreOverlayTestBody...), []byte("agent addition\n")...)
	cases := map[string]struct {
		body   []byte
		record func(t *testing.T, f inheritedCoreOverlayFixture)
	}{
		"matching header, different content": {headerOnly, func(t *testing.T, f inheritedCoreOverlayFixture) {
			writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o444, inheritedCoreOverlayTestBody)
		}},
		"matching content, no record": {inheritedCoreOverlayTestBody, func(*testing.T, inheritedCoreOverlayFixture) {}},
		"matching content, writable record": {inheritedCoreOverlayTestBody, func(t *testing.T, f inheritedCoreOverlayFixture) {
			writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o644, inheritedCoreOverlayTestBody)
		}},
		"matching content, record for another destination": {inheritedCoreOverlayTestBody, func(t *testing.T, f inheritedCoreOverlayFixture) {
			writeInheritedRecord(t, f.overlay, ".config/yolo-jail/config.toml", 0o444, inheritedCoreOverlayTestBody)
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			refuseLinks(t)
			f := newInheritedCoreOverlayFixture(t)
			tc.record(t, f)
			before := writeOwnerFile(t, f.dest, tc.body)
			err := f.install()
			if err == nil {
				t.Fatal("install replaced a destination the record does not admit")
			}
			if !strings.Contains(err.Error(), "relaunch") {
				t.Errorf("the refusal names no next step: %v", err)
			}
			requireUntouched(t, f.dest, before, tc.body)
		})
	}
}

// A RECORDED DELIVERY SWAPPED FOR THE AGENT'S OWN FILE after the check is not replaced: the move
// aside is checked against the file that was hashed, and the agent's file is put back.
func TestInheritedCoreOverlayKeepsAnOwnerFileSwappedInAfterTheRecordCheck(t *testing.T) {
	refuseLinks(t)
	f := newInheritedCoreOverlayFixture(t)
	old := []byte("previous inherited config\n")
	writeOwnerFile(t, f.dest, old)
	writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o444, inheritedCoreOverlayTestBody, old)
	owner := []byte("the agent's own config\n")
	var before os.FileInfo
	withOverlayInstallHook(t, func(dest, window string) {
		if dest == inheritedCoreOverlayTestDest && window == "staged" {
			before = writeOwnerFile(t, f.dest, owner)
		}
	})
	if err := f.install(); err == nil {
		t.Fatal("install replaced an owner file swapped in after the record check")
	}
	if before == nil {
		t.Fatal("the staged hook did not swap the destination")
	}
	requireUntouched(t, f.dest, before, owner)
}

// THE SAME INODE IS NOT THE SAME FILE. The agent rewrites the recorded delivery in place after the
// check: inode and mode are unchanged, the bytes are its own. On a filesystem that reuses a freed
// inode number (ext4, as on CI), the remove-and-recreate swap above produces exactly this, so the
// installer must re-check the content of what it moved aside, not only its identity.
func TestInheritedCoreOverlayKeepsAnOwnerRewriteInPlaceAfterTheRecordCheck(t *testing.T) {
	refuseLinks(t)
	f := newInheritedCoreOverlayFixture(t)
	old := []byte("previous inherited config\n")
	writeOwnerFile(t, f.dest, old)
	writeInheritedRecord(t, f.overlay, inheritedCoreOverlayTestDest, 0o444, inheritedCoreOverlayTestBody, old)
	owner := []byte("the agent's own config, same length\n")
	var before os.FileInfo
	withOverlayInstallHook(t, func(dest, window string) {
		if dest != inheritedCoreOverlayTestDest || window != "staged" {
			return
		}
		if err := os.Chmod(f.dest, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.dest, owner, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(f.dest, 0o444); err != nil {
			t.Fatal(err)
		}
		before = inheritedCoreOverlayEntryInfo(t, f.dest)
	})
	if err := f.install(); err == nil {
		t.Fatal("install replaced an owner file rewritten in place after the record check")
	}
	if before == nil {
		t.Fatal("the staged hook did not rewrite the destination")
	}
	requireUntouched(t, f.dest, before, owner)
}

// THE RECORD IS NOT IN THE SIDECAR: it sits beside the staged overlay in the host's staging root,
// which the sandbox cannot write, never under the workspace's home state, which it can.
func TestTheInheritedDeliveryRecordLivesBesideTheStagedOverlay(t *testing.T) {
	overlay := "/Users/Shared/yolo-staged/home-overlay/yolo-ws-0123"
	if got, want := InheritedDeliveryRecordPath(overlay), "/Users/Shared/yolo-staged/inherited-delivered/yolo-ws-0123.json"; got != want {
		t.Fatalf("InheritedDeliveryRecordPath(%s) = %s, want %s", overlay, got, want)
	}
	if InheritedDeliveryRecordPath("") != "" {
		t.Fatal("no overlay must name no record")
	}
}
