package packload

// embeddedcache.go decides WHERE the embedded packs' on-disk tree lives and how a process
// comes to hold one.
//
// THE CACHE TREE. One immutable tree per distinct embedded FS, named by a content hash of
// that FS, under a base directory:
//
//	<base>/<hash>/              the tree; <hash>/<pack>/ is each Pack.Root
//	<base>/<hash>/.lease        empty, 0444 — the flock every reader holds shared
//	<base>/.tmp-<hash>-<rand>/  a populate in flight
//	<base>/.bad-<hash>-<rand>/  a tree that failed verification, quarantined
//	<base>/.reap-<name>-<rand>/ a tree `yolo prune` is deleting
//
// A CONTENT hash rather than a version stamp, because unstamped dev builds with different
// trees must never share one. Populate is ATOMIC: the tree is written under a .tmp- name and
// renamed into place, so a reader sees either no tree or a whole one, and a process that
// loses the race removes its own temp dir and uses the winner's.
//
// THE BASE. paths.EmbeddedPacksDir() for a real process — under the state dir, NOT the
// jail-writable cache dir (that function states why). A go-test binary run by cmd/go uses a
// directory beside itself inside cmd/go's $WORK instead, which cmd/go deletes when the run
// ends, so the unit suite neither leaks into TMPDIR nor writes the developer's real home.
// The base is resolved through its symlinks once, where it is minted, so Pack.Root, the
// lease inode and what a reaper probes stay one directory for the process's whole life even
// if a link on the way (macos-user's ~/.local, re-pointed by every launch) moves later.
//
// THE FALLBACK. When the base is unresolvable or unwritable, or its tree cannot be trusted,
// the process takes the pre-cache shape: its own tree in TMPDIR, deleted by ReleaseEmbedded. That
// tree carries a lease too, so one whose owner was SIGKILLed is reaped by the next fallback
// materialization (sweepDeadFallbacks) or by `yolo prune`.
//
// LEFTOVERS IN THE BASE. A populate killed mid-write leaves a .tmp- dir, a quarantine leaves a
// .bad- one, a prune killed mid-delete a .reap- one. Every process that loads from the base
// sweeps those of them no process holds (sweepDeadCacheEntries), by the rule `yolo prune`
// applies to the same names. Other builds' FINAL trees are left to prune alone.

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// EmbeddedTreeFormat is the first input to the tree hash.
//
// ⚠ BUMP IT whenever what a populate WRITES changes without the embedded FS changing: the
// modes (sealEmbeddedTree), the layout (one dir per pack, the lease name), anything
// MaterializeEmbedded's copy step does differently. The hash names the tree by its INPUT;
// without the bump a new build whose FS hashes the same silently adopts a tree written the
// old way. TestEmbeddedHashCoversTheFormatConstant pins that the constant is hashed at all;
// only review can pin that it was bumped.
const EmbeddedTreeFormat = "yolo-embedded-packs/v1"

// Age floors. Every non-current entry a reaper considers must be at least embeddedReapFloor
// old — a fallback tree is created and leased within microseconds, but a populate's .tmp-
// dir is written for milliseconds before its lease exists on some paths, and the floor makes
// "just created" never look like "abandoned". An entry with NO lease at all is reaped only
// past embeddedUnleasedFloor, since its owner cannot vouch for it either way.
const (
	embeddedReapFloor     = 10 * time.Minute
	embeddedUnleasedFloor = time.Hour
	embeddedAdoptTries    = 3
)

var (
	// cacheOverrideSet/cacheOverrideDir are OverrideEmbeddedCacheDir's state.
	cacheOverrideSet bool
	cacheOverrideDir string

	embeddedHashMemo *embeddedHashResult

	// embeddedBeforeRename is a test seam: run between a populate's materialize and its
	// rename, which is exactly the window a racing process lands in.
	embeddedBeforeRename func(tmp string)
)

type embeddedHashResult struct {
	sum string
	err error
}

// EmbeddedHash returns the content hash naming this build's cache tree, computed once per
// process on first ask. false when no embedded FS is registered (a binary that does not link
// internal/packreg) or the FS could not be read — a reaper must then treat EVERY tree as
// possibly current.
func EmbeddedHash() (string, bool) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	sum, err := embeddedHashLocked()
	return sum, err == nil
}

