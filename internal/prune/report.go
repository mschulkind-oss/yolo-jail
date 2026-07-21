package prune

import (
	"os"
	"path/filepath"
	"sort"
)

// RelocatedCache is one cache subdir whose bytes do NOT live under
// globalStorage: a `cache_relocations` entry points it at another host
// directory, which the run pipeline nest-mounts over the container's
// ~/.cache/<Subdir>. Host-side, globalStorage/cache/<Subdir> stays an empty
// stub (the mountpoint), so a walk of the cache tree cannot see these bytes —
// which is exactly why prune has to be told about them.
//
// The plan sketched this as a map[string]int64, but the section prune prints
// has to name the target and the filesystem it sits on (the whole point is
// showing the user those GiB are on another device), and two parallel maps
// keyed by subdir would be one truth split in three. One record per relocation
// instead.
type RelocatedCache struct {
	Subdir     string // cache subdir name, e.g. "huggingface"
	Target     string // absolute host directory actually holding the bytes
	Bytes      int64  // recursive size of Target (0 when it is missing/unreadable)
	Filesystem string // mount point Target sits on ("" when undeterminable)
}

// DiskReport is the per-category byte accounting produced by DiskUsageReport.
// Breakdown/CacheBreakdown map a direct-child name to its byte total; stray
// top-level files roll into the "_files" key so the breakdown sum equals the
// top-level total exactly.
type DiskReport struct {
	GlobalStorage  int64
	Workspaces     int64
	Total          int64
	Breakdown      map[string]int64
	CacheBreakdown map[string]int64
	// CacheRelocated is the relocated-subdir accounting, largest first. These
	// bytes are deliberately absent from GlobalStorage/Total: they sit on a
	// different filesystem, so folding them in would claim a prune frees space
	// on a device it never touches.
	CacheRelocated []RelocatedCache
}

// DiskUsageReport computes the per-category byte totals a prune might reclaim.
//   - GlobalStorage: sum of every non-symlink direct child of globalStorage
//     (dirs recursively; stray files rolled into Breakdown["_files"]).
//   - Workspaces: sum of each workspace's .yolo tree size.
//   - Total: GlobalStorage + Workspaces.
//   - Breakdown: {child-name: bytes} for every direct child of globalStorage.
//   - CacheBreakdown: {child-name: bytes} for every direct child of
//     globalStorage/cache (empty when cache/ is absent). Its stray files roll
//     into CacheBreakdown["_files"] but are NOT added to any total (the cache
//     breakdown is recorded for display only).
//   - CacheRelocated: one entry per relocations pair (subdir -> absolute host
//     target), sized by walking the target. Never folded into any total.
//
// relocations may be nil (no cache_relocations configured), which reproduces
// the pre-relocation behavior exactly. A relocated subdir still appears in
// CacheBreakdown at whatever the host-side stub holds — usually 0 B, and that
// is the honest number for THIS filesystem. A non-zero stub is worth seeing: it
// means the config was set but the bytes were never moved, so the jail is
// writing to the target while the old copy still occupies the root filesystem.
func DiskUsageReport(workspaces []string, globalStorage string, relocations map[string]string) DiskReport {
	breakdown := map[string]int64{}
	var gsBytes int64

	if info, err := os.Stat(globalStorage); err == nil && info.IsDir() {
		var stray int64
		entries, err := os.ReadDir(globalStorage)
		if err != nil {
			entries = nil
		}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if fi.IsDir() {
				size := dirSizeBytes(filepath.Join(globalStorage, e.Name()))
				breakdown[e.Name()] = size
				gsBytes += size
			} else if fi.Mode().IsRegular() {
				stray += fi.Size()
				gsBytes += fi.Size()
			}
		}
		if stray != 0 {
			breakdown["_files"] = stray
		}
	}

	cacheBreakdown := map[string]int64{}
	cacheRoot := filepath.Join(globalStorage, "cache")
	if info, err := os.Stat(cacheRoot); err == nil && info.IsDir() {
		var stray int64
		entries, err := os.ReadDir(cacheRoot)
		if err != nil {
			entries = nil
		}
		for _, e := range entries {
			fi, err := e.Info()
			if err != nil {
				continue
			}
			if fi.Mode()&os.ModeSymlink != 0 {
				continue
			}
			if fi.IsDir() {
				cacheBreakdown[e.Name()] = dirSizeBytes(filepath.Join(cacheRoot, e.Name()))
			} else if fi.Mode().IsRegular() {
				stray += fi.Size()
			}
		}
		if stray != 0 {
			cacheBreakdown["_files"] = stray
		}
	}

	var wsBytes int64
	for _, ws := range workspaces {
		wsBytes += dirSizeBytes(filepath.Join(ws, ".yolo"))
	}

	return DiskReport{
		GlobalStorage:  gsBytes,
		Workspaces:     wsBytes,
		Total:          gsBytes + wsBytes,
		Breakdown:      breakdown,
		CacheBreakdown: cacheBreakdown,
		CacheRelocated: relocatedUsage(relocations),
	}
}

// relocatedUsage sizes each relocation target and resolves the filesystem it
// sits on, largest first (ties by subdir, so the rendered order is stable
// across runs the way sortByValueDesc keeps the other panels stable).
//
// A missing or unreadable target yields a 0 B entry rather than being dropped:
// the relocation is configured, so its absence from the report would read as
// "nothing relocated" — the blindness this whole section exists to fix. Sizing
// is a full walk of the target, the same cost DiskUsageReport already pays per
// cache subdir.
func relocatedUsage(relocations map[string]string) []RelocatedCache {
	if len(relocations) == 0 {
		return nil
	}
	out := make([]RelocatedCache, 0, len(relocations))
	for subdir, target := range relocations {
		out = append(out, RelocatedCache{
			Subdir:     subdir,
			Target:     target,
			Bytes:      dirSizeBytes(target),
			Filesystem: mountPointOf(target),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Subdir < out[j].Subdir
	})
	return out
}

// mountPointOf returns the deepest ancestor of path (path itself included) that
// begins a distinct filesystem — i.e. it climbs while the st_dev stays equal
// and stops where it changes. That is the mount point, which is what makes a
// relocation legible ("/data", not "/"): seeing it differ from the storage
// root's is how a user confirms the bytes really moved off the full device.
//
// "" when path cannot be stat'ed. Reuses inode()'s lstat, so it is portable
// across the linux/darwin hosts for the same reason inode() is.
func mountPointOf(path string) string {
	_, dev, ok := inode(path)
	if !ok {
		return ""
	}
	cur := path
	for {
		parent := filepath.Dir(cur)
		if parent == cur {
			return cur // reached the root; nothing above it to compare against
		}
		_, pdev, ok := inode(parent)
		if !ok || pdev != dev {
			return cur
		}
		cur = parent
	}
}
