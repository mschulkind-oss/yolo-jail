package capture

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// materialize.go is the OTHER half of install-capture: putting an admitted entry into a
// jail's home, offline, without paying for the bytes again.
//
// # The mechanism, and why it is not the one the design named
//
// docs/design/program-delivery.md §6.3 wrote the pipeline's second verb as
// *"materialize (per jail, offline): unpack/hardlink the capture"*, and the hardlink half
// of that cannot work from inside a jail. The store is a HOST directory; a jail reaches it
// only through a bind mount; and `link(2)` compares the MOUNT, not the device — a hardlink
// from one bind of a filesystem into another bind of the SAME filesystem returns EXDEV.
// Mounting the store somewhere else does not help, because wherever it is mounted is one
// more mount. (MEASURED 2026-09-04 in this jail, and again in a real podman container
// against the exact two mounts a materialize uses; see clone_linux.go.)
//
// REFLINK is the way through, and it is the one primitive whose predicate is the FILESYSTEM
// rather than the mount: FICLONE refuses only when the two inodes have different
// superblocks, and every bind of one filesystem shares one. So the chain is three steps,
// each MEASURED at the moment it is used rather than predicted from a mount table:
//
//  1. REFLINK — O(1), shares extents, and the destination is its own inode. Works on btrfs,
//     XFS with reflink=1, and ZFS, ACROSS mounts.
//  2. HARDLINK — free to try, and it wins in the one arrangement reflink does not cover: a
//     store and a home that share a mount (a host-side materialize, or a jail whose home and
//     store arrive through one bind). Rare here, and it is second rather than first because
//     of the hazard below.
//  3. COPY — correct everywhere and expensive everywhere. ext4 has no reflink at all, so
//     this is a path real machines take, not a theoretical arm. It is LOUD, and it names the
//     filesystems that forced it, because "materialize was slow and my disk filled up" with
//     no explanation is the failure this whole subsystem exists to remove.
//
// # The hazard reflink DOWNGRADES
//
// install-capture.md's sharpest trap reads: *"a hardlinked CAS file is the running program's
// bytes"* — an installer or self-updater that opens a materialized file for WRITE corrupts
// the store and every other workspace at once, silently. That is true of a hardlink and it
// is NOT true of a reflink: a cloned file is its own inode, so a write to it copies-on-write
// and touches nothing else. The store still chmods its entry files read-only at admit
// (store.go, freezeTree) and that stays — it is what makes the hardlink arm safe, and it
// costs the reflink arm nothing. But under reflink the severity of the trap drops from
// "silent cross-workspace corruption" to "nothing"; under copy it was never there.
//
// The two arms therefore differ in ONE observable way, deliberately: a REFLINKED or COPIED
// file gets the manifest's recorded mode back (its own inode, so 0755 means 0755), while a
// HARDLINKED file keeps the store's frozen 0555, because chmod-ing it would chmod the store
// entry. A launcher's `[ -x "$REAL_BIN" ]` is satisfied either way; a vendor self-updater
// that rewrites its own binary in place succeeds on the first and fails loudly on the second,
// which is the right order of preference and is why reflink is tried first.
//
// # Not transactional, and it must not be
//
// A failed materialize leaves what it had already written. Making it atomic would mean
// staging a second complete copy of the tree and renaming it in — a per-workspace copy of
// gigabytes, which is the exact cost this subsystem exists to delete. The caller's recovery
// is the one the launcher already has: fall through to the vendor installer, which overwrites
// its own paths.

// ErrNotRelocatable reports that an entry cannot be materialized into the home it was asked
// for, because that home is not the one it was captured under.
//
// A sentinel because the answer to it is a DIFFERENT ACT — recapture under this home, or the
// relocating materialize the macos-user slice adds — never a retry. Unreachable on the
// container backends by construction: capture home and materialize home are both
// /home/agent, which is why relocation is macos-user's problem and this is the guard that
// keeps a not-yet-built rewrite from being skipped silently.
//
// See Manifest.Relocatable for the three-clause contract this implements two clauses of.
var ErrNotRelocatable = errors.New("the capture was made under a different home")

// MaterializeOptions configures putting one admitted entry into one home.
type MaterializeOptions struct {
	// Entry is the resolved store entry (Store.Resolve). Its Tree is the source and its
	// Root is where the manifest is read from.
	Entry *Entry
	// Home is the destination HOME. Every manifest path is relative to it.
	Home string
	// Stderr receives the copy-fallback report. Nil discards it — and discarding it is a
	// real choice a caller should have to make on purpose, because the report is the only
	// place a machine learns why its materialize cost a gigabyte.
	Stderr io.Writer
}