func embeddedHashLocked() (string, error) {
	if embeddedFS == nil {
		return "", errors.New("no embedded packs registered")
	}
	if embeddedHashMemo == nil {
		sum, err := hashEmbeddedFS(embeddedFS, EmbeddedTreeFormat)
		embeddedHashMemo = &embeddedHashResult{sum: sum, err: err}
	}
	return embeddedHashMemo.sum, embeddedHashMemo.err
}

// hashEmbeddedFS hashes the WHOLE of f — every path and every byte — as a framed stream, so
// no two distinct trees can produce the same input: the format string, then for each entry
// of fs.WalkDir (lexical order) a type byte, the length-prefixed path and, for a file, the
// length-prefixed content. The name is the first 16 bytes of the sha256, as hex.
func hashEmbeddedFS(f fs.FS, format string) (string, error) {
	h := sha256.New()
	writeFrame(h, []byte(format))
	err := fs.WalkDir(f, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			h.Write([]byte{'d'})
			writeFrame(h, []byte(p))
		case d.Type().IsRegular():
			data, rerr := fs.ReadFile(f, p)
			if rerr != nil {
				return rerr
			}
			h.Write([]byte{'f'})
			writeFrame(h, []byte(p))
			writeFrame(h, data)
		default:
			return fmt.Errorf("%s: unsupported entry type %v", p, d.Type())
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
}

func writeFrame(h hash.Hash, b []byte) {
	var n [binary.MaxVarintLen64]byte
	h.Write(n[:binary.PutUvarint(n[:], uint64(len(b)))])
	h.Write(b)
}

// EmbeddedCacheDir is the base directory this process would keep its cache tree under, or
// "" when it would take the per-process fallback instead. `yolo prune` reads it to know
// which directory is yolo's.
func EmbeddedCacheDir() string {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	base, _ := embeddedCacheBaseLocked()
	return base
}

// OverrideEmbeddedCacheDir points this process's cache base at dir ("" forces the fallback)
// and returns the restore. Both directions release the current tree first, so the next
// Embedded() loads from the new location rather than handing back packs from the old one.
// A TEST SEAM: production resolves the base from the home, never from a caller.
func OverrideEmbeddedCacheDir(dir string) (restore func()) {
	embeddedMu.Lock()
	defer embeddedMu.Unlock()
	releaseEmbeddedLocked()
	prevSet, prevDir := cacheOverrideSet, cacheOverrideDir
	cacheOverrideSet, cacheOverrideDir = true, dir
	return func() {
		embeddedMu.Lock()
		defer embeddedMu.Unlock()
		releaseEmbeddedLocked()
		cacheOverrideSet, cacheOverrideDir = prevSet, prevDir
	}
}

// embeddedCacheBaseLocked resolves the base: an override, else the go-test $WORK location,
// else the state dir. "" means fallback. strict means the base's PARENT must already exist:
// it is set for the $WORK location, whose parent is cmd/go's to create and to delete.
func embeddedCacheBaseLocked() (base string, strict bool) {
	if cacheOverrideSet {
		return cacheOverrideDir, false
	}
	if dir, isTest := testBinaryCacheDir(); isTest {
		return dir, dir != ""
	}
	return paths.EmbeddedPacksDir(), false
}

// testBinaryCacheDir recognises a go test binary and, for one, answers the only base it may
// use: a directory beside the binary when that binary sits in cmd/go's $WORK
// (…/go-build<N>/b<M>/<pkg>.test), which cmd/go removes when the run ends — or when kept by
// `go test -work`, keeps along with the rest of $WORK. Any other test binary (`go test -c`
// run by hand, one renamed, delve's) gets "" and takes the per-process fallback; nothing
// releases that at a test binary's exit, so it stays in TMPDIR until the next fallback's
// sweep or `yolo prune` reaps it (both once it is embeddedReapFloor old and its lease free).
// A test binary NEVER uses HOME: the unit suite must not write the developer's real state
// dir.
//
// Two signals, either sufficient. The NAME: cmd/go builds every test binary as <pkg>.test.
// And the FLAGS: package testing registers `test.v` on flag.CommandLine in testing.Init,
// which the generated test main runs before any test — so a test binary under any name is
// recognised by the time anything asks for a pack (nothing does at package init). Not
// testing.Testing(), which would link package testing into every shipped binary; package
// flag is linked already by most of them, and no shipped binary registers a `test.` flag.
func testBinaryCacheDir() (string, bool) {
	exe, err := os.Executable()
	if err != nil {
		exe = os.Args[0]
	}
	return testBinaryCacheDirFor(exe, flag.Lookup("test.v") != nil)
}

// testBinaryCacheDirFor is testBinaryCacheDir over an explicit exe path and "package testing
// registered its flags", so both signals can be pinned without renaming the running binary.
func testBinaryCacheDirFor(exe string, testingFlags bool) (string, bool) {
	named := strings.HasSuffix(filepath.Base(exe), ".test")
	if !named {
		return "", testingFlags
	}
	bdir := filepath.Dir(exe)
	if strings.HasPrefix(filepath.Base(filepath.Dir(bdir)), "go-build") {
		return filepath.Join(bdir, "embedded-packs"), true
	}
	return "", true
}

// ensureEmbeddedBase creates base and returns it resolved through its symlinks.
//
// Strict (the $WORK base) creates base ALONE, never its parents: a test binary that outlives
// cmd/go — a self-exec'd daemon, say — would otherwise recreate the deleted $WORK/b<N> the
// first time it read a pack, and nothing deletes that once cmd/go has gone. Its populate
// fails instead, and it takes the fallback.
func ensureEmbeddedBase(base string, strict bool) (string, error) {
	var err error
	if strict {
		err = os.Mkdir(base, 0o755)
		if errors.Is(err, fs.ErrExist) {
			err = nil
		}
	} else {
		err = os.MkdirAll(base, 0o755)
	}
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(base)
}

// loadFromCacheLocked adopts (or populates, then adopts) base/<hash>. false sends the caller
// to the fallback; it leaves no state behind.
func loadFromCacheLocked(base string, strict bool) bool {
	sum, err := embeddedHashLocked()
	if err != nil {
		return false
	}
	if base, err = ensureEmbeddedBase(base, strict); err != nil {
		return false
	}
	if !loadCacheTreeLocked(base, sum) {
		return false
	}
	sweepDeadCacheEntries(base, time.Now())
	return true
}

// loadCacheTreeLocked is loadFromCacheLocked's adopt/populate loop over an existing,
// resolved base.
func loadCacheTreeLocked(base, sum string) bool {
	final := filepath.Join(base, sum)
	quarantined := false
	for attempt := 0; attempt < embeddedAdoptTries; attempt++ {
		if _, err := os.Lstat(final); errors.Is(err, fs.ErrNotExist) {
			lease, outcome := populateEmbedded(embeddedFS, base, sum, final)
			switch outcome {
			case populateDone:
				// We wrote it and verified it before the rename, and the lease carried over
				// with the rename (flock is per inode).
				takeCacheTreeLocked(final, lease)
				return true
			case populateLost:
				continue // someone else's tree is in place: adopt it
			default:
				return false
			}
		}
		lease, outcome := adoptEmbedded(embeddedFS, final)
		switch outcome {
		case adoptOK:
			takeCacheTreeLocked(final, lease)
			return true
		case adoptRetry:
			continue
		case adoptUntrusted:
			if quarantined {
				return false
			}
			quarantined = true
			if !quarantineEmbedded(base, sum, final) {
				return false
			}
		default:
			return false
		}
	}
	return false
}

// takeCacheTreeLocked loads the packs out of an adopted tree. LoadDir problems are reported
// exactly as a fallback materialization reports them — an empty set plus the problems — and
// the tree is KEPT: it is a faithful copy of what the binary ships, so the problems are the
// binary's, and every other process of this build would find the same ones.
func takeCacheTreeLocked(final string, lease *os.File) {
	embeddedRoot = final
	embeddedFallback = false
	embeddedLease = lease
	packs, problems := loadEmbeddedPacks(embeddedFS, final)
	if len(problems) > 0 {
		embeddedProblems = problems
		return
	}
	embeddedPacks = packs
}

type populateOutcome int

const (
	populateFailed populateOutcome = iota
	populateDone
	populateLost
)

// populateEmbedded writes the tree under a fresh .tmp- name beside final (base must exist)
// and renames it into place. On populateDone the returned lease (possibly nil: no flock here)
// is on final/.lease. Every other outcome has removed the temp dir and returns no lease.
func populateEmbedded(f fs.FS, base, sum, final string) (*os.File, populateOutcome) {
	tmp, err := os.MkdirTemp(base, ".tmp-"+sum+"-")
	if err != nil {
		return nil, populateFailed
	}
	lease, err := createLease(filepath.Join(tmp, EmbeddedLeaseName))
	abandon := func() {
		if lease != nil {
			_ = lease.Close()
		}
		_ = os.RemoveAll(tmp)
	}
	if err != nil {
		abandon()
		return nil, populateFailed
	}
	if err := copyEmbeddedPacks(f, tmp); err != nil {
		abandon()
		return nil, populateFailed
	}
	if err := sealEmbeddedTree(tmp); err != nil {
		abandon()
		return nil, populateFailed
	}
	// VERIFIED BEFORE IT IS PUBLISHED, by the same check an adopter runs. A filesystem that
	// cannot hold a faithful copy (a case-insensitive volume folding two paths, one that
	// sprinkles AppleDouble files) would otherwise publish a tree every LATER process
	// judges untrusted and quarantines — one ~250 KB .bad- dir per process, forever. A tree
	// this process cannot write faithfully is nobody's: it takes the fallback instead.
	if err := verifyEmbeddedTree(f, tmp); err != nil {
		abandon()
		return nil, populateFailed
	}
	if embeddedBeforeRename != nil {
		embeddedBeforeRename(tmp)
	}
	if err := os.Rename(tmp, final); err != nil {
		// LOST THE RACE: a rename onto an existing non-empty directory fails EEXIST or
		// ENOTEMPTY (both are fs.ErrExist), and that is proof enough that someone else's
		// tree was there. Decided from the error, not from a later look at final: that
		// tree may be reaped between the rename and the look, and the adopt loop already
		// retries a final that has vanished.
		lost := renameLost(err, final)
		abandon()
		if lost {
			return nil, populateLost
		}
		return nil, populateFailed
	}
	return lease, populateDone
}

// renameLost reports whether a failed rename of a populate onto final means another process
// won the race: the error says final existed (fs.ErrExist covers both EEXIST and ENOTEMPTY),
// or failing that, a directory is at final now.
func renameLost(err error, final string) bool {
	if errors.Is(err, fs.ErrExist) {
		return true
	}
	fi, lerr := os.Lstat(final)
	return lerr == nil && fi.IsDir()
}

// copyEmbeddedPacks writes every pack of f under dest, stopping at the first error — unlike
// MaterializeEmbedded, which reports per pack, because a cache tree is all or nothing.
func copyEmbeddedPacks(f fs.FS, dest string) error {
	names, err := embeddedPackDirs(f)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := copyEmbeddedTree(f, name, filepath.Join(dest, name)); err != nil {
			return fmt.Errorf("embedded pack %s: %w", name, err)
		}
	}
	return nil
}

