// Package prune reclaims disk from yolo-jail storage. The byte/behavior-critical
// pieces are the
// hardlink-dedup atomicity, the tri-state orphan-agent-staging sweep (liveness
// unknown → DECLINE to delete), and the per-workspace current-image pointers
// image retention is derived from (currentimages.go).
//
// The runtime-probe layer (podman/container ps+inspect) execs the real runtime
// (output format is the contract) and is injectable for tests via the Runtime
// interface. The pure logic (dedup, sweep decision, retention set) is
// runtime-independent and parity-tested.
package prune

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// Dedup subtrees (per-workspace .yolo/home/<sub>) and global-storage subdirs
// that are safe to hardlink-dedup.
//
// The per-workspace set is paths.InstalledProgramSurfaces() and is NOT re-typed here: install-capture
// walks the same dirs (program-delivery.md §6.3), and the two must agree or be wrong
// together — a surface added for one and not the other is the bug the shared list makes
// unrepresentable.
var (
	dedupeSubtrees      = homeSurfaceSubtrees()
	globalDedupeSubdirs = []string{"cache", "mise", "home"}
)

// homeSurfaceSubtrees is paths.InstalledProgramSurfaces() reduced to the host-side <ws>/.yolo/home/<sub>
// names, which is the half this package walks.
func homeSurfaceSubtrees() []string {
	surfaces := paths.InstalledProgramSurfaces()
	out := make([]string, 0, len(surfaces))
	for _, s := range surfaces {
		out = append(out, s.Subtree)
	}
	return out
}

const hashChunkBytes = 1 << 20 // 1 MiB
// Entry is a dedup candidate: a regular, non-empty, non-symlink file + its size.
type Entry struct {
	Path string
	Size int64
	// root is the tree Path was walked beneath, which HardlinkDuplicateFiles reopens on the
	// same terms; the zero value, an Entry built from a path alone, is Path's parent directory.
	root dedupRoot
}

// dedupRoot is a tree dedup walks and links beneath: a workspace's overlay, or a directory.
type dedupRoot struct {
	workspace string // <workspace>/.yolo/home, refusing a link at `.yolo` or at `home`
	dir       string // else this directory, refusing a link at it
}

// open opens d as an os.Root, refusing a symbolic link at the directories the jail can replace
// (docs/reference/jail-home.md, "Host code in jail-writable state").
func (d dedupRoot) open() (*os.Root, error) {
	if d.workspace != "" {
		return paths.OpenWorkspaceStateSubdir(d.workspace, "home")
	}
	return paths.OpenStateDirRoot(d.dir)
}

// beneath is e's root and its path relative to that root.
func (e Entry) beneath() (dedupRoot, string, error) {
	if e.root == (dedupRoot{}) {
		return dedupRoot{dir: filepath.Dir(e.Path)}, filepath.Base(e.Path), nil
	}
	dir := e.root.dir
	if e.root.workspace != "" {
		dir = paths.WorkspaceHomeState(e.root.workspace)
	}
	rel, err := filepath.Rel(dir, e.Path)
	return e.root, rel, err
}

// WalkDedupTree yields dedup entries for regular, non-empty, non-symlink files
// under root (recursively). Missing root yields nothing, and so does a root that is a
// symbolic link. Uses lstat; skips symlinks, non-regular files, and zero-byte files.
func WalkDedupTree(root string) []Entry {
	r, err := paths.OpenStateDirRoot(root)
	if err != nil {
		return nil
	}
	defer r.Close()
	return walkDedupBeneath(r, dedupRoot{dir: root}, root, ".")
}

// walkDedupBeneath is WalkDedupTree of rel below r, whose directory is dir: every trees dedup
// walks is one a jail can write, so the walk resolves each path beneath the root rather than by
// a plain path a link the jail left could redirect. A symbolic link at rel yields nothing.
func walkDedupBeneath(r *os.Root, root dedupRoot, dir, rel string) []Entry {
	var out []Entry
	info, err := r.Lstat(rel)
	if err != nil || !info.IsDir() {
		return nil
	}
	_ = fs.WalkDir(r.FS(), filepath.ToSlash(rel), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable subtrees, keep walking
		}
		if d.IsDir() {
			return nil
		}
		path = filepath.FromSlash(path)
		st, err := r.Lstat(path)
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
		out = append(out, Entry{Path: filepath.Join(dir, path), Size: st.Size(), root: root})
		return nil
	})
	return out
}

// WalkDedupableWorkspaces yields entries under each workspace's
// .yolo/home/{npm-global,local,go,codex/packages/standalone}, minus dedupExcludedSubtrees.
// The overlay is opened refusing a link at `.yolo` or at `.yolo/home`, and walked beneath
// that root: both are jail-writable, through the workspace bind.
func WalkDedupableWorkspaces(workspaces []string) []Entry {
	var out []Entry
	for _, ws := range workspaces {
		r, err := paths.OpenWorkspaceStateSubdir(ws, "home")
		if err != nil {
			continue
		}
		home := paths.WorkspaceHomeState(ws)
		for _, sub := range dedupeSubtrees {
			root := filepath.Join(home, sub)
			var skip []string
			for _, rel := range dedupExcludedSubtrees[sub] {
				skip = append(skip, filepath.Join(root, rel))
			}
			out = append(out, excludeUnder(walkDedupBeneath(r, dedupRoot{workspace: ws}, home, sub), skip)...)
		}
		r.Close()
	}
	return out
}

