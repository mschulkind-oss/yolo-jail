package prune

// devbundles.go reaps the DEVELOPMENT BUNDLES `just build-dev-bundle` and `just dev` publish
// (scripts/matched-bundle.py: build_dev_bundle). Each source snapshot the checkout is run
// from gets its own read-only directory under the state dir:
//
//	~/.local/share/yolo-jail/dev-bundles/<identity[:16]>-<uuid hex>/   bin/yolo + share/yolo-jail/
//	~/.local/share/yolo-jail/dev-bundles/.building-<random>/          a publish in flight
//
// Nothing deleted them, and a developer's machine collects one per edit-and-run: 113 of them,
// 16 GB, in the maintainer's jail (2026-10-24).
//
// A BUNDLE IS IN USE, and is never deleted, while either of two things is true:
//
//   - a RUNNING JAIL mounts it. A launch from a dev launcher bind-mounts the bundle's own
//     share/yolo-jail/bin/linux-<arch> at /opt/yolo-jail/bin (internal/cli/run/jailprefix.go),
//     so the jail's pid1 executes out of it. LivePrefixSources asks the runtime, the way the
//     flake-bundle generation reaper (internal/flakebundle) does.
//   - a LIVE PROCESS executes out of it: the dev `yolo` itself for the whole of its launch, and
//     every host daemon it self-execs (`yolo internal daemon <name>`). Deleting its binary breaks
//     a daemon the next time it re-execs, and leaves a `(deleted)` executable behind meanwhile.
//
// "I COULD NOT ASK" IS NOT "UNREFERENCED" (AGENTS.md). If either question cannot be answered —
// the runtime is unreachable, a running container cannot be inspected, the process table cannot
// be read, or a process that could be a yolo binary hides its executable — the sweep removes
// NOTHING and says which evidence was missing (DevBundleReap.Unknown).
//
// THE PROCESS TABLE IS READ ON LINUX ONLY. On macOS a macos-user session runs its guest as the
// sandbox ACCOUNT, another user, whose processes' executables this user cannot read; a bundle
// such a session runs from would read as unused. So every other OS declines, naming the
// by-hand removal instead (DevBundleOSWhy).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// DevBundleKeep is how many of the most recently used bundles survive regardless of liveness.
//
// THREE, because what a developer runs is the current source plus the one or two snapshots they
// toggle back to (a stash, the branch before a rebase), and a miss there is a full rebuild — the
// build is minutes, the bundle ~150 MB. More than that is the backlog this sweep exists for.
// "Recently used" is the directory's mtime: a publish sets it, and build_dev_bundle touches a
// bundle it REUSES, so the bundle for the source `just dev` would pick right now is the newest
// one whenever it has been run — the dev wrapper's own reuse stays cheap without this sweep
// recomputing a snapshot identity it has no way to compute.
const DevBundleKeep = 3

// DevBundleGrace keeps a bundle used within the last hour, beyond the newest DevBundleKeep. It
// covers the window between build_dev_bundle choosing a bundle (and touching it) and the
// launcher process starting from it, which neither liveness probe can see — the same window
// flakebundle.GenerationGrace covers for an install. It is also the recheck a guarded deletion
// re-reads, so a reuse that lands between the listing and the deletion keeps its bundle.
const DevBundleGrace = time.Hour

// devBundleStagingFloor is how old a `.building-*` publish directory must be before it is
// judged abandoned. A publish copies an assembled bundle in, validates it, and renames it into
// place — seconds, not minutes (the build itself happens elsewhere, in a temp dir). One an hour
// old belongs to a build that died; the liveness checks still apply to it, since validation runs
// its launcher from the staging path.
const devBundleStagingFloor = time.Hour

// devBundlesLeaf is the state-dir child matched-bundle.py publishes into.
const devBundlesLeaf = "dev-bundles"

// DevBundlesDirUnder is the development-bundle directory under a state dir.
func DevBundlesDirUnder(storage string) string { return filepath.Join(storage, devBundlesLeaf) }