// MaterializeResult reports what was put where, and by which mechanism.
//
// The three mechanism counters are the point of the type. A caller that only learns "it
// worked" cannot tell a 3 ms reflink from a 90-second copy of the same tree, and those are
// the two outcomes this subsystem exists to distinguish.
type MaterializeResult struct {
	// Dirs, Files and Symlinks count the manifest entries realized, by kind.
	Dirs, Files, Symlinks int
	// Reflinked, Linked and Copied partition Files by the mechanism that placed them.
	Reflinked, Linked, Copied int
	// Bytes is the total size of the files placed — what a copy actually cost, and what a
	// reflink did not.
	Bytes int64
	// ReflinkRetired and LinkRetired carry the error that retired each mechanism for this
	// run, empty when it was never needed or never failed. They are the "measured, never
	// assumed" half: a reader can see WHY the chain fell through rather than inferring it
	// from a counter.
	ReflinkRetired, LinkRetired string
	// SourceFS and DestFS are the filesystems of the store and the home, named only when
	// something had to be copied — the clause the loud report owes its reader.
	SourceFS, DestFS string
}

// Mechanism names the arm that placed the bulk of the files, for a one-line report.
func (r *MaterializeResult) Mechanism() string {
	switch {
	case r.Copied > 0 && r.Copied >= r.Reflinked && r.Copied >= r.Linked:
		return "copy"
	case r.Linked > 0 && r.Linked >= r.Reflinked:
		return "hardlink"
	case r.Reflinked > 0:
		return "reflink"
	default:
		return "nothing"
	}
}

// Materialize puts an admitted entry's tree into a home.
//
// IT IS DRIVEN BY THE MANIFEST, not by a walk of the tree, and the reason is the modes. The
// store freezes entry files read-only at admit, so the tree on disk no longer remembers that
// the vendor's binary was 0755 — the manifest, written by the driver BEFORE admission, does.
// A walk would faithfully reproduce the frozen 0555 into every home; the manifest reproduces
// what the installer actually made. (It also creates parents before children for free: the
// manifest is sorted by path, and a parent's path is a prefix of its children's.)
//
// An existing destination FILE or SYMLINK is replaced; an existing DIRECTORY is left exactly
// as it is, mode included. That asymmetry is deliberate: the directories a capture names are
// shared with the rest of the home (`.local`, `.local/bin`, `.npm-global/lib`) and stomping
// their modes would be this function editing a home it was only asked to add to, while the
// files and links are the captured program's own and a stale one is what a re-materialize is
// for.
func Materialize(opts MaterializeOptions) (*MaterializeResult, error) {
	if opts.Entry == nil {
		return nil, errors.New("capture materialize: no entry")
	}
	if opts.Home == "" || !filepath.IsAbs(opts.Home) {
		return nil, fmt.Errorf("capture materialize: home %q must be an absolute path", opts.Home)
	}
	home := filepath.Clean(opts.Home)
	m, err := ReadManifest(opts.Entry.Root)
	if err != nil {
		return nil, fmt.Errorf("capture materialize: %w", err)
	}
	// THE PLATFORM GATE IS HERE AND NOT ONLY IN THE LOOKUP. Whatever chose this entry did
	// so from a receipt; the manifest is the entry's own claim about itself, and a
	// linux/arm64 tree unpacked into a linux/amd64 home is a program that exists and does
	// not run. Checking it twice costs one string compare and removes a whole class of
	// caller bug (§6.3: "captures are per-platform, and only for platforms we can run").
	if m.Platform != "" && m.Platform != Platform() {
		return nil, fmt.Errorf("capture materialize: entry %s is a %s capture and this is %s",
			opts.Entry.Key, m.Platform, Platform())
	}
	// THE RELOCATION CONTRACT (Manifest.Relocatable), two of its three clauses.
	//
	// Clause one is the whole container story and it is the fall-through below: a
	// destination home EQUAL to the capture home ignores Relocatable entirely, because
	// every absolute self-reference in the tree is still correct. Clause two is this
	// refusal. Clause three — rewrite the AbsoluteRefs when Relocatable is true — is the
	// macos-user slice's, and until it lands a relocatable entry is refused HERE with a
	// message that says which of the two refusals it is. Silently materializing a tree
	// full of references to a home that does not exist is the one outcome neither clause
	// permits.
	if m.Home != "" && filepath.Clean(m.Home) != home {
		why := "the relocating materialize is not built yet (it is the macos-user slice's)"
		if !m.Relocatable {
			why = "the capture is not relocatable"
			if len(m.NotRelocatable) > 0 {
				why += ": " + strings.Join(m.NotRelocatable, "; ")
			}
		}
		return nil, fmt.Errorf("capture materialize: entry %s was captured under %s and "+
			"cannot be materialized into %s — %s: %w",
			opts.Entry.Key, m.Home, home, why, ErrNotRelocatable)
	}

	res := &MaterializeResult{}
	ch := &chain{reflink: true, link: true}
	for _, e := range m.Entries {
		if err := placeEntry(opts.Entry.Tree, home, e, ch, res); err != nil {
			return res, fmt.Errorf("capture materialize %s: %w", e.Path, err)
		}
	}
	res.ReflinkRetired, res.LinkRetired = ch.reflinkWhy, ch.linkWhy
	if res.Copied > 0 {
		res.SourceFS, res.DestFS = fsName(opts.Entry.Tree), fsName(home)
		reportCopy(opts.Stderr, opts.Entry, home, res)
	}
	return res, nil
}

