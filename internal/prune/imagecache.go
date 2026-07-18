package prune

import (
	"os"
	"path/filepath"
	"sort"
)

// PruneImageCache keeps the `keep` newest *.tar files under imagesDir and removes
// the rest; it ALWAYS sweeps orphan *.tmp files (leftovers from a crashed
// materialization) regardless of keep. Returns (bytesRemoved, filesRemoved).
// Mirrors _prune_image_cache: skip symlinks/non-files, classify by suffix
// (.tar / .tmp via Path.suffix — the last dotted component), sort tars by mtime
// newest-first and drop the tail beyond keep, then sweep every .tmp. apply=false
// reports without touching disk.
func PruneImageCache(imagesDir string, keep int, apply bool) (bytesRemoved int64, filesRemoved int) {
	info, err := os.Stat(imagesDir)
	if err != nil || !info.IsDir() {
		return 0, 0
	}
	children, err := os.ReadDir(imagesDir)
	if err != nil {
		return 0, 0
	}

	type tarEnt struct {
		path  string
		size  int64
		mtime int64
	}
	var tars []tarEnt
	type tmpEnt struct {
		path string
		size int64
	}
	var tmps []tmpEnt

	for _, c := range children {
		p := filepath.Join(imagesDir, c.Name())
		st, err := os.Lstat(p)
		if err != nil {
			continue
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.Mode().IsRegular() {
			continue
		}
		switch filepath.Ext(c.Name()) {
		case ".tar":
			tars = append(tars, tarEnt{p, st.Size(), st.ModTime().UnixNano()})
		case ".tmp":
			tmps = append(tmps, tmpEnt{p, st.Size()})
		}
	}

	// Tars: newest first (by mtime), drop the tail beyond keep.
	sort.SliceStable(tars, func(i, j int) bool { return tars[i].mtime > tars[j].mtime })
	if keep < 0 {
		keep = 0
	}
	if keep < len(tars) {
		for _, t := range tars[keep:] {
			if apply {
				if err := os.Remove(t.path); err != nil {
					continue
				}
			}
			bytesRemoved += t.size
			filesRemoved++
		}
	}

	// Orphan tmp files: always sweep.
	for _, t := range tmps {
		if apply {
			if err := os.Remove(t.path); err != nil {
				continue
			}
		}
		bytesRemoved += t.size
		filesRemoved++
	}

	return bytesRemoved, filesRemoved
}