// The exact names this sweep may touch. Anything else under dev-bundles/ is left alone.
var (
	// f"{identity[:16]}-{uuid.uuid4().hex}" in matched-bundle.py.
	devBundleName = regexp.MustCompile(`^[0-9a-f]{16}-[0-9a-f]{32}$`)
	// tempfile.mkdtemp(prefix=".building-"): eight characters from [a-z0-9_].
	devBundleStagingName = regexp.MustCompile(`^\.building-[A-Za-z0-9_]+$`)
	// What this sweep renames a bundle to before deleting it, so an interrupted deletion leaves
	// a name build_dev_bundle never reuses (its glob is <identity[:16]>-*) and this sweep finishes.
	// A staging dir is renamed the same way, keeping its `.building-` name inside.
	devBundleReapingName = regexp.MustCompile(`^\.reap-([0-9a-f]{16}-[0-9a-f]{32}|\.building-[A-Za-z0-9_]+)-[0-9]+-[0-9]+$`)
)

// Kept reasons, spelled once so the report's grouping and the tests agree.
const (
	whyDevRecent = "one of the most recently used"
	whyDevInUse  = "in use by a running jail or yolo process"
)

// DevBundleLiveness is the evidence the sweep decides on.
type DevBundleLiveness struct {
	// MountSources are the host directories running jails mount at /opt/yolo-jail/bin
	// (LivePrefixSources). MountsKnown false — with MountsWhy saying why — removes nothing.
	MountSources map[string]bool
	MountsKnown  bool
	MountsWhy    string
	// Procs are this user's live processes (Options.DevBundleProcs). ProcsKnown false — with
	// ProcsWhy saying why — removes nothing.
	Procs      []ProcStart
	ProcsKnown bool
	ProcsWhy   string
	// OSDecline is true when ProcsKnown is false because this OS can never answer
	// (DevBundleOSCanAnswer), not because one read failed. A retry does not help there, so a
	// caller names the by-hand removal instead.
	OSDecline bool
}

// DevBundleEvidence gathers the liveness evidence: the bundle each running jail mounts, asked
// of the runtime, and the process table procs reads. live is the caller's LiveYoloContainers
// answer, so a `yolo prune` that already asked does not ask twice.
//
// The process table is read FIRST, and an unreadable one returns without asking the runtime:
// the sweep removes nothing then anyway, and every container inspect would be wasted.
func DevBundleEvidence(rt string, live runtime.LiveSet, run RunFunc, procs func() ([]ProcStart, bool)) DevBundleLiveness {
	var ev DevBundleLiveness
	ev.Procs, ev.ProcsKnown = procs()
	if !ev.ProcsKnown {
		ev.ProcsWhy = "could not read the process table"
		if !DevBundleOSCanAnswer() {
			ev.ProcsWhy, ev.OSDecline = DevBundleOSWhy, true
		}
		return ev
	}
	sources, known, why := LivePrefixSources(rt, live, prefixBinMountDest, run)
	ev.MountSources, ev.MountsKnown, ev.MountsWhy = sources, known, string(why)
	return ev
}

// DevBundleOSCanAnswer reports whether this OS gives the sweep a process table it trusts:
// Linux only (the file comment says why). Where it cannot, no sweep will ever remove a bundle,
// so the housekeeping slot skips the class outright and the other callers name the by-hand
// removal (DevBundleByHand) rather than a retry.
func DevBundleOSCanAnswer() bool { return goruntime.GOOS == "linux" }

// DevBundleByHand is the by-hand removal a decline names, for a bundle the reader knows is unused.
const DevBundleByHand = "chmod -R u+w <bundle> && rm -rf <bundle>"

// CountDevBundles counts the published bundles (names matched-bundle.py writes) under dir.
func CountDevBundles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && devBundleName.MatchString(e.Name()) {
			n++
		}
	}
	return n
}

// DevBundleOSWhy is the decline on an OS whose process table the sweep does not read.
const DevBundleOSWhy = "this OS has no process table yolo can read every bundle user from " +
	"(a macos-user session runs as another account)"

// DevBundleProcStarts is the default process reader for the sweep: this user's processes on
// Linux, and "could not ask" everywhere else (see the file comment for why macOS declines).
func DevBundleProcStarts() ([]ProcStart, bool) {
	if goruntime.GOOS != "linux" {
		return nil, false
	}
	return readProcStarts()
}

// DevBundleReap is one sweep's outcome.
type DevBundleReap struct {
	EmbeddedReap
	// Unknown is non-empty when the liveness evidence could not be had. Nothing was removed
	// (or would be), and it names the missing evidence.
	Unknown string
	// OSDecline: Unknown is DevBundleOSWhy, which no retry changes.
	OSDecline bool
}

