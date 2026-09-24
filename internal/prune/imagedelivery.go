package prune

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
)

// PruneImageDelivery reclaims the per-attempt directories an archive delivery
// left behind under dir (paths.ImageDeliveryDir; image.newDeliveryWorkDir makes
// them, `<key>-<random>.delivery.tmp/`). A launch removes its own on the way out,
// so one that is still here after the floor is the leftover of a launch killed
// mid-delivery — and a full one is a whole image layout or archive, 3.45 GB.
//
// THE SAME FLOOR AS THE ARCHIVE FILES THIS REPLACED (imageCacheTmpGraceFloor, and
// its reasoning): a delivery in flight is indistinguishable from a corpse by name,
// so only age separates them. The age is the NEWEST mtime anywhere in the
// directory, not the directory's own, because the copy writes blobs into
// `layout/blobs/sha256/` for minutes without touching the top directory.
//
// Only a real directory with the suffix is this sweep's. The delivery records
// beside them (`<key>.delivered.json`) are reaped with their image by
// image.recordedPresentDigests and are never touched here; a symlink, or anything
// the sweep cannot lstat, is left alone. Returns (bytesRemoved, dirsRemoved);
// apply=false reports without touching disk.
func PruneImageDelivery(dir string, apply bool) (bytesRemoved int64, dirsRemoved int) {
	children, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	now := time.Now()
	for _, c := range children {
		if !strings.HasSuffix(c.Name(), image.DeliveryWorkSuffix) {
			continue
		}
		p := filepath.Join(dir, c.Name())
		st, err := imageCacheLstat(p)
		if err != nil || !st.IsDir() {
			continue
		}
		size, newest := treeSizeAndNewest(p, st.ModTime())
		if now.Sub(newest) < imageCacheTmpGraceFloor {
			continue
		}
		if apply {
			if err := os.RemoveAll(p); err != nil {
				continue
			}
		}
		bytesRemoved += size
		dirsRemoved++
	}
	return bytesRemoved, dirsRemoved
}

// treeSizeAndNewest is the bytes of every regular file under root, and the newest
// mtime of any entry (root's own passed in as the floor). An unreadable entry
// counts as zero bytes and no time.
func treeSizeAndNewest(root string, rootMtime time.Time) (int64, time.Time) {
	var total int64
	newest := rootMtime
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if d.Type().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, newest
}
