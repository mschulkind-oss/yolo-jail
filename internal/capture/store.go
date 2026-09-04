// Package capture is the machine-local content-addressed store for INSTALL CAPTURES —
// program-delivery.md §6.3's *"run the installer once, capture the delta, and from then on the
// capture is the package"*.
//
// store.go is the store itself: the layout, admission, offline resolution, and the completion
// marker that makes a half-written entry detectable. Nothing here runs an installer or knows what
// a pack is; it is handed a finished tree and decides where it lives and under what name.
//
// # Layout
//
//	<CapturesDir>/staging/<id>/            scratch for a capture IN FLIGHT
//	<CapturesDir>/entries/<key>/
//	    tree/                              the captured delta, UNPACKED
//	    .yolo-capture-complete             written LAST
//
// The entry root holds yolo's metadata and `tree/` holds the vendor's bytes, so materialization
// can hardlink the tree WHOLESALE without a list of yolo's own files to skip — and slice 2's
// manifest has an obvious place to live that is not inside the thing it describes.
//
// The tree is stored UNPACKED, not as a tar. The design's pipeline line reads "delta → tar+hash",
// but its own *materialize* verb reads "unpack/hardlink", and hardlinking is the point: you cannot
// hardlink out of a tarball, and a tar would have to be unpacked per workspace, which is the
// per-workspace copy this store exists to delete. (There is also no tar code in this repo to reuse.)
//
// # Three properties copied from packsrc.Store
//
// internal/packsrc/store.go is the same shape one problem over — a machine-wide store of fetched
// third-party content that a launch must resolve offline — and its discipline is copied here
// deliberately rather than re-invented:
//
//  1. TWO STAGES. Nothing is ever built inside entries/. A capture is assembled under staging/ and
//     ADMITTED by os.Rename. staging/ lives INSIDE the store on purpose: a rename is atomic and
//     free only within one filesystem, and these entries are gigabytes (claude: 1.2 GB, measured
//     2026-09-03). Admit refuses a staged tree from anywhere else rather than silently copying.
//  2. THE COMPLETION MARKER IS WRITTEN LAST. A process killed mid-admit leaves a tree that reads
//     as ABSENT — Resolve checks the marker and nothing else — and the next Admit clears it and
//     redoes it. A partial install materialized into a home would be worse than no capture at all.
//  3. RESOLVE IS STRICTLY OFFLINE. It never fetches, and a miss is an error naming the act that
//     would fix it. The download is precisely the thing the store exists to have already done.
//
// # And one property that is this store's own
//
// ENTRY FILES ARE CHMOD'D READ-ONLY AT ADMIT, and files are NEVER deduped within the store. A
// materialized file is a hardlink to the entry's inode: it is not a copy of the running program's
// bytes, it IS them. An installer or self-updater that opened one for write would corrupt every
// workspace on the machine at once, silently. Dropping `w` does not make that impossible — an
// owner can chmod it back — but it turns a silent corruption into an error at the moment of the
// write. Directories keep their write bit, because GC has to be able to unlink the entry.
package capture

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/treedigest"
)

const (
	entriesLeaf = "entries"
	stagingLeaf = "staging"
	treeLeaf    = "tree"

	// completeMarker is the last thing Admit writes, and the only thing Resolve reads to
	// decide an entry exists. Named after packsrc's .yolo-pack-complete, which does the same
	// job for a checked-out pack tree.
	completeMarker = ".yolo-capture-complete"
)

// ErrNotCaptured reports that the store holds no COMPLETE entry for a key — either nothing was
// ever admitted under it, or an admit was interrupted before its marker landed. The two are
// deliberately the same answer: a torn entry is not a degraded entry, it is an absent one.
//
// It is a sentinel so the launcher's materialize branch can fall through to today's download on a
// miss (program-delivery.md §10 step four is additive; the fallback is not removable without a
// ruling), while a caller that requires a capture can report the wrapped message as-is.
var ErrNotCaptured = errors.New("no capture entry in the store")

// Store is an install-capture store rooted at Dir (paths.CapturesDir()).
//
// Constructed by the caller with an explicit Dir, like packsrc.Store, so a test never has to
// arrange for the process $HOME to be somewhere safe to write gigabytes.
type Store struct {
	// Dir is the store root. Every path this type touches is under it — including the
	// staging area, which is what makes admission a rename.
	Dir string
}