// PruneDevBundles sweeps dir (DevBundlesDirUnder) and removes — or, with apply false, lists —
// every development bundle that is
//
//   - not one of the keep most recently used (directory mtime),
//   - not used within DevBundleGrace,
//   - not mounted by a running jail and not executed from by a live process,
//
// plus each `.building-*` publish older than devBundleStagingFloor and each `.reap-*` leftover
// of an interrupted deletion, under the same liveness rule. Bundles are read-only (directories
// 0555), so removal makes each directory writable first.
//
// A bundle is renamed to a .reap- name before it is deleted, so a build_dev_bundle running
// meanwhile never selects a half-deleted bundle. guard brackets each removal (Guard; nil runs it
// directly); its recheck re-reads the bundle's mtime, so a reuse since the listing keeps it.
func PruneDevBundles(dir string, keep int, ev DevBundleLiveness, apply bool, now time.Time, guard Guard) DevBundleReap {
	var r DevBundleReap
	if dir == "" {
		return r
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.Declined = fmt.Sprintf("could not list %s: %v", dir, err)
		}
		return r
	}
	if len(entries) == 0 {
		return r
	}
	// The process table first: on an OS that declines it, that is the reason worth naming, not
	// whatever the runtime said (macos-user has no containers to ask at all).
	switch {
	case !ev.ProcsKnown:
		r.Unknown = nonEmpty(ev.ProcsWhy, "could not read the process table")
		r.OSDecline = ev.OSDecline
		return r
	case !ev.MountsKnown:
		r.Unknown = nonEmpty(ev.MountsWhy, "could not ask the runtime which bundle each running jail mounts")
		return r
	}
	inUse := map[string]string{} // resolved path -> what holds it
	for src := range ev.MountSources {
		inUse[resolvePath(src)] = "mounted by a running jail"
	}
	for _, p := range ev.Procs {
		if p.Exe != "" && filepath.IsAbs(p.Exe) {
			inUse[resolvePath(p.Exe)] = fmt.Sprintf("executed by pid %d", p.PID)
			continue
		}
		// A process whose executable the OS would not name. Only a yolo binary can be running
		// out of a bundle (every executable in one is named yolo*), so anything else is no
		// evidence either way; one that could be a yolo — or whose name was cut — is a gap.
		if strings.HasPrefix(p.Name, "yolo") || p.NameTruncated {
			r.Unknown = fmt.Sprintf("could not read the executable of process %d (%s)", p.PID, p.Name)
			return r
		}
	}

	type cand struct {
		e     EmbeddedEntry
		mtime time.Time
	}
	var bundles, leftovers []cand
	for _, de := range entries {
		name := de.Name()
		p := filepath.Join(dir, name)
		e := EmbeddedEntry{Name: name, Path: p}
		final := devBundleName.MatchString(name)
		staging := devBundleStagingName.MatchString(name)
		reaping := devBundleReapingName.MatchString(name)
		if !final && !staging && !reaping {
			r.keep(e, whyUnrecognized, "")
			continue
		}
		fi, err := os.Lstat(p)
		if err != nil || !fi.IsDir() {
			r.keep(e, whyUnrecognized, "not a directory")
			continue
		}
		if !ownedByEuid(fi) {
			r.skip(whyOtherUser)
			continue
		}
		c := cand{e, fi.ModTime()}
		switch {
		case final:
			bundles = append(bundles, c)
		case staging && now.Sub(c.mtime) < devBundleStagingFloor:
			r.keep(e, whyYoung, "a publish younger than "+devBundleStagingFloor.String())
		default:
			leftovers = append(leftovers, c)
		}
	}
	// Newest first; the name breaks a tie so the kept set is the same on every run.
	sort.Slice(bundles, func(i, j int) bool {
		if !bundles[i].mtime.Equal(bundles[j].mtime) {
			return bundles[i].mtime.After(bundles[j].mtime)
		}
		return bundles[i].e.Name < bundles[j].e.Name
	})
	var doomed []cand
	for i, c := range bundles {
		switch {
		case i < keep:
			r.keep(c.e, whyDevRecent, fmt.Sprintf("the newest %d are kept", keep))
		case now.Sub(c.mtime) < DevBundleGrace:
			r.keep(c.e, whyYoung, "used within "+DevBundleGrace.String())
		default:
			doomed = append(doomed, c)
		}
	}
	doomed = append(doomed, leftovers...)
	for _, c := range doomed {
		e := c.e
		if holder := devBundleHolder(resolvePath(e.Path), inUse); holder != "" {
			r.keep(e, whyDevInUse, holder)
			continue
		}
		switch {
		case devBundleName.MatchString(e.Name):
			e.Why = "not among the most recently used, and nothing running uses it"
		case devBundleStagingName.MatchString(e.Name):
			e.Why = "an abandoned publish"
		default:
			e.Why = "an interrupted removal"
		}
		e.Bytes = dirSizeBytes(e.Path)
		if apply {
			mtime := c.mtime
			ran := guard.Do(func() bool {
				fi, err := os.Lstat(e.Path)
				return err == nil && fi.IsDir() && fi.ModTime().Equal(mtime)
			}, func() { e.Err = removeDevBundle(dir, e.Path, e.Name, mtime) })
			if errors.Is(e.Err, errDevBundleReused) {
				e.Err = nil
				ran = false
			}
			if !ran {
				r.keep(e, whyYoung, "used since it was listed")
				continue
			}
		}
		if e.Err == nil {
			r.Bytes += e.Bytes
		}
		r.Removed = append(r.Removed, e)
	}
	return r
}