// placeEntry realizes one manifest entry under home.
func placeEntry(tree, home string, e ManifestEntry, ch *chain, res *MaterializeResult) error {
	rel := filepath.FromSlash(e.Path)
	src := filepath.Join(tree, rel)
	dst := filepath.Join(home, rel)
	switch e.Kind {
	case KindDir:
		// MkdirAll, not Mkdir: a manifest lists every ancestor it captured, but a home
		// may be missing an ancestor the manifest never captured because it already
		// existed at capture time and does not exist here.
		existed := dirExists(dst)
		if err := os.MkdirAll(dst, permOf(e, src, 0o755)); err != nil {
			return err
		}
		if !existed {
			// Mkdir's mode is masked by umask; the home gets the capture's shape.
			if err := os.Chmod(dst, permOf(e, src, 0o755)); err != nil {
				return err
			}
		}
		res.Dirs++
		return nil
	case KindSymlink:
		if err := replaceable(dst); err != nil {
			return err
		}
		if err := os.Symlink(e.Target, dst); err != nil {
			return err
		}
		res.Symlinks++
		return nil
	default:
		if err := replaceable(dst); err != nil {
			return err
		}
		if err := ch.placeFile(src, dst, permOf(e, src, 0o644)); err != nil {
			return err
		}
		res.Files++
		res.Bytes += e.Size
		switch ch.last {
		case mechReflink:
			res.Reflinked++
		case mechLink:
			res.Linked++
		default:
			res.Copied++
		}
		return nil
	}
}

// mechanism identifies which arm of the chain placed the last file.
type mechanism int

const (
	mechReflink mechanism = iota
	mechLink
	mechCopy
)

// chain is the fallback chain, with each arm's verdict remembered FOR THE RUN.
//
// Stickiness is the whole reason this is a type rather than three calls. "Measured, never
// assumed" means the first file is placed by trying reflink for real — but a store and a
// home on ext4 will refuse every single file for the same reason, and paying one failed
// ioctl per file across a 1.2 GB tree of thousands of files is a cost with no information in
// it. So a mechanism that reports "not here" (errCloneUnsupported, EXDEV) is retired, with
// the reason kept for the report; a mechanism that reports a real I/O error is NOT retired,
// because that is a fact about one file and not about the pair of filesystems.
type chain struct {
	reflink, link bool
	// reflinkWhy and linkWhy are the errors that retired each arm — reported, so a human
	// sees the measurement rather than the conclusion.
	reflinkWhy, linkWhy string
	// last is the arm that placed the most recent file.
	last mechanism
}

// placeFile puts one file at dst, trying each arm in turn.
func (c *chain) placeFile(src, dst string, perm fs.FileMode) error {
	if c.reflink {
		err := reflinkOne(src, dst, perm)
		if err == nil {
			c.last = mechReflink
			return nil
		}
		if !errors.Is(err, errCloneUnsupported) {
			return err
		}
		c.reflink = false
		c.reflinkWhy = err.Error()
	}
	if c.link {
		// NO CHMOD AFTER A HARDLINK: dst and the store entry are one inode, so a chmod
		// here would unfreeze the store's copy for every workspace at once. The
		// materialized file therefore keeps the entry's frozen mode (exec bit intact,
		// write bit dropped) — see the file comment on the two arms' one difference.
		err := hardlinkOne(src, dst)
		if err == nil {
			c.last = mechLink
			return nil
		}
		if !isCrossDevice(err) && !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EMLINK) {
			return err
		}
		c.link = false
		c.linkWhy = err.Error()
	}
	if err := copyOne(src, dst, perm); err != nil {
		return err
	}
	c.last = mechCopy
	return nil
}

