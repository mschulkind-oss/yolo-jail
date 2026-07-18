package prune

import (
	"os"
	"path/filepath"
)

// DiskReport is the per-category byte accounting produced by DiskUsageReport,
// mirroring the dict returned by _disk_usage_report. Breakdown/CacheBreakdown
// map a direct-child name to its byte total; stray top-level files roll into the
// "_files" key so the breakdown sum equals the top-level total exactly.
type DiskReport struct {
	GlobalStorage  int64
	Workspaces     int64
	Total          int64
	Breakdown      map[string]int64
	CacheBreakdown map[string]int64
}

// DiskUsageReport computes the per-category byte totals a prune might reclaim.
// Mirrors _disk_usage_report exactly:
//   - GlobalStorage: sum of every non-symlink direct child of globalStorage
//     (dirs recursively; stray files rolled into Breakdown["_files"]).
//   - Workspaces: sum of each workspace's .yolo tree size.
//   - Total: GlobalStorage + Workspaces.
//   - Breakdown: {child-name: bytes} for every direct child of globalStorage.
//   - CacheBreakdown: {child-name: bytes} for every direct child of
//     globalStorage/cache (empty when cache/ is absent). Its stray files roll
//     into CacheBreakdown["_files"] but are NOT added to any total (matching
//     Python, which only records the cache breakdown for display).
func DiskUsageReport(workspaces []string, globalStorage string) DiskReport {
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
	}
}