// devBundleHolder names what holds bundle (a resolved path), or "" when nothing does. The
// comparison is on path segments: <…>/abc must not match <…>/abcd.
func devBundleHolder(bundle string, inUse map[string]string) string {
	for p, holder := range inUse {
		if p == bundle || strings.HasPrefix(p, bundle+string(filepath.Separator)) {
			return holder
		}
	}
	return ""
}

// errDevBundleReused: the bundle was used between the listing and its removal, and was kept.
var errDevBundleReused = errors.New("used since it was listed")

// devBundleBeforeRemove runs just before a removal renames its entry; tests use it to land a
// reuse in the window between the listing and the removal. nil in production.
var devBundleBeforeRemove func(path string)

// removeDevBundle renames a bundle or staging dir aside, checks it was not used since it was
// listed, and removes the read-only tree.
//
// THE RENAME IS THE CLAIM, and it closes the reuse race whether or not a guard runs (the
// post-build sweep and `yolo prune --apply` pass none). build_dev_bundle touches a bundle BEFORE
// it validates and runs it, and skips one it cannot touch. So a reuse either touched the bundle
// before this rename (the renamed directory then carries a newer mtime than the listing saw,
// and it is renamed back and kept) or tries to touch it after, finds no bundle at that name, and
// does not reuse it. A rename does not change the renamed directory's own mtime.
func removeDevBundle(dir, p, name string, listed time.Time) error {
	if devBundleBeforeRemove != nil {
		devBundleBeforeRemove(p)
	}
	target := p
	if !devBundleReapingName.MatchString(name) {
		target = filepath.Join(dir, fmt.Sprintf(".reap-%s-%d-%d", name, os.Getpid(), time.Now().UnixNano()))
		if err := os.Rename(p, target); err != nil {
			return err
		}
		fi, err := os.Lstat(target)
		if err != nil {
			return err
		}
		if fi.ModTime().After(listed) {
			if err := os.Rename(target, p); err != nil {
				return fmt.Errorf("it was used since it was listed, and renaming it back failed: %w", err)
			}
			return errDevBundleReused
		}
	}
	return RemoveReadOnlyTree(target)
}

// RemoveReadOnlyTree removes a tree whose directories may be read-only, as a published bundle's
// are (matched-bundle.py's _make_readonly: directories 0555, files 0444/0555). Removing an entry
// needs write permission on its PARENT, so each directory is made owner-writable before its
// children go. Symbolic links are never followed: WalkDir reports a link as a link, and only
// entries it reports as directories are chmodded.
func RemoveReadOnlyTree(root string) error {
	fi, err := os.Lstat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if fi.IsDir() {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				_ = os.Chmod(path, 0o700)
			}
			return nil
		})
	}
	return os.RemoveAll(root)
}

func nonEmpty(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// ProcessExes is this user's live processes' executable paths, for a sweep that keeps what a
// running process executes from (the flake-bundle generation reaper, PH-D8). Unlike
// DevBundleProcStarts it reads the table on darwin too: the generations are the user's own
// install, run by the user's own helpers. false is "could not ask", and a process whose
// executable the OS would not name is omitted.
func ProcessExes() ([]string, bool) {
	procs, ok := readProcStarts()
	if !ok {
		return nil, false
	}
	var out []string
	for _, p := range procs {
		if p.Exe != "" {
			out = append(out, p.Exe)
		}
	}
	return out, true
}