// Entry is an admitted capture.
type Entry struct {
	// Key is the content address: the first 16 hex chars of sha256 over the tree's canonical
	// digest (see Key).
	Key string
	// Root is <Dir>/entries/<key> — the entry directory, holding yolo's metadata.
	Root string
	// Tree is <Root>/tree — the captured delta as an ordinary directory tree, ready to be
	// hardlinked into a home. Its files are read-only; see the package comment.
	Tree string
	// Digest is the CANONICAL TREE DIGEST the Key was computed from — the full
	// treedigest.Of output, of which Key is hex(sha256(·))[:16].
	//
	// SET BY ADMIT, EMPTY FROM RESOLVE, and that asymmetry is the honest one: admission
	// walks the tree to compute the key, so it has the digest in hand for free, while a
	// resolve reads one marker file and re-deriving the digest there would mean walking
	// gigabytes to answer a question nothing on that path asked. A caller that needs it
	// after a resolve must ask for it (treedigest.Of) and know what it is paying.
	Digest string
}

// Key is the store key for a canonical digest: hex(sha256(x))[:16].
//
// This is image.ImageStoreKey's convention, reused rather than re-decided — one key shape across
// the repo's content-addressed dirs means a human reading `entries/3f2a…` and `roots/3f2a…` does
// not have to ask whether they mean the same kind of thing.
func Key(digest string) string { return DigestHash(digest)[:16] }

// DigestHash is the FULL sha256 of a canonical tree digest, hex-encoded — the 64-character
// string of which Key is the first 16.
//
// Both are written into a capture receipt: Key is what a human looks up under entries/, and
// this is the whole hash, so neither has to be re-derived from the other to be checked.
func DigestHash(digest string) string {
	sum := sha256.Sum256([]byte(digest))
	return hex.EncodeToString(sum[:])
}

// KeyForTree is the key a tree would be admitted under: Key over the tree's canonical digest
// (internal/treedigest — relative paths, entry kinds, exec bits, file bytes, and symlink targets
// read by readlink rather than followed).
//
// The canonical digest IS the "file manifest" §6.3's receipt tuple names, hashed rather than
// stored: two installs that produce byte-identical trees are one capture, and an install whose
// binary merely lost its exec bit is a different one.
func KeyForTree(root string) (string, error) {
	d, err := treedigest.Of(root)
	if err != nil {
		return "", err
	}
	return Key(d), nil
}

// EntryDir is the directory an entry with this key occupies, whether or not it exists.
func (s *Store) EntryDir(key string) string { return filepath.Join(s.Dir, entriesLeaf, key) }

// StagingDir is the scratch directory for a capture in flight, whether or not it exists.
func (s *Store) StagingDir(id string) string { return filepath.Join(s.Dir, stagingLeaf, id) }

