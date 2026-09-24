package prune

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// mkDelivery builds an attempt directory the way image.newDeliveryWorkDir and the
// copier leave one: a layout with a blob, and an archive beside it. Every entry is
// backdated by age unless blobAge says the copy is still writing.
func mkDelivery(t *testing.T, dir, name string, age, blobAge time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	blobs := filepath.Join(p, "layout", "blobs", "sha256")
	must(t, os.MkdirAll(blobs, 0o700))
	blob := filepath.Join(blobs, "aa")
	must(t, os.WriteFile(blob, make([]byte, 300), 0o644))
	must(t, os.WriteFile(filepath.Join(p, "image.oci-archive"), make([]byte, 12), 0o644))
	past := time.Now().Add(-age)
	for _, e := range []string{filepath.Join(p, "image.oci-archive"), blobs,
		filepath.Dir(blobs), filepath.Join(p, "layout"), p} {
		must(t, os.Chtimes(e, past, past))
	}
	bt := time.Now().Add(-blobAge)
	must(t, os.Chtimes(blob, bt, bt))
	return p
}

// TestPruneImageDeliveryReclaimsOnlyInterruptedDeliveries: an attempt directory
// past the floor is a launch killed mid-delivery and is reclaimed whole. Spared:
// one inside the floor (a delivery in flight), one whose TOP directory is old but
// whose copy is still writing blobs (the floor reads the newest mtime), a
// directory without the suffix, a delivery record, and a symlink.
func TestPruneImageDeliveryReclaimsOnlyInterruptedDeliveries(t *testing.T) {
	dir := t.TempDir()
	old := imageCacheTmpGraceFloor + time.Minute
	stale := mkDelivery(t, dir, "k1-1"+image.DeliveryWorkSuffix, old, old)
	fresh := mkDelivery(t, dir, "k2-2"+image.DeliveryWorkSuffix, 0, 0)
	copying := mkDelivery(t, dir, "k3-3"+image.DeliveryWorkSuffix, old, 0)
	other := mkDelivery(t, dir, "not-ours", 48*time.Hour, 48*time.Hour)
	record := filepath.Join(dir, "k1.delivered.json")
	must(t, os.WriteFile(record, []byte("{}"), 0o600))
	backdateFile(t, record, 48*time.Hour)
	link := filepath.Join(dir, "k4-4"+image.DeliveryWorkSuffix)
	must(t, os.Symlink(other, link))

	got, n := PruneImageDelivery(dir, false)
	if n != 1 || got != 312 {
		t.Errorf("dry run = (%d bytes, %d dirs), want (312, 1)", got, n)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("apply=false removed the stale delivery")
	}
	if _, n = PruneImageDelivery(dir, true); n != 1 {
		t.Errorf("swept %d entries, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a delivery past the floor is a crashed launch's and must be reclaimed")
	}
	for _, p := range []string{fresh, copying, other, record, link} {
		if _, err := os.Lstat(p); err != nil {
			t.Errorf("%s was removed", filepath.Base(p))
		}
	}
	if _, err := os.Stat(filepath.Join(other, "layout")); err != nil {
		t.Error("the symlink's target was followed and removed")
	}
}

func backdateFile(t *testing.T, p string, age time.Duration) {
	t.Helper()
	past := time.Now().Add(-age)
	must(t, os.Chtimes(p, past, past))
}

// TestYoloPruneReclaimsInterruptedDeliveries pins the `yolo prune` call site: the
// state dir's image-delivery/ is swept in the cached-image section, reported on a
// line of its own, and removed under --apply. Delete the PruneImageDelivery call
// in Run and this fails, where the sweep's own test stays green.
func TestYoloPruneReclaimsInterruptedDeliveries(t *testing.T) {
	o, gs := baseOpts(t)
	dir := filepath.Join(gs, imageDeliveryLeaf)
	must(t, os.MkdirAll(dir, 0o700))
	stale := mkDelivery(t, dir, "k1-1"+image.DeliveryWorkSuffix, 48*time.Hour, 48*time.Hour)

	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	want := "  would remove: " + FmtBytes(312) + " across 1 interrupted image delivery dir(s) in image-delivery/"
	if !hasLine(&buf, want) {
		t.Errorf("missing %q in:\n%s", want, buf.String())
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("the dry run removed the stale delivery")
	}
	o.Apply = true
	buf.Reset()
	Run(o)
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("--apply left the interrupted delivery in place:\n%s", buf.String())
	}
}
