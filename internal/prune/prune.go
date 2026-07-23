// Package prune reclaims disk from yolo-jail storage. The byte/behavior-critical
// pieces are the
// hardlink-dedup atomicity, the tri-state orphan-agent-staging sweep (liveness
// unknown → DECLINE to delete), and the lexical CreatedAt image sort.
//
// The runtime-probe layer (podman/container ps+inspect) execs the real runtime
// (output format is the contract) and is injectable for tests via the Runtime
// interface. The pure logic (dedup, sweep decision, image sort) is
// runtime-independent and parity-tested.
package prune

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Dedup subtrees (per-workspace .yolo/home/<sub>) and global-storage subdirs
// that are safe to hardlink-dedup.
var (
	dedupeSubtrees      = []string{"npm-global", "local", "go"}
	globalDedupeSubdirs = []string{"cache", "mise", "home"}
)

const hashChunkBytes = 1 << 20 // 1 MiB
// Entry is a dedup candidate: a regular, non-empty, non-symlink file + its size.
type Entry struct {
	Path string
	Size int64
}

// WalkDedupTree yields dedup entries for regular, non-empty, non-symlink files
// under root (recursively). Missing root yields nothing. Uses lstat; skips
// symlinks, non-regular files, and zero-byte files.
func WalkDedupTree(root string) []Entry {
	var out []Entry
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees, keep walking
		}
		if d.IsDir() {
			return nil
		}
		st, err := os.Lstat(path)
		if err != nil {
			return nil
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if !st.Mode().IsRegular() {
			return nil
		}
		if st.Size() == 0 {
			return nil
		}
		out = append(out, Entry{Path: path, Size: st.Size()})
		return nil
	})
	return out
}

// WalkDedupableWorkspaces yields entries under each workspace's
// .yolo/home/{npm-global,local,go}.
func WalkDedupableWorkspaces(workspaces []string) []Entry {
	var out []Entry
	for _, ws := range workspaces {
		home := filepath.Join(ws, ".yolo", "home")
		for _, sub := range dedupeSubtrees {
			out = append(out, WalkDedupTree(filepath.Join(home, sub))...)
		}
	}
	return out
}

// WalkGlobalDedupable yields entries under the global-storage dedupe subdirs
// (cache, mise, home) — never the containers/agents/state/nix scratch subdirs.
func WalkGlobalDedupable(globalStorage string) []Entry {
	var out []Entry
	for _, sub := range globalDedupeSubdirs {
		out = append(out, WalkDedupTree(filepath.Join(globalStorage, sub))...)
	}
	return out
}

// hashFile SHA-256s a file in 1 MiB chunks; "" on I/O error (skip).
func hashFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, hashChunkBytes)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// HardlinkDuplicateFiles groups entries by (size, sha256) and hardlinks
// duplicates. Within a group the first entry is canonical; the rest are
// linked to it via the ATOMIC link-to-tmp-then-rename discipline (NEVER unlink
// the original first). Same-inode pairs are skipped. Returns (bytesSaved,
// linksMade); apply=false computes the same numbers without mutating.
func HardlinkDuplicateFiles(entries []Entry, apply bool) (bytesSaved int64, linksMade int) {
	// Bucket by size first (cheap filter; only hash colliding sizes).
	bySize := map[int64][]Entry{}
	sizeOrder := []int64{}
	for _, e := range entries {
		if _, seen := bySize[e.Size]; !seen {
			sizeOrder = append(sizeOrder, e.Size)
		}
		bySize[e.Size] = append(bySize[e.Size], e)
	}

	for _, size := range sizeOrder {
		group := bySize[size]
		if len(group) < 2 {
			continue
		}
		byHash := map[string][]Entry{}
		hashOrder := []string{}
		for _, e := range group {
			digest := hashFile(e.Path)
			if digest == "" {
				continue
			}
			if _, seen := byHash[digest]; !seen {
				hashOrder = append(hashOrder, digest)
			}
			byHash[digest] = append(byHash[digest], e)
		}
		for _, digest := range hashOrder {
			same := byHash[digest]
			if len(same) < 2 {
				continue
			}
			canonical := same[0]
			cIno, cDev, ok := inode(canonical.Path)
			if !ok {
				continue
			}
			for _, dup := range same[1:] {
				dIno, dDev, ok := inode(dup.Path)
				if !ok {
					continue
				}
				if dIno == cIno && dDev == cDev {
					continue // already linked
				}
				if !apply {
					bytesSaved += size
					linksMade++
					continue
				}
				tmp := dup.Path + ".yolo-dedup-tmp"
				if err := os.Link(canonical.Path, tmp); err != nil {
					_ = os.Remove(tmp) // clean partial
					continue
				}
				if err := os.Rename(tmp, dup.Path); err != nil {
					_ = os.Remove(tmp)
					continue
				}
				bytesSaved += size
				linksMade++
			}
		}
	}
	return bytesSaved, linksMade
}

// ImageEntry is (id, createdAt) for the old-image prune.
type ImageEntry struct {
	ID      string
	Created string
}

// OldImagesToRemove sorts images newest-first by the CreatedAt string
// (ISO-ish sorts LEXICALLY — never parsed) and returns the IDs beyond the
// newest `keep`.
func OldImagesToRemove(images []ImageEntry, keep int) []string {
	sorted := append([]ImageEntry(nil), images...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Created > sorted[j].Created // reverse (newest first)
	})
	if keep >= len(sorted) {
		return []string{}
	}
	out := []string{}
	for _, e := range sorted[keep:] {
		out = append(out, e.ID)
	}
	return out
}