// Stage creates an EMPTY scratch directory for a capture in flight and returns it.
//
// Any leftover from an interrupted capture under the same id is cleared first: a redo that
// inherited a dead run's files would admit an entry whose key describes a tree no single installer
// run ever produced.
//
// The returned path is inside the store, which is the whole reason this method exists rather than
// a caller reaching for os.MkdirTemp: see the package comment on admission being a rename.
func (s *Store) Stage(id string) (string, error) {
	if err := validSegment(id); err != nil {
		return "", fmt.Errorf("capture staging id: %w", err)
	}
	dir := s.StagingDir(id)
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Admit moves a finished tree out of staging and into the store under the key its own content
// dictates, returning the entry. The staged tree is consumed either way — renamed away on a fresh
// admit, removed on a repeat.
//
// The caller does not choose the key. Passing one in would make it possible to file a tree under
// an address that does not describe it, and every later slice (materialize, GC) trusts that the
// address and the bytes agree.
//
// Idempotent, and idempotent WITHOUT touching the existing entry: a re-admit of an identical tree
// returns the entry already on disk rather than rebuilding it, because a workspace may be holding
// one of those inodes by hardlink and swapping it out would be a silent version change under a
// running program.
func (s *Store) Admit(staged string) (*Entry, error) {
	return s.admit(staged, staged, false)
}

// AdmitEntry admits a whole ENTRY-SHAPED staging directory — `<staged>/tree` plus whatever
// metadata sits beside it (the capture manifest, and the receipt a later act appends) — as
// one rename, returning the entry.
//
// It exists because Admit alone cannot keep the completion marker's promise for anything
// but the tree. Admit renames `tree/` in and writes the marker LAST, so a caller that then
// moved the manifest up beside it would have a window in which the entry reads COMPLETE and
// its manifest is not there yet — small, harmless today (nothing on the materialize path
// reads the manifest), and exactly the kind of "it cannot happen in practice" that the
// two-stage discipline exists to not rely on. Moving the whole directory closes it by
// construction: everything the driver produced arrives together or not at all.
//
// The key is still computed from the TREE and only the tree. Metadata beside it must not
// change the content address, or a manifest whose Home string differed would file identical
// vendor bytes under a second key.
func (s *Store) AdmitEntry(staged string) (*Entry, error) {
	tree := TreeDir(staged)
	if fi, err := os.Lstat(tree); err != nil {
		return nil, fmt.Errorf("capture admit: %s is not entry-shaped (no tree/): %w", staged, err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("capture admit: %s is not a directory", tree)
	}
	return s.admit(staged, tree, true)
}

// admit is the shared body of Admit and AdmitEntry.
//
// `staged` is the directory RENAMED into the store, `digestOf` is the tree whose content
// decides the key, and `whole` says which of the two shapes this is: for Admit they are the
// same path and the tree lands INSIDE a freshly-made entry dir; for AdmitEntry the staged
// dir already IS the entry (tree plus metadata) and becomes the entry dir itself. That is
// the whole difference between the two.
func (s *Store) admit(staged, digestOf string, whole bool) (*Entry, error) {
	stagingRoot := filepath.Join(s.Dir, stagingLeaf)
	if !within(stagingRoot, staged) {
		return nil, fmt.Errorf("capture admit: staged tree %s is not under %s — admission is an "+
			"os.Rename, so a scratch tree outside the store would silently become a full copy "+
			"of the bytes the store exists to stop copying (use Store.Stage)", staged, stagingRoot)
	}
	if fi, err := os.Lstat(digestOf); err != nil {
		return nil, fmt.Errorf("capture admit: %w", err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("capture admit: staged tree %s is not a directory", digestOf)
	}
	digest, err := treedigest.Of(digestOf)
	if err != nil {
		return nil, fmt.Errorf("capture admit: digesting %s: %w", digestOf, err)
	}
	key := Key(digest)
	entry := s.EntryDir(key)
	if _, err := os.Stat(filepath.Join(entry, completeMarker)); err == nil {
		// Already admitted, and the key says the bytes are the same. Drop the duplicate
		// rather than re-linking it in: the entry's inodes may already be materialized.
		if err := os.RemoveAll(staged); err != nil {
			return nil, err
		}
		return &Entry{Key: key, Root: entry, Tree: TreeDir(entry), Digest: digest}, nil
	}
	// Absent, or a previous admit died partway. Start clean: a torn entry silently kept would
	// be an installer's half-written state presented as a package.
	if err := os.RemoveAll(entry); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		return nil, err
	}
	dest := entry
	if !whole {
		// A bare tree: the entry directory is ours to create, and the tree lands inside it.
		if err := os.MkdirAll(entry, 0o755); err != nil {
			return nil, err
		}
		dest = TreeDir(entry)
	}
	if err := os.Rename(staged, dest); err != nil {
		return nil, fmt.Errorf("capture admit: moving %s into the store: %w", staged, err)
	}
	tree := TreeDir(entry)
	if err := freezeTree(tree); err != nil {
		return nil, fmt.Errorf("capture admit: making %s read-only: %w", tree, err)
	}
	// LAST, always: everything above is redoable, and the marker is the only claim that it
	// all happened.
	if err := os.WriteFile(filepath.Join(entry, completeMarker), []byte(key+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &Entry{Key: key, Root: entry, Tree: tree, Digest: digest}, nil
}

// Resolve returns the COMPLETE entry for a key, using only what is already on disk.
//
// Strictly offline, and it never fetches: this is the materialize path, which runs per workspace
// and must not turn into a surprise download halfway through a launch. A miss — nothing admitted,
// or an admit interrupted before its marker — is ErrNotCaptured, wrapped in a message naming the
// act that would fix it.
//
// The marker is the ONLY thing checked. That is the point of writing it last: a second check
// against the tree would quietly say the marker is not trusted, and then it would not be.
func (s *Store) Resolve(key string) (*Entry, error) {
	entry := s.EntryDir(key)
	if _, err := os.Stat(filepath.Join(entry, completeMarker)); err != nil {
		return nil, fmt.Errorf("capture %s is not in the store at %s — run `yolo capture <bin>` "+
			"to make one: %w", key, s.Dir, ErrNotCaptured)
	}
	return &Entry{Key: key, Root: entry, Tree: TreeDir(entry)}, nil
}

// freezeTree drops the write bits from every REGULAR FILE in the tree.
//
// Files only. Directories keep `w` so the entry stays removable (GC unlinks whole entries), and
// symlinks are skipped entirely — os.Chmod follows them, so chmod-ing a link would reach through
// to whatever it points at, which for a captured installer is frequently a file outside the tree.
func freezeTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		return os.Chmod(path, info.Mode().Perm()&^0o222)
	})
}

// within reports whether path is dir itself or lies beneath it, lexically.
func within(dir, path string) bool {
	dir = filepath.Clean(dir)
	path = filepath.Clean(path)
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// validSegment rejects anything that is not a single, non-traversing path component, so a
// caller-supplied id can never address a directory outside the staging area.
func validSegment(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsRune(id, filepath.Separator) ||
		strings.Contains(id, "/") {
		return fmt.Errorf("%q must be a single path segment", id)
	}
	return nil
}