// reflinkOne and hardlinkOne are the two O(1) arms, behind package vars so a test can force
// the chain down its lower steps.
//
// A SEAM RATHER THAN A FILESYSTEM FIXTURE, because the property under test is the FALLBACK,
// and arranging for a real ext4 (no reflink) and a real second mount (no hardlink) inside a
// unit test would mean requiring root and two loopback filesystems to assert a branch. The
// real mechanisms are measured where they can be — the integration cell and the premise test
// in materialize_test.go — and the chain's ORDER and stickiness are measured here.
var (
	reflinkOne = func(src, dst string, perm fs.FileMode) error {
		sf, err := os.Open(src)
		if err != nil {
			return err
		}
		defer sf.Close()
		// O_EXCL: replaceable() already unlinked anything here, so a destination that
		// exists now is a race or a bug, and clobbering it would be the wrong answer.
		df, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if err != nil {
			return err
		}
		if cerr := cloneFile(df, sf); cerr != nil {
			df.Close()
			// The destination was created by THIS call and holds nothing anyone
			// wants; leaving it behind would make the hardlink arm fail EEXIST.
			_ = os.Remove(dst)
			return cerr
		}
		if cerr := df.Close(); cerr != nil {
			return cerr
		}
		// Its own inode, so the manifest's mode is safe to assert (O_CREATE's is
		// masked by umask).
		return os.Chmod(dst, perm)
	}

	hardlinkOne = func(src, dst string) error { return os.Link(src, dst) }
)

// copyOne duplicates the bytes. The arm that is always correct and never cheap.
func copyOne(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}

// replaceable removes whatever is at dst so a file or symlink can be created there.
//
// A DIRECTORY IS REFUSED rather than removed. A capture that says "this path is a file" and
// a home that says "this path is a directory full of things" disagree about something this
// function cannot adjudicate, and `os.RemoveAll` on the home's side of that disagreement is
// how a materialize would delete a user's data.
func replaceable(dst string) error {
	fi, err := os.Lstat(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s is a directory in this home and a file in the capture — "+
			"refusing to remove it", dst)
	}
	return os.Remove(dst)
}

// dirExists reports whether dst is already a directory, so a materialize can leave an
// existing home directory's mode alone.
func dirExists(dst string) bool {
	fi, err := os.Lstat(dst)
	return err == nil && fi.IsDir()
}

// permOf is the manifest's recorded mode, falling back to the SOURCE's when the manifest
// carries none.
//
// The fallback matters for exactly one thing and it is the important one: the source's mode
// has been frozen read-only by admit, so falling back means a file that would have been 0755
// arrives 0555. That is degraded but not broken (the exec bit survives freezeTree), and it
// only happens for a manifest field the schema says is always present — belt to the braces of
// a version boundary that already refuses anything it does not understand.
func permOf(e ManifestEntry, src string, def fs.FileMode) fs.FileMode {
	if e.Mode != "" {
		if n, err := strconv.ParseUint(strings.TrimPrefix(e.Mode, "0"), 8, 32); err == nil {
			return fs.FileMode(n).Perm()
		}
	}
	if fi, err := os.Lstat(src); err == nil {
		return fi.Mode().Perm()
	}
	return def
}

// reportCopy is the LOUD half of the copy fallback.
//
// It is loud because a silent copy is indistinguishable from the thing this subsystem
// promises: the launch still works, the program still runs, and the only symptom is that
// every workspace on the machine quietly holds its own gigabyte. Naming the two filesystems
// is what makes the message actionable — "ext2/3/4" is the whole diagnosis, and the reader's
// next move (a btrfs/XFS store, or accepting the cost) follows from it directly.
func reportCopy(w io.Writer, entry *Entry, home string, res *MaterializeResult) {
	if w == nil {
		return
	}
	where := ""
	if res.SourceFS != "" || res.DestFS != "" {
		where = fmt.Sprintf(" — the store is on %s and this home is on %s",
			orUnknown(res.SourceFS), orUnknown(res.DestFS))
	}
	why := res.ReflinkRetired
	if why == "" {
		why = "reflink was not attempted"
	}
	fmt.Fprintf(w, "yolo: capture %s was COPIED into %s: %d of %d files (%d bytes) could be "+
		"neither reflinked nor hardlinked%s (%s). Those bytes are this workspace's own, and "+
		"every other workspace will pay for them again.\n",
		entry.Key, home, res.Copied, res.Files, res.Bytes, where, why)
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown filesystem"
	}
	return s
}