// sealEmbeddedTree makes a populated tree READ-ONLY: files 0444, directories 0755.
//
// Directories stay owner-WRITABLE on purpose. Unlinking needs write permission on the parent,
// so a 0555 directory would make the tree undeletable by `rm -rf ~/.local/share/yolo-jail`,
// by `yolo prune`, by a race loser's cleanup and by t.TempDir's — for no protection: these
// modes are not the boundary (root ignores them), the location is. What they buy is that a
// stray write by a tool reading a Pack.Root fails instead of silently editing every other
// process's copy.
//
// Applied HERE and never in copyEmbeddedTree, which also feeds MaterializeEmbedded's callers
// with their own trees (stagePacks' scratch, whose copies must stay writable).
func sealEmbeddedTree(root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return os.Chmod(p, 0o444)
	})
}

type adoptOutcome int

const (
	adoptFailed adoptOutcome = iota
	adoptOK
	adoptRetry
	adoptUntrusted
)

// adoptEmbedded takes a shared lease on an existing final tree and verifies it.
//
// THE TRUST RULE for a tree found on disk, since the process that wrote it may have died
// mid-way or something may have edited it since:
//
//   - final must be a real directory (not a symlink) owned by this euid — anything else is
//     not ours to read with a shipped pack's authority, and the process falls back rather
//     than touching it;
//   - it must contain exactly the files and directories the embedded FS says, each file a
//     regular file with identical bytes, plus the lease — a missing, extra, retyped or
//     edited entry makes it UNTRUSTED, and the caller quarantines it and repopulates once.
//
// A partial tree cannot reach the final name through populate (the rename is atomic), so an
// untrusted tree is tampering or disk damage; either way the answer is the same.
//
// ONLY A MISMATCH quarantines. A verify that could not READ the tree (EMFILE, EIO, a
// permission) proves nothing about it, and quarantining is a rename out from under every
// other process holding it — so a read error is adoptFailed: this process falls back and
// the tree stays where the others are using it.
func adoptEmbedded(f fs.FS, final string) (*os.File, adoptOutcome) {
	fi, err := os.Lstat(final)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, adoptRetry
		}
		return nil, adoptFailed
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() || !ownedByEuid(fi) {
		return nil, adoptFailed
	}
	leasePath := filepath.Join(final, EmbeddedLeaseName)
	lease, err := holdLease(leasePath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, adoptFailed
		}
		// No lease: moved away under us (prune's rename) — retry — or a tree that never had
		// one, which is untrusted.
		if again, lerr := os.Lstat(final); lerr == nil && os.SameFile(fi, again) {
			return nil, adoptUntrusted
		}
		return nil, adoptRetry
	}
	if lease != nil {
		// The inode we locked must still be the one at the final name: a reaper holds the
		// exclusive lock across its rename, so a shared lock granted after it names a tree
		// that is on its way out.
		held, herr := lease.Stat()
		cur, cerr := os.Stat(leasePath)
		if herr != nil || cerr != nil || !os.SameFile(held, cur) {
			_ = lease.Close()
			return nil, adoptRetry
		}
	}
	if err := verifyEmbeddedTree(f, final); err != nil {
		if lease != nil {
			_ = lease.Close()
		}
		var mm treeMismatch
		if errors.As(err, &mm) {
			return nil, adoptUntrusted
		}
		return nil, adoptFailed
	}
	return lease, adoptOK
}