// dedupExcludedSubtrees maps a workspace surface subtree to the paths under it that dedup
// must never link: today the jail's own embedded-pack cache
// (~/.local/share/yolo-jail/embedded-packs, internal/packload's embeddedcache.go).
//
// Each tree there is IMMUTABLE and PER-WORKSPACE on purpose. Hardlinking it to a byte-identical
// file in another workspace would share one inode across jails — and a jail is its home's
// owner, so it could write another workspace's tree through that inode — while dedup's
// link-to-temp-then-rename drops a name inside the tree, which a concurrent in-jail adopter
// sees as an unexpected entry and quarantines. It is ~250 KB per tree; nothing is worth that.
// capture excludes the whole state dir for its own reason (capture.DefaultExcludes).
var dedupExcludedSubtrees = func() map[string][]string {
	out := map[string][]string{}
	// The in-jail location, home-relative: EmbeddedPacksDirUnder a root of "/".
	rel := strings.TrimPrefix(filepath.ToSlash(paths.EmbeddedPacksDirUnder("/")), "/")
	for _, s := range paths.InstalledProgramSurfaces() {
		if under, ok := strings.CutPrefix(rel, strings.TrimSuffix(s.HomeRel, "/")+"/"); ok {
			out[s.Subtree] = append(out[s.Subtree], filepath.FromSlash(under))
		}
	}
	return out
}()

// excludeUnder is entries minus every entry at or under a skip path.
func excludeUnder(entries []Entry, skip []string) []Entry {
	if len(skip) == 0 {
		return entries
	}
	var out []Entry
	for _, e := range entries {
		excluded := false
		for _, s := range skip {
			if e.Path == s || strings.HasPrefix(e.Path, s+string(filepath.Separator)) {
				excluded = true
				break
			}
		}
		if !excluded {
			out = append(out, e)
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

// hashedEntry is an entry opened beneath its root and hashed: rel is its path below root,
// and ino/dev the identity of the file that was hashed, which the link checks the name
// against before and after linking.
type hashedEntry struct {
	root     *os.Root
	rel      string
	ino, dev uint64
}

// hashBeneath SHA-256s rel below r in 1 MiB chunks, reading only a regular file (a link at rel
// is not followed; paths.OpenRegularFileBeneath), and returns the hashed file's identity with
// the digest; "" on I/O error (skip).
func hashBeneath(r *os.Root, rel string) (digest string, ino, dev uint64) {
	f, err := paths.OpenRegularFileBeneath(r, rel)
	if err != nil {
		return "", 0, 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", 0, 0
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, 0
	}
	h := sha256.New()
	buf := make([]byte, hashChunkBytes)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", 0, 0
	}
	return hex.EncodeToString(h.Sum(nil)), st.Ino, uint64(st.Dev)
}

// dedupBeforeLink, when set, is called after the files are hashed and before each link. It is a
// test seam, nil in production: the window it opens is the one in which a jail can swap a
// directory on the way for a link after the hash has vouched for the file below it.
var dedupBeforeLink func()

// HardlinkDuplicateFiles groups entries by (size, sha256) and hardlinks
// duplicates. Within a group the first entry is canonical; the rest are
// linked to it via the ATOMIC link-to-tmp-then-rename discipline (NEVER unlink
// the original first). Same-inode pairs are skipped. Returns (bytesSaved,
// linksMade); apply=false computes the same numbers without mutating.
//
// Every file is hashed and linked BENEATH the root it was walked under, never by plain path
// (docs/reference/jail-home.md, "Host code in jail-writable state"): the jail can swap a
// directory on the way for a link between the walk and the link, and a plain path then replaced
// the host file behind it with a hardlink to the jail's file, or, on the canonical's side,
// hardlinked a HOST file into the jail's tree (linkBeneath).
func HardlinkDuplicateFiles(entries []Entry, apply bool) (bytesSaved int64, linksMade int) {
	roots := map[dedupRoot]*os.Root{}
	defer func() {
		for _, r := range roots {
			if r != nil {
				r.Close()
			}
		}
	}()
	openRoot := func(d dedupRoot) *os.Root {
		r, seen := roots[d]
		if !seen {
			r, _ = d.open()
			roots[d] = r
		}
		return r
	}

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
		byHash := map[string][]hashedEntry{}
		hashOrder := []string{}
		for _, e := range group {
			d, rel, err := e.beneath()
			if err != nil {
				continue
			}
			r := openRoot(d)
			if r == nil {
				continue
			}
			digest, ino, dev := hashBeneath(r, rel)
			if digest == "" {
				continue
			}
			if _, seen := byHash[digest]; !seen {
				hashOrder = append(hashOrder, digest)
			}
			byHash[digest] = append(byHash[digest], hashedEntry{root: r, rel: rel, ino: ino, dev: dev})
		}
		for _, digest := range hashOrder {
			same := byHash[digest]
			if len(same) < 2 {
				continue
			}
			canonical := same[0]
			for _, dup := range same[1:] {
				if dup.ino == canonical.ino && dup.dev == canonical.dev {
					continue // already linked
				}
				if !apply {
					bytesSaved += size
					linksMade++
					continue
				}
				if dedupBeforeLink != nil {
					dedupBeforeLink()
				}
				if err := linkBeneath(canonical, dup); err != nil {
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
//
// OldImagesToRemove — the "keep the newest N by CreatedAt" selector this type
// existed to feed — was DELETED by OQ-LS3
// (docs/reference/image-retention.md#why-its-this-way): retention is
// the per-workspace current pointers, so nothing is selected by its position in
// a sort any more. Created survives because PruneOldImages still orders its
// report by it, and because it is the only timestamp that pass has.
type ImageEntry struct {
	ID      string
	Created string
}