// treeMismatch is a verify finding about the tree's CONTENT — an entry missing, extra,
// retyped or byte-different — as opposed to an error reading it.
type treeMismatch struct{ msg string }

func (m treeMismatch) Error() string { return m.msg }

func mismatchf(format string, a ...any) error { return treeMismatch{fmt.Sprintf(format, a...)} }

// verifyEmbeddedTree checks root holds exactly what copyEmbeddedPacks would write from f. A
// treeMismatch error is a finding about the tree; any other error is a failure to read it.
func verifyEmbeddedTree(f fs.FS, root string) error {
	names, err := embeddedPackDirs(f)
	if err != nil {
		return err
	}
	want := map[string]bool{} // slash path -> isDir
	for _, n := range names {
		if err := fs.WalkDir(f, n, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			want[p] = d.IsDir()
			return nil
		}); err != nil {
			return err
		}
	}
	seen := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == EmbeddedLeaseName {
			if !d.Type().IsRegular() {
				return mismatchf("%s is not a regular file", rel)
			}
			return nil
		}
		isDir, ok := want[rel]
		if !ok {
			return mismatchf("unexpected entry %s", rel)
		}
		seen++
		if isDir {
			if !d.IsDir() {
				return mismatchf("%s is not a directory", rel)
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return mismatchf("%s is not a regular file", rel)
		}
		got, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		exp, rerr := fs.ReadFile(f, rel)
		if rerr != nil {
			return rerr
		}
		if !bytes.Equal(got, exp) {
			return mismatchf("%s differs from the embedded copy", rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if seen != len(want) {
		return mismatchf("tree has %d of %d embedded entries", seen, len(want))
	}
	return nil
}

// quarantineEmbedded renames an untrusted final tree aside (atomically, so no reader adopts
// it half-deleted) for `yolo prune` to reap.
func quarantineEmbedded(base, sum, final string) bool {
	bad := filepath.Join(base, fmt.Sprintf(".bad-%s-%d-%d", sum, os.Getpid(), time.Now().UnixNano()))
	return os.Rename(final, bad) == nil
}

func ownedByEuid(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

// sweepDeadCacheEntries removes the leftovers in a cache base that no process holds: .tmp-
// (a populate that died mid-write), .bad- (a quarantined tree) and .reap- (a prune that died
// mid-delete) dirs this euid owns, at least embeddedReapFloor old with a lease this call can
// lock exclusively, or past embeddedUnleasedFloor with no lease at all — the rule `yolo
// prune` applies to the same names (internal/prune/embeddedtrees.go). Held, unprobeable,
// another user's or too young is kept. FINAL trees are never touched here: whether another
// build's tree is still wanted is prune's question, asked with the current build's hash.
//
// None of these names is ever adopted (only <base>/<hash> is), so removing one cannot pull a
// Pack.Root out from under a reader that did not hold its lease.
func sweepDeadCacheEntries(base string, now time.Time) {
	sweepLeasedDirs(base, now, func(name string) bool {
		return strings.HasPrefix(name, ".tmp-") || strings.HasPrefix(name, ".bad-") ||
			strings.HasPrefix(name, ".reap-")
	})
}

// loadFallbackLocked materializes a per-process tree in TMPDIR, leased, after reaping any
// sibling whose owner died without releasing its own.
func loadFallbackLocked() {
	sweepDeadFallbacks(os.TempDir(), time.Now())
	dir, err := os.MkdirTemp("", EmbeddedFallbackPrefix)
	if err != nil {
		embeddedProblems = []string{"embedded packs: " + err.Error()}
		return
	}
	lease, err := createLease(filepath.Join(dir, EmbeddedLeaseName))
	if err != nil {
		_ = os.RemoveAll(dir)
		embeddedProblems = []string{"embedded packs: " + err.Error()}
		return
	}
	packs, problems := MaterializeEmbedded(embeddedFS, dir)
	if len(problems) > 0 {
		// The tree is unusable, so it is removed HERE rather than waiting for the exit
		// path: nothing is going to read a Root out of a set no caller receives.
		_ = os.RemoveAll(dir)
		if lease != nil {
			_ = lease.Close()
		}
		embeddedProblems = problems
		return
	}
	embeddedRoot = dir
	embeddedFallback = true
	embeddedLease = lease
	embeddedPacks = packs
}

// sweepDeadFallbacks removes EmbeddedFallbackPrefix dirs in dir that this euid owns and no
// process holds: at least embeddedReapFloor old with a lease this call can lock exclusively,
// or past embeddedUnleasedFloor with no lease file at all (its creator writes the lease
// immediately after the directory, so an unleased one that old was never finished).
// Everything else — held, unprobeable, another user's, too young — is kept. It never touches
// a LegacyEmbeddedPrefix dir: those carry no lease, so nothing here can prove one dead.
//
// This is what makes the fallback leak-proof against the exits no code runs on (SIGKILL, the
// OOM killer): the kernel drops the dead owner's lock, and the next fallback reaps the tree.
func sweepDeadFallbacks(dir string, now time.Time) {
	sweepLeasedDirs(dir, now, func(name string) bool {
		return strings.HasPrefix(name, EmbeddedFallbackPrefix)
	})
}

// sweepLeasedDirs is the two sweeps' shared body: every entry of dir that match names, by
// the age-and-lease rule their doc comments state.
func sweepLeasedDirs(dir string, now time.Time, match func(name string) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !match(e.Name()) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 || !ownedByEuid(fi) {
			continue
		}
		age := now.Sub(fi.ModTime())
		if age < embeddedReapFloor {
			continue
		}
		state, unlock, _ := ProbeLease(p)
		switch {
		case state == LeaseFree:
			_ = os.RemoveAll(p)
		case state == LeaseAbsent && age >= embeddedUnleasedFloor:
			_ = os.RemoveAll(p)
		}
		unlock()
	}
}
